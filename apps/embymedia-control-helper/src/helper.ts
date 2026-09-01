import { createHash, randomUUID } from 'node:crypto'
import { chmod, copyFile, lstat, readFile, rename, rm, stat, writeFile } from 'node:fs/promises'
import { isAbsolute } from 'node:path'
import { spawn } from 'node:child_process'
import { parse, stringify, type TomlTable } from 'smol-toml'
import type { ControlRequest, ControlResponse } from '@embymedia/dsh-operations/schemas'

export interface HelperConfig {
  readonly compose: string
  readonly envFile: string
  readonly webhookToml: string
  readonly mountPath: string
  readonly canaryPath: string
}

export type CommandRunner = (program: string, args: readonly string[]) => Promise<void>

type StatPath = (path: string) => Promise<unknown>

function hash(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}

function validateConfig(config: HelperConfig): void {
  for (const [key, value] of Object.entries(config)) {
    if (!isAbsolute(value) || value.includes('\0')) throw new Error(`${key} must be an absolute NUL-free path`)
  }
  if (!config.webhookToml.endsWith('/config/webhooks/webhook.toml')) throw new Error('webhook TOML path is not the fixed CloudDrive config path')
  if (!config.compose.endsWith('/deploy/compose.yml')) throw new Error('compose path is not the fixed deployment compose file')
}

function validateRequest(value: unknown): ControlRequest {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('request must be an object')
  const request = value as Record<string, unknown>
  if (request.version !== 1 || request.action !== 'webhook.rotate' || (request.phase !== 'apply' && request.phase !== 'restore')) throw new Error('unsupported helper request')
  if (typeof request.planId !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(request.planId)) throw new Error('invalid plan id')
  const allowed = request.phase === 'apply'
    ? ['version', 'action', 'phase', 'planId', 'pendingSecret', 'previousSecretHash']
    : ['version', 'action', 'phase', 'planId']
  if (Object.keys(request).some(key => !allowed.includes(key))) throw new Error('unknown helper request field')
  if (request.phase === 'apply') {
    if (typeof request.pendingSecret !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(request.pendingSecret)) throw new Error('invalid pending webhook secret')
    if (typeof request.previousSecretHash !== 'string' || !/^[a-f0-9]{64}$/.test(request.previousSecretHash)) throw new Error('invalid previous secret hash')
  }
  return request as unknown as ControlRequest
}

function backupPath(config: HelperConfig, planId: string): string {
  return `${config.webhookToml}.embymedia-${planId}.bak`
}

async function run(program: string, args: readonly string[]): Promise<void> {
  const completed = Promise.withResolvers<void>()
  const child = spawn(program, [...args], { stdio: ['ignore', 'ignore', 'pipe'], shell: false })
  let stderr = ''
  child.stderr.setEncoding('utf8')
  child.stderr.on('data', (chunk) => { if (stderr.length < 4096) stderr += String(chunk) })
  child.once('error', completed.reject)
  child.once('exit', (code) => {
    if (code === 0) completed.resolve()
    else completed.reject(new Error(`${program} exited ${String(code)}: ${stderr.trim()}`))
  })
  return completed.promise
}

async function restartAndCheck(config: HelperConfig, runCommand: CommandRunner, statPath: StatPath): Promise<void> {
  await runCommand('/usr/bin/docker', ['compose', '--env-file', config.envFile, '-f', config.compose, 'restart', 'clouddrive2'])
  await runCommand('/usr/bin/mountpoint', ['-q', config.mountPath])
  await statPath(config.canaryPath)
}

async function atomicToml(path: string, document: TomlTable, mode: number): Promise<void> {
  const temporary = `${path}.tmp-${process.pid}-${randomUUID()}`
  try {
    await writeFile(temporary, stringify(document), { mode, flag: 'wx' })
    await chmod(temporary, mode)
    await rename(temporary, path)
  } finally {
    await rm(temporary, { force: true })
  }
}

export class WebhookControlHelper {
  constructor(
    private readonly config: HelperConfig,
    private readonly runCommand: CommandRunner = run,
    private readonly statPath: StatPath = stat,
  ) {
    validateConfig(config)
  }

  async handle(value: unknown): Promise<ControlResponse> {
    const request = validateRequest(value)
    return request.phase === 'restore' ? this.restore(request.planId) : this.apply(request)
  }

  private async apply(request: Extract<ControlRequest, { phase: 'apply' }>): Promise<ControlResponse> {
    const source = await readFile(this.config.webhookToml, 'utf8')
    const metadata = await lstat(this.config.webhookToml)
    if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error('webhook TOML must be a regular file')
    const document = parse(source)
    if (typeof document.enabled !== 'boolean' || typeof document.url !== 'string' || typeof document.method !== 'string') throw new Error('webhook TOML must contain enabled, url, and method')
    const current = new URL(document.url)
    const currentSecret = current.searchParams.get('key')
    if (currentSecret === null || hash(currentSecret) !== request.previousSecretHash) throw new Error('webhook sender secret does not match approved previous hash')
    const backup = backupPath(this.config, request.planId)
    await copyFile(this.config.webhookToml, backup)
    await chmod(backup, metadata.mode & 0o777)
    current.searchParams.set('key', request.pendingSecret)
    document.url = current.toString()
    try {
      await atomicToml(this.config.webhookToml, document, metadata.mode & 0o777)
      await restartAndCheck(this.config, this.runCommand, this.statPath)
      return { version: 1, ok: true, planId: request.planId, phase: 'apply', senderHash: hash(request.pendingSecret) }
    } catch (error) {
      await copyFile(backup, this.config.webhookToml)
      await chmod(this.config.webhookToml, metadata.mode & 0o777)
      await restartAndCheck(this.config, this.runCommand, this.statPath)
      throw error
    }
  }

  private async restore(planId: string): Promise<ControlResponse> {
    const backup = backupPath(this.config, planId)
    const metadata = await lstat(backup)
    if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error('rotation backup is unavailable')
    await copyFile(backup, this.config.webhookToml)
    await chmod(this.config.webhookToml, metadata.mode & 0o777)
    await restartAndCheck(this.config, this.runCommand, this.statPath)
    await rm(backup)
    return { version: 1, ok: true, planId, phase: 'restore' }
  }
}

export function redactedFailure(planId: string, phase: 'apply' | 'restore', error: unknown): ControlResponse {
  return {
    version: 1,
    ok: false,
    planId,
    phase,
    error: {
      code: 'UPSTREAM_UNAVAILABLE',
      message: error instanceof Error ? error.message.replace(/[A-Za-z0-9_-]{43}/g, '[REDACTED]') : 'helper failed',
    },
  }
}

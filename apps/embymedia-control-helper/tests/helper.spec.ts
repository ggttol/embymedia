import { createHash, randomUUID } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { parse } from 'smol-toml'
import { WebhookControlHelper } from '../src/helper.ts'

const roots: string[] = []
afterEach(async () => {
  await Promise.all(roots.splice(0).map(root => rm(root, { recursive: true, force: true })))
})

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), 'embymedia-helper-'))
  roots.push(root)
  const webhookDir = join(root, 'config/webhooks')
  const deployDir = join(root, 'deploy')
  const mountPath = join(root, 'mount')
  await Promise.all([mkdir(webhookDir, { recursive: true }), mkdir(deployDir), mkdir(mountPath)])
  const webhookToml = join(webhookDir, 'webhook.toml')
  const compose = join(deployDir, 'compose.yml')
  const envFile = join(root, 'stack.env')
  const canaryPath = join(mountPath, '.embymedia-health-canary')
  await writeFile(webhookToml, 'enabled = true\nurl = "https://dsh.gaotao.cc/hooks/clouddrive2?key=old-secret"\nmethod = "POST"\n', { mode: 0o640 })
  await Promise.all([writeFile(compose, 'services: {}\n'), writeFile(envFile, ''), writeFile(canaryPath, '')])
  return { root, webhookToml, compose, envFile, mountPath, canaryPath }
}

describe('restricted control helper', () => {
  it('atomically applies and restores only the fixed webhook TOML', async () => {
    const paths = await fixture()
    const commands: Array<{ program: string; args: readonly string[] }> = []
    const run = vi.fn(async (program: string, args: readonly string[]) => { commands.push({ program, args }) })
    const helper = new WebhookControlHelper(paths, run, async path => readFile(path))
    const planId = randomUUID()
    const pending = 'n'.repeat(43)
    const previousSecretHash = createHash('sha256').update('old-secret').digest('hex')
    const applied = await helper.handle({ version: 1, action: 'webhook.rotate', phase: 'apply', planId, pendingSecret: pending, previousSecretHash })
    expect(applied).toEqual({
      version: 1, ok: true, planId, phase: 'apply', senderHash: createHash('sha256').update(pending).digest('hex'),
    })
    expect(JSON.stringify(applied)).not.toContain(pending)
    const changed = parse(await readFile(paths.webhookToml, 'utf8'))
    expect(new URL(String(changed.url)).searchParams.get('key')).toBe(pending)
    expect(commands.map(command => command.program)).toEqual(['/usr/bin/docker', '/usr/bin/mountpoint'])

    await expect(helper.handle({ version: 1, action: 'webhook.rotate', phase: 'restore', planId }))
      .resolves.toEqual({ version: 1, ok: true, planId, phase: 'restore' })
    const restored = parse(await readFile(paths.webhookToml, 'utf8'))
    expect(new URL(String(restored.url)).searchParams.get('key')).toBe('old-secret')
  })

  it('restores the original sender when restart or mount health fails', async () => {
    const paths = await fixture()
    let calls = 0
    const run = async () => {
      calls++
      if (calls === 1) throw new Error('restart failed')
    }
    const helper = new WebhookControlHelper(paths, run, async path => readFile(path))
    await expect(helper.handle({
      version: 1,
      action: 'webhook.rotate',
      phase: 'apply',
      planId: randomUUID(),
      pendingSecret: 'p'.repeat(43),
      previousSecretHash: createHash('sha256').update('old-secret').digest('hex'),
    })).rejects.toThrow('restart failed')
    const restored = parse(await readFile(paths.webhookToml, 'utf8'))
    expect(new URL(String(restored.url)).searchParams.get('key')).toBe('old-secret')
  })

  it('rejects unknown actions, paths, fields, and secret mismatches', async () => {
    const paths = await fixture()
    const helper = new WebhookControlHelper(paths, vi.fn(), async path => readFile(path))
    const base = {
      version: 1, action: 'webhook.rotate', phase: 'apply', planId: randomUUID(), pendingSecret: 'p'.repeat(43),
      previousSecretHash: createHash('sha256').update('different').digest('hex'),
    }
    await expect(helper.handle({ ...base, command: 'sh' })).rejects.toThrow(/unknown/)
    await expect(helper.handle(base)).rejects.toThrow(/previous hash/)
    expect(() => new WebhookControlHelper({ ...paths, webhookToml: join(paths.root, 'other.toml') }, vi.fn(), vi.fn()))
      .toThrow(/fixed CloudDrive/)
  })
})

import { timingSafeEqual } from 'node:crypto'
import { lstat, readdir } from 'node:fs/promises'
import { posix, relative } from 'node:path'
import type { IncomingMessage, ServerResponse } from 'node:http'
import type { WebServer } from '@deepseek-ai/dsh-host-webserver'
import type { Database } from './database/index.ts'
import type { EmbymediaCredentialRecords } from './credentials.ts'
import type { EmbyClient } from './clients/emby.ts'
import { EmbymediaError } from './errors.ts'
import { safeUnder, writeStrm } from './media/paths.ts'

const MAX_WEBHOOK_BODY_BYTES = 256 * 1024
const MAX_ROTATION_MS = 5 * 60 * 1000
const EMPTY_SIGNAL_WINDOW_MS = 30 * 60 * 1000
const MAX_EMPTY_SIGNAL_ENTRIES = 100_000
const MAX_EMPTY_SIGNAL_TOPS = 50
const VIDEO_EXTENSIONS: Readonly<Record<string, true>> = {
  '.3gp': true, '.asf': true, '.avi': true, '.divx': true, '.flv': true, '.iso': true,
  '.m2ts': true, '.m4v': true, '.mkv': true, '.mov': true, '.mp4': true, '.mpeg': true,
  '.mpg': true, '.mts': true, '.rm': true, '.rmvb': true, '.ts': true, '.vob': true,
  '.webm': true, '.wmv': true,
}

export interface CloudDriveWebhookEvent {
  readonly library: string
  readonly mediaLibrary: string
  readonly top: string
  readonly sourceFile: string
  readonly mtime: number
}

export interface CloudDriveWebhookResult {
  readonly accepted: number
  readonly ignored: number
  readonly duplicate: number
  readonly generated: number
  readonly refreshed: readonly string[]
  readonly errors: readonly { readonly library: string; readonly top: string; readonly message: string }[]
}

interface RotationState {
  readonly planId: string
  readonly pending: string
  readonly expiresAt: number
  timer: NodeJS.Timeout
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new EmbymediaError('INVALID_INPUT', `${label} must be an object`)
  return value as Record<string, unknown>
}

function equalSecret(expected: string, supplied: string): boolean {
  const left = Buffer.from(expected)
  const right = Buffer.from(supplied)
  return left.length === right.length && timingSafeEqual(left, right)
}

async function readJsonBody(request: IncomingMessage): Promise<unknown> {
  const declared = request.headers['content-length']
  if (typeof declared === 'string' && Number(declared) > MAX_WEBHOOK_BODY_BYTES) throw new EmbymediaError('INVALID_INPUT', 'webhook body exceeds 256 KiB')
  const chunks: Buffer[] = []
  let size = 0
  for await (const chunk of request) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)
    size += bytes.length
    if (size > MAX_WEBHOOK_BODY_BYTES) throw new EmbymediaError('INVALID_INPUT', 'webhook body exceeds 256 KiB')
    chunks.push(bytes)
  }
  const body = Buffer.concat(chunks)
  if (body.length === 0) return undefined
  const text = body.toString('utf8')
  try {
    return JSON.parse(text) as unknown
  } catch {
    const firstByte = body[0]?.toString(16).padStart(2, '0') ?? 'none'
    const looksFormEncoded = /^[A-Za-z_][A-Za-z0-9_.-]*=/.test(text)
    const looksPercentEncodedJson = /^%7[bB]/.test(text)
    throw new EmbymediaError(
      'INVALID_INPUT',
      `webhook body must be valid JSON (bytes=${String(body.length)}, firstByte=${firstByte}, form=${String(looksFormEncoded)}, percentJson=${String(looksPercentEncodedJson)})`,
    )
  }
}

function responseJson(response: ServerResponse, status: number, body: unknown): void {
  const text = JSON.stringify(body)
  response.writeHead(status, {
    'cache-control': 'no-store',
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(text),
  })
  response.end(text)
}

function normalizedEventPath(value: string, mountPrefix: string, mediaRoot: string): string[] | undefined {
  const normalized = value.trim().replaceAll('\\', '/')
  const prefixes = [mountPrefix, mediaRoot, '/media'].map(prefix => prefix.replaceAll('\\', '/').replace(/\/$/, ''))
  const prefix = prefixes.find(candidate => normalized.startsWith(`${candidate}/`))
  if (prefix === undefined) return undefined
  const relativePath = normalized.slice(prefix.length + 1)
  const parts = relativePath.split('/').filter(Boolean)
  if (parts.length < 3 || parts.some(part => part === '.' || part === '..' || part.normalize('NFC') !== part)) return undefined
  return parts
}

export class CloudDriveWebhookService {
  private rotation: RotationState | undefined

  constructor(
    private readonly database: Database,
    private readonly credentials: EmbymediaCredentialRecords,
    private readonly mediaRoot: string,
    private readonly strmRoot: string,
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly mountPrefix = '/CloudNAS/CloudDrive',
    private readonly writeEnabled: () => boolean = () => true,
  ) {}

  register(webServer: WebServer): () => void {
    return webServer.register({
      kind: 'exact',
      path: '/hooks/clouddrive2',
      handler: (request, response) => this.handle(request, response),
    })
  }

  async beginRotation(planId: string): Promise<void> {
    this.endRotation()
    const pending = await this.credentials.readPending(planId, 'clouddrive-webhook-secret')
    const expiresAt = Date.now() + MAX_ROTATION_MS
    const state: RotationState = {
      planId,
      pending,
      expiresAt,
      timer: setTimeout(() => {
        if (this.rotation === state) this.rotation = undefined
      }, MAX_ROTATION_MS),
    }
    this.rotation = state
  }

  endRotation(planId?: string): void {
    if (this.rotation === undefined || (planId !== undefined && this.rotation.planId !== planId)) return
    clearTimeout(this.rotation.timer)
    this.rotation = undefined
  }

  private async authenticate(request: IncomingMessage): Promise<void> {
    const url = new URL(request.url ?? '/', 'http://127.0.0.1')
    if (url.search.length > 0) throw new EmbymediaError('POLICY_DENIED', 'webhook query parameters are forbidden')
    const supplied = typeof request.headers['x-webhook-secret'] === 'string' ? request.headers['x-webhook-secret'] : ''
    const formal = await this.credentials.read('clouddrive-webhook-secret')
    if (equalSecret(formal, supplied)) return
    const rotation = this.rotation
    if (rotation !== undefined && rotation.expiresAt > Date.now() && equalSecret(rotation.pending, supplied)) return
    throw new EmbymediaError('AUTH_REQUIRED', 'webhook authentication failed')
  }

  async handle(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const lifetime = new AbortController()
    const abort = (): void => { lifetime.abort(new Error('webhook request disconnected')) }
    request.once('aborted', abort)
    try {
      if (request.method !== 'POST') throw new EmbymediaError('NOT_FOUND', 'webhook route accepts POST only')
      const contentType = request.headers['content-type']?.split(';', 1)[0]?.trim().toLowerCase()
      if (contentType !== 'application/json') throw new EmbymediaError('INVALID_INPUT', 'webhook requires application/json')
      await this.authenticate(request)
      const payload = await readJsonBody(request)
      const signalPayload = payload === undefined ? await this.filesystemSignalPayload(lifetime.signal) : payload
      const result = await this.process(signalPayload, lifetime.signal)
      responseJson(response, 200, { schemaVersion: 1, ...result })
    } catch (error) {
      const failure = error instanceof EmbymediaError ? error : new EmbymediaError('UPSTREAM_UNAVAILABLE', 'webhook processing failed')
      console.error(`embymedia webhook rejected: ${failure.code} ${failure.message}`)
      responseJson(response, failure.httpStatus === 499 ? 499 : failure.httpStatus, failure.toJSON())
    } finally {
      request.off('aborted', abort)
    }
  }
  private async filesystemSignalPayload(signal: AbortSignal): Promise<{ readonly data: readonly Record<string, unknown>[] }> {
    const data: Array<Record<string, unknown>> = []
    const cutoff = Date.now() - EMPTY_SIGNAL_WINDOW_MS
    let visited = 0
    for (const library of await readdir(this.mediaRoot, { withFileTypes: true })) {
      signal.throwIfAborted()
      if (!library.isDirectory() || library.isSymbolicLink()) continue
      const libraryPath = await safeUnder(this.mediaRoot, library.name)
      for (const top of await readdir(libraryPath, { withFileTypes: true })) {
        visited++
        if (visited > MAX_EMPTY_SIGNAL_ENTRIES) throw new EmbymediaError('POLICY_DENIED', 'empty webhook signal scan exceeds 100000 entries')
        if (!top.isDirectory() || top.isSymbolicLink()) continue
        const topPath = await safeUnder(this.mediaRoot, posix.join(library.name, top.name))
        if ((await lstat(topPath)).mtimeMs < cutoff) continue
        data.push({
          action: 'signal',
          is_dir: false,
          destination_file: posix.join(this.mountPrefix, library.name, top.name, '__clouddrive_signal__.mp4'),
        })
        if (data.length > MAX_EMPTY_SIGNAL_TOPS) throw new EmbymediaError('POLICY_DENIED', 'empty webhook signal has more than 50 recent tops')
      }
    }
    return { data }
  }


  async process(payload: unknown, signal: AbortSignal): Promise<CloudDriveWebhookResult> {
    if (!this.writeEnabled()) throw new EmbymediaError('POLICY_DENIED', 'writes are disabled')
    const root = object(payload, 'webhook payload')
    if (!Array.isArray(root.data)) throw new EmbymediaError('INVALID_INPUT', 'webhook payload data must be an array')
    const emby = await this.embyClient(signal)
    const aliases = await this.libraryAliases(emby, signal)
    const events: Record<string, CloudDriveWebhookEvent> = {}
    let ignored = 0
    for (const raw of root.data) {
      const event = object(raw, 'webhook event')
      const action = typeof event.action === 'string' ? event.action.toLowerCase() : ''
      const isDirectory = event.is_dir === true || event.is_dir === 1 || (typeof event.is_dir === 'string' && ['true', '1', 'yes'].includes(event.is_dir.toLowerCase()))
      if (isDirectory || ['delete', 'remove', 'removed', 'deleted', 'unlink', 'rmdir'].includes(action)) {
        ignored++
        continue
      }
      const source = typeof event.destination_file === 'string'
        ? event.destination_file
        : typeof event.source_file === 'string' ? event.source_file : undefined
      if (source === undefined || !VIDEO_EXTENSIONS[posix.extname(source).toLowerCase()]) {
        ignored++
        continue
      }
      const parts = normalizedEventPath(source, this.mountPrefix, this.mediaRoot)
      if (parts === undefined) {
        ignored++
        continue
      }
      const [folder, top] = parts
      if (folder === undefined || top === undefined) {
        ignored++
        continue
      }
      const alias = aliases[folder]
      const library = alias?.library ?? folder
      const mediaLibrary = alias?.mediaLibrary ?? folder
      let topPath: string
      let mtime: number
      try {
        topPath = await safeUnder(this.mediaRoot, posix.join(mediaLibrary, top))
        mtime = Math.floor((await lstat(topPath)).mtimeMs)
      } catch {
        ignored++
        continue
      }
      events[`${library}\0${top}\0${String(mtime)}`] = { library, mediaLibrary, top, sourceFile: source, mtime }
    }

    let duplicate = 0
    let generated = 0
    const refresh = new Set<string>()
    const errors: Array<{ library: string; top: string; message: string }> = []
    for (const event of Object.values(events)) {
      signal.throwIfAborted()
      const seen = await this.database.query<{ mtime: string }>(
        'SELECT mtime::text AS mtime FROM autostrm_seen WHERE lib = $1 AND top = $2 ORDER BY mtime DESC LIMIT 1',
        [event.library, event.top],
      )
      if (seen.rows.some(row => Number(row.mtime) >= event.mtime)) {
        duplicate++
        continue
      }
      try {
        const topRelative = posix.join(event.mediaLibrary, event.top)
        const topAbsolute = await safeUnder(this.mediaRoot, topRelative)
        const files = await this.videoFiles(topAbsolute, signal)
        for (const file of files) {
          const relativeMedia = posix.join(event.top, relative(topAbsolute, file).split(posix.sep).join('/'))
          try {
            await writeStrm(this.strmRoot, event.library, relativeMedia, event.mediaLibrary)
            generated++
          } catch (error) {
            if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
          }
        }
        await this.database.query(
          `INSERT INTO autostrm_seen(lib,top,mtime,updated_at) VALUES ($1,$2,$3,now())
           ON CONFLICT(lib,top,mtime) DO UPDATE SET updated_at=now()`,
          [event.library, event.top, event.mtime],
        )
        if (files.length > 0) refresh.add(event.library)
      } catch (error) {
        errors.push({ library: event.library, top: event.top, message: error instanceof Error ? error.message : String(error) })
      }
    }
    for (const library of refresh) {
      await emby.refreshLibrary(signal)
      void library
    }
    return { accepted: Object.keys(events).length, ignored, duplicate, generated, refreshed: [...refresh], errors }
  }

  private async libraryAliases(emby: EmbyClient, signal: AbortSignal): Promise<Readonly<Record<string, { library: string; mediaLibrary: string }>>> {
    const aliases: Record<string, { library: string; mediaLibrary: string }> = {}
    for (const library of await emby.libraries(signal)) {
      aliases[library.name] = { library: library.name, mediaLibrary: library.name }
      for (const location of library.locations) {
        const folder = location.replaceAll('\\', '/').split('/').filter(Boolean).at(-1)
        if (folder !== undefined) aliases[folder] = { library: library.name, mediaLibrary: folder }
      }
    }
    return aliases
  }

  private async videoFiles(root: string, signal: AbortSignal): Promise<readonly string[]> {
    const output: string[] = []
    const stack: Array<{ path: string; depth: number }> = [{ path: root, depth: 0 }]
    let visited = 0
    while (stack.length > 0) {
      signal.throwIfAborted()
      const current = stack.pop()
      if (current === undefined) break
      if (current.depth > 64) throw new EmbymediaError('POLICY_DENIED', 'webhook media traversal exceeds 64 directories')
      for (const entry of await readdir(current.path, { withFileTypes: true })) {
        visited++
        if (visited > 100_000) throw new EmbymediaError('POLICY_DENIED', 'webhook media traversal exceeds 100000 entries')
        const path = posix.join(current.path, entry.name)
        if (entry.isSymbolicLink()) throw new EmbymediaError('POLICY_DENIED', 'symlink found during webhook media traversal')
        if (entry.isDirectory()) stack.push({ path, depth: current.depth + 1 })
        else if (entry.isFile() && VIDEO_EXTENSIONS[posix.extname(entry.name).toLowerCase()]) output.push(path)
      }
    }
    return output.sort()
  }
}

import { createServer, type Server } from 'node:http'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { CloudDriveWebhookService } from '../src/webhook.ts'
import type { EmbyClient } from '../src/clients/emby.ts'
import type { EmbymediaCredentialRecords } from '../src/credentials.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeWebhook = databaseUrl === undefined ? describe.skip : describe

describeWebhook('CloudDrive webhook', () => {
  let database: Database
  let root: string
  let mediaRoot: string
  let strmRoot: string
  let service: CloudDriveWebhookService
  let server: Server
  let endpoint: string
  let refreshes = 0
  let formal = 'formal-secret'
  let pending = 'pending-secret'

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    await database.query('TRUNCATE autostrm_seen')
    root = await mkdtemp(join(tmpdir(), 'embymedia-webhook-'))
    mediaRoot = join(root, 'media')
    strmRoot = join(root, 'strm')
    await mkdir(join(mediaRoot, 'MoviesFolder', 'Top'), { recursive: true })
    await mkdir(strmRoot, { recursive: true })
    await writeFile(join(mediaRoot, 'MoviesFolder', 'Top', 'Movie.mkv'), 'fixture')
    const credentials = {
      read: async (id: string) => {
        if (id !== 'clouddrive-webhook-secret') throw new Error('unexpected credential')
        return formal
      },
      readPending: async () => pending,
    } as unknown as EmbymediaCredentialRecords
    const emby = {
      libraries: async () => [{ id: 'library-1', name: 'MoviesDisplay', locations: ['/strm/MoviesFolder'] }],
      refreshLibrary: async () => { refreshes++ },
    } as unknown as EmbyClient
    service = new CloudDriveWebhookService(database, credentials, mediaRoot, strmRoot, async () => emby)
    server = createServer((request, response) => { void service.handle(request, response) })
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject)
      server.listen(0, '127.0.0.1', resolve)
    })
    const address = server.address()
    if (address === null || typeof address === 'string') throw new Error('webhook server did not bind')
    endpoint = `http://127.0.0.1:${String(address.port)}/hooks/clouddrive2`
  })

  afterAll(async () => {
    service.endRotation()
    await new Promise<void>((resolve, reject) => server.close((error) => { if (error) reject(error); else resolve() }))
    await database.close()
    await rm(root, { recursive: true, force: true })
  })

  const payload = {
    data: [
      { action: 'create', is_dir: false, destination_file: '/CloudNAS/CloudDrive/MoviesFolder/Top/Movie.mkv' },
      { action: 'create', is_dir: false, destination_file: '/CloudNAS/CloudDrive/MoviesFolder/Top/Movie.mkv' },
      { action: 'delete', is_dir: false, destination_file: '/CloudNAS/CloudDrive/MoviesFolder/Top/Old.mkv' },
    ],
  }

  it('requires header auth, JSON, bounded body, and rejects every query', async () => {
    const unauthenticated = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(payload) })
    expect(unauthenticated.status).toBe(401)
    const query = await fetch(`${endpoint}?key=${formal}`, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': formal }, body: JSON.stringify(payload) })
    expect(query.status).toBe(403)
    const wrongType = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'text/plain', 'x-webhook-secret': formal }, body: '{}' })
    expect(wrongType.status).toBe(400)
    const oversized = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': formal }, body: JSON.stringify({ data: [], padding: 'x'.repeat(256 * 1024) }) })
    expect(oversized.status).toBe(400)
  })

  it('deduplicates lib/top/mtime, generates STRM, refreshes Emby, and persists seen state', async () => {
    refreshes = 0
    const first = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': formal }, body: JSON.stringify(payload) })
    expect(first.status).toBe(200)
    await expect(first.json()).resolves.toMatchObject({ accepted: 1, ignored: 1, duplicate: 0, generated: 1, refreshed: ['MoviesDisplay'], errors: [] })
    expect(refreshes).toBe(1)
    expect(await readFile(join(strmRoot, 'MoviesDisplay', 'Top', 'Movie.strm'), 'utf8')).toBe('/media/MoviesFolder/Top/Movie.mkv\n')

    const second = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': formal }, body: JSON.stringify(payload) })
    await expect(second.json()).resolves.toMatchObject({ accepted: 1, ignored: 1, duplicate: 1, generated: 0 })
    expect(refreshes).toBe(1)
  })

  it('accepts old and pending secrets only during the five-minute rotation state', async () => {
    formal = 'old-formal'
    pending = 'new-pending'
    await service.beginRotation('plan-1')
    for (const secret of [formal, pending]) {
      const response = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': secret }, body: JSON.stringify({ data: [] }) })
      expect(response.status).toBe(200)
    }
    service.endRotation('plan-1')
    const rejected = await fetch(endpoint, { method: 'POST', headers: { 'content-type': 'application/json', 'x-webhook-secret': pending }, body: JSON.stringify({ data: [] }) })
    expect(rejected.status).toBe(401)
  })
})

describe('CloudDrive webhook event isolation', () => {
  it('refuses to generate STRM or refresh Emby when writes are not enabled', async () => {
    const database = { query: async () => ({ rows: [], rowCount: 0 }) } as unknown as Database
    const service = new CloudDriveWebhookService(database, {} as EmbymediaCredentialRecords, '/media', '/strm', async () => {
      throw new Error('embyClient must not be called when writes are disabled')
    }, undefined, () => false)
    await expect(service.process({
      data: [{ action: 'create', is_dir: false, destination_file: '/CloudNAS/CloudDrive/Movies/Top/Movie.mkv' }],
    }, new AbortController().signal)).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })

  it('ignores one malformed path instead of aborting its authenticated batch', async () => {
    const database = { query: async () => ({ rows: [], rowCount: 0 }) } as unknown as Database
    const credentials = {} as EmbymediaCredentialRecords
    const emby = {
      libraries: async () => [{ id: 'library-1', name: 'Movies', locations: ['/strm/Movies'] }],
      refreshLibrary: async () => {},
    } as unknown as EmbyClient
    const service = new CloudDriveWebhookService(database, credentials, '/media', '/strm', async () => emby)
    await expect(service.process({
      data: [{ action: 'create', is_dir: false, destination_file: '/CloudNAS/CloudDrive/Movies/Top\u0000/Movie.mkv' }],
    }, new AbortController().signal)).resolves.toMatchObject({ accepted: 0, ignored: 1, errors: [] })
  })

  it('turns an authenticated empty CloudDrive signal into a bounded recent-top scan', async () => {
    const root = await mkdtemp(join(tmpdir(), 'embymedia-empty-webhook-'))
    const mediaRoot = join(root, 'media')
    const strmRoot = join(root, 'strm')
    await mkdir(join(mediaRoot, 'Movies', 'Recent'), { recursive: true })
    await mkdir(strmRoot, { recursive: true })
    await writeFile(join(mediaRoot, 'Movies', 'Recent', 'fixture.mp4'), 'fixture')
    const database = { query: vi.fn(async () => ({ rows: [], rowCount: 0 })) } as unknown as Database
    const credentials = { read: async () => 'fixture-secret' } as unknown as EmbymediaCredentialRecords
    const emby = {
      libraries: async () => [{ id: 'library-1', name: 'Movies', locations: ['/strm/Movies'] }],
      refreshLibrary: async () => {},
    } as unknown as EmbyClient
    const service = new CloudDriveWebhookService(database, credentials, mediaRoot, strmRoot, async () => emby)
    const server = createServer((request, response) => { void service.handle(request, response) })
    try {
      await new Promise<void>((resolve, reject) => {
        server.once('error', reject)
        server.listen(0, '127.0.0.1', resolve)
      })
      const address = server.address()
      if (address === null || typeof address === 'string') throw new Error('webhook server did not bind')
      const response = await fetch(`http://127.0.0.1:${String(address.port)}/hooks/clouddrive2`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'x-webhook-secret': 'fixture-secret' },
        body: '',
      })
      expect(response.status).toBe(200)
      await expect(response.json()).resolves.toMatchObject({ accepted: 1, generated: 1, refreshed: ['Movies'], errors: [] })
      await expect(readFile(join(strmRoot, 'Movies', 'Recent', 'fixture.strm'), 'utf8')).resolves.toBe('/media/Movies/Recent/fixture.mp4\n')
    } finally {
      await new Promise<void>(resolve => server.close(() => { resolve() }))
      await rm(root, { recursive: true, force: true })
    }
  })
})

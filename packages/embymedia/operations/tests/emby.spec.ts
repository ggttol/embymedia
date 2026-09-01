import { createServer, type Server } from 'node:http'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { EmbyClient } from '../src/clients/emby.ts'

interface SeenRequest {
  readonly method: string
  readonly url: string
  readonly token?: string
}

let server: Server
let baseUrl: string
let status = 200
let delayMs = 0
let malformedItems = false
const seen: SeenRequest[] = []

beforeAll(async () => {
  server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://127.0.0.1')
    seen.push({
      method: request.method ?? '',
      url: url.toString(),
      ...(typeof request.headers['x-emby-token'] === 'string' ? { token: request.headers['x-emby-token'] } : {}),
    })
    const respond = (): void => {
      if (status !== 200) {
        response.writeHead(status).end()
        return
      }
      response.setHeader('content-type', 'application/json')
      if (url.pathname === '/emby/Library/VirtualFolders') {
        response.end(JSON.stringify([{ ItemId: 'library-1', Name: 'Movies', CollectionType: 'movies', Locations: ['/strm/Movies'] }]))
      } else if (url.pathname === '/emby/Items') {
        if (malformedItems) {
          response.end('{"Items":"wrong","TotalRecordCount":2}')
          return
        }
        const start = Number(url.searchParams.get('StartIndex') ?? 0)
        const limit = Number(url.searchParams.get('Limit') ?? 2)
        const all = [
          { Id: 'item-1', Name: 'One' },
          { Id: 'item-2', Name: 'Two' },
          { Id: 'item-3', Name: 'Three' },
        ]
        response.end(JSON.stringify({ Items: all.slice(start, start + limit), TotalRecordCount: all.length }))
      } else {
        response.end('{}')
      }
    }
    if (delayMs > 0) setTimeout(respond, delayMs)
    else respond()
  })
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => { resolve() })
  })
  const address = server.address()
  if (address === null || typeof address === 'string') throw new Error('fake Emby server did not bind TCP')
  baseUrl = `http://127.0.0.1:${String(address.port)}/emby`
})

afterAll(async () => {
  await new Promise<void>((resolve, reject) => server.close((error) => { if (error) reject(error); else resolve() }))
})

function client(overrides: Partial<ConstructorParameters<typeof EmbyClient>[0]> = {}): EmbyClient {
  return new EmbyClient({ baseUrl, token: 'secret-token', pageSize: 2, connectTimeoutMs: 100, totalTimeoutMs: 500, ...overrides })
}

describe('EmbyClient', () => {
  it('uses only X-Emby-Token and bounded pagination', async () => {
    status = 200
    delayMs = 0
    malformedItems = false
    seen.length = 0
    const subject = client()
    await expect(subject.libraries(new AbortController().signal)).resolves.toEqual([
      { id: 'library-1', name: 'Movies', collectionType: 'movies', locations: ['/strm/Movies'] },
    ])
    const items = await subject.items('library-1', 'Movie', 'Path', new AbortController().signal)
    expect(items.map(item => item.Id)).toEqual(['item-1', 'item-2', 'item-3'])
    expect(seen).toHaveLength(3)
    expect(seen.every(request => request.token === 'secret-token')).toBe(true)
    expect(seen.every(request => !new URL(request.url).searchParams.has('api_key'))).toBe(true)
  })

  it('returns an exact total from one bounded item page request', async () => {
    status = 200
    malformedItems = false
    seen.length = 0
    await expect(client().itemPage('library-1', 'Movie', 'Path', 1, new AbortController().signal))
      .resolves.toMatchObject({ items: [{ Id: 'item-1' }], total: 3 })
    expect(seen).toHaveLength(1)
    const url = new URL(seen[0]!.url)
    expect(url.searchParams.get('Limit')).toBe('1')
    expect(url.searchParams.get('IncludeItemTypes')).toBe('Movie')
  })

  it('scopes search to one library instead of loading the full catalog', async () => {
    status = 200
    seen.length = 0
    await client().search('我们的歌', 'Series', new AbortController().signal, 'library-1')
    const url = new URL(seen[0]!.url)
    expect(url.searchParams.get('SearchTerm')).toBe('我们的歌')
    expect(url.searchParams.get('ParentId')).toBe('library-1')
    expect(url.searchParams.get('Limit')).toBe('2')
  })

  it('looks up numeric item ids through the Ids query contract', async () => {
    status = 200
    seen.length = 0
    await client().item('171909', 'ProviderIds,ImageTags', new AbortController().signal)
    const url = new URL(seen[0]!.url)
    expect(url.pathname).toBe('/emby/Items')
    expect(url.searchParams.get('Ids')).toBe('171909')
    expect(url.searchParams.get('Limit')).toBe('1')
  })

  it('maps structured upstream errors and never retries writes', async () => {
    const expected: Readonly<Record<number, string>> = {
      401: 'AUTH_REQUIRED',
      404: 'NOT_FOUND',
      409: 'CONFLICT',
      429: 'RATE_LIMITED',
      500: 'UPSTREAM_UNAVAILABLE',
    }
    for (const [code, errorCode] of Object.entries(expected)) {
      status = Number(code)
      seen.length = 0
      await expect(client().refreshLibrary(new AbortController().signal)).rejects.toMatchObject({ code: errorCode })
      expect(seen).toHaveLength(1)
    }
    status = 200
  })

  it('fails closed on malformed responses, timeouts, and cancellation', async () => {
    malformedItems = true
    await expect(client().items('library-1', 'Movie', 'Path', new AbortController().signal))
      .rejects.toMatchObject({ code: 'UPSTREAM_UNAVAILABLE' })
    malformedItems = false

    delayMs = 150
    await expect(client({ connectTimeoutMs: 20, totalTimeoutMs: 50 }).refreshLibrary(new AbortController().signal))
      .rejects.toMatchObject({ code: 'UPSTREAM_UNAVAILABLE' })
    delayMs = 100
    const abort = new AbortController()
    const pending = client().refreshLibrary(abort.signal)
    abort.abort()
    await expect(pending).rejects.toMatchObject({ code: 'CANCELLED' })
    delayMs = 0
  })
})

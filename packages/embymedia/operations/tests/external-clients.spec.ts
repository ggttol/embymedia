import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { C115Client, parseC115Share } from '../src/clients/c115.ts'
import { ResourceApiClient } from '../src/clients/resource-api.ts'
import { TmdbClient } from '../src/clients/tmdb.ts'
import { ProxyHttpTransport } from '../src/clients/http.ts'

let upstream: Server
let proxy: Server
let upstreamBase: string
let proxyUrl: string
let active115 = 0
let maxActive115 = 0
const proxyRequests: string[] = []
const c115ListRequests: Array<{ cid: string | null; offset: string | null }> = []

function listen(server: Server): Promise<number> {
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      if (address === null || typeof address === 'string') reject(new Error('server did not bind TCP'))
      else resolve(address.port)
    })
  })
}

function json(response: ServerResponse, body: unknown): void {
  response.setHeader('content-type', 'application/json')
  response.end(JSON.stringify(body))
}

function c115Response(request: IncomingMessage, response: ServerResponse): void {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1')
  active115++
  maxActive115 = Math.max(maxActive115, active115)
  setTimeout(() => {
    active115--
    if (url.pathname === '/files/index_info') json(response, { state: true, data: { used: 12 } })
    else if (url.pathname === '/share/snap') {
      const recursive = url.searchParams.get('share_code') === 'recursive'
      const cid = url.searchParams.get('cid') ?? '0'
      json(response, recursive
        ? { state: true, data: { list: cid === '0' ? [{ cid: 'series-root', n: 'Series Pack' }] : [{ fid: 'e1', n: 'Series.S01E01.mkv', s: 10 }, { fid: 'e2', n: 'Series.S01E02.mkv', s: 11 }], total: cid === '0' ? 1 : 2, shareinfo: { share_title: 'Series Pack' } } }
        : { state: true, data: { list: [{ fid: 'f1', n: 'Movie.mkv', s: 10 }], total: 1, shareinfo: { share_title: 'Fixture' } } })
    }
    else if (url.pathname === '/share/receive') json(response, { state: true })
    else if (url.pathname === '/' && url.searchParams.get('ct') === 'offline') json(response, { state: true, sign: 'sign', time: 'time' })
    else if (url.pathname === '/web/lixian/') json(response, { state: true, info_hash: 'hash' })
    else if (url.pathname === '/files') {
      c115ListRequests.push({ cid: url.searchParams.get('cid'), offset: url.searchParams.get('offset') })
      json(response, {
        state: true,
        data: url.searchParams.get('cid') === '0'
          ? [{ cid: '10', n: 'Movies' }, { file_id: '20', n: 'Loose.mkv' }]
          : [],
      })
    } else if (url.pathname === '/rb/delete') json(response, { state: true })
    else response.writeHead(404).end()
  }, 10)
}

beforeAll(async () => {
  upstream = createServer(c115Response)
  upstreamBase = `http://127.0.0.1:${String(await listen(upstream))}`
  proxy = createServer((request, response) => {
    const raw = request.url ?? ''
    proxyRequests.push(raw)
    const url = new URL(raw)
    if (url.pathname === '/3/tv/123') json(response, { id: 123, name: 'Series', status: 'Returning Series' })
    else if (url.pathname === '/3/search/tv') json(response, { results: [{ id: 123, name: 'Series' }], total_pages: 1, total_results: 1 })
    else if (url.pathname === '/api/v1/search') {
      json(response, { code: 0, message: 'ok', data: { total: 1, limit: 80, offset: 0, has_more: false, query: 'Movie', exact: false, sort: 'relevance', disk_types: [{ disk_type: '115', count: 1 }], results: [{ title: 'Movie', url: 'https://115.com/s/abcde', disk_type: '115', source_channels: ['fixture'] }] } })
    } else response.writeHead(404).end()
  })
  proxyUrl = `http://127.0.0.1:${String(await listen(proxy))}`
})

afterAll(async () => {
  await Promise.all([upstream, proxy].map(server => new Promise<void>((resolve, reject) => server.close((error) => { if (error) reject(error); else resolve() }))))
})

describe('115, TMDB, and Resource clients', () => {
  it('parses and serializes every 115 critical section through one slot', async () => {
    expect(() => parseC115Share('https://evil.test/s/abcde')).toThrow(/115\.com or 115cdn\.com/)
    expect(parseC115Share('https://115cdn.com/s/abcde?password=xy')).toEqual({ shareCode: 'abcde', receiveCode: 'xy' })
    expect(parseC115Share('https://115.com/s/abcde?password=xy')).toEqual({ shareCode: 'abcde', receiveCode: 'xy' })
    const client = new C115Client({ apiBaseUrl: upstreamBase, siteBaseUrl: upstreamBase, cookie: 'UID=42_A1; CID=x' })
    maxActive115 = 0
    c115ListRequests.length = 0
    const signal = new AbortController().signal
    const [tested, snapshot, entries, directories] = await Promise.all([
      client.test(signal),
      client.snapshot('https://115.com/s/abcde', undefined, undefined, signal),
      client.listEntries('0', signal),
      client.listDirectories('0', signal),
    ])
    expect(tested).toEqual({ uid: '42', used: 12 })
    expect(snapshot).toMatchObject({ shareCode: 'abcde', title: 'Fixture', files: [{ id: 'f1', name: 'Movie.mkv', size: 10 }] })
    expect(entries).toEqual([
      { id: '10', name: 'Movies', directory: true },
      { id: '20', name: 'Loose.mkv', directory: false },
    ])
    expect(directories).toEqual([{ cid: '10', name: 'Movies' }])
    expect(maxActive115).toBe(1)
    await expect(client.saveShare('abcde', undefined, undefined, '10', signal)).resolves.toEqual({ count: 1, cid: '10' })
    await expect(client.offline('magnet:?xt=fixture', '10', signal)).resolves.toEqual({ accepted: true, infoHash: 'hash' })
    await expect(client.autoCid({ movie: 'Movies' }, {}, 2, signal)).resolves.toMatchObject({ matches: { movie: [{ cid: '10', path: 'Movies' }] } })
    expect(c115ListRequests).toEqual([
      { cid: '0', offset: '0' },
      { cid: '0', offset: '0' },
      { cid: '0', offset: '0' },
      { cid: '10', offset: '0' },
    ])
    await expect(client.treeHash('0', signal)).resolves.toMatch(/^[0-9a-f]{64}$/)
    await expect(client.deleteIds('10', ['f1'], signal)).resolves.toBeUndefined()
  })

  it('captures bounded recursive evidence without changing transferable roots', async () => {
    const client = new C115Client({ apiBaseUrl: upstreamBase, siteBaseUrl: upstreamBase, cookie: 'UID=42_A1; CID=x' })

    const snapshot = await client.snapshot('https://115.com/s/recursive', undefined, undefined, new AbortController().signal)

    expect(snapshot.files).toEqual([{ id: 'series-root', name: 'Series Pack', directory: true }])
    expect(snapshot.evidence).toEqual([
      { id: 'e1', name: 'Series.S01E01.mkv', size: 10, directory: false, path: 'Series Pack/Series.S01E01.mkv' },
      { id: 'e2', name: 'Series.S01E02.mkv', size: 11, directory: false, path: 'Series Pack/Series.S01E02.mkv' },
    ])
  })

  it('bounds cyclic share trees and rejects non-advancing pages', async () => {
    const visited: string[] = []
    const cyclic = new C115Client({
      cookie: 'UID=42_A1; CID=x',
      fetch: async (input) => {
        const url = new URL(String(input))
        const cid = url.searchParams.get('cid') ?? '0'
        visited.push(cid)
        const list = cid === '0' ? [{ cid: 'a', n: 'Root' }] : cid === 'a' ? [{ cid: 'b', n: 'B' }] : [{ cid: 'a', n: 'A' }]
        return new Response(JSON.stringify({ state: true, data: { list, total: 1 } }), { status: 200, headers: { 'content-type': 'application/json' } })
      },
    })
    const snapshot = await cyclic.snapshot('https://115.com/s/cycle', undefined, undefined, new AbortController().signal)
    expect(visited).toEqual(['0', 'a', 'b'])
    expect(snapshot.evidence).toHaveLength(2)

    const repeatedPage = Array.from({ length: 1_000 }, (_, index) => ({ fid: `f${String(index)}`, n: `File${String(index)}.mkv` }))
    const stalled = new C115Client({
      cookie: 'UID=42_A1; CID=x',
      fetch: async () => new Response(JSON.stringify({ state: true, data: { list: repeatedPage, total: 3_000 } }), {
        status: 200, headers: { 'content-type': 'application/json' },
      }),
    })
    await expect(stalled.snapshot('https://115.com/s/stalled', undefined, undefined, new AbortController().signal))
      .rejects.toThrow(/pagination did not advance/)
  })

  it('rejects malformed or rejected 115 directory listings as unknown state', async () => {
    for (const body of [
      { state: false, data: [] },
      { state: true, data: {} },
      { state: true, data: [{ n: 'missing-id' }] },
    ]) {
      const client = new C115Client({
        cookie: 'UID=42_A1; CID=x',
        fetch: async () => new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      })
      await expect(client.listEntries('0', new AbortController().signal)).rejects.toMatchObject({
        code: 'UPSTREAM_UNAVAILABLE',
      })
    }
  })

  it('rejects credentialed remote HTTP unless deployment opts in explicitly', () => {
    expect(() => new ResourceApiClient({ baseUrl: 'http://resource.invalid', token: 'token' })).toThrow(/requires HTTPS/)
    expect(() => new ResourceApiClient({ baseUrl: 'http://127.0.0.1:8100', token: 'token' })).not.toThrow()
  })

  it('routes only TMDB and Resource requests through the configured proxy', async () => {
    proxyRequests.length = 0
    const signal = new AbortController().signal
    const tmdb = new TmdbClient({ baseUrl: 'http://tmdb.invalid', apiKey: 'key', proxyUrl })
    const resource = new ResourceApiClient({ baseUrl: 'http://resource.invalid', token: 'token', proxyUrl, allowInsecureHttp: true })
    await expect(tmdb.tv(123, signal)).resolves.toMatchObject({ id: 123, name: 'Series' })
    await expect(tmdb.search('tv', 'Series', undefined, 1, signal)).resolves.toMatchObject({ totalResults: 1 })
    await expect(resource.search('Movie', {}, signal)).resolves.toMatchObject({ items: [{ title: 'Movie', diskType: '115' }] })
    expect(tmdb.proxied).toBe(true)
    expect(resource.proxied).toBe(true)
    expect(proxyRequests).toHaveLength(3)
    expect(proxyRequests.some(url => new URL(url).searchParams.get('api_key') === 'key')).toBe(true)
    expect(proxyRequests.some(url => new URL(url).hostname === 'resource.invalid')).toBe(true)
  })

  it('uses proxy-side DNS for SOCKS5 URLs', () => {
    const implicit = new ProxyHttpTransport({ proxyUrl: 'socks5://proxy.local:1080' }) as unknown as { proxyUrl: URL }
    const explicit = new ProxyHttpTransport({ proxyUrl: 'socks5h://proxy.local:1080' }) as unknown as { proxyUrl: URL }
    expect(implicit.proxyUrl.protocol).toBe('socks5h:')
    expect(explicit.proxyUrl.protocol).toBe('socks5h:')
  })
})

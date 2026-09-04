import { describe, expect, it } from 'vitest'
import { assertPublicImageUrl, cleanMediaName, PosterDomainService } from '../src/domain/poster.ts'
import type { EmbyClient } from '../src/clients/emby.ts'
import type { TmdbClient } from '../src/clients/tmdb.ts'
import { ProxyHttpTransport } from '../src/clients/http.ts'

describe('poster and metadata repair', () => {
  it('preserves useful legacy name cleanup and year matching heuristics', () => {
    expect(cleanMediaName('The.Movie.2024.2160p.BluRay.REMUX.DV.HDR')).toEqual({ name: 'The Movie', year: 2024 })
    expect(cleanMediaName('剧集名称 [国语中字] 1080p WEB-DL')).toEqual({ name: '剧集名称' })
    expect(cleanMediaName('能有多大事 [全30集][臻彩MAXPLUS]')).toEqual({ name: '能有多大事' })
    expect(cleanMediaName('满分恋人 [全 25 集][臻彩 MAXPLUS] (2026)')).toEqual({ name: '满分恋人', year: 2026 })
    expect(cleanMediaName('F1：狂飙飞车 内嵌官方简繁字幕 F1 (2025)')).toEqual({ name: 'F1：狂飙飞车 内嵌官方简繁字幕 F1', year: 2025 })
  })

  it('rejects private, loopback, credentialed, and non-HTTP image URLs', async () => {
    for (const url of ['http://127.0.0.1/image.jpg', 'http://192.168.1.2/image.jpg', 'file:///etc/passwd', 'https://user:pass@example.com/image.jpg']) {
      await expect(assertPublicImageUrl(url)).rejects.toMatchObject({ code: 'POLICY_DENIED' })
    }
  })

  it('searches deterministically, applies RemoteSearch, refreshes, and verifies TMDB identity', async () => {
    const calls: string[] = []
    const emby = {
      applyRemoteSearch: async () => { calls.push('apply') },
      refreshItem: async () => { calls.push('refresh') },
      item: async () => ({ Id: 'item-1', Name: 'The Movie', ProductionYear: 2024, ProviderIds: { Tmdb: '42' } }),
    } as unknown as EmbyClient
    const tmdb = {
      search: async () => ({
        results: [
          { id: 7, title: 'Other Movie', release_date: '2024-01-01' },
          { id: 42, title: 'The Movie', release_date: '2024-01-01', poster_path: '/poster.jpg' },
        ],
        totalPages: 1,
        totalResults: 2,
      }),
    } as unknown as TmdbClient
    const service = new PosterDomainService(async () => emby, async () => tmdb, new ProxyHttpTransport())
    const candidates = await service.search('The.Movie.2024.2160p', undefined, 'movie', new AbortController().signal)
    expect(candidates[0]).toMatchObject({ tmdbId: '42', name: 'The Movie', year: 2024, score: 100 })

    await expect(service.apply('item-1', '42', new AbortController().signal)).resolves.toMatchObject({ ProviderIds: { Tmdb: '42' } })
    expect(calls).toEqual(['apply', 'refresh'])
  })

  it('locks item poster against automatic metadata overwrite', async () => {
    const calls: Array<{ itemId: string; body: unknown }> = []
    const emby = {
      updateItem: async (itemId: string, body: unknown) => { calls.push({ itemId, body }) },
    } as unknown as EmbyClient
    const service = new PosterDomainService(async () => emby, async () => ({} as TmdbClient), new ProxyHttpTransport())
    await service.lockPoster('lib-123', new AbortController().signal)
    expect(calls).toEqual([{ itemId: 'lib-123', body: { LockData: true, LockedFields: ['PrimaryImage', 'All'] } }])
  })
})

import { describe, expect, it, vi } from 'vitest'
import type { EmbyClient, EmbyItem } from '../src/clients/emby.ts'
import type { ResourceApiClient, ResourceSearchResult } from '../src/clients/resource-api.ts'
import type { TmdbClient } from '../src/clients/tmdb.ts'
import { EmbymediaError } from '../src/errors.ts'
import {
  absoluteEpisodeKeysFromText,
  computeEpisodeGaps,
  declaredTmdbId,
  episodeKeysFromText,
  normalizedSeriesName,
  SeriesDomainService,
  seriesFolderFromPath,
  type ArchiveRequest,
} from '../src/domain/series.ts'

const library = { id: 'library-1', name: 'Shows', collectionType: 'tvshows', locations: ['/strm/Shows'] }
const canonicalItem: EmbyItem = {
  Id: 'series-1',
  Name: 'Sample Show',
  Path: '/strm/Shows/Sample Show (2020) [tmdbid-123]',
  ProviderIds: { Tmdb: '123' },
}
const expectedEpisodes = [
  { id: 1, season_number: 1, episode_number: 1, name: 'Pilot', air_date: '2020-01-01', absolute_number: 1 },
  { id: 2, season_number: 1, episode_number: 2, name: 'Second', air_date: '2020-01-08', absolute_number: 2 },
]
const searchResult: ResourceSearchResult = {
  items: [{ title: 'Sample Show S01E02', url: 'https://115.com/s/example', sourceChannels: ['channel'] }],
  total: 1,
  limit: 80,
  offset: 0,
  hasMore: false,
  query: 'Sample Show S01E02',
  exact: false,
  sort: 'relevance',
  diskTypes: [],
}

function createService(
  items: readonly EmbyItem[] = [canonicalItem],
  tmdbOverrides: Partial<Pick<TmdbClient, 'tv' | 'season'>> = {},
) {
  const emby = {
    libraries: async () => [library],
    items: async () => items,
    itemPage: async (_libraryId: string, _itemTypes: string, _fields: string, limit: number, _signal: AbortSignal, _search?: string, offset = 0) => ({ items: items.slice(offset, offset + limit), total: items.length }),
    item: async (id: string) => items.find(item => item.Id === id),
    episodes: async () => [{ Id: 'episode-1', Name: 'Pilot', ParentIndexNumber: 1, IndexNumber: 1 }],
  } as unknown as EmbyClient
  const tmdb = {
    tv: async () => ({
      id: 123,
      name: 'Sample Show',
      status: 'Returning Series',
      seasons: [{ season_number: 1, episode_count: 2 }],
    }),
    season: async () => expectedEpisodes,
    ...tmdbOverrides,
  } as unknown as TmdbClient
  const search = vi.fn(async () => searchResult)
  const resource = { search } as unknown as ResourceApiClient
  const service = new SeriesDomainService(
    async () => emby,
    async () => tmdb,
    async () => resource,
    {
      moveCloud: async () => ({ ok: true }),
      moveStrm: async () => ({ ok: true }),
      notifyEmby: async () => ({ ok: true }),
      verify: async () => ({ ok: true }),
    },
  )
  return { service, search }
}

describe('series identity helpers', () => {
  it('recognizes only the supported TMDB marker forms', () => {
    expect(declaredTmdbId('Show [tmdbid-123]')).toBe('123')
    expect(declaredTmdbId('Show (tmdbid_456)')).toBe('456')
    expect(declaredTmdbId('Show.tmdb-789')).toBe('789')
    expect(declaredTmdbId('Show retmdbid-123')).toBeUndefined()
    expect(declaredTmdbId('Show tmdbid-123extra')).toBeUndefined()
  })

  it('normalizes equivalent decorated series names without fuzzy matching', () => {
    const plain = normalizedSeriesName('Sample Show')
    expect(normalizedSeriesName('Sample.Show (2020) [tmdbid-123]')).toBe(plain)
    expect(normalizedSeriesName('Sample-Show 2020 tmdbid_123')).toBe(plain)
    expect(normalizedSeriesName('Sample Show {tmdb-123}')).toBe(plain)
    expect(normalizedSeriesName('Sample Shows')).not.toBe(plain)
  })

  it('expands same-season episode ranges and rejects ambiguous ranges', () => {
    expect([...episodeKeysFromText('Sample Show S01E02-S01E04 and S02E005~E006')]).toEqual([
      '1:2',
      '1:3',
      '1:4',
      '2:5',
      '2:6',
    ])
    expect([...episodeKeysFromText('S03E07-09')]).toEqual(['3:7', '3:8', '3:9'])
    expect([...episodeKeysFromText('S01E08-S02E02 S02E09-E08 S00E01')]).toEqual([])
  })

  it('expands standalone absolute episode ranges without matching season tokens', () => {
    expect([...absoluteEpisodeKeysFromText('Sample Show E13-E15 and S02E03')]).toEqual([
      'absolute:13',
      'absolute:14',
      'absolute:15',
    ])
  })

  it('resolves only a direct folder under the canonical STRM library root', () => {
    const folder = 'Sample Show (2020) [tmdbid-123]'
    expect(seriesFolderFromPath(`/strm/Shows/${folder}`, 'Shows')).toBe(folder)
    expect(seriesFolderFromPath(`/strm/Shows/Collection/${folder}`, 'Shows')).toBeUndefined()
    expect(seriesFolderFromPath(`/other/Shows/${folder}`, 'Shows')).toBeUndefined()
    expect(seriesFolderFromPath('/strm/Shows', 'Shows')).toBeUndefined()
    expect(seriesFolderFromPath(undefined, 'Shows')).toBeUndefined()
  })
})

describe('series follow-up and gaps', () => {
  it('preserves season and absolute numbering semantics and excludes future episodes', () => {
    const expected = [
      ...expectedEpisodes,
      { id: 3, season_number: 2, episode_number: 1, absolute_number: 3, air_date: '2027-01-01' },
    ]
    const now = new Date('2026-06-01T00:00:00Z')
    expect(computeEpisodeGaps([{ season: 1, episode: 1 }], expected, 'season', now)).toEqual([
      expect.objectContaining({ season: 1, episode: 2, absolute: 2 }),
    ])
    expect(computeEpisodeGaps([{ absolute: 2 }], expected, 'absolute', now)).toEqual([
      expect.objectContaining({ season: 1, episode: 1, absolute: 1 }),
    ])
  })

  it('returns canonical binding fields and aired gaps while flagging noncanonical targets', async () => {
    const nested: EmbyItem = {
      Id: 'series-nested',
      Name: 'Nested Show',
      Path: '/strm/Shows/Collection/Nested Show [tmdbid-456]',
      ProviderIds: { Tmdb: '456' },
    }
    const mismatched: EmbyItem = {
      Id: 'series-mismatch',
      Name: 'Wrong Binding',
      Path: '/strm/Shows/Wrong Binding [tmdb-999]',
      ProviderIds: { Tmdb: '789' },
    }
    const { service } = createService([canonicalItem, nested, mismatched])

    const rows = await service.status('library-1', 'season', new AbortController().signal)

    expect(rows[0]).toEqual({
      id: 'series-1',
      libraryId: 'library-1',
      libraryName: 'Shows',
      name: 'Sample Show',
      path: '/strm/Shows/Sample Show (2020) [tmdbid-123]',
      folder: 'Sample Show (2020) [tmdbid-123]',
      tmdbId: '123',
      tmdbStatus: 'Returning Series',
      localCount: 1,
      missingCount: 1,
      lane: 'update_needed',
      gaps: [{ season: 1, episode: 2, absolute: 2, airDate: '2020-01-08', name: 'Second' }],
    })
    expect(rows[1]).toMatchObject({
      id: 'series-nested',
      libraryId: 'library-1',
      libraryName: 'Shows',
      path: nested.Path,
      tmdbId: '456',
      lane: 'target_error',
    })
    expect(rows[1]?.folder).toBeUndefined()
    expect(rows[2]).toMatchObject({
      id: 'series-mismatch',
      path: mismatched.Path,
      folder: 'Wrong Binding [tmdb-999]',
      tmdbId: '789',
      lane: 'target_error',
    })
  })

  it.each([
    {
      Id: 'series-nested',
      Name: 'Nested Show',
      Path: '/strm/Shows/Collection/Nested Show [tmdbid-456]',
      ProviderIds: { Tmdb: '456' },
    },
    {
      Id: 'series-mismatch',
      Name: 'Wrong Binding',
      Path: '/strm/Shows/Wrong Binding [tmdbid_999]',
      ProviderIds: { Tmdb: '789' },
    },
  ] satisfies readonly EmbyItem[])('rejects noncanonical detail for $Id', async (item) => {
    const { service } = createService([item])

    await expect(service.detail('library-1', item.Id, 'season', new AbortController().signal)).rejects.toMatchObject({
      code: 'POLICY_DENIED',
    })
  })

  it('preserves typed inventory blockers and never swallows cancellation', async () => {
    const blocked = createService([canonicalItem], {
      tv: async () => { throw new EmbymediaError('AUTH_REQUIRED', 'TMDB authentication failed') },
    }).service
    await expect(blocked.gapsSummary('library-1', 'season', new AbortController().signal)).resolves.toMatchObject({
      rows: [{ id: 'series-1', lane: 'unknown', blocker: { code: 'AUTH_REQUIRED', message: 'TMDB authentication failed' } }],
      laneCounts: { unknown: 1 },
    })

    const controller = new AbortController()
    const cancelled = createService([canonicalItem], {
      tv: async () => {
        controller.abort()
        throw new EmbymediaError('CANCELLED', 'TMDB lookup cancelled')
      },
    }).service
    await expect(cancelled.status('library-1', 'season', controller.signal)).rejects.toMatchObject({ code: 'CANCELLED' })
  })

  it('returns the canonical Series binding, requested gaps, query, and search result in a resource plan', async () => {

    const { service, search } = createService()

    const plan = await service.resourcePlan('library-1', 'series-1', 'season', new AbortController().signal)

    expect(plan).toEqual({
      series: {
        id: 'series-1',
        libraryId: 'library-1',
        libraryName: 'Shows',
        name: 'Sample Show',
        path: '/strm/Shows/Sample Show (2020) [tmdbid-123]',
        folder: 'Sample Show (2020) [tmdbid-123]',
        tmdbId: '123',
        tmdbStatus: 'Returning Series',
        localCount: 1,
        missingCount: 1,
        lane: 'update_needed',
        gaps: [{ season: 1, episode: 2, absolute: 2, airDate: '2020-01-08', name: 'Second' }],
      },
      query: 'Sample Show S01E02',
      gapTotal: 1,
      deferredGapCount: 0,
      searchHasMore: false,
      requestedEpisodes: [{ season: 1, episode: 2, absolute: 2, airDate: '2020-01-08', name: 'Second' }],
      numberingMode: 'season',
      search: searchResult,
    })
    expect(search).toHaveBeenCalledWith('Sample Show S01E02', { limit: 80, exact: false, diskType: '115' }, expect.any(AbortSignal))
  })


  it('reports deferred gaps and candidate pagination explicitly', async () => {
    const { service, search } = createService()
    const gaps = Array.from({ length: 25 }, (_, index) => ({ season: 1, episode: index + 1, absolute: index + 1 }))
    vi.spyOn(service, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-1', libraryName: 'Shows', name: 'Sample Show',
      path: '/strm/Shows/Sample Show (2020) [tmdbid-123]', folder: 'Sample Show (2020) [tmdbid-123]',
      tmdbId: '123', tmdbStatus: 'Ended', localCount: 0, missingCount: 25, lane: 'complete_after_update', gaps,
    })
    search.mockResolvedValueOnce({ ...searchResult, hasMore: true })

    const plan = await service.resourcePlan('library-1', 'series-1', 'season', new AbortController().signal)

    expect(plan.requestedEpisodes).toHaveLength(20)
    expect(plan).toMatchObject({ gapTotal: 25, deferredGapCount: 5, searchHasMore: true })
  })
  it('falls back to a 115-only Series-name query when episode terms return no candidates', async () => {
    const { service, search } = createService()
    const empty = { ...searchResult, items: [], total: 0, query: 'Sample Show S01E02' }
    const broad = { ...searchResult, query: 'Sample Show' }
    search.mockResolvedValueOnce(empty).mockResolvedValueOnce(broad)

    const plan = await service.resourcePlan('library-1', 'series-1', 'season', new AbortController().signal)

    expect(plan.query).toBe('Sample Show')
    expect(plan.search).toBe(broad)
    expect(search).toHaveBeenNthCalledWith(1, 'Sample Show S01E02', { limit: 80, exact: false, diskType: '115' }, expect.any(AbortSignal))
    expect(search).toHaveBeenNthCalledWith(2, 'Sample Show', { limit: 80, exact: false, diskType: '115' }, expect.any(AbortSignal))
  })

  it('uses absolute episode keys in resource queries', async () => {
    const { service, search } = createService()
    await service.resourcePlan('library-1', 'series-1', 'absolute', new AbortController().signal)
    expect(search).toHaveBeenCalledWith('Sample Show E02', { limit: 80, exact: false, diskType: '115' }, expect.any(AbortSignal))
  })

  it('returns a bounded summary containing only incomplete Series', async () => {
    const { service } = createService()
    const incomplete = {
      id: 'missing', libraryId: 'library-1', libraryName: 'Shows', name: 'Missing Show',
      path: '/strm/Shows/Missing Show', folder: 'Missing Show', tmdbId: '456', tmdbStatus: 'Ended',
      localCount: 1, missingCount: 2, lane: 'complete_after_update' as const,
      gaps: [{ season: 1, episode: 2 }, { season: 1, episode: 3 }],
    }
    const complete = { ...incomplete, id: 'complete', missingCount: 0, lane: 'archive_ready' as const, gaps: [] }
    vi.spyOn(service, 'statusPage').mockResolvedValue({ rows: [incomplete, complete], total: 2, offset: 0, limit: 100, hasMore: false })

    const summary = await service.gapsSummary('library-1', 'season', new AbortController().signal)

    expect(summary).toEqual({
      rows: [{
        id: 'missing', libraryId: 'library-1', libraryName: 'Shows', name: 'Missing Show',
        tmdbId: '456', tmdbStatus: 'Ended', localCount: 1, missingCount: 2, lane: 'complete_after_update',
      }],
      laneCounts: { complete_after_update: 1 },
      statusCounts: { Ended: 1 },
      behindTotal: 2,
      totalSeries: 2,
      offset: 0,
      limit: 100,
      hasMore: false,
    })
  })

  it('archives cloud, STRM, Emby, then verifies', async () => {
    const calls: string[] = []
    const service = new SeriesDomainService(
      async () => ({}) as EmbyClient,
      async () => ({}) as TmdbClient,
      async () => ({}) as ResourceApiClient,
      {
        moveCloud: async () => { calls.push('cloud'); return { ok: true } },
        moveStrm: async () => { calls.push('strm'); return { ok: true } },
        notifyEmby: async () => { calls.push('emby'); return { ok: true } },
        verify: async () => { calls.push('verify'); return { ok: true } },
      },
    )
    const archive: ArchiveRequest = {
      seriesId: 'series-1', fromLibraryId: 'a', toLibraryId: 'b', fromLibrary: 'A', toLibrary: 'B', folder: 'Series',
    }

    await expect(service.archive(archive, new AbortController().signal)).resolves.toMatchObject({ verification: { ok: true } })
    expect(calls).toEqual(['cloud', 'strm', 'emby', 'verify'])
  })
})

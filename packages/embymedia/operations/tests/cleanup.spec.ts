import { mkdtemp, mkdir, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { C115Client, C115Entry } from '../src/clients/c115.ts'
import type { EmbyClient, EmbyItem } from '../src/clients/emby.ts'
import { MediaCleanupDomainService } from '../src/domain/cleanup.ts'

const signal = new AbortController().signal

describe('media cleanup preparation and verification', () => {
  let root: string
  let mediaRoot: string
  let strmRoot: string
  let embyItems: Map<string, EmbyItem>
  let cloudEntries: C115Entry[]
  let cloudLookups: string[]
  let service: MediaCleanupDomainService

  beforeEach(async () => {
    root = await mkdtemp(join(tmpdir(), 'embymedia-cleanup-'))
    mediaRoot = join(root, 'media')
    strmRoot = join(root, 'strm')
    await mkdir(mediaRoot)
    await mkdir(strmRoot)
    embyItems = new Map()
    cloudEntries = []
    cloudLookups = []
    const emby = {
      item: async (itemId: string) => embyItems.get(itemId),
      items: async () => [...embyItems.values()].filter(item => item.Type === 'Series'),
      itemsByPath: async (_libraryId: string, path: string) => [...embyItems.values()].filter(item => item.Path === path || item.Path?.startsWith(`${path}/`) === true),
    } as unknown as EmbyClient
    const c115 = {
      listEntries: async (cid: string) => {
        cloudLookups.push(cid)
        return cloudEntries
      },
      treeHash: async (cid: string) => `tree:${cid}`,
    } as unknown as C115Client
    service = new MediaCleanupDomainService(
      { mediaRoot, strmRoot },
      { libraries: async () => [{ id: 'library-1', name: 'Shows', collectionType: 'tvshows', locations: ['/strm/Shows'] }] },
      async () => emby,
      async () => c115,
      async libraryName => libraryName === 'Shows' ? '115-root' : 'wrong-root',
    )
  })

  afterEach(async () => {
    await rm(root, { recursive: true, force: true })
  })

  it('prepares and verifies an exact standalone episode at the library root', async () => {
    await mkdir(join(strmRoot, 'Shows'))
    await writeFile(join(strmRoot, 'Shows', 'Bad Episode.strm'), '/media/Shows/Bad Episode.mkv\n')
    embyItems.set('episode-bad', {
      Id: 'episode-bad',
      Name: 'Bad Standalone Episode',
      Type: 'Episode',
      Path: '/strm/Shows/Bad Episode.strm',
    })
    cloudEntries = [{ id: 'cloud-episode', name: 'Bad Episode.mkv', directory: false }]

    const prepared = await service.prepareMediaDelete('library-1', ['episode-bad'], signal)

    expect(prepared.deleteTargets).toHaveLength(1)
    expect(prepared.deleteTargets[0]).toMatchObject({
      id: 'library-1:episode-bad',
      embyItemId: 'episode-bad',
      strm: { relativePath: 'Shows/Bad Episode.strm', type: 'file' },
      cloudParentCid: '115-root',
      cloudIds: ['cloud-episode'],
    })
    expect(prepared.targets).toHaveLength(1)
    expect(prepared.targets[0]).toMatchObject({
      id: 'episode-bad',
      label: 'Bad Standalone Episode / Bad Episode.mkv',
      canonicalLibraryId: 'library-1',
      canonicalCid: '115-root',
      path: 'Shows/Bad Episode.strm',
    })
    expect(prepared.cloudNames).toEqual([{ itemId: 'episode-bad', name: 'Bad Episode.mkv', id: 'cloud-episode' }])

    await rm(join(strmRoot, 'Shows', 'Bad Episode.strm'))
    embyItems.delete('episode-bad')
    cloudEntries = []
    const verified = await service.verify(prepared.deleteTargets, undefined, signal)
    expect(verified).toEqual({
      ok: true,
      facts: {
        targets: [{ id: 'library-1:episode-bad', embyAbsent: true, strmAbsent: true, cloudAbsent: true }],
      },
    })
    expect(cloudLookups).toEqual(['115-root', '115-root'])
  })

  it('requires and verifies one explicit keeper for the complete duplicate Series group', async () => {
    await mkdir(join(strmRoot, 'Shows', 'Series old'), { recursive: true })
    await mkdir(join(strmRoot, 'Shows', 'Series keeper'), { recursive: true })
    embyItems.set('series-old', {
      Id: 'series-old', Name: 'Series old', Type: 'Series', Path: '/strm/Shows/Series old', ProviderIds: { Tmdb: '42' },
    })
    embyItems.set('series-keeper', {
      Id: 'series-keeper', Name: 'Series keeper', Type: 'Series', Path: '/strm/Shows/Series keeper', ProviderIds: { Tmdb: '42' },
    })
    cloudEntries = [
      { id: 'cloud-old', name: 'Series old', directory: true },
      { id: 'cloud-keeper', name: 'Series keeper', directory: true },
    ]

    const prepared = await service.prepareDedupDelete(
      'library-1',
      '42',
      'series-keeper',
      ['series-old'],
      signal,
    )
    expect(prepared.dedupExpectation).toEqual({ libraryId: 'library-1', tmdbId: '42', keepItemId: 'series-keeper' })
    expect(prepared.cloudNames).toEqual([{ itemId: 'series-old', name: 'Series old', id: 'cloud-old' }])
    expect(prepared.deleteTargets[0]?.strm).toMatchObject({ relativePath: 'Shows/Series old', type: 'directory' })

    await rm(join(strmRoot, 'Shows', 'Series old'), { recursive: true })
    embyItems.delete('series-old')
    cloudEntries = [{ id: 'cloud-keeper', name: 'Series keeper', directory: true }]
    await expect(service.verify(prepared.deleteTargets, prepared.dedupExpectation, signal)).resolves.toEqual({
      ok: true,
      facts: {
        targets: [{ id: 'library-1:series-old', embyAbsent: true, strmAbsent: true, cloudAbsent: true }],
        dedup: {
          libraryId: 'library-1',
          tmdbId: '42',
          keepItemId: 'series-keeper',
          currentItemIds: ['series-keeper'],
          keeperRetained: true,
        },
      },
    })
  })

  it('binds an isolated nested Series to its direct library-root directory', async () => {
    await mkdir(join(strmRoot, 'Shows', 'Orphan Root', 'S'), { recursive: true })
    await mkdir(join(strmRoot, 'Shows', 'Series keeper'), { recursive: true })
    embyItems.set('series-orphan', {
      Id: 'series-orphan', Name: 'Series orphan', Type: 'Series', Path: '/strm/Shows/Orphan Root/S', ProviderIds: { Tmdb: '42' },
    })
    embyItems.set('series-keeper', {
      Id: 'series-keeper', Name: 'Series keeper', Type: 'Series', Path: '/strm/Shows/Series keeper', ProviderIds: { Tmdb: '42' },
    })
    cloudEntries = [
      { id: 'cloud-orphan', name: 'Series orphan (2026) {tmdb-42}', directory: true },
      { id: 'cloud-keeper', name: 'Series keeper', directory: true },
    ]
    const prepared = await service.prepareDedupDelete('library-1', '42', 'series-keeper', ['series-orphan'], signal)

    expect(prepared.deleteTargets[0]).toMatchObject({
      embyItemId: 'series-orphan',
      embyPath: '/strm/Shows/Orphan Root/S',
      strm: { relativePath: 'Shows/Orphan Root', type: 'directory' },
      cloudIds: ['cloud-orphan'],
      cloudName: 'Series orphan (2026) {tmdb-42}',
    })
    expect(prepared.targets[0]).toMatchObject({ path: 'Shows/Orphan Root' })

    cloudEntries = [
      { id: 'cloud-orphan', name: 'Masked Root', directory: true },
      { id: 'cloud-keeper', name: 'Series keeper', directory: true },
    ]
    const explicit = await service.prepareDedupDelete(
      'library-1', '42', 'series-keeper', ['series-orphan'], signal, { 'series-orphan': 'cloud-orphan' },
    )
    expect(explicit.deleteTargets[0]).toMatchObject({ cloudIds: ['cloud-orphan'], cloudName: 'Masked Root' })
    await expect(service.prepareDedupDelete(
      'library-1', '42', 'series-keeper', ['series-orphan'], signal, { other: 'cloud-orphan' },
    )).rejects.toThrow(/must name only removal targets/)

    embyItems.set('series-neighbor', {
      Id: 'series-neighbor', Name: 'Neighbor', Type: 'Series', Path: '/strm/Shows/Orphan Root/Other', ProviderIds: { Tmdb: '99' },
    })
    await expect(service.prepareDedupDelete('library-1', '42', 'series-keeper', ['series-orphan'], signal))
      .rejects.toThrow(/contains an unselected Series/)
  })


  it('binds an isolated nested Movie to its direct library-root folder', async () => {
    await mkdir(join(strmRoot, 'Shows', 'Fall 4K'), { recursive: true })
    await writeFile(join(strmRoot, 'Shows', 'Fall 4K', 'Fall.strm'), '/media/Shows/Fall 4K/Fall.mkv\n')
    await mkdir(join(mediaRoot, 'Shows', 'Fall 4K'), { recursive: true })
    embyItems.set('movie-fall', {
      Id: 'movie-fall', Name: '坠落 4K原盘REMUX', Type: 'Movie', Path: '/strm/Shows/Fall 4K/Fall.strm',
    })
    cloudEntries = [{ id: 'cloud-fall', name: 'Fall 4K', directory: true }]

    const prepared = await service.prepareMediaDelete('library-1', ['movie-fall'], signal)
    expect(prepared.deleteTargets[0]).toMatchObject({
      embyItemId: 'movie-fall', strm: { relativePath: 'Shows/Fall 4K', type: 'directory' },
      cloudIds: ['cloud-fall'], cloudName: 'Fall 4K', cloudDirectory: true,
    })
    expect(prepared.targets[0]).toMatchObject({ id: 'movie-fall', path: 'Shows/Fall 4K' })

    embyItems.set('movie-neighbor', { Id: 'movie-neighbor', Name: 'Other', Type: 'Movie', Path: '/strm/Shows/Fall 4K/Other.strm' })
    await expect(service.prepareMediaDelete('library-1', ['movie-fall'], signal)).rejects.toThrow(/contains an unselected Emby media item/)
    const grouped = await service.prepareMediaDelete('library-1', ['movie-fall', 'movie-neighbor'], signal)
    expect(grouped.deleteTargets.filter(target => target.strm !== undefined)).toEqual([
      expect.objectContaining({ embyItemId: 'movie-neighbor', cloudIds: ['cloud-fall'] }),
    ])
    expect(grouped.deleteTargets.find(target => target.embyItemId === 'movie-fall')).not.toHaveProperty('strm')
  })

  it('rejects nested, duplicate, and oversized media deletion requests', async () => {
    embyItems.set('nested', {
      Id: 'nested', Name: 'Nested Episode', Type: 'Episode', Path: '/strm/Shows/Season 01/Episode.strm',
    })
    await expect(service.prepareMediaDelete('library-1', ['nested'], signal)).rejects.toMatchObject({ code: 'POLICY_DENIED' })
    await expect(service.prepareMediaDelete('library-1', ['nested', 'nested'], signal)).rejects.toMatchObject({ code: 'INVALID_INPUT' })
    await expect(service.prepareMediaDelete('library-1', Array.from({ length: 101 }, (_, index) => `item-${String(index)}`), signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })

  it('groups a complete Series selection by direct library roots without a keeper', async () => {
    for (const path of ['Series A', 'Series B/S01', 'Series B/S02']) {
      await mkdir(join(strmRoot, 'Shows', path), { recursive: true })
      await writeFile(join(strmRoot, 'Shows', path, 'episode.strm'), `/media/Shows/${path}/episode.mkv\n`)
      await mkdir(join(mediaRoot, 'Shows', path), { recursive: true })
    }
    embyItems.set('series-a', { Id: 'series-a', Name: 'Series A', Type: 'Series', Path: '/strm/Shows/Series A', ProviderIds: { Tmdb: '1' } })
    embyItems.set('series-b1', { Id: 'series-b1', Name: 'S01', Type: 'Series', Path: '/strm/Shows/Series B/S01', ProviderIds: {} })
    embyItems.set('series-b2', { Id: 'series-b2', Name: 'S02', Type: 'Series', Path: '/strm/Shows/Series B/S02', ProviderIds: {} })
    cloudEntries = [
      { id: 'cloud-a', name: 'Series A', directory: true },
      { id: 'cloud-b', name: 'Series B', directory: true },
    ]

    await expect(service.prepareMediaDelete('library-1', ['series-b1'], signal)).rejects.toThrow(/unselected Series/)
    const prepared = await service.prepareMediaDelete('library-1', ['series-a', 'series-b1', 'series-b2'], signal)
    expect(prepared.deleteTargets).toHaveLength(3)
    expect(prepared.deleteTargets.filter(target => target.strm !== undefined)).toEqual([
      expect.objectContaining({ embyItemId: 'series-a', cloudIds: ['cloud-a'] }),
      expect.objectContaining({ embyItemId: 'series-b2', cloudIds: ['cloud-b'] }),
    ])
    expect(prepared.deleteTargets.find(target => target.embyItemId === 'series-b1')).not.toHaveProperty('strm')
    expect(prepared.deleteTargets.find(target => target.embyItemId === 'series-b1')).not.toHaveProperty('cloudIds')
  })

  it('rejects symlinked STRM roots before destructive snapshotting', async () => {
    await mkdir(join(strmRoot, 'Shows', 'Real'), { recursive: true })
    await symlink(join(strmRoot, 'Shows', 'Real'), join(strmRoot, 'Shows', 'Linked'))
    embyItems.set('linked', { Id: 'linked', Name: 'Linked', Type: 'Episode', Path: '/strm/Shows/Linked' })
    cloudEntries = [{ id: 'cloud-linked', name: 'Linked', directory: true }]
    await expect(service.prepareMediaDelete('library-1', ['linked'], signal)).rejects.toThrow(/symlink/)
  })

  it('treats a recreated same-name 115 entry as still present', async () => {
    await mkdir(join(strmRoot, 'Shows'))
    await writeFile(join(strmRoot, 'Shows', 'Bad Episode.strm'), '/media/Shows/Bad Episode.mkv\n')
    embyItems.set('episode-bad', { Id: 'episode-bad', Name: 'Bad', Type: 'Episode', Path: '/strm/Shows/Bad Episode.strm' })
    cloudEntries = [{ id: 'cloud-old', name: 'Bad Episode.mkv', directory: false }]
    const prepared = await service.prepareMediaDelete('library-1', ['episode-bad'], signal)
    await rm(join(strmRoot, 'Shows', 'Bad Episode.strm'))
    embyItems.delete('episode-bad')
    cloudEntries = [{ id: 'cloud-new', name: 'Bad Episode.mkv', directory: false }]
    await expect(service.verify(prepared.deleteTargets, undefined, signal)).resolves.toMatchObject({
      ok: false,
      facts: { targets: [{ embyAbsent: true, strmAbsent: true, cloudAbsent: false }] },
    })
  })

  it('rejects nested STRM media targets and missing or ambiguous 115 root entries', async () => {
    await mkdir(join(strmRoot, 'Shows'))
    embyItems.set('episode', {
      Id: 'episode', Name: 'Episode', Type: 'Episode', Path: '/strm/Shows/Episode.strm',
    })
    await writeFile(join(strmRoot, 'Shows', 'Episode.strm'), '/media/Shows/Season 01/Episode.mkv\n')
    await expect(service.prepareMediaDelete('library-1', ['episode'], signal)).rejects.toMatchObject({ code: 'POLICY_DENIED' })

    await writeFile(join(strmRoot, 'Shows', 'Episode.strm'), '/media/Shows/Episode.mkv\n')
    cloudEntries = []
    await expect(service.prepareMediaDelete('library-1', ['episode'], signal)).rejects.toMatchObject({ code: 'CONFLICT' })
    cloudEntries = [
      { id: 'cloud-a', name: 'Episode.mkv', directory: false },
      { id: 'cloud-b', name: 'Episode.mkv', directory: false },
    ]
    await expect(service.prepareMediaDelete('library-1', ['episode'], signal)).rejects.toMatchObject({ code: 'CONFLICT' })
  })

  it('rejects a wrong keeper and any incomplete or substituted same-TMDB group', async () => {
    for (const id of ['series-a', 'series-b', 'series-c']) {
      embyItems.set(id, {
        Id: id,
        Name: id,
        Type: 'Series',
        Path: `/strm/Shows/${id}`,
        ProviderIds: { Tmdb: '42' },
      })
    }
    embyItems.set('foreign', {
      Id: 'foreign', Name: 'foreign', Type: 'Series', Path: '/strm/Shows/foreign', ProviderIds: { Tmdb: '99' },
    })

    await expect(service.prepareDedupDelete('library-1', '42', 'foreign', ['series-a', 'series-b', 'series-c'], signal))
      .rejects.toThrow(/keep item/)
    await expect(service.prepareDedupDelete('library-1', '42', 'series-c', ['series-a'], signal))
      .rejects.toThrow(/complete current/)
    await expect(service.prepareDedupDelete('library-1', '42', 'series-c', ['series-a', 'foreign'], signal))
      .rejects.toThrow(/complete current/)
  })

  it('reports remaining Emby, STRM, 115, and extra duplicate-Series facts', async () => {
    await mkdir(join(strmRoot, 'Shows', 'Series old'), { recursive: true })
    embyItems.set('series-old', {
      Id: 'series-old', Name: 'Series old', Type: 'Series', Path: '/strm/Shows/Series old', ProviderIds: { Tmdb: '42' },
    })
    embyItems.set('series-keeper', {
      Id: 'series-keeper', Name: 'Series keeper', Type: 'Series', Path: '/strm/Shows/Series keeper', ProviderIds: { Tmdb: '42' },
    })
    cloudEntries = [{ id: 'cloud-old', name: 'Series old', directory: true }]
    const prepared = await service.prepareDedupDelete('library-1', '42', 'series-keeper', ['series-old'], signal)

    const verified = await service.verify(prepared.deleteTargets, prepared.dedupExpectation, signal)
    expect(verified).toMatchObject({
      ok: false,
      facts: {
        targets: [{ embyAbsent: false, strmAbsent: false, cloudAbsent: false }],
        dedup: { currentItemIds: ['series-old', 'series-keeper'], keeperRetained: false },
      },
    })
  })
})

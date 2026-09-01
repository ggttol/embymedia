import { randomUUID } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { LibraryDomainService } from '../src/domain/library.ts'
import type { EmbyClient } from '../src/clients/emby.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeLibrary = databaseUrl === undefined ? describe.skip : describe

describe('bounded library discovery', () => {
  it('applies the search term and returns duplicate context without full-library output', async () => {
    const search = vi.fn(async () => [
      { Id: 'one', Name: '我们的歌', Type: 'Series', Path: '/media/综艺/我们的歌', ProviderIds: { Tmdb: '100' } },
      { Id: 'two', Name: '我们的歌 第六季', Type: 'Series', Path: '/media/综艺/我们的歌6', ProviderIds: { Tmdb: '100' } },
    ])
    const emby = {
      libraries: async () => [{ id: 'variety', name: '综艺', locations: ['/strm/综艺'] }],
      search,
    } as unknown as EmbyClient
    const service = new LibraryDomainService({} as Database, '/media', '/strm', async () => emby)
    const context = await service.context('我们的歌', 'variety', 10, new AbortController().signal)
    expect(search).toHaveBeenCalledWith('我们的歌', 'Movie,Series,Episode', expect.any(AbortSignal), 'variety')
    expect(context).toMatchObject({ totalMatches: 2, truncated: false, duplicateGroups: [{ key: 'tmdb:100' }] })
    expect(context.items).toHaveLength(2)
  })

  it('counts one media type without loading full item payloads', async () => {
    const itemPage = vi.fn(async () => ({ items: [{ Id: 'one', Name: 'One' }], total: 519 }))
    const emby = { itemPage } as unknown as EmbyClient
    const service = new LibraryDomainService({} as Database, '/media', '/strm', async () => emby)
    await expect(service.itemCount('movies', new AbortController().signal, undefined, 'Movie')).resolves.toBe(519)
    expect(itemPage).toHaveBeenCalledWith('movies', 'Movie', '', 1, expect.any(AbortSignal), undefined)
  })

  it('matches titles across punctuation and quote variants in one lookup', async () => {
    const emby = {
      items: vi.fn(async () => [
        { Id: 'target', Name: '“爸”气外露 (2025)', Type: 'Movie' },
        { Id: 'other', Name: '别的电影', Type: 'Movie' },
      ]),
    } as unknown as EmbyClient
    const service = new LibraryDomainService({} as Database, '/media', '/strm', async () => emby)
    await expect(service.searchItems('movies', '爸气外露', 10, 0, new AbortController().signal, 'Movie'))
      .resolves.toEqual({ items: [{ Id: 'target', Name: '“爸”气外露 (2025)', Type: 'Movie' }], total: 1 })
  })

  it('summarizes all libraries with one compact count per library', async () => {
    const itemPage = vi.fn(async (_id: string, itemTypes: string) => ({ items: [], total: itemTypes === 'Movie' ? 519 : 42 }))
    const emby = {
      libraries: async () => [
        { id: 'movies', name: '电影', collectionType: 'movies', locations: ['/strm/电影'] },
        { id: 'series', name: '综艺', collectionType: 'tvshows', locations: ['/strm/综艺'] },
      ],
      itemPage,
    } as unknown as EmbyClient
    const service = new LibraryDomainService({} as Database, '/media', '/strm', async () => emby)
    await expect(service.summary(new AbortController().signal)).resolves.toEqual([
      { id: 'movies', name: '电影', collectionType: 'movies', itemTypes: 'Movie', total: 519, locations: ['/strm/电影'] },
      { id: 'series', name: '综艺', collectionType: 'tvshows', itemTypes: 'Series', total: 42, locations: ['/strm/综艺'] },
    ])
    expect(itemPage).toHaveBeenCalledTimes(2)
  })
})

describeLibrary('library and STRM operations', () => {
  let database: Database
  let root: string
  let mediaRoot: string
  let strmRoot: string
  let service: LibraryDomainService
  let refreshed = 0

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    root = await mkdtemp(join(tmpdir(), 'embymedia-library-'))
    mediaRoot = join(root, 'media')
    strmRoot = join(root, 'strm')
    await mkdir(join(mediaRoot, 'MoviesFolder', 'Top'), { recursive: true })
    await mkdir(strmRoot, { recursive: true })
    await writeFile(join(mediaRoot, 'MoviesFolder', 'Top', 'Movie.mkv'), 'fixture')
    await writeFile(join(mediaRoot, 'MoviesFolder', 'Single.mkv'), 'single fixture')
    const emby = {
      libraries: async () => [{ id: 'library-1', name: 'MoviesDisplay', collectionType: 'movies', locations: ['/strm/MoviesFolder'] }],
      refreshLibrary: async () => { refreshed++ },
      items: async () => [
        { Id: randomUUID(), Name: 'Movie', Path: '/strm/MoviesDisplay/Top/Movie.strm' },
        { Id: randomUUID(), Name: 'Single', Path: '/strm/MoviesDisplay/Single.strm' },
      ],
    } as unknown as EmbyClient
    service = new LibraryDomainService(database, mediaRoot, strmRoot, async () => emby, 1, 0)
  })

  afterAll(async () => {
    await database.close()
    await rm(root, { recursive: true, force: true })
  })

  it('lists canonical libraries and bounded items', async () => {
    const signal = new AbortController().signal
    await expect(service.libraries(signal)).resolves.toMatchObject([{ id: 'library-1', name: 'MoviesDisplay' }])
    await expect(service.items('library-1', 10, signal)).resolves.toEqual(expect.arrayContaining([
      expect.objectContaining({ Name: 'Movie' }),
    ]))
  })

  it('persists scan progress, writes container-stable STRM, refreshes, and verifies visibility', async () => {
    refreshed = 0
    const task = await service.scan({
      libraryId: 'library-1', libraryName: 'MoviesDisplay', mediaFolder: 'MoviesFolder', top: 'Top',
    }, new AbortController().signal)
    expect(task).toMatchObject({ kind: 'library.scan', status: 'done', progress: 1, total: 1, result: { generated: 1, expected: 1, visible: 1 } })
    expect(refreshed).toBe(1)
    expect(await readFile(join(strmRoot, 'MoviesDisplay', 'Top', 'Movie.strm'), 'utf8')).toBe('/media/MoviesFolder/Top/Movie.mkv\n')
    const persisted = await database.query<{ status: string }>('SELECT status FROM task_runs WHERE id=$1', [task.id])
    expect(persisted.rows[0]?.status).toBe('done')
  })

  it('scans a single shared media file without treating it as a directory', async () => {
    const task = await service.scan({
      libraryId: 'library-1', libraryName: 'MoviesDisplay', mediaFolder: 'MoviesFolder', top: 'Single.mkv',
    }, new AbortController().signal)
    expect(task).toMatchObject({ status: 'done', total: 1, result: { expected: 1, visible: 1 } })
    expect(await readFile(join(strmRoot, 'MoviesDisplay', 'Single.strm'), 'utf8')).toBe('/media/MoviesFolder/Single.mkv\n')
  })

  it('reports orphan STRM without deleting it', async () => {
    await mkdir(join(strmRoot, 'MoviesDisplay', 'Missing'), { recursive: true })
    await writeFile(join(strmRoot, 'MoviesDisplay', 'Missing', 'Gone.strm'), '/media/MoviesFolder/Missing/Gone.mkv\n')
    const entries = await service.listStrm('MoviesDisplay', 100, new AbortController().signal)
    expect(entries).toEqual(expect.arrayContaining([
      expect.objectContaining({ relativePath: 'MoviesDisplay/Top/Movie.strm', orphan: false }),
      expect.objectContaining({ relativePath: 'MoviesDisplay/Missing/Gone.strm', orphan: true }),
    ]))
  })
})

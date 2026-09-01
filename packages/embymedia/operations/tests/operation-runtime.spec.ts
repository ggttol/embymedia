import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import type { Database } from '../src/database/index.ts'
import type { EmbyClient } from '../src/clients/emby.ts'
import type { C115Client, C115SnapshotFile } from '../src/clients/c115.ts'
import type { ResourceApiClient } from '../src/clients/resource-api.ts'
import { resolveConfig } from '../src/config.ts'
import { EmbymediaOperationRuntime } from '../src/operation-runtime.ts'
import { EmbymediaError } from '../src/errors.ts'
import { prepareOperationInput, type PreviewResult } from '../src/operations.ts'
import type { EmbymediaCredentialRecords } from '../src/credentials.ts'
import type { JsonValue, OperationProjection } from '../src/schemas.ts'

const input = {
  candidate: {
    url: 'https://115.com/s/fixture',
    cid: 'cid-variety',
    title: 'Fixture Variety',
    diskType: '115',
  },
  scan: true,
}

function runtime(
  snapshotFiles: readonly C115SnapshotFile[] = [{ id: 'file-1', name: 'Fixture Variety', directory: true }],
  writeMode: 'disabled' | 'enabled' = 'enabled',
  planSecret?: (planId: string) => string | undefined,
  directories = [{ cid: 'series-cid', name: 'Fixture Variety {tmdb-123}' }],
  mediaRoot = '/media',
  strmRoot = '/strm',
  snapshotSequence: ReadonlyArray<readonly C115SnapshotFile[]> = [snapshotFiles],
  snapshotEvidence: readonly C115SnapshotFile[] = [{ id: 'episode', name: 'Fixture.S01E03.mkv', path: 'Fixture Variety/Fixture.S01E03.mkv', directory: false }],
) {
  const database = {
    query: vi.fn(async (sql: string) => sql.includes("key='c115_cid_map'")
      ? { rows: [{ value: { '综艺': 'cid-variety' } }], rowCount: 1 }
      : { rows: [], rowCount: 0 }),
  } as unknown as Database
  const emby = {
    libraries: vi.fn(async () => [{ id: 'library-variety', name: '综艺', locations: ['/strm/综艺'] }]),
    items: vi.fn(async (_libraryId: string, itemTypes: string) => itemTypes === 'Series'
      ? [{ Id: 'series-1', Name: 'Fixture Variety', Path: '/strm/综艺/Fixture Variety {tmdb-123}', ProviderIds: { Tmdb: '123' } }]
      : [{ Id: 'item-1', Name: 'Fixture Variety', Path: '/strm/综艺/Fixture Variety/episode.strm', ProviderIds: { Tmdb: '123' }, ImageTags: { Primary: 'poster-tag' } }]),
    itemsByPath: vi.fn(async (_libraryId: string, _path: string, itemTypes: string) => itemTypes === 'Series'
      ? [{ Id: 'series-1', Name: 'Fixture Variety', Path: '/strm/综艺/Fixture Variety {tmdb-123}', ProviderIds: { Tmdb: '123' } }]
      : [{ Id: 'item-1', Name: 'Fixture Variety', Path: '/strm/综艺/Fixture Variety/episode.strm', ProviderIds: { Tmdb: '123' }, ImageTags: { Primary: 'poster-tag' } }]),
  } as unknown as EmbyClient
  let snapshotIndex = 0
  const c115 = {
    snapshot: vi.fn(async () => ({
      shareCode: 'fixture',
      title: 'Fixture Variety',
      files: snapshotSequence[Math.min(snapshotIndex++, snapshotSequence.length - 1)] ?? snapshotFiles,
      ...(snapshotEvidence.length === 0 ? {} : { evidence: snapshotEvidence }),
    })),
    listDirectories: vi.fn(async () => directories),
  } as unknown as C115Client
  const resource = {} as ResourceApiClient
  const credentials = {} as EmbymediaCredentialRecords
  return new EmbymediaOperationRuntime({
    database,
    credentials,
    config: resolveConfig({ writeMode, mediaRoot, strmRoot }, {}),
    embyClient: async () => emby,
    c115Client: async () => c115,
    resourceClient: async () => resource,
    ...(planSecret === undefined ? {} : { planSecret }),
  })
}

function plan(preview: PreviewResult, confirmationInput: JsonValue = input): OperationProjection {
  return {
    schemaVersion: 1,
    id: 'plan-1',
    kind: 'resource.add_new',
    status: 'previewed',
    previewHash: 'hash',
    risk: 'high',
    destructive: false,
    reversible: false,
    confirmation: { kind: 'resource.add_new', input: confirmationInput },
    targets: preview.targets,
    steps: preview.steps,
    verification: preview.verification,
    expiresAt: new Date(Date.now() + 60_000).toISOString(),
    correlationId: 'correlation-1',
  }
}

describe('wired operation runtime', () => {
  it('canonicalizes the current add-new shape and wires execute + verify', async () => {
    const instance = runtime()
    await expect(instance.previewResourceAddNew(input, new AbortController().signal)).resolves.toMatchObject({
      kind: 'resource.add_new',
      input,
      targets: [expect.objectContaining({ canonicalCid: 'cid-variety', canonicalLibraryId: 'library-variety' })],
    })
    const previewer = instance.previewers['resource.add_new']!
    const preview = await previewer(input, new AbortController().signal)
    expect(preview.targets).toEqual([expect.objectContaining({
      canonicalCid: 'cid-variety',
      canonicalLibraryId: 'library-variety',
      path: '综艺/Fixture Variety',
    })])
    const current = plan(preview)
    await expect(instance.revalidate(current, new AbortController().signal)).resolves.toBeUndefined()

    vi.spyOn(instance.resources, 'executeAddNew').mockImplementation(async (request) => {
      expect(request.candidate.fileIds).toEqual(['file-1'])
      return {
        request,
        accepted: { mode: 'share', detail: { count: 1, cid: 'cid-variety' } },
        visible: { visible: true },
        scanTask: {
          schemaVersion: 1, id: 'task-1', kind: 'library.scan', label: 'scan', status: 'done', progress: 1, total: 1,
          statusText: 'verified', cancelRequested: false, correlationId: 'task-correlation', queuedAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
        },
        verification: { ok: true, embyVisible: 1 },
        stages: ['validate-candidate', 'accept-115', 'business-verification'],
      }
    })
    const result = await instance.handlers['resource.add_new']!.execute(current, new AbortController().signal)
    const verifying = { ...current, status: 'verifying' as const, result }
    await expect(instance.verifiers['resource.add_new']!.verify(verifying, new AbortController().signal))
      .resolves.toMatchObject({ ok: true, facts: { embyVisible: 1, scanTaskStatus: 'done' } })
  })

  it('rechecks the current write mode before executing a previously previewed plan', async () => {
    const previewRuntime = runtime()
    const preview = await previewRuntime.previewers['resource.add_new']!(input, new AbortController().signal)
    const disabledRuntime = runtime([{ id: 'file-1', name: 'Fixture Variety', directory: true }], 'disabled')
    await expect(disabledRuntime.revalidate(plan(preview), new AbortController().signal)).rejects.toMatchObject({

      code: 'POLICY_DENIED',
      message: 'writes are disabled',
    })
  })
  it('rejects a share that changes between approved preview and handler preparation', async () => {
    const before = [{ id: 'file-1', name: 'Fixture Variety', directory: true }]
    const after = [{ id: 'file-2', name: 'Changed Variety', directory: true }]
    const instance = runtime(before, 'enabled', undefined, undefined, '/media', '/strm', [before, after])
    const preview = await instance.previewers['resource.add_new']!(input, new AbortController().signal)
    const current = plan(preview)
    const execute = vi.spyOn(instance.resources, 'executeAddNew')
    await expect(instance.handlers['resource.add_new']!.execute(current, new AbortController().signal)).rejects.toThrow(/changed after approval/)
    expect(execute).not.toHaveBeenCalled()
  })


  it('rejects multi-root shares before any transfer can write into the library root', async () => {
    const instance = runtime([{ id: 'one', name: 'Season 00' }, { id: 'two', name: 'Season 01' }])
    await expect(instance.previewResourceAddNew(input, new AbortController().signal)).rejects.toMatchObject({
      code: 'POLICY_DENIED',
      message: expect.stringContaining('one wrapped top-level share root'),
    })
  })

  it('stages share access codes without persisting or rendering them', async () => {
    const protectedInput = {
      ...input,
      candidate: { ...input.candidate, url: 'https://115.com/s/fixture?password=secret' },
    }
    const prepared = prepareOperationInput('resource.add_new', protectedInput)
    expect(prepared.secret).toBe('secret')
    expect(JSON.stringify(prepared.persistedValue)).not.toContain('secret')
    expect(prepared.persistedValue).toMatchObject({
      candidate: { url: 'https://115.com/s/fixture', passwordStaged: true },
    })
    const previewRuntime = runtime()
    await expect(previewRuntime.previewResourceAddNew(protectedInput, new AbortController().signal)).resolves.toMatchObject({
      input: { candidate: { url: 'https://115.com/s/fixture', passwordStaged: true } },
    })
    const preview = await previewRuntime.previewers['resource.add_new']!(protectedInput, new AbortController().signal)
    const current = plan(preview, prepared.persistedValue as JsonValue)
    await expect(runtime(undefined, 'enabled', planId => planId === current.id ? prepared.secret : undefined)
      .revalidate(current, new AbortController().signal)).resolves.toBeUndefined()
    await expect(runtime().revalidate(current, new AbortController().signal)).rejects.toMatchObject({
      code: 'CONFLICT', message: expect.stringContaining('password expired'),
    })
    const verifying = {
      ...current,
      status: 'verifying' as const,
      result: { scanTask: { status: 'done' }, verification: { ok: true } },
    }


    await expect(runtime().verifiers['resource.add_new']!.verify(verifying, new AbortController().signal))
      .resolves.toMatchObject({ ok: true, facts: { pipelineVerified: true, scanTaskStatus: 'done' } })
  })
  it('binds multiple episode shares into one scan and verification set', async () => {
    const root = await mkdtemp(join(tmpdir(), 'embymedia-batch-add-'))
    try {
      await mkdir(join(root, '综艺'))
      const first = [{ id: 'episode-1', name: 'Gold.S01E01.mkv', directory: false }]
      const second = [{ id: 'episode-2', name: 'Gold.S01E02.mkv', directory: false }]
      const instance = runtime(first, 'enabled', undefined, undefined, root, '/strm', [first, second], [])
      const batchInput = {
        candidates: [
          { url: 'https://115.com/s/first', title: 'Gold E01', targetCid: 'cid-variety' },
          { url: 'https://115cdn.com/s/second', title: 'Gold E02', targetCid: 'cid-variety' },
        ],
        scan: { libraryId: 'library-variety', libraryName: '综艺', mediaFolder: '综艺', outputFolder: 'Gold (2026)' },
      }
      const preview = await instance.previewers['resource.add_new']!(batchInput, new AbortController().signal)
      expect(preview).toMatchObject({ verification: {
        expectedNames: ['Gold.S01E01.mkv', 'Gold.S01E02.mkv'],
        snapshotHashes: [expect.any(String), expect.any(String)], top: null, outputFolder: 'Gold (2026)',
      } })
      const current = plan(preview, batchInput)
      await expect(instance.revalidate(current, new AbortController().signal)).resolves.toBeUndefined()
      const c115 = await (instance as unknown as { deps: { c115Client: (signal: AbortSignal) => Promise<C115Client> } }).deps.c115Client(new AbortController().signal)
      expect(c115.snapshot).toHaveBeenCalledTimes(2)
    } finally {
      await rm(root, { recursive: true, force: true })
    }
  })

  it('binds a series update to one existing TMDB folder and verifies requested episodes', async () => {
    const seriesInput = {
      libraryId: 'library-variety',
      seriesId: 'series-1',
      candidate: { mode: 'share', url: 'https://115.com/s/fixture?password=secret', title: 'Fixture.Variety.S01E03' },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }
    const preparedInput = prepareOperationInput('series.update', seriesInput)
    const before = {
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Returning Series',
      localCount: 2, missingCount: 1, lane: 'update_needed' as const, gaps: [{ season: 1, episode: 3 }],
    }
    const instance = runtime([{ id: 'episode-3', name: 'Fixture.Variety.S01E03.mkv', directory: false }], 'enabled', () => preparedInput.secret)
    vi.spyOn(instance.series, 'detail').mockResolvedValue(before)
    const preview = await instance.previewers['series.update']!(seriesInput, new AbortController().signal)
    expect(preview).toMatchObject({
      targets: [{ id: 'series-1', canonicalLibraryId: 'library-variety', canonicalCid: 'series-cid', path: '综艺/Fixture Variety {tmdb-123}' }],
      verification: { tmdbId: '123', requestedEpisodes: [{ season: 1, episode: 3 }], seriesCid: 'series-cid' },
    })
    const persistedInput = preparedInput.persistedValue as JsonValue
    const current: OperationProjection = {
      ...plan(preview, persistedInput),
      kind: 'series.update',
      confirmation: { kind: 'series.update', input: persistedInput },
    }
    await expect(instance.revalidate(current, new AbortController().signal)).resolves.toBeUndefined()
    vi.spyOn(instance.resources, 'executeAddNew').mockImplementation(async (request) => {
      expect(request.candidate.fileIds).toEqual(['episode-3'])
      return {
        request,
        scanTask: {
          schemaVersion: 1, id: 'task-series', kind: 'library.scan', label: 'scan', status: 'done', progress: 1, total: 1,
          statusText: 'verified', cancelRequested: false, correlationId: 'task-correlation', queuedAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
        },
        verification: { ok: true, requestedEpisodes: [{ season: 1, episode: 3 }], missingAfter: [] },
        stages: ['validate-candidate', 'accept-115', 'business-verification'],
      }
    })
    const result = await instance.handlers['series.update']!.execute(current, new AbortController().signal)
    const verifying = { ...current, status: 'verifying' as const, result }
    const verificationRuntime = runtime([{ id: 'episode-3', name: 'Fixture.Variety.S01E03.mkv', directory: false }])
    vi.spyOn(verificationRuntime.series, 'detail').mockResolvedValue({ ...before, localCount: 3, missingCount: 0, lane: 'healthy_airing', gaps: [] })
    await expect(verificationRuntime.verifiers['series.update']!.verify(verifying, new AbortController().signal))


      .resolves.toMatchObject({ ok: true, facts: { sameBinding: true, uniqueSeries: true, missingAfter: [] } })
  })
  it('binds multiple per-episode candidates to one existing Series update', async () => {
    const first = [{ id: 'episode-2', name: 'Fixture.Variety.S01E02.mkv', directory: false }]
    const second = [{ id: 'episode-3', name: 'Fixture.Variety.S01E03.mkv', directory: false }]
    const instance = runtime(first, 'enabled', undefined, undefined, '/media', '/strm', [first, second], [])
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Returning Series',
      localCount: 1, missingCount: 2, lane: 'update_needed', gaps: [{ season: 1, episode: 2 }, { season: 1, episode: 3 }],
    })
    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidates: [
        { url: 'https://115.com/s/first', title: 'Fixture Variety S01E02' },
        { url: 'https://115cdn.com/s/second', title: 'Fixture Variety S01E03' },
      ],
      requestedEpisodes: [{ season: 1, episode: 2 }, { season: 1, episode: 3 }],
    }, new AbortController().signal)).resolves.toMatchObject({
      verification: {
        snapshotHashes: [expect.any(String), expect.any(String)],
        requestedEpisodes: [{ season: 1, episode: 2 }, { season: 1, episode: 3 }],
      },
    })
  })

  it('accepts requested episodes proven by recursive share evidence', async () => {
    const root = [{ id: 'wrapped-root', name: 'Fixture Variety', directory: true }]
    const evidence = [{ id: 'episode-3', name: 'Fixture.Variety.S01E03.mkv', path: 'Fixture Variety/Season 01/Fixture.Variety.S01E03.mkv', directory: false }]
    const instance = runtime(root, 'enabled', undefined, undefined, '/media', '/strm', [root], evidence)
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Ended',
      localCount: 2, missingCount: 1, lane: 'complete_after_update', gaps: [{ season: 1, episode: 3 }],
    })

    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidate: { url: 'https://115.com/s/fixture', title: 'Fixture Variety' },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }, new AbortController().signal)).resolves.toMatchObject({
      verification: { requestedEpisodes: [{ season: 1, episode: 3 }] },
    })
  })

  it('rejects aggregate pack claims contradicted by recursive evidence', async () => {
    const root = [{ id: 'wrapped-root', name: 'Fixture Variety S01E01-E03', directory: true }]
    const evidence = [
      { id: 'season-range', name: 'S01E01-E03', path: 'Fixture Variety/S01E01-E03', directory: true },
      { id: 'episode-1', name: 'Fixture.Variety.S01E01.mkv', path: 'Fixture Variety/S01E01-E03/Fixture.Variety.S01E01.mkv' },
    ]
    const instance = runtime(root, 'enabled', undefined, undefined, '/media', '/strm', [root], evidence)
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Ended',
      localCount: 1, missingCount: 1, lane: 'complete_after_update', gaps: [{ season: 1, episode: 2 }],
    })

    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidate: { url: 'https://115.com/s/fixture', title: 'Fixture Variety S01E01-E03' },
      requestedEpisodes: [{ season: 1, episode: 2 }],
    }, new AbortController().signal)).rejects.toThrow(/does not prove coverage/)
  })

  it('rejects model-title episode claims absent from the 115 snapshot', async () => {
    const unrelated = [{ id: 'unrelated', name: 'Unrelated.Movie.mkv', directory: false }]
    const instance = runtime(unrelated, 'enabled', undefined, undefined, '/media', '/strm', [unrelated], [])
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Returning Series',
      localCount: 2, missingCount: 1, lane: 'update_needed', gaps: [{ season: 1, episode: 3 }],
    })
    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidate: { url: 'https://115.com/s/fixture', title: 'Fixture.Variety.S01E03' },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }, new AbortController().signal)).rejects.toThrow(/snapshot does not prove/)
  })

  it('rejects episode claims made only by an empty directory name', async () => {
    const root = [{ id: 'empty-directory', name: 'Fixture.Variety.S01E03', directory: true }]
    const instance = runtime(root, 'enabled', undefined, undefined, '/media', '/strm', [root], [])
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Returning Series',
      localCount: 2, missingCount: 1, lane: 'update_needed', gaps: [{ season: 1, episode: 3 }],
    })
    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidate: { url: 'https://115.com/s/fixture', title: 'Fixture.Variety.S01E03' },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }, new AbortController().signal)).rejects.toThrow(/snapshot does not prove/)
  })

  it('rejects different 115 directories matched by exact name and TMDB marker', async () => {
    const instance = runtime(
      [{ id: 'episode-3', name: 'Fixture.Variety.S01E03.mkv' }],
      'enabled',
      undefined,
      [
        { cid: 'exact-cid', name: 'Fixture Variety {tmdb-123}' },
        { cid: 'other-cid', name: 'Other Name {tmdb-123}' },
      ],
    )
    vi.spyOn(instance.series, 'detail').mockResolvedValue({
      id: 'series-1', libraryId: 'library-variety', libraryName: '综艺', name: 'Fixture Variety',
      path: '/strm/综艺/Fixture Variety {tmdb-123}', folder: 'Fixture Variety {tmdb-123}', tmdbId: '123', tmdbStatus: 'Returning Series',
      localCount: 2, missingCount: 1, lane: 'update_needed', gaps: [{ season: 1, episode: 3 }],
    })
    await expect(instance.previewers['series.update']!({
      libraryId: 'library-variety', seriesId: 'series-1',
      candidate: { url: 'https://115.com/s/fixture', title: 'Fixture Variety' },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }, new AbortController().signal)).rejects.toThrow(/multiple 115 directories/)
  })

  it('binds explicit scan objects to the verified single-file share root', async () => {
    const instance = runtime([{ id: 'movie', name: 'Fixture.Movie.2026.mkv', directory: false }])
    const explicit = {
      ...input,
      candidate: { ...input.candidate, title: 'Fixture Movie' },
      scan: { libraryId: 'library-variety', libraryName: '综艺', mediaFolder: '综艺' },
    }
    await expect(instance.previewResourceAddNew(explicit, new AbortController().signal)).resolves.toMatchObject({
      verification: { top: 'Fixture.Movie.2026.mkv' },
    })
    await expect(instance.previewResourceAddNew({
      ...explicit,
      candidate: { ...explicit.candidate, cid: 'different-cid' },
    }, new AbortController().signal)).rejects.toThrow(/configured library root/)
  })




  it('classifies cancellation after an Emby write as partial', async () => {
    const instance = runtime()
    const controller = new AbortController()
    const emby = {
      deleteUser: async () => { controller.abort() },
      users: async (signal: AbortSignal) => { signal.throwIfAborted(); return [] },
    } as unknown as EmbyClient
    ;(instance as unknown as { deps: { embyClient: (signal: AbortSignal) => Promise<EmbyClient> } }).deps.embyClient = async () => emby
    const current: OperationProjection = {
      schemaVersion: 1, id: 'user-delete-plan', kind: 'user.delete', status: 'running', previewHash: 'hash', risk: 'critical', destructive: true, reversible: false,
      confirmation: { kind: 'user.delete', input: { userId: 'user-1' } }, targets: [{ id: 'user-1', label: 'User' }], steps: [], verification: {},
      expiresAt: new Date(Date.now() + 60_000).toISOString(), correlationId: 'correlation',
    }
    await expect(instance.handlers['user.delete']!.execute(current, controller.signal)).rejects.toMatchObject({ code: 'PARTIAL_FAILURE' })
  })

  it('propagates cleanup verification cancellation for resumable accounting', async () => {
    const instance = runtime()
    const controller = new AbortController()
    const cleanup = (instance as unknown as { cleanup: { verify: (...args: unknown[]) => Promise<unknown> } }).cleanup
    vi.spyOn(cleanup, 'verify').mockImplementation(async () => {
      controller.abort()
      throw new EmbymediaError('CANCELLED', 'cleanup verification cancelled')
    })
    const current: OperationProjection = {
      schemaVersion: 1, id: 'cleanup-plan', kind: 'media.delete', status: 'verifying', previewHash: 'hash', risk: 'high', destructive: true, reversible: false,
      confirmation: {}, targets: [], steps: [], expiresAt: new Date(Date.now() + 60_000).toISOString(), correlationId: 'correlation', result: { status: 'done' },
      verification: { deleteTargets: [{
        id: 'target', embyItemId: 'item', embyPath: '/strm/item.strm', embyType: 'Episode',
        strm: { root: '/strm', rootRealpath: '/strm', relativePath: 'item.strm', absolutePath: '/strm/item.strm', realpath: '/strm/item.strm', inode: '1', mtimeMs: 1, size: 1, type: 'file' },
        cloudParentCid: '10', cloudIds: ['20'], cloudName: 'item.mkv', cloudDirectory: false,
      }] },
    }
    await expect(instance.verifiers['media.delete']!.verify(current, controller.signal)).rejects.toMatchObject({ code: 'CANCELLED' })
  })

  it('reuses only same-owner partial cleanup targets without retargeting', async () => {
    const instance = runtime()
    const canonicalTargets = [{ id: 'item-1', label: 'Item 1', canonicalLibraryId: 'library-variety', path: '综艺/Item 1' }]
    const source = {
      kind: 'media.delete', status: 'partial', requested_by: 'gaotao', session_id: 'session-1',
      targets: canonicalTargets, confirmation: { verification: { deleteTargets: [{ id: 'delete-1' }] } },
    }
    const query = vi.fn(async () => ({ rows: [source], rowCount: 1 }))
    ;(instance as unknown as { deps: { database: { query: typeof query } } }).deps.database.query = query
    const context = { principal: 'gaotao', sessionId: 'session-1', idempotencyKey: 'retry-key' }
    await expect(instance.previewers['media.delete']!({ retryPlanId: 'partial-1' }, new AbortController().signal, context))
      .resolves.toMatchObject({ targets: canonicalTargets, verification: { deleteTargets: [{ id: 'delete-1' }], retryOf: 'partial-1' } })
    await expect(instance.previewers['media.delete']!({ retryPlanId: 'partial-1', itemIds: ['other'] }, new AbortController().signal, context))
      .rejects.toThrow(/only retryPlanId/)
    source.session_id = 'other-session'
    await expect(instance.previewers['media.delete']!({ retryPlanId: 'partial-1' }, new AbortController().signal, context))
      .rejects.toThrow(/another owner or session/)
    source.session_id = 'session-1'
    source.status = 'done'
    await expect(instance.previewers['media.delete']!({ retryPlanId: 'partial-1' }, new AbortController().signal, context))
      .rejects.toThrow(/partial plan/)
    source.status = 'partial'
    source.kind = 'dedup.delete'
    await expect(instance.previewers['media.delete']!({ retryPlanId: 'partial-1' }, new AbortController().signal, context))
      .rejects.toThrow(/same kind/)
  })

  it('rejects an add-new root that already exists before transfer', async () => {
    const root = await mkdtemp(join(tmpdir(), 'embymedia-add-new-'))
    try {
      await mkdir(join(root, '综艺'), { recursive: true })
      await writeFile(join(root, '综艺', 'Fixture.Movie.2026.mkv'), 'existing')
      const instance = runtime([{ id: 'movie', name: 'Fixture.Movie.2026.mkv', directory: false }], 'enabled', undefined, undefined, root)
      await expect(instance.previewResourceAddNew({
        ...input,
        candidate: { ...input.candidate, title: 'Fixture Movie' },
        scan: { libraryId: 'library-variety', libraryName: '综艺', mediaFolder: '综艺' },
      }, new AbortController().signal)).rejects.toThrow(/target already exists/)
    } finally {
      await rm(root, { recursive: true, force: true })
    }
  })

  it('wires every operation with a complete preview, executor, and verifier trio', () => {
    const instance = runtime()
    const supported = [
      'library.create', 'library.scan', 'resource.add_new', 'series.update', 'media.delete', 'dedup.delete',
      'poster.apply', 'poster.fix_batch', 'metadata.refresh',
      'user.create', 'user.policy_update', 'user.delete',
      'config.update', 'smart_action.policy_update', 'smart_action.dismiss', 'undo.execute',
    ] as const
    for (const kind of supported) {
      expect(instance.previewers[kind], `${kind} preview`).toBeDefined()
      expect(instance.handlers[kind], `${kind} executor`).toBeDefined()
      expect(instance.verifiers[kind], `${kind} verifier`).toBeDefined()
    }
  })
  it('does not create fake plans for unwired destructive operations', () => {
    const instance = runtime()
    expect(instance.previewers['media.move']).toBeUndefined()
    expect(instance.handlers['media.move']).toBeUndefined()
    expect(instance.verifiers['media.move']).toBeUndefined()
  })
})

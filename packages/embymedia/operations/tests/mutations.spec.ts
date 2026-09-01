import { randomUUID } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'
import { analyzeDuplicates, MediaMutationService } from '../src/domain/mutations.ts'
import { capturePath } from '../src/media/paths.ts'
import type { EmbyClient } from '../src/clients/emby.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeMutations = databaseUrl === undefined ? describe.skip : describe

describeMutations('dedup, cleanup, moves, and undo', () => {
  let database: Database
  let root: string
  let service: MediaMutationService
  const order: string[] = []

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    root = await mkdtemp(join(tmpdir(), 'embymedia-mutation-'))
    const emby = {
      item: async () => ({ Id: 'item-1', Name: 'Fixture', Path: '/strm/item.strm', Type: 'Episode' }),
      deleteItem: async () => { order.push('emby') },
    } as unknown as EmbyClient
    service = new MediaMutationService(database, async () => emby, {
      resolveIds: async (_parentCid, targets) => targets.map(target => target.id),
      deleteIds: async () => { order.push('cloud'); throw new Error('cloud unavailable') },
    })
  })

  afterAll(async () => {
    await database.close()
    await rm(root, { recursive: true, force: true })
  })

  it('uses deterministic smart retention scoring', () => {
    const groups = analyzeDuplicates([
      { id: 'old-1080', itemId: '1', libraryId: 'l', library: 'Movies', folder: 'Old', tmdbId: '42', resolutionHeight: 1080, size: 100, updatedAt: '2025-01-01' },
      { id: 'new-2160', itemId: '2', libraryId: 'l', library: 'Movies', folder: 'New', tmdbId: '42', resolutionHeight: 2160, size: 200, updatedAt: '2026-01-01' },
    ])
    expect(groups).toEqual([
      expect.objectContaining({ tmdbId: '42', keep: expect.objectContaining({ id: 'new-2160' }), remove: [expect.objectContaining({ id: 'old-1080' })], confidence: 'high' }),
    ])
  })

  it('deletes STRM before Emby/Cloud and returns explicit partial target state', async () => {
    const strmRoot = join(root, 'delete-strm')
    await mkdir(strmRoot)
    await writeFile(join(strmRoot, 'item.strm'), 'fixture')
    const strm = await capturePath(strmRoot, 'item.strm')
    order.length = 0
    const target = {
      id: 'target-1',
      embyItemId: 'item-1',
      embyPath: '/strm/item.strm',
      embyType: 'Episode',
      strm,
      cloudParentCid: '10',
      cloudIds: ['20'],
      cloudName: 'item.mkv',
      cloudDirectory: false,
    }
    const result = await service.deleteTargets(randomUUID(), [target], new AbortController().signal)
    expect(order).toEqual(['emby', 'cloud'])
    expect(result).toEqual({
      status: 'partial',
      targets: [{ id: 'target-1', emby: 'deleted', strm: 'deleted', cloud: 'failed', errors: ['Cloud: cloud unavailable'] }],
    })
    await expect(readFile(join(strmRoot, 'item.strm'))).rejects.toMatchObject({ code: 'ENOENT' })
    const resumed = new MediaMutationService(database, async () => ({ item: async () => undefined }) as unknown as EmbyClient, {
      resolveIds: async () => [],
      deleteIds: async () => { throw new Error('must not delete an absent id') },
    })
    await expect(resumed.deleteTargets(randomUUID(), [target], new AbortController().signal)).resolves.toMatchObject({ status: 'done' })
  })

  it('reports cancellation after the first destructive target as partial', async () => {
    const controller = new AbortController()
    let deletions = 0
    const emby = {
      item: async (id: string) => ({ Id: id, Name: 'Item', Path: `/strm/${id}.strm`, Type: 'Episode' }),
      deleteItem: async () => {
        deletions++
        if (deletions === 1) controller.abort()
      },
    } as unknown as EmbyClient
    const interrupted = new MediaMutationService(database, async () => emby, {
      resolveIds: async () => [],
      deleteIds: async () => {},
    })
    const targets = ['one', 'two'].map(id => ({
      id,
      embyItemId: id,
      embyPath: `/strm/${id}.strm`,
      embyType: 'Episode',
      cloudParentCid: '10',
      cloudIds: [id],
      cloudName: `${id}.mkv`,
      cloudDirectory: false,
    }))
    await expect(interrupted.deleteTargets(randomUUID(), targets, controller.signal)).rejects.toMatchObject({
      code: 'PARTIAL_FAILURE',
    })
    expect(deletions).toBe(1)
  })

  it('moves atomically across both roots and restores through a new undo execution', async () => {
    const fromStrm = join(root, 'from-strm')
    const toStrm = join(root, 'to-strm')
    const fromCloud = join(root, 'from-cloud')
    const toCloud = join(root, 'to-cloud')
    for (const directory of [fromStrm, toStrm, fromCloud, toCloud]) await mkdir(directory)
    await writeFile(join(fromStrm, 'Series.strm'), 'strm')
    await writeFile(join(fromCloud, 'Series.mkv'), 'cloud')
    const plans = new OperationPlanStore(database)
    const plan = await plans.create({
      kind: 'media.move', requestedBy: 'gaotao', sessionId: 'session', risk: 'high', destructive: false, reversible: true,
      confirmation: {}, targets: [], steps: [], verification: {}, expiresAt: new Date(Date.now() + 60_000), idempotencyKey: randomUUID(),
    })
    const moved = await service.moveTarget(plan.id, {
      id: 'move-1',
      strm: await capturePath(fromStrm, 'Series.strm'),
      cloud: await capturePath(fromCloud, 'Series.mkv'),
      destinationStrmRoot: toStrm,
      destinationStrmRelative: 'Series.strm',
      destinationCloudRoot: toCloud,
      destinationCloudRelative: 'Series.mkv',
    }, new AbortController().signal)
    expect(await readFile(moved.strm, 'utf8')).toBe('strm')
    expect(await readFile(moved.cloud, 'utf8')).toBe('cloud')
    await expect(service.executeUndo(moved.undoId, new AbortController().signal)).resolves.toMatchObject({ status: 'done' })
    expect(await readFile(join(fromStrm, 'Series.strm'), 'utf8')).toBe('strm')
    expect(await readFile(join(fromCloud, 'Series.mkv'), 'utf8')).toBe('cloud')
  })
})

describe('post-write cleanup accounting', () => {
  it('removes the Emby record before deleting STRM and cloud storage', async () => {
    const root = await mkdtemp(join(tmpdir(), 'embymedia-delete-order-'))
    const path = join(root, 'item.strm')
    try {
      await writeFile(path, 'fixture')
      const strm = await capturePath(root, 'item.strm')
      let embyDeleted = false
      const emby = {
        item: async () => ({ Id: 'item', Name: 'Item', Path: '/strm/item.strm', Type: 'Episode' }),
        deleteItem: async () => {
          expect(await readFile(path, 'utf8')).toBe('fixture')
          embyDeleted = true
        },
      } as unknown as EmbyClient
      const database = { query: async () => ({ rows: [], rowCount: 1 }) } as unknown as Database
      const service = new MediaMutationService(database, async () => emby, {
        resolveIds: async () => ['20'],
        deleteIds: async () => { expect(embyDeleted).toBe(true); await expect(readFile(path)).rejects.toMatchObject({ code: 'ENOENT' }) },
      })
      await expect(service.deleteTargets(randomUUID(), [{
        id: 'target',
        embyItemId: 'item',
        embyPath: '/strm/item.strm',
        embyType: 'Episode',
        strm,
        cloudParentCid: '10',
        cloudIds: ['20'],
        cloudName: 'item.mkv',
        cloudDirectory: false,
      }], new AbortController().signal)).resolves.toEqual({
        status: 'done',
        targets: [{ id: 'target', emby: 'deleted', strm: 'deleted', cloud: 'deleted', errors: [] }],
      })
    } finally {
      await rm(root, { recursive: true, force: true })
    }
  })

  it('classifies an audit failure after deletion as partial', async () => {
    const database = { query: async () => { throw new Error('audit unavailable') } } as unknown as Database
    const emby = {
      item: async () => ({ Id: 'item', Name: 'Item', Path: '/strm/item.strm', Type: 'Episode' }),
      deleteItem: async () => {},
    } as unknown as EmbyClient
    const service = new MediaMutationService(database, async () => emby, {
      resolveIds: async () => [],
      deleteIds: async () => {},
    })
    await expect(service.deleteTargets(randomUUID(), [{
      id: 'target',
      embyItemId: 'item',
      embyPath: '/strm/item.strm',
      embyType: 'Episode',
      cloudParentCid: '10',
      cloudIds: ['20'],
      cloudName: 'item.mkv',
      cloudDirectory: false,
    }], new AbortController().signal)).rejects.toMatchObject({ code: 'PARTIAL_FAILURE' })
  })

  it('rejects a stale later target before deleting any target', async () => {
    let embyDeletes = 0
    let cloudDeletes = 0
    const emby = {
      item: async (id: string) => ({ Id: id, Name: id, Path: id === 'two' ? '/strm/changed.strm' : `/strm/${id}.strm`, Type: 'Episode' }),
      deleteItem: async () => { embyDeletes++ },
    } as unknown as EmbyClient
    const service = new MediaMutationService({} as Database, async () => emby, {
      resolveIds: async (_parent, targets) => targets.map(target => target.id),
      deleteIds: async () => { cloudDeletes++ },
    })
    const targets = ['one', 'two'].map(id => ({
      id, embyItemId: id, embyPath: `/strm/${id}.strm`, embyType: 'Episode',
      cloudParentCid: '10', cloudIds: [id], cloudName: `${id}.mkv`, cloudDirectory: false,
    }))
    await expect(service.deleteTargets(randomUUID(), targets, new AbortController().signal)).rejects.toMatchObject({ code: 'CONFLICT' })
    expect(embyDeletes).toBe(0)
    expect(cloudDeletes).toBe(0)
  })

  it('refreshes Emby after an item delete rejection once storage is removed', async () => {
    const root = await mkdtemp(join(tmpdir(), 'embymedia-delete-refresh-'))
    const path = join(root, 'item.strm')
    try {
      await writeFile(path, 'fixture')
      let refreshed = false
      const emby = {
        item: async () => refreshed ? undefined : ({ Id: 'item', Name: 'Item', Path: '/strm/item.strm', Type: 'Movie' }),
        deleteItem: async () => { throw new Error('Emby request failed') },
        refreshLibrary: async () => { refreshed = true },
      } as unknown as EmbyClient
      const service = new MediaMutationService({ query: async () => ({ rows: [], rowCount: 1 }) } as unknown as Database, async () => emby, {
        resolveIds: async () => ['20'], deleteIds: async () => {},
      })
      await expect(service.deleteTargets(randomUUID(), [{
        id: 'target', embyItemId: 'item', embyPath: '/strm/item.strm', embyType: 'Movie',
        strm: await capturePath(root, 'item.strm'), cloudParentCid: '10', cloudIds: ['20'], cloudName: 'item.mkv', cloudDirectory: false,
      }], new AbortController().signal)).resolves.toEqual({
        status: 'done', targets: [{ id: 'target', emby: 'missing', strm: 'deleted', cloud: 'deleted', errors: [] }],
      })
    } finally {
      await rm(root, { recursive: true, force: true })
    }
  })
})

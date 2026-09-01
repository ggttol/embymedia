import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'
import { OPERATION_KINDS } from '../src/capabilities.ts'
import { OperationPlanner, OPERATION_SPECS, operationInputGuide, prepareOperationInput, type OperationPreviewer } from '../src/operations.ts'
import { resolveConfig } from '../src/config.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeOperations = databaseUrl === undefined ? describe.skip : describe

function inputFor(kind: (typeof OPERATION_KINDS)[number]): Record<string, unknown> {
  const input: Record<string, unknown> = Object.fromEntries(OPERATION_SPECS[kind].required.map(key => [key, 'value']))
  if (kind === 'config.credential_rotate') input.credential = 'tmdb-api-key'
  if (kind === 'smart_action.policy_update') Object.assign(input, { key: 'library_scan', enabled: true, mode: 'confirm', maxRisk: 'medium', params: {} })
  if (kind === 'series.update') Object.assign(input, { libraryId: 'library', seriesId: 'series', candidate: {}, requestedEpisodes: [] })
  if (kind === 'resource.add_new') Object.assign(input, { candidate: {}, scan: {} })
  if (kind === 'poster.fix_batch') input.items = []
  if (kind === 'media.delete') input.itemIds = []
  if (kind === 'media.move') input.targets = []
  if (kind === 'dedup.delete') Object.assign(input, { libraryId: 'library', tmdbId: '123', keepItemId: 'keep', removeItemIds: [] })
  if (['cleanup.empty_strm', 'cleanup.empty_cloud', 'cleanup.execute'].includes(kind)) input.targets = []
  if (kind === 'schedule.upsert') Object.assign(input, { params: {}, cadence: {}, enabled: false })
  if (kind === 'config.update') input.settings = {}
  return input
}

describe('operation input guidance', () => {
  it('derives every operation field list and explicit delete forms from validation specs', () => {
    const guide = operationInputGuide()
    for (const kind of OPERATION_KINDS) expect(guide).toContain(kind)
    expect(guide).toContain('media.delete input={"libraryId":"<library id>","itemIds":["<Emby item id>"]}')
    expect(guide).toContain('resource.add_new input={"candidates"')
    expect(guide).toContain('retry={"retryPlanId":"<partial plan id>"}')
  })
})

describe('batch candidate secret staging', () => {
  it('removes every access code from persisted candidates while preserving index alignment', () => {
    const prepared = prepareOperationInput('resource.add_new', {
      candidates: [
        { url: 'https://115cdn.com/s/first?password=one', title: 'E01' },
        { url: 'https://115.com/s/second', title: 'E02' },
      ],
      scan: {},
    })
    expect(prepared.secrets).toEqual(['one', undefined])
    expect(JSON.stringify(prepared.persistedValue)).not.toContain('password=one')
    expect(prepared.persistedValue).toMatchObject({ candidates: [{ passwordStaged: true }, { url: 'https://115.com/s/second' }] })
  })
})

describeOperations('complete operation planning contract', () => {
  let database: Database
  let store: OperationPlanStore
  const previewer: OperationPreviewer = async () => ({
    targets: [{ id: 'target-1', label: 'Fixture', canonicalLibraryId: 'library-staging' }],
    steps: ['execute deterministic action'],
    verification: { observable: true },
  })

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    store = new OperationPlanStore(database)
  })

  afterAll(async () => {
    await database.close()
  })

  it('creates all 28 discriminated operation kinds through one planner', async () => {
    const previewers = Object.fromEntries(OPERATION_KINDS.map(kind => [kind, previewer]))
    const planner = new OperationPlanner(store, resolveConfig({ writeMode: 'enabled' }), previewers)
    for (const kind of OPERATION_KINDS) {
      const plan = await planner.create(kind, inputFor(kind), {
        principal: 'gaotao', sessionId: 'session', idempotencyKey: randomUUID(),
      }, new AbortController().signal)
      expect(plan).toMatchObject({ kind, status: 'previewed', risk: OPERATION_SPECS[kind].risk })
      expect(plan.confirmation).toMatchObject({ kind, input: inputFor(kind), targets: [{ id: 'target-1' }] })
    }
  })

  it('rejects unknown and legacy control fields without side effects', async () => {
    const planner = new OperationPlanner(store, resolveConfig({ writeMode: 'enabled' }), { 'library.scan': previewer })
    for (const forbidden of ['confirm', 'async', 'apply', 'execute']) {
      await expect(planner.create('library.scan', {
        ...inputFor('library.scan'), [forbidden]: true,
      }, { principal: 'gaotao', sessionId: 'session', idempotencyKey: randomUUID() }, new AbortController().signal))
        .rejects.toThrow(/unknown fields/)
    }
  })

  it('fails closed in disabled mode and enforces canonical staging ids', async () => {
    const context = { principal: 'gaotao', sessionId: 'session', idempotencyKey: randomUUID() }
    const disabled = new OperationPlanner(store, resolveConfig({ writeMode: 'disabled' }), { 'library.scan': previewer })
    await expect(disabled.create('library.scan', inputFor('library.scan'), context, new AbortController().signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })

    const staging = new OperationPlanner(store, resolveConfig({ writeMode: 'staging', stagingLibraryIds: ['library-staging'] }), { 'library.scan': previewer })
    await expect(staging.create('library.scan', inputFor('library.scan'), { ...context, idempotencyKey: randomUUID() }, new AbortController().signal))
      .resolves.toMatchObject({ status: 'previewed' })
    const denied = new OperationPlanner(store, resolveConfig({ writeMode: 'staging', stagingLibraryIds: ['different'] }), { 'library.scan': previewer })
    await expect(denied.create('library.scan', inputFor('library.scan'), { ...context, idempotencyKey: randomUUID() }, new AbortController().signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })
})

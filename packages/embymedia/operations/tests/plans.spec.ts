import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'
import { EmbymediaError } from '../src/errors.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describePlans = databaseUrl === undefined ? describe.skip : describe

describePlans('operation plan state machine', () => {
  let database: Database
  let plans: OperationPlanStore

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    plans = new OperationPlanStore(database)
  })

  afterAll(async () => {
    await database.close()
  })

  function input(overrides: Partial<Parameters<OperationPlanStore['create']>[0]> = {}) {
    return {
      kind: 'media.delete' as const,
      requestedBy: 'gaotao',
      sessionId: 'session-1',
      risk: 'critical' as const,
      destructive: true,
      reversible: true,
      confirmation: { libraryId: 'library-1', targets: ['item-1'] },
      targets: [{ id: 'item-1', label: 'Fixture Item', canonicalLibraryId: 'library-1' }],
      steps: ['delete Emby item', 'delete STRM path', 'verify absence'],
      verification: { embyAbsent: true, pathAbsent: true },
      expiresAt: new Date(Date.now() + 60_000),
      idempotencyKey: randomUUID(),
      ...overrides,
    }
  }

  it('persists a replay-stable preview and idempotently returns it', async () => {
    const spec = input()
    const first = await plans.create(spec)
    const second = await plans.create(spec)
    expect(second).toEqual(first)
    expect(first).toMatchObject({
      kind: 'media.delete',
      status: 'previewed',
      destructive: true,
      risk: 'critical',
      targets: [{ id: 'item-1' }],
    })
    await expect(plans.create(input({ idempotencyKey: spec.idempotencyKey, sessionId: 'session-2' })))
      .rejects.toMatchObject({ code: 'CONFLICT' })
  })

  it('enforces the exact transition graph and terminal replay', async () => {
    const plan = await plans.create(input())
    await expect(plans.transition(plan.id, 'previewed', 'running')).rejects.toBeInstanceOf(EmbymediaError)
    const approved = await plans.transition(plan.id, 'previewed', 'approved', { approvedAt: new Date() })
    expect(approved.status).toBe('approved')
    expect((await plans.transition(plan.id, 'approved', 'queued')).status).toBe('queued')
    expect((await plans.transition(plan.id, 'queued', 'running')).status).toBe('running')
    expect((await plans.transition(plan.id, 'running', 'verifying')).status).toBe('verifying')
    const done = await plans.transition(plan.id, 'verifying', 'done', {
      result: { deleted: 1 },
      verification: { embyAbsent: true, pathAbsent: true },
    })
    expect(done.status).toBe('done')
    expect(await plans.transition(plan.id, 'previewed', 'approved')).toEqual(done)
  })

  it('expires stale previews and caps destructive target disclosure', async () => {
    await expect(plans.create(input({ targets: Array.from({ length: 101 }, (_, index) => ({ id: String(index), label: String(index) })) })))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
    const plan = await plans.create(input({ expiresAt: new Date(Date.now() + 20) }))
    await new Promise(resolve => setTimeout(resolve, 30))
    const expired = await plans.transition(plan.id, 'previewed', 'approved')
    expect(expired.status).toBe('expired')
  })
})

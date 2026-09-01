import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'
import { OperationVerificationService } from '../src/verification.ts'
import type { OperationProjection } from '../src/schemas.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeVerify = databaseUrl === undefined ? describe.skip : describe

describeVerify('independent operation verification', () => {
  let database: Database
  let store: OperationPlanStore

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    store = new OperationPlanStore(database)
  })

  afterAll(async () => {
    await database.close()
  })

  async function verifyingPlan() {
    const plan = await store.create({
      kind: 'library.scan', requestedBy: 'gaotao', sessionId: 'verify-session', risk: 'medium', destructive: false, reversible: false,
      confirmation: {}, targets: [], steps: [], verification: { expected: true }, expiresAt: new Date(Date.now() + 60_000), idempotencyKey: randomUUID(),
    })
    await store.transition(plan.id, 'previewed', 'approved', { approvedAt: new Date() })
    await store.transition(plan.id, 'approved', 'queued')
    await store.transition(plan.id, 'queued', 'running')
    return store.transition(plan.id, 'running', 'verifying', { result: { executed: true } })
  }

  it('re-reads facts and moves to done only when the verifier succeeds', async () => {
    const plan = await verifyingPlan()
    const verify = vi.fn(async () => ({ ok: true, facts: { embyVisible: true, strmExists: true, databaseRecorded: true } }))
    const service = new OperationVerificationService(database, store, { 'library.scan': { verify } }, 'gaotao')
    const done = await service.verify(plan.id, 'verify-session', new AbortController().signal)
    expect(done).toMatchObject({ status: 'done', verification: { ok: true, facts: { embyVisible: true } } })
    expect(verify).toHaveBeenCalledOnce()
    await expect(service.verify(plan.id, 'verify-session', new AbortController().signal)).resolves.toEqual(done)
    expect(verify).toHaveBeenCalledOnce()
  })

  it('distinguishes partial facts from failed facts', async () => {
    const partialPlan = await verifyingPlan()
    const failedPlan = await verifyingPlan()
    const partial = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => ({ ok: false, partial: true, facts: { embyVisible: true, cloudVisible: false } }) },
    }, 'gaotao')
    await expect(partial.verify(partialPlan.id, 'verify-session', new AbortController().signal))
      .resolves.toMatchObject({ status: 'partial', error: { code: 'VERIFICATION_FAILED' } })
    const failed = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => ({ ok: false, facts: { embyVisible: false } }) },
    }, 'gaotao')
    await expect(failed.verify(failedPlan.id, 'verify-session', new AbortController().signal))
      .resolves.toMatchObject({ status: 'failed', error: { code: 'VERIFICATION_FAILED' } })
  })

  it('keeps an effectful cancelled verification resumable', async () => {
    const plan = await verifyingPlan()
    const controller = new AbortController()
    const cancelled = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => {
        controller.abort()
        controller.signal.throwIfAborted()
        return { ok: true, facts: {} }
      } },
    }, 'gaotao')

    await expect(cancelled.verify(plan.id, 'verify-session', controller.signal)).rejects.toMatchObject({ code: 'CANCELLED' })
    await expect(store.get(plan.id)).resolves.toMatchObject({ status: 'verifying', result: { executed: true } })

    const resumed = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => ({ ok: true, facts: { resumed: true } }) },
    }, 'gaotao')
    await expect(resumed.verify(plan.id, 'verify-session', new AbortController().signal))
      .resolves.toMatchObject({ status: 'done', verification: { facts: { resumed: true } } })
  })

  it('rejects foreign sessions before reading verification facts', async () => {
    const plan = await verifyingPlan()
    const verify = vi.fn(async () => ({ ok: true, facts: {} }))
    const service = new OperationVerificationService(database, store, { 'library.scan': { verify } }, 'gaotao')
    await expect(service.verify(plan.id, 'foreign', new AbortController().signal)).rejects.toMatchObject({ code: 'POLICY_DENIED' })
    expect(verify).not.toHaveBeenCalled()
  })
})

describe('verification cancellation accounting', () => {
  it('leaves a persisted effect result in verifying until a later verification succeeds', async () => {
    let current: OperationProjection = {
      schemaVersion: 1,
      id: randomUUID(),
      kind: 'library.scan',
      status: 'verifying',
      previewHash: 'hash',
      risk: 'medium',
      destructive: false,
      reversible: false,
      confirmation: {},
      targets: [],
      steps: [],
      verification: {},
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      correlationId: randomUUID(),
      result: { executed: true },
    }
    const transition = vi.fn(async (_id: string, _from: string, to: string, patch: Partial<OperationProjection>) => {
      current = { ...current, ...patch, status: to as OperationProjection['status'] }
      return current
    })
    const store = { get: vi.fn(async () => current), transition } as unknown as OperationPlanStore
    const database = {
      query: vi.fn(async (sql: string) => sql.startsWith('SELECT session_id')
        ? { rows: [{ session_id: 'verify-session', requested_by: 'gaotao' }], rowCount: 1 }
        : { rows: [], rowCount: 1 }),
    } as unknown as Database
    const controller = new AbortController()
    const cancelled = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => {
        controller.abort()
        controller.signal.throwIfAborted()
        return { ok: true, facts: {} }
      } },
    }, 'gaotao')

    await expect(cancelled.verify(current.id, 'verify-session', controller.signal)).rejects.toMatchObject({ code: 'CANCELLED' })
    expect(current).toMatchObject({ status: 'verifying', result: { executed: true } })
    expect(transition).not.toHaveBeenCalled()

    const resumed = new OperationVerificationService(database, store, {
      'library.scan': { verify: async () => ({ ok: true, facts: { resumed: true } }) },
    }, 'gaotao')
    await expect(resumed.verify(current.id, 'verify-session', new AbortController().signal))
      .resolves.toMatchObject({ status: 'done', result: { executed: true }, verification: { facts: { resumed: true } } })
    await expect(resumed.verify(current.id, 'foreign-session', new AbortController().signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })
})

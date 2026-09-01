import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'
import { OperationExecutionService, operationApprovalReason } from '../src/execution.ts'
import { EmbymediaError, PartialOperationError } from '../src/errors.ts'
import type { OperationProjection } from '../src/schemas.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
describe('operation approval copy', () => {
  it('keeps the canonical kind and risk beside Chinese labels', () => {
    expect(operationApprovalReason('config.update', 'medium', 'plan-1'))
      .toBe('更新配置 / config.update · 中风险 / medium · 计划 / plan plan-1')
  })

  it('binds destructive approval copy to the preview and target set', () => {
    expect(operationApprovalReason('media.delete', 'critical', 'plan-2', {
      previewHash: 'preview-hash',
      targets: [{ id: 'item-1', label: 'Episode 1' }, { id: 'item-2', label: 'Episode 2' }],
    })).toContain('preview preview-hash · targets 2: item-1, item-2')
  })
})

const describeExecution = databaseUrl === undefined ? describe.skip : describe

describeExecution('approval and execution authorization', () => {
  let database: Database
  let store: OperationPlanStore
  const agent = { id: 'execution-session' } as Agent

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    store = new OperationPlanStore(database)
  })

  afterAll(async () => {
    await database.close()
  })

  async function plan(risk: 'low' | 'medium' | 'critical' = 'medium') {
    return store.create({
      kind: risk === 'low' ? 'smart_action.dismiss' : 'library.scan',
      requestedBy: 'gaotao',
      sessionId: agent.id,
      risk,
      destructive: risk === 'critical',
      reversible: false,
      confirmation: { kind: 'fixture', targets: ['one', 'two'] },
      targets: [{ id: 'target', label: 'Target', canonicalLibraryId: 'library' }],
      steps: ['execute'],
      verification: { expected: true },
      expiresAt: new Date(Date.now() + 60_000),
      idempotencyKey: randomUUID(),
    })
  }

  it('rejects mismatched hash, confirmation, session, and principal before side effects', async () => {
    const current = await plan()
    const handler = vi.fn(async () => ({ changed: true }))
    const approval = vi.fn(async () => 'allowed-once' as const)
    const service = new OperationExecutionService(database, store, { request: approval }, { 'library.scan': { execute: handler } }, 'gaotao', async () => {})
    await expect(service.execute(agent, { planId: current.id, previewHash: 'wrong', confirmation: current.confirmation }, new AbortController().signal))
      .rejects.toMatchObject({ code: 'CONFLICT' })
    await expect(service.execute(agent, { planId: current.id, previewHash: current.previewHash, confirmation: { kind: 'fixture', targets: ['two', 'one'] } }, new AbortController().signal))
      .rejects.toMatchObject({ code: 'CONFLICT' })
    await expect(service.execute({ id: 'other-session' } as Agent, { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation }, new AbortController().signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
    expect(handler).not.toHaveBeenCalled()
    expect(approval).not.toHaveBeenCalled()
  })

  it('requires allowed-once for medium and critical plans and creates zero effects when rejected', async () => {
    for (const risk of ['medium', 'critical'] as const) {
      const current = await plan(risk)
      const handler = vi.fn(async () => ({ changed: true }))
      const service = new OperationExecutionService(
        database,
        store,
        { request: async () => 'rejected' },
        { 'library.scan': { execute: handler } },
        'gaotao',
        async () => {},
      )
      await expect(service.execute(agent, { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation }, new AbortController().signal))
        .rejects.toMatchObject({ code: 'POLICY_DENIED' })
      expect(handler).not.toHaveBeenCalled()
      await expect(store.get(current.id)).resolves.toMatchObject({ status: 'cancelled' })
    }
  })

  it('binds allowed-once to one plan and leaves successful execution in independent verification', async () => {
    const current = await plan()
    const handler = vi.fn(async () => ({ changed: true }))
    const approval = vi.fn(async () => 'allowed-once' as const)
    const revalidate = vi.fn(async () => {})
    const service = new OperationExecutionService(database, store, { request: approval }, { 'library.scan': { execute: handler } }, 'gaotao', revalidate)
    const result = await service.execute(agent, { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation }, new AbortController().signal)
    expect(result).toMatchObject({ status: 'verifying', result: { changed: true } })
    expect(approval).toHaveBeenCalledOnce()
    expect(revalidate).toHaveBeenCalledTimes(2)
    expect(handler).toHaveBeenCalledOnce()
    await expect(service.execute(agent, { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation }, new AbortController().signal))
      .rejects.toMatchObject({ code: 'CONFLICT' })
    expect(handler).toHaveBeenCalledOnce()
  })

  it('revalidates after approval and starts no handler when approved targets changed', async () => {
    const current = await plan()
    const handler = vi.fn(async () => ({ changed: true }))
    const revalidate = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new EmbymediaError('CONFLICT', 'target changed during approval'))
    const service = new OperationExecutionService(
      database,
      store,
      { request: async () => 'allowed-once' },
      { 'library.scan': { execute: handler } },
      'gaotao',
      revalidate,
    )
    await expect(service.execute(agent, {
      planId: current.id,
      previewHash: current.previewHash,
      confirmation: current.confirmation,
    }, new AbortController().signal)).rejects.toMatchObject({ code: 'CONFLICT' })
    expect(revalidate).toHaveBeenCalledTimes(2)
    expect(handler).not.toHaveBeenCalled()
    await expect(store.get(current.id)).resolves.toMatchObject({ status: 'previewed' })
  })

  it('allows low-risk dismiss policy without an approval prompt', async () => {
    const current = await plan('low')
    const handler = vi.fn(async () => ({ dismissed: true }))
    const approval = vi.fn(async () => 'unavailable' as const)
    const service = new OperationExecutionService(database, store, { request: approval }, { 'smart_action.dismiss': { execute: handler } }, 'gaotao', async () => {})
    await expect(service.execute(agent, { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation }, new AbortController().signal))
      .resolves.toMatchObject({ status: 'verifying' })
    expect(approval).not.toHaveBeenCalled()
    expect(handler).toHaveBeenCalledOnce()
  })
})

describe('partial execution accounting', () => {
  it('does not report a post-write failure as a clean failure or cancellation', async () => {
    const current: OperationProjection = {
      schemaVersion: 1,
      id: randomUUID(),
      kind: 'library.scan',
      status: 'previewed',
      previewHash: 'preview-hash',
      risk: 'medium',
      destructive: false,
      reversible: false,
      confirmation: { kind: 'library.scan' },
      targets: [{ id: 'library', label: 'Library', canonicalLibraryId: 'library' }],
      steps: ['scan'],
      verification: {},
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      correlationId: randomUUID(),
    }
    let stored = current
    const client = {
      query: vi.fn(async () => ({ rows: [{
        id: current.id,
        kind: current.kind,
        status: current.status,
        requested_by: 'gaotao',
        session_id: 'execution-session',
        preview_hash: current.previewHash,
        risk: current.risk,
        confirmation: current.confirmation,
        expires_at: new Date(current.expiresAt),
      }] })),
    }
    const fakeDatabase = {
      transaction: vi.fn(async (callback: (value: typeof client) => Promise<void>) => callback(client)),
      query: vi.fn(async () => ({ rows: [], rowCount: 0 })),
    } as unknown as Database
    const fakeStore = {
      get: vi.fn(async () => stored),
      transition: vi.fn(async (_id: string, _from: string, status: OperationProjection['status'], patch = {}) => {
        stored = { ...stored, ...patch, status } as OperationProjection
        return stored
      }),
    } as unknown as OperationPlanStore
    const service = new OperationExecutionService(
      fakeDatabase,
      fakeStore,
      { request: async () => 'allowed-once' },
      { 'library.scan': { execute: async () => { throw new PartialOperationError('VERIFICATION_FAILED', 'post-write verification failed') } } },
      'gaotao',
      async () => {},
    )
    await expect(service.execute(
      { id: 'execution-session' } as Agent,
      { planId: current.id, previewHash: current.previewHash, confirmation: current.confirmation },
      new AbortController().signal,
    )).resolves.toMatchObject({ status: 'partial', error: { code: 'VERIFICATION_FAILED' } })
  })

  it('authenticates terminal plan replay before returning persisted details', async () => {
    const terminal: OperationProjection = {
      schemaVersion: 1,
      id: randomUUID(),
      kind: 'library.scan',
      status: 'done',
      previewHash: 'terminal-hash',
      risk: 'medium',
      destructive: false,
      reversible: false,
      confirmation: { kind: 'library.scan' },
      targets: [{ id: 'library', label: 'Library' }],
      steps: ['scan'],
      verification: { ok: true },
      expiresAt: new Date(Date.now() - 60_000).toISOString(),
      correlationId: randomUUID(),
    }
    const client = {
      query: vi.fn(async () => ({ rows: [{
        id: terminal.id,
        kind: terminal.kind,
        status: terminal.status,
        requested_by: 'gaotao',
        session_id: 'owner-session',
        preview_hash: terminal.previewHash,
        risk: terminal.risk,
        confirmation: terminal.confirmation,
        expires_at: new Date(terminal.expiresAt),
      }] })),
    }
    const fakeDatabase = {
      transaction: vi.fn(async (callback: (value: typeof client) => Promise<void>) => callback(client)),
      query: vi.fn(async () => ({ rows: [], rowCount: 0 })),
    } as unknown as Database
    const fakeStore = { get: vi.fn(async () => terminal) } as unknown as OperationPlanStore
    const service = new OperationExecutionService(
      fakeDatabase,
      fakeStore,
      { request: async () => 'allowed-once' },
      {},
      'gaotao',
      async () => {},
    )
    await expect(service.execute(
      { id: 'other-session' } as Agent,
      { planId: terminal.id, previewHash: terminal.previewHash, confirmation: terminal.confirmation },
      new AbortController().signal,
    )).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })
})

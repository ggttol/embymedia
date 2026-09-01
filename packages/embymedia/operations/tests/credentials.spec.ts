import { randomUUID } from 'node:crypto'
import { Context } from '@deepseek-ai/cordis'
import {
  CredentialProvider,
  credentialKey,
  type CredentialInfo,
  type CredentialKey,
  type CredentialRecord,
  type CredentialRecordEntry,
  type CredentialRecordInfo,
  type CredentialRef,
  type ResolvedCredential,
} from '@deepseek-ai/dsh-credentials'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { EmbymediaCredentialRecords } from '../src/credentials.ts'
import { Database } from '../src/database/index.ts'
import { OperationPlanStore } from '../src/plans.ts'

class MemoryCredentialProvider extends CredentialProvider {
  readonly records = new Map<CredentialKey, CredentialRecord>()
  resolve(_ref: CredentialRef): Promise<ResolvedCredential | undefined> { return Promise.resolve(undefined) }
  describe(_ref: CredentialRef): Promise<CredentialInfo> { return Promise.resolve({ configured: false, writable: true }) }
  set(_ref: CredentialRef, _value: string): Promise<void> { return Promise.resolve() }
  unset(_ref: CredentialRef): Promise<void> { return Promise.resolve() }
  readRecord(key: CredentialKey): Promise<CredentialRecord | undefined> { return Promise.resolve(this.records.get(key)) }
  describeRecord(key: CredentialKey): Promise<CredentialRecordInfo> {
    const record = this.records.get(key)
    return Promise.resolve({ configured: record !== undefined, writable: true, ...(record === undefined ? {} : { kind: record.kind }) })
  }
  listRecords(): Promise<readonly CredentialRecordEntry[]> {
    return Promise.resolve([...this.records].map(([key, value]) => ({ key, kind: value.kind })))
  }
  async modifyRecord(key: CredentialKey, mutate: (record: CredentialRecord | undefined) => Promise<CredentialRecord | undefined>) {
    const result = await mutate(this.records.get(key))
    if (result !== undefined) this.records.set(key, result)
    return result ?? this.records.get(key)
  }
  deleteRecord(key: CredentialKey): Promise<void> {
    this.records.delete(key)
    return Promise.resolve()
  }
}

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeCredentials = databaseUrl === undefined ? describe.skip : describe


describe('credential rotation plan binding', () => {
  it('stages a c115 cookie from the canonical confirmation input', async () => {
    const context = new Context()
    const provider = new MemoryCredentialProvider(context)
    const database = {
      query: vi.fn(async () => ({ rows: [{
        kind: 'config.credential_rotate',
        status: 'previewed',
        session_id: 'credential-session',
        expires_at: new Date(Date.now() + 60_000),
        confirmation: { kind: 'config.credential_rotate', input: { credential: 'c115-cookie' } },
      }] })),
    } as unknown as Database
    const records = new EmbymediaCredentialRecords(provider, database)

    await expect(records.stage({ id: 'credential-session' } as Agent, 'plan-id', 'qa-cookie', new AbortController().signal))
      .resolves.toEqual({ configured: true })
    await expect(records.readPending('plan-id', 'c115-cookie')).resolves.toBe('qa-cookie')
  })
})
describeCredentials('product credential records', () => {
  let database: Database
  let provider: MemoryCredentialProvider
  let records: EmbymediaCredentialRecords
  let plans: OperationPlanStore
  const agent = { id: 'credential-session' } as Agent

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    const context = new Context()
    provider = new MemoryCredentialProvider(context)
    records = new EmbymediaCredentialRecords(provider, database)
    plans = new OperationPlanStore(database)
  })

  afterAll(async () => {
    await database.close()
  })

  async function rotationPlan(credential: 'c115-cookie' | 'tmdb-api-key' | 'clouddrive-webhook-secret') {
    const targets = [{ id: credential, label: credential }]
    const steps = ['stage', 'validate', 'replace']
    const verification = { configured: true }
    return plans.create({
      kind: 'config.credential_rotate',
      requestedBy: 'gaotao',
      sessionId: agent.id,
      risk: 'high',
      destructive: false,
      reversible: true,
      confirmation: { kind: 'config.credential_rotate', input: { credential }, targets, steps, verification, risk: 'high', destructive: false, reversible: true },
      targets,
      steps,
      verification,
      expiresAt: new Date(Date.now() + 60_000),
      idempotencyKey: randomUUID(),
    })
  }

  it('stages by plan identity without returning or caching values', async () => {
    const plan = await rotationPlan('c115-cookie')
    const result = await records.stage(agent, plan.id, 'first-secret', new AbortController().signal)
    expect(result).toEqual({ configured: true })
    expect(JSON.stringify(result)).not.toContain('first-secret')
    expect(await records.readPending(plan.id, 'c115-cookie')).toBe('first-secret')
    await records.promotePending(plan.id, 'c115-cookie')
    expect(await records.read('c115-cookie')).toBe('first-secret')

    provider.records.set(credentialKey('embymedia', 'c115-cookie'), {
      kind: 'grant', payload: { version: 1, value: 'second-secret' },
    })
    expect(await records.read('c115-cookie')).toBe('second-secret')
    const status = await records.status()
    expect(status.find(item => item.id === 'c115-cookie')).toEqual({
      id: 'c115-cookie', configured: true, writable: true, kind: 'grant',
    })
    expect(JSON.stringify(status)).not.toContain('second-secret')
  })

  it('generates webhook values on the Host and removes orphan pending records', async () => {
    const plan = await rotationPlan('clouddrive-webhook-secret')
    await expect(records.stage(agent, plan.id, 'user-value', new AbortController().signal))
      .rejects.toMatchObject({ code: 'POLICY_DENIED' })
    await records.generateWebhookPending(plan.id)
    expect(await records.readPending(plan.id, 'clouddrive-webhook-secret')).toMatch(/^[A-Za-z0-9_-]{43}$/)

    const orphan = randomUUID()
    provider.records.set(credentialKey('embymedia', `pending-${orphan}-tmdb-api-key`), {
      kind: 'grant', payload: { version: 1, value: 'orphan-secret' },
    })
    await expect(records.cleanupOrphans()).resolves.toBe(1)
    expect(provider.records.has(credentialKey('embymedia', `pending-${orphan}-tmdb-api-key`))).toBe(false)
  })
})

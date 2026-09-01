import { createHash, randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { CredentialAvailabilityService, CredentialRotationService, SettingsDomainService, UserDomainService } from '../src/domain/admin.ts'
import { OperationPlanStore } from '../src/plans.ts'
import type { EmbyClient, EmbyUser } from '../src/clients/emby.ts'
import type { EmbymediaCredentialRecords } from '../src/credentials.ts'
import type { CloudDriveWebhookService } from '../src/webhook.ts'
import { EmbymediaError } from '../src/errors.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeAdmin = databaseUrl === undefined ? describe.skip : describe

describe('credential availability checks', () => {
  it('validates formal values without returning secrets and reports safe failures', async () => {
    const validate = vi.fn(async () => undefined)
    const records = { read: vi.fn(async () => 'formal-secret') } as unknown as EmbymediaCredentialRecords
    const service = new CredentialAvailabilityService(records, { validate })

    const success = await service.check('c115-cookie', new AbortController().signal)
    expect(success).toMatchObject({ id: 'c115-cookie', ok: true, message: '115 可访问，Cookie 有效' })
    expect(validate).toHaveBeenCalledWith('c115-cookie', 'formal-secret', expect.any(AbortSignal))
    expect(JSON.stringify(success)).not.toContain('formal-secret')

    validate.mockRejectedValueOnce(new EmbymediaError('AUTH_REQUIRED', '115 rejected the cookie'))
    await expect(service.check('c115-cookie', new AbortController().signal)).resolves.toMatchObject({
      id: 'c115-cookie', ok: false, code: 'AUTH_REQUIRED', message: '115 rejected the cookie',
    })
  })

  it('checks webhook receiver readiness without emitting a media event', async () => {
    const validate = vi.fn()
    const records = { read: vi.fn(async () => 'formal-webhook-secret') } as unknown as EmbymediaCredentialRecords
    const service = new CredentialAvailabilityService(records, { validate })
    const result = await service.check('clouddrive-webhook-secret', new AbortController().signal)
    expect(result).toMatchObject({ id: 'clouddrive-webhook-secret', ok: true })
    expect(validate).not.toHaveBeenCalled()
    expect(JSON.stringify(result)).not.toContain('formal-webhook-secret')
  })
})

describeAdmin('users, settings, and credential rotation', () => {
  let database: Database

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
  })

  afterAll(async () => {
    await database.close()
  })

  it('creates, updates, deletes, and verifies Emby users', async () => {
    let users: EmbyUser[] = []
    const emby = {
      users: async () => users,
      createUser: async (name: string) => {
        const user = { Id: 'user-1', Name: name, Policy: {} }
        users = [user]
        return user
      },
      updateUserPolicy: async (_id: string, policy: object) => {
        users = users.map(user => ({ ...user, Policy: { ...user.Policy, ...policy } }))
      },
      deleteUser: async () => { users = [] },
    } as unknown as EmbyClient
    const service = new UserDomainService(async () => emby)
    await expect(service.create('Viewer', undefined, { IsDisabled: false }, new AbortController().signal))
      .resolves.toMatchObject({ Id: 'user-1' })
    await expect(service.updatePolicy('user-1', { IsDisabled: true }, new AbortController().signal))
      .resolves.toMatchObject({ Policy: { IsDisabled: true } })
    await expect(service.delete('user-1', new AbortController().signal)).resolves.toBeUndefined()
  })

  it('accepts allowlisted settings and restores them through a persisted undo', async () => {
    const plans = new OperationPlanStore(database)
    const plan = await plans.create({
      kind: 'config.update',
      requestedBy: 'gaotao',
      sessionId: 'settings-test',
      risk: 'medium',
      destructive: false,
      reversible: true,
      confirmation: {},
      targets: [],
      steps: [],
      verification: {},
      expiresAt: new Date(Date.now() + 60_000),
      idempotencyKey: randomUUID(),
    })
    const service = new SettingsDomainService(database)
    const updated = await service.update(plan.id, { auto_strm_enabled: true, tmdb_timeout_seconds: 30 })
    expect(updated.settings).toEqual({ auto_strm_enabled: true, tmdb_timeout_seconds: 30 })
    await expect(service.previewUndo(updated.undoId)).resolves.toMatchObject({ keys: ['auto_strm_enabled', 'tmdb_timeout_seconds'] })
    await expect(service.executeUndo(updated.undoId)).resolves.toMatchObject({ status: 'done' })
    await expect(service.verifyUndo(updated.undoId)).resolves.toBe(true)
    await expect(service.update(plan.id, { api_key: 'secret' })).rejects.toMatchObject({ code: 'POLICY_DENIED' })
    await expect(service.update(plan.id, { unsupported: true })).rejects.toMatchObject({ code: 'INVALID_INPUT' })
  })

  it('validates ordinary pending records before promotion', async () => {
    const calls: string[] = []
    const records = {
      readPending: async () => 'pending-value',
      promotePending: async () => { calls.push('promote') },
    } as unknown as EmbymediaCredentialRecords
    const service = new CredentialRotationService(records, {
      validate: async (id, value) => { calls.push(`validate:${id}:${value}`) },
    }, {} as CloudDriveWebhookService, {} as never)
    await service.rotate('plan-1', 'tmdb-api-key', new AbortController().signal)
    expect(calls).toEqual(['validate:tmdb-api-key:pending-value', 'promote'])
  })

  it('rotates webhook sender, waits for real canary, then commits; failures restore', async () => {
    const calls: string[] = []
    const records = {
      read: async () => 'old-secret',
      readPending: async () => 'new-secret',
      promotePending: async () => { calls.push('promote') },
      discardPending: async () => { calls.push('discard') },
    } as unknown as EmbymediaCredentialRecords
    const webhook = {
      beginRotation: async () => { calls.push('dual-start') },
      endRotation: () => { calls.push('dual-end') },
    } as unknown as CloudDriveWebhookService
    const sender = {
      rotate: async (_plan: string, pending: string, previousHash: string) => {
        calls.push(`sender:${pending}`)
        expect(previousHash).toBe(createHash('sha256').update('old-secret').digest('hex'))
      },
      restore: async () => { calls.push('restore') },
      canary: async () => { calls.push('canary') },
    }
    const service = new CredentialRotationService(records, { validate: vi.fn() }, webhook, sender)
    await service.rotate('plan-1', 'clouddrive-webhook-secret', new AbortController().signal)
    expect(calls).toEqual(['dual-start', 'sender:new-secret', 'canary', 'promote', 'dual-end'])

    calls.length = 0
    sender.canary = async () => { calls.push('canary'); throw new Error('canary failed') }
    await expect(service.rotate('plan-2', 'clouddrive-webhook-secret', new AbortController().signal)).rejects.toThrow('canary failed')
    expect(calls).toEqual(['dual-start', 'sender:new-secret', 'canary', 'restore', 'dual-end', 'discard'])
  })
})

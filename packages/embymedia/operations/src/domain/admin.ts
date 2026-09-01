import { createHash, randomUUID } from 'node:crypto'
import type { Database } from '../database/index.ts'
import type { EmbyClient, EmbyUser, EmbyUserPolicy } from '../clients/emby.ts'
import type { EmbymediaCredentialRecords } from '../credentials.ts'
import type { CloudDriveWebhookService } from '../webhook.ts'
import { EmbymediaError } from '../errors.ts'
import { canonicalJson, type CredentialId, type JsonValue } from '../schemas.ts'

const SETTING_VALIDATORS: Readonly<Record<string, (value: JsonValue) => boolean>> = {
  auto_strm_enabled: value => typeof value === 'boolean',
  auto_strm_fullauto: value => typeof value === 'boolean',
  c115_cid_map: value => typeof value === 'object' && value !== null && !Array.isArray(value),
  cd2_mount_prefix: value => typeof value === 'string' && value.startsWith('/'),
  resource_api_base_url: value => typeof value === 'string' && /^https?:\/\//.test(value),
  tmdb_base_url: value => typeof value === 'string' && /^https?:\/\//.test(value),
  tmdb_timeout_seconds: value => typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 300,
}

export interface CredentialValidators {
  readonly validate: (id: Exclude<CredentialId, 'clouddrive-webhook-secret'>, value: string, signal: AbortSignal) => Promise<void>
}

export interface CredentialAvailabilityResult {
  readonly id: CredentialId
  readonly ok: boolean
  readonly checkedAt: string
  readonly latencyMs: number
  readonly message: string
  readonly code?: string
}

const CREDENTIAL_SUCCESS: Readonly<Record<CredentialId, string>> = {
  'emby-api-key': 'Emby 可访问，API Key 有效',
  'c115-cookie': '115 可访问，Cookie 有效',
  'tmdb-api-key': 'TMDB 可访问，API Key 有效',
  'resource-api-token': '资源 API 可访问，Token 有效',
  'outbound-proxy-url': 'TMDB HTTPS 与资源 API HTTP 均可经代理访问',
  'clouddrive-webhook-secret': 'Webhook 正式凭据存在，接收器已就绪；未发送媒体事件',
}

export class CredentialAvailabilityService {
  constructor(
    private readonly records: EmbymediaCredentialRecords,
    private readonly validators: CredentialValidators,
  ) {}

  async check(id: CredentialId, signal: AbortSignal): Promise<CredentialAvailabilityResult> {
    signal.throwIfAborted()
    const startedAt = Date.now()
    try {
      const value = await this.records.read(id)
      if (id !== 'clouddrive-webhook-secret') await this.validators.validate(id, value, signal)
      return {
        id,
        ok: true,
        checkedAt: new Date().toISOString(),
        latencyMs: Date.now() - startedAt,
        message: CREDENTIAL_SUCCESS[id],
      }
    } catch (error) {
      signal.throwIfAborted()
      const failure = error instanceof EmbymediaError
        ? error
        : new EmbymediaError('UPSTREAM_UNAVAILABLE', '凭据可用性检查失败')
      return {
        id,
        ok: false,
        checkedAt: new Date().toISOString(),
        latencyMs: Date.now() - startedAt,
        message: failure.message,
        code: failure.code,
      }
    }
  }
}

export interface WebhookSenderControl {
  readonly rotate: (planId: string, pendingSecret: string, previousSecretHash: string, signal: AbortSignal) => Promise<void>
  readonly restore: (planId: string, signal: AbortSignal) => Promise<void>
  readonly canary: (secret: string, signal: AbortSignal) => Promise<void>
}

export class UserDomainService {
  constructor(private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>) {}

  async list(signal: AbortSignal): Promise<readonly EmbyUser[]> {
    return (await this.embyClient(signal)).users(signal)
  }

  async create(name: string, password: string | undefined, policy: EmbyUserPolicy | undefined, signal: AbortSignal): Promise<EmbyUser> {
    const emby = await this.embyClient(signal)
    const created = await emby.createUser(name, password, signal)
    if (policy !== undefined) await emby.updateUserPolicy(created.Id, policy, signal)
    const verified = (await emby.users(signal)).find(user => user.Id === created.Id)
    if (verified === undefined) throw new EmbymediaError('VERIFICATION_FAILED', 'created Emby user is not visible')
    return verified
  }

  async updatePolicy(userId: string, policy: EmbyUserPolicy, signal: AbortSignal): Promise<EmbyUser> {
    const emby = await this.embyClient(signal)
    await emby.updateUserPolicy(userId, policy, signal)
    const verified = (await emby.users(signal)).find(user => user.Id === userId)
    const actualPolicy = verified?.Policy
    const matches = actualPolicy !== undefined && Object.entries(policy)
      .every(([key, value]) => JSON.stringify(actualPolicy[key]) === JSON.stringify(value))
    if (verified === undefined || !matches) {
      throw new EmbymediaError('VERIFICATION_FAILED', 'Emby user policy did not match requested state')
    }
    return verified
  }

  async delete(userId: string, signal: AbortSignal): Promise<void> {
    const emby = await this.embyClient(signal)
    await emby.deleteUser(userId, signal)
    if ((await emby.users(signal)).some(user => user.Id === userId)) {
      throw new EmbymediaError('VERIFICATION_FAILED', 'deleted Emby user remains visible')
    }
  }
}

interface ConfigUndoEntry {
  readonly key: string
  readonly existed: boolean
  readonly value?: JsonValue
}

function configUndoEntries(payload: JsonValue): readonly ConfigUndoEntry[] {
  if (typeof payload !== 'object' || payload === null || Array.isArray(payload)) throw new EmbymediaError('POLICY_DENIED', 'config undo payload is malformed')
  const record = payload as Record<string, JsonValue>
  if (record.kind !== 'config.update' || !Array.isArray(record.entries)) throw new EmbymediaError('POLICY_DENIED', 'undo entry is not a config update')
  return record.entries.map((raw) => {
    if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) throw new EmbymediaError('POLICY_DENIED', 'config undo entry is malformed')
    const entry = raw as Record<string, JsonValue>
    if (typeof entry.key !== 'string' || typeof entry.existed !== 'boolean') throw new EmbymediaError('POLICY_DENIED', 'config undo entry fields are malformed')
    const validate = SETTING_VALIDATORS[entry.key]
    if (validate === undefined) throw new EmbymediaError('POLICY_DENIED', `config undo key ${entry.key} is unsupported`)
    if (entry.existed && !validate(entry.value ?? null)) throw new EmbymediaError('POLICY_DENIED', `config undo value for ${entry.key} is invalid`)
    return { key: entry.key, existed: entry.existed, ...(entry.existed ? { value: entry.value ?? null } : {}) }
  })
}

export class SettingsDomainService {
  constructor(private readonly database: Database) {}

  async update(planId: string, settings: Readonly<Record<string, JsonValue>>): Promise<{ readonly settings: Readonly<Record<string, JsonValue>>; readonly undoId: string }> {
    for (const [key, value] of Object.entries(settings)) {
      if (/password|cookie|secret|api[_-]?key|token/i.test(key)) throw new EmbymediaError('POLICY_DENIED', 'secret settings must use credential rotation')
      const validate = SETTING_VALIDATORS[key]
      if (validate === undefined) throw new EmbymediaError('INVALID_INPUT', `unsupported setting ${key}`)
      if (!validate(value)) throw new EmbymediaError('INVALID_INPUT', `invalid value for setting ${key}`)
    }
    return this.database.transaction(async (client) => {
      const keys = Object.keys(settings)
      const previousRows = await client.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings WHERE key = ANY($1) FOR UPDATE', [keys])
      const previous = new Map(previousRows.rows.map(row => [row.key, row.value]))
      for (const [key, value] of Object.entries(settings)) {
        await client.query(
          `INSERT INTO app_settings(key,value,updated_at) VALUES ($1,$2,now())
           ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`,
          [key, JSON.stringify(value)],
        )
      }
      const undoId = randomUUID()
      const entries: ConfigUndoEntry[] = keys.map(key => previous.has(key)
        ? { key, existed: true, value: previous.get(key) ?? null }
        : { key, existed: false })
      await client.query(
        'INSERT INTO undo_entries(id,plan_id,op,payload) VALUES ($1,$2,\'config.update\',$3)',
        [undoId, planId, JSON.stringify({ kind: 'config.update', entries })],
      )
      const rows = await client.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings WHERE key = ANY($1) ORDER BY key', [keys])
      return { settings: Object.fromEntries(rows.rows.map(row => [row.key, row.value])), undoId }
    })
  }

  async previewUndo(undoId: string): Promise<{ readonly undoId: string; readonly keys: readonly string[] }> {
    const row = (await this.database.query<{ op: string; payload: JsonValue; undone: boolean }>(
      'SELECT op,payload,undone FROM undo_entries WHERE id=$1',
      [undoId],
    )).rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'undo entry not found')
    if (row.undone) throw new EmbymediaError('CONFLICT', 'undo entry was already applied')
    if (row.op !== 'config.update') throw new EmbymediaError('POLICY_DENIED', 'undo entry kind is not wired')
    return { undoId, keys: configUndoEntries(row.payload).map(entry => entry.key) }
  }

  async executeUndo(undoId: string): Promise<JsonValue> {
    return this.database.transaction(async (client) => {
      const row = (await client.query<{ op: string; payload: JsonValue; undone: boolean }>(
        'SELECT op,payload,undone FROM undo_entries WHERE id=$1 FOR UPDATE',
        [undoId],
      )).rows[0]
      if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'undo entry not found')
      if (row.undone) return { undoId, status: 'already-undone' }
      if (row.op !== 'config.update') throw new EmbymediaError('POLICY_DENIED', 'undo entry kind is not wired')
      const entries = configUndoEntries(row.payload)
      for (const entry of entries) {
        if (entry.existed) {
          await client.query(
            `INSERT INTO app_settings(key,value,updated_at) VALUES ($1,$2,now())
             ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`,
            [entry.key, JSON.stringify(entry.value ?? null)],
          )
        } else {
          await client.query('DELETE FROM app_settings WHERE key=$1', [entry.key])
        }
      }
      await client.query('UPDATE undo_entries SET undone=true WHERE id=$1', [undoId])
      return { undoId, status: 'done', keys: entries.map(entry => entry.key) }
    })
  }

  async verifyUndo(undoId: string): Promise<boolean> {
    const row = (await this.database.query<{ payload: JsonValue; undone: boolean }>('SELECT payload,undone FROM undo_entries WHERE id=$1', [undoId])).rows[0]
    if (row === undefined || !row.undone) return false
    const entries = configUndoEntries(row.payload)
    const rows = await this.database.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings WHERE key = ANY($1)', [entries.map(entry => entry.key)])
    const actual = new Map(rows.rows.map(item => [item.key, item.value]))
    return entries.every(entry => entry.existed
      ? actual.has(entry.key) && canonicalJson(actual.get(entry.key) ?? null) === canonicalJson(entry.value ?? null)
      : !actual.has(entry.key))
  }
}

function sha256Hex(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}

export class CredentialRotationService {
  constructor(
    private readonly records: EmbymediaCredentialRecords,
    private readonly validators: CredentialValidators,
    private readonly webhook: CloudDriveWebhookService,
    private readonly sender: WebhookSenderControl,
  ) {}

  async rotate(planId: string, id: CredentialId, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted()
    if (id !== 'clouddrive-webhook-secret') {
      const pending = await this.records.readPending(planId, id)
      await this.validators.validate(id, pending, signal)
      await this.records.promotePending(planId, id)
      return
    }
    const previous = await this.records.read(id)
    const pending = await this.records.readPending(planId, id)
    await this.webhook.beginRotation(planId)
    try {
      await this.sender.rotate(planId, pending, sha256Hex(previous), signal)
      await this.sender.canary(pending, signal)
      await this.records.promotePending(planId, id)
      this.webhook.endRotation(planId)
    } catch (error) {
      try {
        await this.sender.restore(planId, signal)
      } finally {
        this.webhook.endRotation(planId)
        await this.records.discardPending(planId, id)
      }
      throw error
    }
  }
}

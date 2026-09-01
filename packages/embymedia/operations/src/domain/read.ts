import { randomUUID } from 'node:crypto'
import { access, statfs } from 'node:fs/promises'
import type { Database } from '../database/index.ts'
import type { EmbymediaCredentialRecords } from '../credentials.ts'
import type { ResolvedConfig } from '../config.ts'
import { SCHEMA_VERSION, type JsonValue, type QueryResult } from '../schemas.ts'
import { EmbymediaError } from '../errors.ts'

const SENSITIVE_KEY = /password|cookie|secret|api[_-]?key|key$|proxy_url|token/i

export interface HealthCheck {
  readonly key: string
  readonly ok: boolean
  readonly detail: JsonValue
}

export interface SystemProbeSet {
  readonly emby: (signal: AbortSignal) => Promise<JsonValue>
  readonly tmdb: (signal: AbortSignal) => Promise<JsonValue>
  readonly resource: (signal: AbortSignal) => Promise<JsonValue>
  readonly c115: (signal: AbortSignal) => Promise<JsonValue>
  readonly scheduler: () => Promise<JsonValue>
  readonly backup: () => Promise<JsonValue>
}

export type HealthCheckName = 'database' | 'emby' | 'tmdb' | 'resource' | 'c115' | 'storage' | 'scheduler' | 'backup'

function mask(value: JsonValue): JsonValue {
  if (Array.isArray(value)) return value.map(mask)
  if (typeof value !== 'object' || value === null) return value
  const output: Record<string, JsonValue> = {}
  for (const [key, item] of Object.entries(value)) output[key] = SENSITIVE_KEY.test(key) ? '[REDACTED]' : mask(item)
  return output
}

function result<T extends JsonValue>(capabilityId: string, data: T, warnings: readonly string[] = []): QueryResult<T> {
  return { schemaVersion: SCHEMA_VERSION, capabilityId, correlationId: randomUUID(), data, warnings }
}

export class SystemReadService {
  constructor(
    private readonly database: Database,
    private readonly config: ResolvedConfig,
    private readonly credentials: EmbymediaCredentialRecords,
    private readonly probes: SystemProbeSet,
  ) {}

  async health(checkNames: readonly HealthCheckName[], signal: AbortSignal): Promise<QueryResult> {
    const names = checkNames.length === 0
      ? ['database', 'emby', 'tmdb', 'resource', 'c115', 'storage', 'scheduler', 'backup'] as const
      : checkNames
    const checks: HealthCheck[] = []
    for (const name of names) {
      signal.throwIfAborted()
      try {
        let detail: JsonValue
        if (name === 'database') {
          await this.database.health(signal)
          detail = { reachable: true }
        } else if (name === 'storage') {
          const [media, strm] = await Promise.all([
            this.storagePath(this.config.mediaRoot),
            this.storagePath(this.config.strmRoot),
          ])
          detail = { media, strm }
        } else {
          const probe = this.probes[name]
          detail = await probe(signal)
        }
        checks.push({ key: name, ok: true, detail: mask(detail) })
      } catch (error) {
        checks.push({ key: name, ok: false, detail: { message: error instanceof Error ? error.message : String(error) } })
      }
    }
    return result('health.check', { ok: checks.every(check => check.ok), checks } as unknown as JsonValue)
  }

  async dashboard(signal: AbortSignal): Promise<QueryResult> {
    signal.throwIfAborted()
    const [tasks, plans, actions, schedules, errors] = await Promise.all([
      this.database.query<{ status: string; count: string }>('SELECT status,count(*)::text AS count FROM task_runs GROUP BY status'),
      this.database.query<{ status: string; count: string }>('SELECT status,count(*)::text AS count FROM operation_plans GROUP BY status'),
      this.database.query<{ status: string; count: string }>('SELECT status,count(*)::text AS count FROM smart_action_runs GROUP BY status'),
      this.database.query<{ enabled: boolean; count: string }>('SELECT enabled,count(*)::text AS count FROM schedule_jobs GROUP BY enabled'),
      this.database.query<{ count: string }>("SELECT count(*)::text AS count FROM app_logs WHERE level = 'error' AND created_at > now() - interval '7 days'"),
    ])
    return result('analyze.dashboard', {
      tasks: Object.fromEntries(tasks.rows.map(row => [row.status, Number(row.count)])),
      plans: Object.fromEntries(plans.rows.map(row => [row.status, Number(row.count)])),
      smartActions: Object.fromEntries(actions.rows.map(row => [row.status, Number(row.count)])),
      schedules: Object.fromEntries(schedules.rows.map(row => [row.enabled ? 'enabled' : 'disabled', Number(row.count)])),
      errors7d: Number(errors.rows[0]?.count ?? 0),
    })
  }

  async tasks(limit: number, cursor?: string): Promise<QueryResult> {
    const safeLimit = Math.min(Math.max(limit, 1), 200)
    const values: unknown[] = [safeLimit + 1]
    const cursorSql = cursor === undefined ? '' : 'WHERE updated_at < $2::timestamptz'
    if (cursor !== undefined) {
      const parsed = new Date(cursor)
      if (Number.isNaN(parsed.getTime())) throw new EmbymediaError('INVALID_INPUT', 'task cursor must be an ISO timestamp')
      values.push(parsed.toISOString())
    }
    const rows = await this.database.query<Record<string, JsonValue>>(
      `SELECT id,kind,label,status,progress,total,status_text,cancel_requested,correlation_id,queued_at,started_at,ended_at,updated_at::text AS updated_at
       FROM task_runs ${cursorSql} ORDER BY updated_at DESC LIMIT $1`,
      values,
    )
    const page = rows.rows.slice(0, safeLimit)
    const lastUpdatedAt = page.at(-1)?.updated_at
    if (rows.rows.length > safeLimit && typeof lastUpdatedAt !== 'string') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'task cursor row is malformed')
    return {
      ...result('task.list', page),
      page: {
        ...(cursor === undefined ? {} : { cursor }),
        ...(rows.rows.length > safeLimit ? { nextCursor: lastUpdatedAt as string } : {}),
        limit: safeLimit,
      },
    }
  }

  async logs(kind: 'logs' | 'audit' | 'undo', limit: number): Promise<QueryResult> {
    const safeLimit = Math.min(Math.max(limit, 1), 200)
    const selection: Readonly<Record<typeof kind, { capability: string; sql: string }>> = {
      logs: { capability: 'audit.logs', sql: 'SELECT id,level,message,detail,correlation_id,created_at FROM app_logs ORDER BY created_at DESC LIMIT $1' },
      audit: { capability: 'audit.audit', sql: 'SELECT id,actor,action,detail,session_id,agent_id,tool_name,call_id,correlation_id,destructive,created_at FROM audit_logs ORDER BY created_at DESC LIMIT $1' },
      undo: { capability: 'audit.undo', sql: 'SELECT id,plan_id,legacy_id,op,undone,expires_at,created_at FROM undo_entries ORDER BY created_at DESC LIMIT $1' },
    }
    const selected = selection[kind]
    const rows = await this.database.query<Record<string, JsonValue>>(selected.sql, [safeLimit])
    return { ...result(selected.capability, rows.rows.map(row => mask(row))), page: { limit: safeLimit } }
  }

  async configuration(action: 'get' | 'export' | 'diagnostics' | 'credential_status', signal: AbortSignal): Promise<QueryResult> {
    signal.throwIfAborted()
    if (action === 'credential_status') return result('config.credential_status', await this.credentials.status() as unknown as JsonValue)
    const rows = await this.database.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings ORDER BY key')
    const settings = mask(Object.fromEntries(rows.rows.map(row => [row.key, row.value])))
    if (action === 'get') return result('config.get', settings)
    if (action === 'export') return result('config.export', { settings, exportedAt: new Date().toISOString() })
    const health = await this.health([], signal)
    return result('config.diagnostics', { configuration: settings, health: health.data })
  }

  private async storagePath(path: string): Promise<JsonValue> {
    await access(path)
    const stats = await statfs(path)
    return {
      path,
      blocks: stats.blocks,
      availableBlocks: stats.bavail,
      blockSize: stats.bsize,
      availableBytes: stats.bavail * stats.bsize,
    }
  }
}

export { mask as maskConfiguration }

import { createHash } from 'node:crypto'
import { stat } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { Context } from '@deepseek-ai/cordis'
import { credentialKey, credentialRef, type CredentialProvider } from '@deepseek-ai/dsh-credentials'
import LocalCredentialProvider from '@deepseek-ai/dsh-credentials-local'
import { CREDENTIAL_IDS, Database, type CredentialId } from '@embymedia/dsh-operations'
import { Pool, type QueryResultRow } from 'pg'
import { readSecretFile, writeJsonReport } from './io.ts'
import type { DatabaseInventory, ImportReport, VerifyReport } from './types.ts'

const PRESERVED_TABLES = [
  'app_settings',
  'task_runs',
  'schedule_jobs',
  'undo_entries',
  'audit_logs',
  'app_logs',
  'autostrm_seen',
  'autostrm_unmatched',
  'smart_action_runs',
  'smart_action_policies',
  'smart_action_workbench_reports',
] as const

const SECRET_KEYS: Readonly<Record<string, true>> = {
  api_key: true,
  c115_cookie: true,
  cd2_webhook_secret: true,
  tmdb_api_key: true,
  tmdb_key: true,
  tg_resource_api_token: true,
  outbound_proxy_url: true,
  password_hash: true,
}

const OPTIONAL_CREDENTIAL_IDS = new Set<CredentialId>(['resource-api-token', 'outbound-proxy-url'])
const LEGACY_KEYS: Readonly<Record<CredentialId, readonly string[]>> = {
  'emby-api-key': ['api_key'],
  'c115-cookie': ['c115_cookie'],
  'tmdb-api-key': ['tmdb_api_key', 'tmdb_key'],
  'resource-api-token': ['tg_resource_api_token'],
  'clouddrive-webhook-secret': ['cd2_webhook_secret'],
  'outbound-proxy-url': ['outbound_proxy_url'],
}

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}

function stableUuid(value: string): string {
  const bytes = createHash('sha256').update(value).digest().subarray(0, 16)
  bytes[6] = (bytes[6]! & 0x0f) | 0x50
  bytes[8] = (bytes[8]! & 0x3f) | 0x80
  const hex = bytes.toString('hex')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

function json(value: unknown, fallback: unknown = {}): string {
  return JSON.stringify(value ?? fallback)
}

async function tableExists(pool: Pool, table: string): Promise<boolean> {
  const result = await pool.query<{ name: string | null }>('SELECT to_regclass($1) AS name', [`public.${table}`])
  return result.rows[0]?.name !== null
}

async function tableCounts(pool: Pool): Promise<Record<string, number>> {
  const counts: Record<string, number> = {}
  for (const table of PRESERVED_TABLES) {
    if (!await tableExists(pool, table)) {
      counts[table] = 0
      continue
    }
    const result = table === 'app_settings'
      ? await pool.query<{ count: string }>(
        'SELECT count(*)::text AS count FROM app_settings WHERE NOT (key = ANY($1)) AND key <> \'schedules\'',
        [Object.keys(SECRET_KEYS)],
      )
      : await pool.query<{ count: string }>(`SELECT count(*)::text AS count FROM ${table}`)
    counts[table] = Number(result.rows[0]?.count ?? 0)
  }
  return counts
}

async function appSettings(pool: Pool): Promise<Map<string, unknown>> {
  if (!await tableExists(pool, 'app_settings')) return new Map()
  const result = await pool.query<{ key: string; value: unknown }>('SELECT key,value FROM app_settings')
  return new Map(result.rows.map(row => [row.key, row.value]))
}

function secretString(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed.length === 0 ? undefined : trimmed
}

async function secretInventory(pool: Pool) {
  const settings = await appSettings(pool)
  return CREDENTIAL_IDS.map((id) => {
    const value = LEGACY_KEYS[id].map(key => secretString(settings.get(key))).find(candidate => candidate !== undefined)
    return { id, configured: value !== undefined, ...(value === undefined ? {} : { sha256: sha256(value) }) }
  })
}

async function activeCounts(pool: Pool): Promise<{ activeSchedules: number; runningTasks: number }> {
  const activeSchedules = await tableExists(pool, 'schedule_jobs')
    ? Number((await pool.query<{ count: string }>('SELECT count(*)::text AS count FROM schedule_jobs WHERE enabled')).rows[0]?.count ?? 0)
    : 0
  const runningTasks = await tableExists(pool, 'task_runs')
    ? Number((await pool.query<{ count: string }>("SELECT count(*)::text AS count FROM task_runs WHERE status IN ('pending','queued','running','verifying')")).rows[0]?.count ?? 0)
    : 0
  return { activeSchedules, runningTasks }
}

export async function inventory(options: {
  readonly databaseUrlFile: string
  readonly dshHome: string
  readonly output: string
}): Promise<DatabaseInventory> {
  const pool = new Pool({ connectionString: await readSecretFile(options.databaseUrlFile), max: 2 })
  try {
    const counts = await tableCounts(pool)
    const active = await activeCounts(pool)
    const settings = await appSettings(pool)
    const report: DatabaseInventory = {
      schemaVersion: 1,
      generatedAt: new Date().toISOString(),
      tables: counts,
      ...active,
      credentials: await secretInventory(pool),
      secretSettingKeys: [...settings.keys()].filter(key => SECRET_KEYS[key]).sort(),
    }
    await writeJsonReport(options.output, report)
    return report
  } finally {
    await pool.end()
  }
}

async function withCredentialProvider<T>(
  dshHome: string,
  work: (provider: CredentialProvider) => Promise<T>,
): Promise<T> {
  const context = new Context()
  const fiber = context.plugin(LocalCredentialProvider, { dshHome: resolve(dshHome), watch: false })
  await fiber
  try {
    return await work(context.credentials)
  } finally {
    await fiber.dispose()
  }
}

async function rows(pool: Pool, table: string): Promise<QueryResultRow[]> {
  if (!await tableExists(pool, table)) return []
  return (await pool.query(`SELECT * FROM ${table}`)).rows
}

async function copyState(source: Pool, target: Database): Promise<Record<string, number>> {
  const copied: Record<string, number> = {}
  const settings = await rows(source, 'app_settings')
  copied.app_settings = 0
  for (const row of settings) {
    if (typeof row.key !== 'string' || SECRET_KEYS[row.key] || row.key === 'schedules') continue
    const result = await target.query(
      `INSERT INTO app_settings(key,value,updated_at) VALUES ($1,$2,$3)
       ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`,
      [row.key, json(row.value), row.updated_at ?? new Date()],
    )
    copied.app_settings += result.rowCount ?? 0
  }

  const tasks = await rows(source, 'task_runs')
  copied.task_runs = 0
  for (const row of tasks) {
    const status = row.status === 'pending' ? 'queued' : row.status === 'running' ? 'interrupted' : row.status
    const result = await target.query(
      `INSERT INTO task_runs(
        id,kind,label,source,params,status,progress,total,status_text,result,error,cancel_requested,
        correlation_id,queued_at,started_at,ended_at,updated_at
      ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
      ON CONFLICT(id) DO NOTHING`,
      [
        row.id, row.kind, row.label ?? '', row.source ?? 'legacy-import', json(row.params), status,
        row.progress ?? 0, row.total ?? 0, row.status_text ?? '', row.result === null ? null : json(row.result),
        row.error == null ? null : json(typeof row.error === 'string' ? { message: row.error } : row.error),
        row.cancel_requested ?? false, stableUuid(`task:${String(row.id)}`), row.queued_at ?? new Date(),
        row.started_at, row.ended_at, row.updated_at ?? new Date(),
      ],
    )
    copied.task_runs += result.rowCount ?? 0
  }

  const schedules = await rows(source, 'schedule_jobs')
  copied.schedule_jobs = 0
  for (const row of schedules) {
    const params = row.params ?? {}
    const cadence = row.cadence ?? row.schedule ?? {}
    const result = await target.query(
      `INSERT INTO schedule_jobs(
        id,name,kind,params,cadence,enabled,version,version_hash,risk,approved_at,approved_by,
        last_run_at,last_ended_at,last_status,last_task_id,last_error,created_at,updated_at
      ) VALUES ($1,$2,$3,$4,$5,false,1,$6,'medium',$7,'legacy-import',$8,$9,$10,$11,$12,$13,$14)
      ON CONFLICT(id) DO NOTHING`,
      [
        row.id, row.name, row.kind, json(params), json(cadence), sha256(json({ kind: row.kind, params, cadence })),
        new Date(0), row.last_run_at, row.last_ended_at, row.last_status, row.last_task_id,
        row.last_error == null ? null : json({ message: row.last_error }), row.created_at ?? new Date(), row.updated_at ?? new Date(),
      ],
    )
    copied.schedule_jobs += result.rowCount ?? 0
  }

  const undo = await rows(source, 'undo_entries')
  copied.undo_entries = 0
  for (const row of undo) {
    const result = await target.query(
      `INSERT INTO undo_entries(id,plan_id,legacy_id,op,payload,undone,created_at)
       VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`,
      [row.id, stableUuid(`legacy-undo-plan:${String(row.id)}`), row.legacy_id, row.op, json(row.payload), row.undone ?? false, row.created_at ?? new Date()],
    )
    copied.undo_entries += result.rowCount ?? 0
  }

  for (const table of ['audit_logs', 'app_logs'] as const) {
    const sourceRows = await rows(source, table)
    copied[table] = 0
    for (const row of sourceRows) {
      const result = table === 'audit_logs'
        ? await target.query(
          `INSERT INTO audit_logs(id,actor,action,detail,correlation_id,created_at)
             VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,
          [row.id, row.actor ?? 'legacy', row.action, json(row.detail), stableUuid(`audit:${String(row.id)}`), row.created_at ?? new Date()],
        )
        : await target.query(
          `INSERT INTO app_logs(id,level,message,detail,correlation_id,created_at)
             VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,
          [row.id, row.level, row.message, json(row.detail), stableUuid(`log:${String(row.id)}`), row.created_at ?? new Date()],
        )
      copied[table] += result.rowCount ?? 0
    }
  }

  for (const table of ['autostrm_seen', 'autostrm_unmatched'] as const) {
    const sourceRows = await rows(source, table)
    copied[table] = 0
    for (const row of sourceRows) {
      const result = table === 'autostrm_seen'
        ? await target.query(
          'INSERT INTO autostrm_seen(id,lib,top,mtime,updated_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING',
          [row.id, row.lib, row.top, row.mtime, row.updated_at ?? new Date()],
        )
        : await target.query(
          `INSERT INTO autostrm_unmatched(id,lib,top,emby_id,name,created_at,updated_at)
             VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
          [row.id, row.lib, row.top, row.emby_id, row.name, row.created_at ?? new Date(), row.updated_at ?? new Date()],
        )
      copied[table] += result.rowCount ?? 0
    }
  }

  const actions = await rows(source, 'smart_action_runs')
  copied.smart_action_runs = 0
  for (const row of actions) {
    const result = await target.query(
      `INSERT INTO smart_action_runs(
        id,action_type,status,subject,title,summary,recommendation,evidence,plan,risk,policy,verification,
        source,tab,action_label,task_id,result,error,created_at,updated_at,expires_at
      ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
      ON CONFLICT(id) DO NOTHING`,
      [
        row.id, row.action_type, row.status, json(row.subject), row.title, row.summary, json(row.recommendation),
        json(row.evidence), json(row.plan), json(row.risk), json(row.policy), json(row.verification),
        row.source ?? 'legacy-import', row.tab ?? 'smart-actions', row.action_label ?? '查看详情', row.task_id,
        row.result == null ? null : json(row.result), row.error == null ? null : json({ message: row.error }),
        row.created_at ?? new Date(), row.updated_at ?? new Date(), row.expires_at,
      ],
    )
    copied.smart_action_runs += result.rowCount ?? 0
  }

  for (const table of ['smart_action_policies', 'smart_action_workbench_reports'] as const) {
    const sourceRows = await rows(source, table)
    copied[table] = 0
    for (const row of sourceRows) {
      const result = table === 'smart_action_policies'
        ? await target.query(
          `INSERT INTO smart_action_policies(key,enabled,mode,max_risk,params,updated_at)
             VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(key) DO UPDATE SET enabled=EXCLUDED.enabled,mode=EXCLUDED.mode,max_risk=EXCLUDED.max_risk,params=EXCLUDED.params,updated_at=EXCLUDED.updated_at`,
          [row.key, row.enabled, row.mode, row.max_risk, json(row.params), row.updated_at ?? new Date()],
        )
        : await target.query(
          `INSERT INTO smart_action_workbench_reports(id,params,report,created_at,expires_at)
             VALUES ($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`,
          [row.id, json(row.params), json(row.report), row.created_at ?? new Date(), row.expires_at],
        )
      copied[table] += result.rowCount ?? 0
    }
  }
  return copied
}

export async function importState(options: {
  readonly databaseUrlFile: string
  readonly targetUrlFile: string
  readonly deepseekApiKeyFile: string
  readonly dshHome: string
  readonly report: string
}): Promise<ImportReport> {
  const source = new Pool({ connectionString: await readSecretFile(options.databaseUrlFile), max: 2 })
  const target = new Database(await readSecretFile(options.targetUrlFile), 4)
  try {
    await target.initialize()
    const credentials = await secretInventory(source)
    const settings = await appSettings(source)
    const required = credentials.filter(item => !item.configured && !OPTIONAL_CREDENTIAL_IDS.has(item.id))
    if (required.length > 0) throw new Error(`legacy database is missing product credentials: ${required.map(item => item.id).join(', ')}`)
    const copied = await copyState(source, target)
    const deepseek = await readSecretFile(options.deepseekApiKeyFile)
    const writtenCredentials: CredentialId[] = []
    await withCredentialProvider(options.dshHome, async (provider) => {
      for (const id of CREDENTIAL_IDS) {
        const value = LEGACY_KEYS[id].map(key => secretString(settings.get(key))).find(candidate => candidate !== undefined)
        if (value === undefined) continue
        await provider.modifyRecord(credentialKey('embymedia', id), async () => ({
          kind: 'grant', payload: { version: 1, value },
        }))
        writtenCredentials.push(id)
      }
      await provider.set(credentialRef('DEEPSEEK_API_KEY'), deepseek)
    })
    const report: ImportReport = {
      schemaVersion: 1,
      importedAt: new Date().toISOString(),
      copied,
      credentialRecords: writtenCredentials,
      deepseekConfigured: true,
      activeSchedules: 0,
      runningTasks: 0,
      warnings: [],
    }
    await writeJsonReport(options.report, report)
    return report
  } finally {
    await Promise.all([source.end(), target.close()])
  }
}

export async function verifyState(options: {
  readonly source: string
  readonly targetUrlFile: string
  readonly dshHome: string
  readonly report: string
}): Promise<VerifyReport> {
  const expected = JSON.parse(await readSecretFile(options.source)) as DatabaseInventory
  const targetPool = new Pool({ connectionString: await readSecretFile(options.targetUrlFile), max: 2 })
  const failures: string[] = []
  try {
    const counts = await tableCounts(targetPool)
    const active = await activeCounts(targetPool)
    const settings = await appSettings(targetPool)
    const secretSettingKeys = [...settings.keys()].filter(key => SECRET_KEYS[key]).sort()
    const tableCountsMatch = PRESERVED_TABLES.every(table => counts[table] === expected.tables[table])
    if (!tableCountsMatch) failures.push('preserved table counts differ')
    if (active.activeSchedules !== 0) failures.push('active schedules remain')
    if (active.runningTasks !== 0) failures.push('running tasks remain')
    if (secretSettingKeys.length > 0) failures.push('app_settings still contains secrets')
    const home = resolve(options.dshHome)
    const credentialsFile = join(home, '.credentials.yaml')
    const file = await stat(credentialsFile)
    const credentialsMode = (file.mode & 0o777).toString(8).padStart(4, '0')
    const ownerMatches = typeof process.getuid !== 'function'
      || (file.uid === process.getuid() && file.gid === process.getgid!())
    if (credentialsMode !== '0600') failures.push('credentials file mode is not 0600')
    if (!ownerMatches) failures.push('credentials file owner differs from migration process')

    const credentialRecords = await withCredentialProvider(home, async (provider) => {
      const output = {} as Record<CredentialId, { configured: boolean; writable: boolean }>
      for (const id of CREDENTIAL_IDS) {
        const status = await provider.describeRecord(credentialKey('embymedia', id))
        output[id] = { configured: status.configured && status.kind === 'grant', writable: status.writable }
        if (OPTIONAL_CREDENTIAL_IDS.has(id)) continue
        if (!output[id].configured || !output[id].writable) failures.push(`${id} record is not configured and writable`)
      }
      const deepseek = await provider.describe(credentialRef('DEEPSEEK_API_KEY'))
      if (!deepseek.configured) failures.push('DEEPSEEK_API_KEY ref is not configured')
      return { output, deepseekConfigured: deepseek.configured }
    })

    const report: VerifyReport = {
      schemaVersion: 1,
      verifiedAt: new Date().toISOString(),
      ok: failures.length === 0,
      dshHome: home,
      credentialsFile,
      credentialsMode,
      credentialsOwnerMatchesProcess: ownerMatches,
      tableCountsMatch,
      ...active,
      credentialRecords: credentialRecords.output,
      deepseekConfigured: credentialRecords.deepseekConfigured,
      secretSettingKeys,
      failures,
    }
    await writeJsonReport(options.report, report)
    return report
  } finally {
    await targetPool.end()
  }
}

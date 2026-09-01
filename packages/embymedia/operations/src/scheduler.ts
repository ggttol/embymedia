import { createHash, randomUUID } from 'node:crypto'
import type { Database } from './database/index.ts'
import type { PersistentJobService, JobOutcome } from './jobs.ts'
import { canonicalJson, type JsonValue, type TaskRunProjection } from './schemas.ts'
import { SCHEDULE_KINDS, type Risk, type ScheduleKind } from './capabilities.ts'
import { SCHEDULE_RISKS, assertStandingAuthorization, riskAtMost } from './risk.ts'
import { EmbymediaError } from './errors.ts'

export interface ScheduleCadence {
  readonly mode: 'interval' | 'hourly' | 'daily'
  readonly minutes?: number
  readonly minute?: number
  readonly hour?: number
}

export interface ScheduleVersionInput {
  readonly scheduleId?: string
  readonly name: string
  readonly kind: ScheduleKind
  readonly params: JsonValue
  readonly cadence: ScheduleCadence
  readonly enabled: boolean
  readonly approvedRisk: Risk
  readonly approvedBy: string
}

export interface ScheduleProjection {
  readonly id: string
  readonly name: string
  readonly kind: ScheduleKind
  readonly params: JsonValue
  readonly cadence: ScheduleCadence
  readonly enabled: boolean
  readonly version: number
  readonly versionHash: string
  readonly risk: Risk
  readonly approvedAt: string
  readonly approvedBy: string
  readonly nextRunAt?: string
  readonly lastRunAt?: string
  readonly lastStatus?: string
  readonly lastTaskId?: string
}

export interface ScheduleHandler {
  readonly operation: 'library.scan' | 'poster.fix_batch' | 'metadata.refresh' | 'schedule.run'
  readonly run: (params: JsonValue, signal: AbortSignal) => Promise<JobOutcome>
}

interface ScheduleRow {
  readonly id: string
  readonly name: string
  readonly kind: ScheduleKind
  readonly params: JsonValue
  readonly cadence: ScheduleCadence
  readonly enabled: boolean
  readonly version: number
  readonly version_hash: string
  readonly risk: Risk
  readonly approved_at: Date
  readonly approved_by: string
  readonly next_run_at: Date | null
  readonly last_run_at: Date | null
  readonly last_status: string | null
  readonly last_task_id: string | null
}

function projection(row: ScheduleRow): ScheduleProjection {
  return {
    id: row.id,
    name: row.name,
    kind: row.kind,
    params: row.params,
    cadence: row.cadence,
    enabled: row.enabled,
    version: row.version,
    versionHash: row.version_hash,
    risk: row.risk,
    approvedAt: row.approved_at.toISOString(),
    approvedBy: row.approved_by,
    ...(row.next_run_at === null ? {} : { nextRunAt: row.next_run_at.toISOString() }),
    ...(row.last_run_at === null ? {} : { lastRunAt: row.last_run_at.toISOString() }),
    ...(row.last_status === null ? {} : { lastStatus: row.last_status }),
    ...(row.last_task_id === null ? {} : { lastTaskId: row.last_task_id }),
  }
}

function validateCadence(cadence: ScheduleCadence): void {
  if (cadence.mode === 'interval') {
    if (!Number.isInteger(cadence.minutes) || cadence.minutes! < 1 || cadence.minutes! > 24 * 60) throw new EmbymediaError('INVALID_INPUT', 'interval minutes must be 1..1440')
  } else if (cadence.mode === 'hourly') {
    if (!Number.isInteger(cadence.minute) || cadence.minute! < 0 || cadence.minute! > 59) throw new EmbymediaError('INVALID_INPUT', 'hourly minute must be 0..59')
  } else if (!Number.isInteger(cadence.hour) || cadence.hour! < 0 || cadence.hour! > 23
    || !Number.isInteger(cadence.minute) || cadence.minute! < 0 || cadence.minute! > 59) {
    throw new EmbymediaError('INVALID_INPUT', 'daily hour/minute are invalid')
  }
}

export function nextRun(cadence: ScheduleCadence, after: Date): Date {
  validateCadence(cadence)
  if (cadence.mode === 'interval') return new Date(after.getTime() + cadence.minutes! * 60_000)
  const next = new Date(after)
  next.setUTCSeconds(0, 0)
  if (cadence.mode === 'hourly') {
    next.setUTCMinutes(cadence.minute!)
    if (next.getTime() <= after.getTime()) next.setUTCHours(next.getUTCHours() + 1)
  } else {
    next.setUTCHours(cadence.hour!, cadence.minute, 0, 0)
    if (next.getTime() <= after.getTime()) next.setUTCDate(next.getUTCDate() + 1)
  }
  return next
}

export class PersistentScheduler {
  private timer: NodeJS.Timeout | undefined
  private tick: Promise<void> = Promise.resolve()
  private readonly completions = new Set<Promise<void>>()

  constructor(
    private readonly database: Database,
    private readonly jobs: PersistentJobService,
    private readonly handlers: Readonly<Partial<Record<ScheduleKind, ScheduleHandler>>>,
    private readonly onError: (error: unknown) => void = () => {},
  ) {}

  async upsertApproved(input: ScheduleVersionInput): Promise<ScheduleProjection> {
    if (!(SCHEDULE_KINDS as readonly string[]).includes(input.kind)) throw new EmbymediaError('INVALID_INPUT', 'unsupported schedule kind')
    validateCadence(input.cadence)
    const risk = SCHEDULE_RISKS[input.kind]
    if (!riskAtMost(risk, input.approvedRisk) || !riskAtMost(risk, 'medium')) throw new EmbymediaError('POLICY_DENIED', 'approved schedule risk is insufficient')
    const scheduleId = input.scheduleId ?? randomUUID()
    const material = canonicalJson({ kind: input.kind, params: input.params, cadence: input.cadence, risk } as unknown as JsonValue)
    const versionHash = createHash('sha256').update(material).digest('hex')
    return this.database.transaction(async (client) => {
      const current = (await client.query<ScheduleRow>('SELECT * FROM schedule_jobs WHERE id=$1 FOR UPDATE', [scheduleId])).rows[0]
      if (current?.version_hash === versionHash && current.enabled === input.enabled && current.name === input.name) return projection(current)
      const version = (current?.version ?? 0) + 1
      const approvedAt = new Date()
      await client.query(
        `INSERT INTO schedule_versions(schedule_id,version,version_hash,kind,params,cadence,risk,approved_at,approved_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
        [scheduleId, version, versionHash, input.kind, JSON.stringify(input.params), JSON.stringify(input.cadence), risk, approvedAt, input.approvedBy],
      )
      const next = input.enabled ? nextRun(input.cadence, approvedAt) : null
      const row = await client.query<ScheduleRow>(
        `INSERT INTO schedule_jobs(
          id,name,kind,params,cadence,enabled,version,version_hash,risk,approved_at,approved_by,next_run_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
        ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,kind=EXCLUDED.kind,params=EXCLUDED.params,cadence=EXCLUDED.cadence,
          enabled=EXCLUDED.enabled,version=EXCLUDED.version,version_hash=EXCLUDED.version_hash,risk=EXCLUDED.risk,
          approved_at=EXCLUDED.approved_at,approved_by=EXCLUDED.approved_by,next_run_at=EXCLUDED.next_run_at,updated_at=now()
        RETURNING *`,
        [scheduleId, input.name, input.kind, JSON.stringify(input.params), JSON.stringify(input.cadence), input.enabled, version, versionHash, risk, approvedAt, input.approvedBy, next],
      )
      return projection(row.rows[0]!)
    })
  }

  async list(): Promise<readonly ScheduleProjection[]> {
    const rows = await this.database.query<ScheduleRow>('SELECT * FROM schedule_jobs ORDER BY created_at')
    return rows.rows.map(projection)
  }

  async disable(id: string): Promise<void> {
    const result = await this.database.query('UPDATE schedule_jobs SET enabled=false,next_run_at=NULL,updated_at=now() WHERE id=$1', [id])
    if (result.rowCount === 0) throw new EmbymediaError('NOT_FOUND', 'schedule not found')
  }

  async runNow(id: string): Promise<TaskRunProjection> {
    const row = (await this.database.query<ScheduleRow>('SELECT * FROM schedule_jobs WHERE id=$1', [id])).rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'schedule not found')
    return this.launch(row)
  }

  async runDue(now = new Date()): Promise<number> {
    const due = await this.database.transaction(async (client) => {
      const lock = await client.query<{ acquired: boolean }>("SELECT pg_try_advisory_xact_lock(hashtext('embymedia-scheduler')) AS acquired")
      if (lock.rows[0]?.acquired !== true) return [] as ScheduleRow[]
      const rows = await client.query<ScheduleRow>(
        'SELECT * FROM schedule_jobs WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at FOR UPDATE SKIP LOCKED',
        [now],
      )
      for (const row of rows.rows) {
        await client.query('UPDATE schedule_jobs SET next_run_at=$2,updated_at=now() WHERE id=$1', [row.id, nextRun(row.cadence, now)])
      }
      return rows.rows
    })
    for (const row of due) await this.launch(row)
    return due.length
  }

  start(intervalMs = 30_000): void {
    if (this.timer !== undefined) throw new EmbymediaError('CONFLICT', 'scheduler already started')
    this.timer = setInterval(() => {
      this.tick = this.tick.then(() => this.runDue()).then(() => undefined).catch((error) => { this.onError(error) })
    }, intervalMs)
  }

  async close(): Promise<void> {
    if (this.timer !== undefined) clearInterval(this.timer)
    this.timer = undefined
    await this.tick
    await Promise.all([...this.completions])
  }

  private async launch(row: ScheduleRow): Promise<TaskRunProjection> {
    const handler = this.handlers[row.kind]
    if (handler === undefined) throw new EmbymediaError('POLICY_DENIED', `schedule ${row.kind} has no handler`)
    assertStandingAuthorization(row.kind, handler.operation, row.risk)
    const task = await this.jobs.start({
      kind: row.kind,
      label: row.name,
      source: 'schedule',
      params: row.params,
      authorizingScheduleId: row.id,
      authorizingScheduleVersion: row.version,
      run: signal => handler.run(row.params, signal),
    })
    await this.database.query(
      'UPDATE schedule_jobs SET last_run_at=now(),last_task_id=$2,last_status=$3,updated_at=now() WHERE id=$1',
      [row.id, task.id, task.status],
    )
    const completion = this.jobs.wait(task.id).then(async (completed) => {
      await this.database.query(
        'UPDATE schedule_jobs SET last_ended_at=now(),last_status=$2,last_error=$3,updated_at=now() WHERE id=$1',
        [row.id, completed.status, completed.error === undefined ? null : JSON.stringify(completed.error)],
      )
    }).catch((error) => { this.onError(error) })
    this.completions.add(completion)
    void completion.finally(() => { this.completions.delete(completion) }).catch(() => {})
    return task
  }
}

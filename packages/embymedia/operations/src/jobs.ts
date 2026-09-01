import { randomUUID } from 'node:crypto'
import type { Database } from './database/index.ts'
import { CancellationScope } from './cancellation.ts'
import { Semaphore } from './clients/semaphore.ts'
import { EmbymediaError } from './errors.ts'
import { SCHEMA_VERSION, type JsonValue, type TaskRunProjection, type TaskStatus } from './schemas.ts'

export interface JobOutcome {
  readonly status?: 'done' | 'partial'
  readonly result: JsonValue
}

export interface JobSpec {
  readonly kind: string
  readonly label: string
  readonly source: 'interactive' | 'webhook' | 'schedule' | 'maintenance'
  readonly params: JsonValue
  readonly total?: number
  readonly cloud?: boolean
  readonly planId?: string
  readonly authorizingScheduleId?: string
  readonly authorizingScheduleVersion?: number
  readonly run: (
    signal: AbortSignal,
    progress: (completed: number, total: number, statusText: string) => Promise<void>,
  ) => Promise<JobOutcome>
}

interface TaskRow {
  readonly id: string
  readonly kind: string
  readonly label: string
  readonly status: TaskStatus
  readonly progress: string
  readonly total: string
  readonly status_text: string
  readonly cancel_requested: boolean
  readonly correlation_id: string
  readonly queued_at: Date
  readonly updated_at: Date
  readonly started_at: Date | null
  readonly ended_at: Date | null
  readonly result: JsonValue | null
  readonly error: JsonValue | null
}

interface ActiveJob {
  readonly scope: CancellationScope
  readonly done: Promise<void>
}

function project(row: TaskRow): TaskRunProjection {
  return {
    schemaVersion: SCHEMA_VERSION,
    id: row.id,
    kind: row.kind,
    label: row.label,
    status: row.status,
    progress: Number(row.progress),
    total: Number(row.total),
    statusText: row.status_text,
    cancelRequested: row.cancel_requested,
    correlationId: row.correlation_id,
    queuedAt: row.queued_at.toISOString(),
    updatedAt: row.updated_at.toISOString(),
    ...(row.started_at === null ? {} : { startedAt: row.started_at.toISOString() }),
    ...(row.ended_at === null ? {} : { endedAt: row.ended_at.toISOString() }),
    ...(row.result === null ? {} : { result: row.result }),
    ...(row.error === null ? {} : { error: row.error }),
  }
}

export class PersistentJobService {
  private readonly taskSlots: Semaphore
  private readonly cloudSlot = new Semaphore(1)
  private readonly active = new Map<string, ActiveJob>()
  private accepting = true

  constructor(private readonly database: Database, concurrency: number) {
    this.taskSlots = new Semaphore(concurrency)
  }

  async start(spec: JobSpec): Promise<TaskRunProjection> {
    if (!this.accepting) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'job service is stopping')
    const id = randomUUID()
    const correlationId = randomUUID()
    const inserted = await this.database.query<TaskRow>(
      `INSERT INTO task_runs(
        id,plan_id,kind,label,source,params,status,total,correlation_id,authorizing_schedule_id,authorizing_schedule_version
      ) VALUES ($1,$2,$3,$4,$5,$6,'queued',$7,$8,$9,$10) RETURNING *`,
      [
        id, spec.planId ?? null, spec.kind, spec.label, spec.source, JSON.stringify(spec.params), spec.total ?? 0,
        correlationId, spec.authorizingScheduleId ?? null, spec.authorizingScheduleVersion ?? null,
      ],
    )
    const scope = new CancellationScope()
    const done = this.run(id, spec, scope)
    this.active.set(id, { scope, done })
    void done.finally(() => { this.active.delete(id) }).catch(() => {})
    return project(inserted.rows[0]!)
  }

  async list(limit = 100): Promise<readonly TaskRunProjection[]> {
    const rows = await this.database.query<TaskRow>('SELECT * FROM task_runs ORDER BY updated_at DESC LIMIT $1', [Math.min(Math.max(limit, 1), 500)])
    return rows.rows.map(project)
  }

  async listPage(limit = 100, cursor?: string): Promise<{ readonly items: readonly TaskRunProjection[]; readonly nextCursor?: string }> {
    const safeLimit = Math.min(Math.max(limit, 1), 500)
    let updatedAt: string | undefined
    let id: string | undefined
    if (cursor !== undefined) {
      try {
        const decoded = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8')) as { updatedAt?: unknown; id?: unknown }
        if (typeof decoded.updatedAt !== 'string' || Number.isNaN(new Date(decoded.updatedAt).getTime()) || typeof decoded.id !== 'string' || decoded.id.length === 0) throw new Error('invalid cursor')
        updatedAt = decoded.updatedAt
        id = decoded.id
      } catch {
        throw new EmbymediaError('INVALID_INPUT', 'task cursor is invalid')
      }
    }
    const rows = updatedAt === undefined
      ? await this.database.query<TaskRow>('SELECT * FROM task_runs ORDER BY updated_at DESC,id DESC LIMIT $1', [safeLimit + 1])
      : await this.database.query<TaskRow>('SELECT * FROM task_runs WHERE (updated_at,id) < ($2::timestamptz,$3) ORDER BY updated_at DESC,id DESC LIMIT $1', [safeLimit + 1, updatedAt, id])
    const page = rows.rows.slice(0, safeLimit)
    const last = page.at(-1)
    return {
      items: page.map(project),
      ...(rows.rows.length > safeLimit && last !== undefined ? { nextCursor: Buffer.from(JSON.stringify({ updatedAt: last.updated_at.toISOString(), id: last.id })).toString('base64url') } : {}),
    }
  }

  async get(id: string): Promise<TaskRunProjection> {
    const row = (await this.database.query<TaskRow>('SELECT * FROM task_runs WHERE id=$1', [id])).rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'task not found')
    return project(row)
  }

  async wait(id: string): Promise<TaskRunProjection> {
    const active = this.active.get(id)
    if (active !== undefined) await active.done
    return this.get(id)
  }

  async cancel(id: string, actor: string): Promise<TaskRunProjection> {
    const active = this.active.get(id)
    if (active === undefined) {
      const task = await this.get(id)
      if (['done', 'partial', 'error', 'cancelled', 'interrupted'].includes(task.status)) return task
      throw new EmbymediaError('CONFLICT', 'task is not owned by this running process')
    }
    const row = await this.database.query<{ source: string }>('SELECT source FROM task_runs WHERE id=$1', [id])
    const source = row.rows[0]?.source
    if (source !== undefined && source !== 'interactive') {
      throw new EmbymediaError('POLICY_DENIED', 'system-source tasks cannot be cancelled via the tool')
    }
    await this.database.query('UPDATE task_runs SET cancel_requested=true,updated_at=now(),status_text=$2 WHERE id=$1', [id, 'cancellation requested'])
    await this.database.query(
      'INSERT INTO audit_logs(actor,action,detail,correlation_id) VALUES ($1,\'task.cancel\',$2,$3)',
      [actor, JSON.stringify({ taskId: id }), randomUUID()],
    )
    await active.scope.cancelAndWait(new Error(`task ${id} cancelled by ${actor}`))
    await active.done
    return this.get(id)
  }

  async close(): Promise<void> {
    this.accepting = false
    const jobs = [...this.active.values()]
    await Promise.all(jobs.map(job => job.scope.cancelAndWait(new Error('job service stopping'))))
    await Promise.all(jobs.map(job => job.done))
  }

  private async run(id: string, spec: JobSpec, scope: CancellationScope): Promise<void> {
    try {
      await this.taskSlots.run(scope.signal, async () => {
        const execute = async (): Promise<JobOutcome> => {
          await this.database.query("UPDATE task_runs SET status='running',started_at=now(),updated_at=now(),status_text='running' WHERE id=$1", [id])
          return scope.run(signal => spec.run(signal, async (completed, total, statusText) => {
            signal.throwIfAborted()
            if (completed < 0 || total < 0 || completed > total) throw new EmbymediaError('INVALID_INPUT', 'invalid task progress')
            await this.database.query('UPDATE task_runs SET progress=$2,total=$3,status_text=$4,updated_at=now() WHERE id=$1', [id, completed, total, statusText])
          }))
        }
        const outcome = spec.cloud ? await this.cloudSlot.run(scope.signal, execute) : await execute()
        const status = outcome.status ?? 'done'
        await this.database.query(
          'UPDATE task_runs SET status=$2,result=$3,ended_at=now(),updated_at=now(),status_text=$4 WHERE id=$1',
          [id, status, JSON.stringify(outcome.result), status === 'done' ? 'completed' : 'partially completed'],
        )
      })
    } catch (error) {
      const cancelled = scope.signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED')
      await this.database.query(
        'UPDATE task_runs SET status=$2,error=$3,ended_at=now(),updated_at=now(),status_text=$4 WHERE id=$1',
        [id, cancelled ? 'cancelled' : 'error', JSON.stringify({ message: error instanceof Error ? error.message : String(error) }), cancelled ? 'cancelled' : 'failed'],
      )
    }
  }
}

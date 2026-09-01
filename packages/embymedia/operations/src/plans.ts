import { randomUUID } from 'node:crypto'
import type { PoolClient } from 'pg'
import type { Database } from './database/index.ts'
import { EmbymediaError } from './errors.ts'
import {
  canonicalJson,
  previewHash,
  SCHEMA_VERSION,
  type JsonValue,
  type OperationProjection,
  type PlanStatus,
  type TargetProjection,
} from './schemas.ts'
import type { OperationKind, Risk } from './capabilities.ts'

const TRANSITIONS: Readonly<Record<PlanStatus, readonly PlanStatus[]>> = {
  previewed: ['approved', 'cancelled', 'expired'],
  approved: ['queued', 'failed', 'cancelled'],
  queued: ['running', 'cancelled', 'interrupted'],
  running: ['verifying', 'partial', 'failed', 'cancelled', 'interrupted'],
  verifying: ['done', 'partial', 'failed', 'cancelled', 'interrupted'],
  done: [],
  partial: [],
  failed: [],
  cancelled: [],
  expired: [],
  interrupted: [],
}

export const TERMINAL_STATUS: Readonly<Record<PlanStatus, boolean>> = {
  previewed: false,
  approved: false,
  queued: false,
  running: false,
  verifying: false,
  done: true,
  partial: true,
  failed: true,
  cancelled: true,
  expired: true,
  interrupted: true,
}

type OperationError = NonNullable<OperationProjection['error']>

interface PlanDatabaseRow {
  readonly id: string
  readonly kind: OperationKind
  readonly status: PlanStatus
  readonly requested_by: string
  readonly session_id: string
  readonly preview: JsonValue
  readonly preview_hash: string
  readonly risk: Risk
  readonly destructive: boolean
  readonly reversible: boolean
  readonly confirmation: JsonValue
  readonly targets: readonly TargetProjection[]
  readonly steps: readonly string[]
  readonly idempotency_key: string
  readonly expires_at: Date
  readonly approved_at: Date | null
  readonly task_id: string | null
  readonly result: JsonValue | null
  readonly verification: JsonValue | null
  readonly error: OperationError | null
  readonly correlation_id: string
  readonly created_at: Date
  readonly updated_at: Date
}

export interface CreateOperationPlan {
  readonly kind: OperationKind
  readonly requestedBy: string
  readonly sessionId: string
  readonly risk: Risk
  readonly destructive: boolean
  readonly reversible: boolean
  readonly confirmation: JsonValue
  readonly targets: readonly TargetProjection[]
  readonly steps: readonly string[]
  readonly verification: JsonValue
  readonly expiresAt: Date
  readonly idempotencyKey: string
  readonly correlationId?: string
}

export interface TransitionPatch {
  readonly approvedAt?: Date
  readonly taskId?: string
  readonly result?: JsonValue
  readonly verification?: JsonValue
  readonly error?: OperationError
}

function projection(row: PlanDatabaseRow): OperationProjection {
  return {
    schemaVersion: SCHEMA_VERSION,
    id: row.id,
    kind: row.kind,
    status: row.status,
    previewHash: row.preview_hash,
    risk: row.risk,
    destructive: row.destructive,
    reversible: row.reversible,
    confirmation: row.confirmation,
    targets: row.targets,
    steps: row.steps,
    verification: row.verification ?? {},
    expiresAt: row.expires_at.toISOString(),
    correlationId: row.correlation_id,
    ...(row.task_id === null ? {} : { taskId: row.task_id }),
    ...(row.result === null ? {} : { result: row.result }),
    ...(row.error === null ? {} : { error: row.error }),
  }
}

export class OperationPlanStore {
  constructor(private readonly database: Database) {}

  async create(input: CreateOperationPlan): Promise<OperationProjection> {
    if (input.expiresAt.getTime() <= Date.now()) throw new EmbymediaError('INVALID_INPUT', 'plan expiry must be in the future')
    if (input.targets.length > 100) throw new EmbymediaError('POLICY_DENIED', 'a plan may contain at most 100 explicit targets')
    const id = randomUUID()
    const correlationId = input.correlationId ?? randomUUID()
    const preview = {
      kind: input.kind,
      risk: input.risk,
      destructive: input.destructive,
      reversible: input.reversible,
      confirmation: input.confirmation,
      targets: input.targets,
      steps: input.steps,
      verification: input.verification,
      expiresAt: input.expiresAt.toISOString(),
    } as unknown as JsonValue
    const hash = previewHash(preview)
    try {
      const inserted = await this.database.query<PlanDatabaseRow>(
        `INSERT INTO operation_plans(
          id,kind,status,requested_by,session_id,preview,preview_hash,risk,destructive,reversible,
          confirmation,targets,steps,idempotency_key,expires_at,verification,correlation_id
        ) VALUES ($1,$2,'previewed',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
        RETURNING *`,
        [
          id, input.kind, input.requestedBy, input.sessionId, canonicalJson(preview), hash, input.risk,
          input.destructive, input.reversible, canonicalJson(input.confirmation),
          JSON.stringify(input.targets), JSON.stringify(input.steps), input.idempotencyKey, input.expiresAt,
          canonicalJson(input.verification), correlationId,
        ],
      )
      return projection(inserted.rows[0]!)
    } catch (error) {
      if ((error as { code?: string }).code !== '23505') throw error
      const existing = await this.database.query<PlanDatabaseRow>(
        'SELECT * FROM operation_plans WHERE idempotency_key = $1',
        [input.idempotencyKey],
      )
      const row = existing.rows[0]
      if (row === undefined) throw error
      if (row.preview_hash !== hash || row.session_id !== input.sessionId || row.kind !== input.kind) {
        throw new EmbymediaError('CONFLICT', 'idempotency key already belongs to a different plan')
      }
      return projection(row)
    }
  }

  async get(id: string): Promise<OperationProjection> {
    const result = await this.database.query<PlanDatabaseRow>('SELECT * FROM operation_plans WHERE id = $1', [id])
    const row = result.rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'operation plan not found')
    return projection(row)
  }

  async lock(client: PoolClient, id: string): Promise<PlanDatabaseRow> {
    const result = await client.query<PlanDatabaseRow>('SELECT * FROM operation_plans WHERE id = $1 FOR UPDATE', [id])
    const row = result.rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'operation plan not found')
    return row
  }

  async transition(
    id: string,
    expected: PlanStatus,
    next: PlanStatus,
    patch: TransitionPatch = {},
  ): Promise<OperationProjection> {
    return this.database.transaction(async (client) => {
      const row = await this.lock(client, id)
      if (row.status !== expected) {
        if (TERMINAL_STATUS[row.status]) return projection(row)
        throw new EmbymediaError('CONFLICT', `plan is ${row.status}, expected ${expected}`)
      }
      if (!TRANSITIONS[expected].includes(next)) {
        throw new EmbymediaError('CONFLICT', `invalid plan transition ${expected} -> ${next}`)
      }
      if (expected === 'previewed' && row.expires_at.getTime() <= Date.now() && next !== 'expired') {
        const expired = await client.query<PlanDatabaseRow>(
          'UPDATE operation_plans SET status = \'expired\', updated_at = now() WHERE id = $1 RETURNING *',
          [id],
        )
        return projection(expired.rows[0]!)
      }
      const updated = await client.query<PlanDatabaseRow>(
        `UPDATE operation_plans
         SET status = $2,
             approved_at = COALESCE($3, approved_at),
             task_id = COALESCE($4, task_id),
             result = COALESCE($5, result),
             verification = COALESCE($6, verification),
             error = COALESCE($7, error),
             updated_at = now()
         WHERE id = $1 RETURNING *`,
        [
          id,
          next,
          patch.approvedAt ?? null,
          patch.taskId ?? null,
          patch.result === undefined ? null : canonicalJson(patch.result),
          patch.verification === undefined ? null : canonicalJson(patch.verification),
          patch.error === undefined ? null : canonicalJson(patch.error),
        ],
      )
      return projection(updated.rows[0]!)
    })
  }

  async expireDue(): Promise<number> {
    const result = await this.database.query(
      `UPDATE operation_plans SET status = 'expired', updated_at = now()
       WHERE status = 'previewed' AND expires_at <= now()`,
    )
    return result.rowCount ?? 0
  }
}

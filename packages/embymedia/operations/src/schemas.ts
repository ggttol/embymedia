import { createHash } from 'node:crypto'
import { OPERATION_KINDS, type OperationKind, type Risk } from './capabilities.ts'

export const SCHEMA_VERSION = 1 as const
export const PRINCIPAL = 'gaotao' as const

export const CREDENTIAL_IDS = [
  'emby-api-key',
  'c115-cookie',
  'tmdb-api-key',
  'resource-api-token',
  'clouddrive-webhook-secret',
  'outbound-proxy-url',
] as const

export const ERROR_CODES = [
  'INVALID_INPUT',
  'AUTH_REQUIRED',
  'NOT_FOUND',
  'CONFLICT',
  'RATE_LIMITED',
  'UPSTREAM_UNAVAILABLE',
  'POLICY_DENIED',
  'CANCELLED',
  'PARTIAL_FAILURE',
  'VERIFICATION_FAILED',
] as const

export const PLAN_STATUSES = [
  'previewed',
  'approved',
  'cancelled',
  'expired',
  'queued',
  'running',
  'verifying',
  'done',
  'partial',
  'failed',
  'interrupted',
] as const

export const TASK_STATUSES = [
  'queued',
  'running',
  'verifying',
  'done',
  'partial',
  'error',
  'cancelled',
  'interrupted',
] as const

export const WRITE_MODES = ['disabled', 'staging', 'enabled'] as const
export const TERMINAL_PLAN_STATUSES = ['cancelled', 'expired', 'done', 'partial', 'failed', 'interrupted'] as const

export type CredentialId = (typeof CREDENTIAL_IDS)[number]
export type ErrorCode = (typeof ERROR_CODES)[number]
export type PlanStatus = (typeof PLAN_STATUSES)[number]
export type TaskStatus = (typeof TASK_STATUSES)[number]
export type WriteMode = (typeof WRITE_MODES)[number]
export type JsonPrimitive = string | number | boolean | null
export type JsonValue = JsonPrimitive | readonly JsonValue[] | { readonly [key: string]: JsonValue }

export interface CredentialGrantRecord {
  readonly kind: 'grant'
  readonly payload: { readonly version: 1; readonly value: string }
}

export interface Page {
  readonly cursor?: string
  readonly nextCursor?: string
  readonly limit: number
  readonly total?: number
}

export interface QueryResult<T extends JsonValue = JsonValue> {
  readonly schemaVersion: typeof SCHEMA_VERSION
  readonly capabilityId: string
  readonly correlationId: string
  readonly data: T
  readonly warnings: readonly string[]
  readonly page?: Page
}

export interface TargetProjection {
  readonly id: string
  readonly label: string
  readonly canonicalLibraryId?: string
  readonly canonicalCid?: string
  readonly path?: string
  readonly snapshot?: {
    readonly inode: string
    readonly mtimeMs: number
    readonly size: number
  }
}

export interface OperationProjection {
  readonly schemaVersion: typeof SCHEMA_VERSION
  readonly id: string
  readonly kind: OperationKind
  readonly status: PlanStatus
  readonly previewHash: string
  readonly risk: Risk
  readonly destructive: boolean
  readonly reversible: boolean
  readonly confirmation: JsonValue
  readonly targets: readonly TargetProjection[]
  readonly steps: readonly string[]
  readonly verification: JsonValue
  readonly expiresAt: string
  readonly correlationId: string
  readonly taskId?: string
  readonly result?: JsonValue
  readonly error?: { readonly code: ErrorCode; readonly message: string; readonly detail?: JsonValue }
}

export interface OperationPlanRow {
  readonly id: string
  readonly kind: OperationKind
  readonly status: PlanStatus
  readonly requestedBy: string
  readonly sessionId: string
  readonly preview: JsonValue
  readonly previewHash: string
  readonly risk: Risk
  readonly idempotencyKey: string
  readonly expiresAt: Date
  readonly approvedAt: Date | null
  readonly taskId: string | null
  readonly result: JsonValue | null
  readonly verification: JsonValue | null
  readonly error: JsonValue | null
  readonly createdAt: Date
  readonly updatedAt: Date
}

export interface TaskRunProjection {
  readonly schemaVersion: typeof SCHEMA_VERSION
  readonly id: string
  readonly kind: string
  readonly label: string
  readonly status: TaskStatus
  readonly progress: number
  readonly total: number
  readonly statusText: string
  readonly cancelRequested: boolean
  readonly correlationId: string
  readonly queuedAt: string
  readonly updatedAt: string
  readonly startedAt?: string
  readonly endedAt?: string
  readonly result?: JsonValue
  readonly error?: JsonValue
}

export type ControlRequest =
  | {
    readonly version: 1
    readonly action: 'webhook.rotate'
    readonly phase: 'apply'
    readonly planId: string
    readonly pendingSecret: string
    readonly previousSecretHash: string
  }
  | {
    readonly version: 1
    readonly action: 'webhook.rotate'
    readonly phase: 'restore'
    readonly planId: string
  }

export interface ControlResponse {
  readonly version: 1
  readonly ok: boolean
  readonly planId: string
  readonly phase: 'apply' | 'restore'
  readonly senderHash?: string
  readonly error?: { readonly code: ErrorCode; readonly message: string }
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)

/** JSON stringify with recursively sorted object keys for hashes, replay, and approval equality. */
export function canonicalJson(value: JsonValue): string {
  if (Array.isArray(value)) return `[${value.map(item => canonicalJson(item)).join(',')}]`
  if (isRecord(value)) {
    return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJson(value[key] as JsonValue)}`).join(',')}}`
  }
  return JSON.stringify(value)
}

export function previewHash(value: JsonValue): string {
  return createHash('sha256').update(canonicalJson(value)).digest('hex')
}

export function isCredentialId(value: unknown): value is CredentialId {
  return typeof value === 'string' && (CREDENTIAL_IDS as readonly string[]).includes(value)
}

export function isOperationKind(value: unknown): value is OperationKind {
  return typeof value === 'string' && (OPERATION_KINDS as readonly string[]).includes(value)
}

export function assertExactKeys(value: unknown, keys: readonly string[], label = 'input'): asserts value is Record<string, unknown> {
  if (!isRecord(value)) throw new TypeError(`${label} must be an object`)
  const allowed = new Set(keys)
  const unknown = Object.keys(value).filter(key => !allowed.has(key))
  if (unknown.length > 0) throw new TypeError(`${label} has unknown fields: ${unknown.sort().join(', ')}; accepted fields: ${[...allowed].sort().join(', ')}`)
}

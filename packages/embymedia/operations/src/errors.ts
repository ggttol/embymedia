import { randomUUID } from 'node:crypto'
import { ERROR_CODES, type ErrorCode, type JsonValue } from './schemas.ts'

const HTTP_STATUS: Readonly<Record<ErrorCode, number>> = {
  INVALID_INPUT: 400,
  AUTH_REQUIRED: 401,
  NOT_FOUND: 404,
  CONFLICT: 409,
  RATE_LIMITED: 429,
  UPSTREAM_UNAVAILABLE: 503,
  POLICY_DENIED: 403,
  CANCELLED: 499,
  PARTIAL_FAILURE: 500,
  VERIFICATION_FAILED: 422,
}

const SENSITIVE_DETAIL_KEY = /password|cookie|secret|api[_-]?key|token/i
const MAX_DETAIL_DEPTH = 6
const MAX_DETAIL_ITEMS = 100
const MAX_DETAIL_STRING = 1024

function sanitizeDetail(value: JsonValue, depth = 0): JsonValue {
  if (depth >= MAX_DETAIL_DEPTH) return '[truncated]'
  if (typeof value === 'string') return value.length <= MAX_DETAIL_STRING ? value : `${value.slice(0, MAX_DETAIL_STRING)}…`
  if (Array.isArray(value)) return value.slice(0, MAX_DETAIL_ITEMS).map(item => sanitizeDetail(item, depth + 1))
  if (typeof value === 'object' && value !== null) {
    const output: Record<string, JsonValue> = {}
    for (const [key, item] of Object.entries(value).slice(0, MAX_DETAIL_ITEMS)) {
      if (!SENSITIVE_DETAIL_KEY.test(key)) output[key] = sanitizeDetail(item, depth + 1)
    }
    return output
  }
  return value
}

export class EmbymediaError extends Error {
  readonly code: ErrorCode
  readonly correlationId: string
  readonly detail: JsonValue | undefined
  readonly httpStatus: number

  constructor(code: ErrorCode, message: string, detail?: JsonValue, correlationId: string = randomUUID()) {
    if (!(ERROR_CODES as readonly string[]).includes(code)) throw new TypeError(`unknown Embymedia error code ${code}`)
    super(message)
    this.name = 'EmbymediaError'
    this.code = code
    this.correlationId = correlationId
    this.detail = detail === undefined ? undefined : sanitizeDetail(detail)
    this.httpStatus = HTTP_STATUS[code]
  }

  toJSON(): { code: ErrorCode; message: string; correlationId: string; detail?: JsonValue } {
    return {
      code: this.code,
      message: this.message,
      correlationId: this.correlationId,
      ...(this.detail === undefined ? {} : { detail: this.detail }),
    }
  }
}

/** A handler failed after an external mutation may already have committed. */
export class PartialOperationError extends EmbymediaError {}

export function cancelled(signal: AbortSignal, correlationId?: string): void {
  if (signal.aborted) throw new EmbymediaError('CANCELLED', 'operation cancelled', undefined, correlationId)
}

export function asEmbymediaError(error: unknown, correlationId: string = randomUUID()): EmbymediaError {
  if (error instanceof EmbymediaError) return error
  if (error instanceof DOMException && error.name === 'AbortError') {
    return new EmbymediaError('CANCELLED', 'operation cancelled', undefined, correlationId)
  }
  return new EmbymediaError('UPSTREAM_UNAVAILABLE', 'operation failed', undefined, correlationId)
}

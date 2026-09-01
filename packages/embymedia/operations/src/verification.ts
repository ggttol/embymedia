import type { Database } from './database/index.ts'
import type { OperationPlanStore } from './plans.ts'
import { TERMINAL_STATUS } from './plans.ts'
import { EmbymediaError } from './errors.ts'
import type { JsonValue, OperationProjection } from './schemas.ts'
import type { OperationKind } from './capabilities.ts'

export interface VerificationResult {
  readonly ok: boolean
  readonly partial?: boolean
  readonly facts: JsonValue
  readonly diagnostics?: readonly string[]
}

export interface OperationVerifier {
  readonly verify: (plan: OperationProjection, signal: AbortSignal) => Promise<VerificationResult>
}

export class OperationVerificationService {
  constructor(
    private readonly database: Database,
    private readonly store: OperationPlanStore,
    private readonly verifiers: Readonly<Partial<Record<OperationKind, OperationVerifier>>>,
    private readonly principal: string,
  ) {}

  async verify(planId: string, sessionId: string, signal: AbortSignal): Promise<OperationProjection> {
    signal.throwIfAborted()
    const plan = await this.store.get(planId)
    const owner = await this.database.query<{ session_id: string; requested_by: string }>(
      'SELECT session_id,requested_by FROM operation_plans WHERE id=$1',
      [planId],
    )
    if (owner.rows[0]?.session_id !== sessionId || owner.rows[0]?.requested_by !== this.principal) {
      throw new EmbymediaError('POLICY_DENIED', 'operation verification belongs to a different session or principal')
    }
    if (TERMINAL_STATUS[plan.status]) return plan
    if (plan.status !== 'verifying') throw new EmbymediaError('CONFLICT', `operation plan is ${plan.status}, not verifying`)
    const verifier = this.verifiers[plan.kind]
    if (verifier === undefined) {
      return this.store.transition(plan.id, 'verifying', 'failed', {
        verification: { ok: false, facts: {}, diagnostics: [`no verifier for ${plan.kind}`] },
        error: { code: 'VERIFICATION_FAILED', message: `no verifier for ${plan.kind}` },
      })
    }
    try {
      const verification = await verifier.verify(plan, signal)
      if (verification.ok) {
        const done = await this.store.transition(plan.id, 'verifying', 'done', { verification: verification as unknown as JsonValue })
        await this.audit(sessionId, done)
        return done
      }
      const status = verification.partial ? 'partial' : 'failed'
      const failed = await this.store.transition(plan.id, 'verifying', status, {
        verification: verification as unknown as JsonValue,
        error: { code: 'VERIFICATION_FAILED', message: verification.partial ? 'operation partially verified' : 'operation verification failed' },
      })
      await this.audit(sessionId, failed)
      return failed
    } catch (error) {
      if (signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED')) {
        throw new EmbymediaError('CANCELLED', 'verification cancelled', { planId: plan.id }, plan.correlationId)
      }
      const failed = await this.store.transition(plan.id, 'verifying', 'failed', {
        verification: { ok: false, facts: {}, diagnostics: [error instanceof Error ? error.message : String(error)] },
        error: { code: 'VERIFICATION_FAILED', message: 'operation verification failed' },
      })
      await this.audit(sessionId, failed)
      return failed
    }
  }

  private async audit(sessionId: string, plan: OperationProjection): Promise<void> {
    await this.database.query(
      `INSERT INTO audit_logs(actor,action,detail,session_id,tool_name,correlation_id,destructive)
       VALUES ($1,'operation.verified',$2,$3,'embymedia_verify',$4,$5)`,
      [this.principal, JSON.stringify({ planId: plan.id, kind: plan.kind, status: plan.status }), sessionId, plan.correlationId, plan.destructive],
    )
  }
}

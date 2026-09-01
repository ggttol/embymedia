import type { Agent } from '@deepseek-ai/dsh-agent'
import type { Database } from './database/index.ts'
import type { OperationPlanStore } from './plans.ts'
import { TERMINAL_STATUS } from './plans.ts'
import { EmbymediaError, PartialOperationError } from './errors.ts'
import { canonicalJson, type JsonValue, type OperationProjection } from './schemas.ts'
import type { OperationKind, Risk } from './capabilities.ts'

export interface ExecuteRequest {
  readonly planId: string
  readonly previewHash: string
  readonly confirmation: JsonValue
}

export interface ApprovalRequester {
  request(request: {
    readonly agent: Agent
    readonly toolName: string
    readonly reason?: string
    readonly signal?: AbortSignal
  }): Promise<'allowed-once' | 'rejected' | 'cancelled' | 'unavailable'>
}

export interface OperationExecutionHandler {
  readonly execute: (plan: OperationProjection, signal: AbortSignal) => Promise<JsonValue>
}

interface ExecutionPlanRow {
  readonly id: string
  readonly kind: OperationKind
  readonly status: OperationProjection['status']
  readonly requested_by: string
  readonly session_id: string
  readonly preview_hash: string
  readonly risk: Risk
  readonly confirmation: JsonValue
  readonly expires_at: Date
}

export const SAFE_AUTO_OPERATION_KINDS: ReadonlySet<OperationKind> = new Set([
  'resource.add_new',
  'library.scan',
  'poster.apply',
  'poster.fix_batch',
  'metadata.refresh',
])

const OPERATION_APPROVAL_LABELS: Readonly<Record<OperationKind, string>> = {
  'library.create': '创建媒体库',
  'library.scan': '扫描媒体库',
  'resource.save_share': '保存 115 分享',
  'resource.offline': '创建离线任务',
  'resource.add_new': '一条龙入库',
  'series.update': '更新剧集',
  'series.archive': '归档剧集',
  'poster.apply': '应用海报',
  'poster.fix_batch': '批量修复海报',
  'metadata.refresh': '刷新元数据',
  'media.delete': '删除媒体',
  'media.move': '移动媒体',
  'dedup.delete': '删除重复项',
  'dedup.replace': '替换重复版本',
  'cleanup.empty_strm': '清理空 STRM',
  'cleanup.empty_cloud': '清理空云盘目录',
  'cleanup.execute': '执行清理',
  'user.create': '创建 Emby 用户',
  'user.policy_update': '更新用户策略',
  'user.delete': '删除 Emby 用户',
  'schedule.upsert': '保存计划任务',
  'schedule.delete': '删除计划任务',
  'schedule.run': '运行计划任务',
  'config.update': '更新配置',
  'config.credential_rotate': '轮换凭据',
  'smart_action.policy_update': '更新智能动作策略',
  'smart_action.dismiss': '忽略智能动作',
  'undo.execute': '执行撤销',
}

const RISK_APPROVAL_LABELS: Readonly<Record<Risk, string>> = {
  low: '低风险', medium: '中风险', high: '高风险', critical: '严重风险',
}

export function operationApprovalReason(
  kind: OperationKind,
  risk: Risk,
  planId: string,
  plan?: Pick<OperationProjection, 'previewHash' | 'targets'>,
): string {
  const base = `${OPERATION_APPROVAL_LABELS[kind]} / ${kind} · ${RISK_APPROVAL_LABELS[risk]} / ${risk} · 计划 / plan ${planId}`
  if (plan === undefined) return base
  const targetIds = plan.targets.slice(0, 8).map(target => target.id).join(', ')
  const omitted = Math.max(0, plan.targets.length - 8)
  return `${base} · preview ${plan.previewHash} · targets ${plan.targets.length}: ${targetIds}${omitted === 0 ? '' : ` (+${omitted})`}`
}

export class OperationExecutionService {
  constructor(
    private readonly database: Database,
    private readonly store: OperationPlanStore,
    private readonly approval: ApprovalRequester,
    private readonly handlers: Readonly<Partial<Record<OperationKind, OperationExecutionHandler>>>,
    private readonly principal: string,
    private readonly revalidateTargets: (plan: OperationProjection, signal: AbortSignal) => Promise<void>,
    private readonly autoApproveKinds: ReadonlySet<OperationKind> = new Set(),
  ) {}

  async execute(agent: Agent, request: ExecuteRequest, signal: AbortSignal): Promise<OperationProjection> {
    signal.throwIfAborted()
    const plan = await this.store.get(request.planId)
    await this.validate(agent, request)
    if (TERMINAL_STATUS[plan.status]) return plan
    await this.revalidateTargets(plan, signal)

    if (plan.risk !== 'low' && !this.autoApproveKinds.has(plan.kind)) {
      const outcome = await this.approval.request({
        agent,
        toolName: 'embymedia_execute',
        reason: operationApprovalReason(plan.kind, plan.risk, plan.id, plan),
        signal,
      })
      if (outcome !== 'allowed-once') {
        await this.store.transition(plan.id, 'previewed', 'cancelled', {
          error: { code: outcome === 'cancelled' ? 'CANCELLED' : 'POLICY_DENIED', message: `approval ${outcome}` },
        })
        throw new EmbymediaError(outcome === 'cancelled' ? 'CANCELLED' : 'POLICY_DENIED', `operation approval ${outcome}`)
      }
    }
    await this.revalidateTargets(plan, signal)

    const approved = await this.store.transition(plan.id, 'previewed', 'approved', { approvedAt: new Date() })
    if (approved.status !== 'approved') return approved
    try {
      await this.audit(agent.id, plan, 'approved')
    } catch (error) {
      return this.store.transition(plan.id, 'approved', 'failed', {
        error: {
          code: 'UPSTREAM_UNAVAILABLE',
          message: `approval audit failed: ${error instanceof Error ? error.message : String(error)}`,
        },
      })
    }
    await this.store.transition(plan.id, 'approved', 'queued')
    await this.store.transition(plan.id, 'queued', 'running')
    const handler = this.handlers[plan.kind]
    if (handler === undefined) {
      const failed = await this.store.transition(plan.id, 'running', 'failed', {
        error: { code: 'POLICY_DENIED', message: `no executor for ${plan.kind}` },
      })
      return failed
    }
    try {
      const result = await handler.execute(plan, signal)
      try {
        await this.audit(agent.id, { ...plan, status: 'verifying', result }, 'executed')
      } catch (error) {
        throw new PartialOperationError('PARTIAL_FAILURE', `execution audit failed after effects may have started: ${error instanceof Error ? error.message : String(error)}`, result)
      }
      try {
        return await this.store.transition(plan.id, 'running', 'verifying', { result })
      } catch (error) {
        throw new PartialOperationError(
          'PARTIAL_FAILURE',
          `execution result persistence failed after effects may have started: ${error instanceof Error ? error.message : String(error)}`,
          result,
        )
      }
    } catch (error) {
      const partial = error instanceof PartialOperationError
      const cancelled = !partial && (signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED'))
      const status = partial ? 'partial' : cancelled ? 'cancelled' : 'failed'
      const failed = await this.store.transition(plan.id, 'running', status, {
        error: {
          code: error instanceof EmbymediaError ? error.code : 'UPSTREAM_UNAVAILABLE',
          message: error instanceof Error ? error.message : String(error),
        },
      })
      await this.audit(agent.id, failed, failed.status)
      return failed
    }
  }

  private async validate(agent: Agent, request: ExecuteRequest): Promise<void> {
    await this.database.transaction(async (client) => {
      const row = (await client.query<ExecutionPlanRow>(
        `SELECT id,kind,status,requested_by,session_id,preview_hash,risk,confirmation,expires_at
         FROM operation_plans WHERE id=$1 FOR UPDATE`,
        [request.planId],
      )).rows[0]
      if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'operation plan not found')
      if (row.session_id !== agent.id) throw new EmbymediaError('POLICY_DENIED', 'operation plan belongs to a different session')
      if (row.requested_by !== this.principal) throw new EmbymediaError('POLICY_DENIED', 'operation plan belongs to a different principal')
      if (row.preview_hash !== request.previewHash) throw new EmbymediaError('CONFLICT', 'operation preview hash changed')
      if (canonicalJson(row.confirmation) !== canonicalJson(request.confirmation)) {
        throw new EmbymediaError('CONFLICT', 'operation confirmation differs from the canonical preview')
      }
      if (TERMINAL_STATUS[row.status]) return
      if (row.status !== 'previewed') throw new EmbymediaError('CONFLICT', `operation plan is ${row.status}`)
      if (row.expires_at.getTime() <= Date.now()) throw new EmbymediaError('CONFLICT', 'operation plan expired')
    })
  }

  private async audit(sessionId: string, plan: OperationProjection, action: string): Promise<void> {
    await this.database.query(
      `INSERT INTO audit_logs(actor,action,detail,session_id,tool_name,correlation_id,destructive)
       VALUES ($1,$2,$3,$4,'embymedia_execute',$5,$6)`,
      [this.principal, `operation.${action}`, JSON.stringify({ planId: plan.id, kind: plan.kind, risk: plan.risk, status: plan.status }), sessionId, plan.correlationId, plan.destructive],
    )
  }
}

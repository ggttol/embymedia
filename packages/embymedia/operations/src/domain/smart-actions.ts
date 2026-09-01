import { randomUUID } from 'node:crypto'
import type { Database } from '../database/index.ts'
import { EmbymediaError } from '../errors.ts'
import type { JsonValue } from '../schemas.ts'
import type { Risk } from '../capabilities.ts'

export const SMART_ACTION_TYPES = [
  'transfer_add_new',
  'transfer_update_series',
  'dedup_remove_old',
  'dedup_review',
  'poster_fix',
  'metadata_refresh',
  'library_scan',
  'archive_series',
  'cleanup_empty_folder',
  'task_retry_or_diagnose',
] as const

export type SmartActionType = (typeof SMART_ACTION_TYPES)[number]
export type Confidence = 'high' | 'medium' | 'suggestion'

export interface SmartFacts {
  readonly subject: { readonly kind: string; readonly id: string; readonly label: string }
  readonly resourceCandidate?: boolean
  readonly alreadyInLibrary?: boolean
  readonly missingEpisodes?: number
  readonly duplicateCount?: number
  readonly dedupConfidence?: number
  readonly posterMismatch?: boolean
  readonly noRating?: boolean
  readonly pendingMediaFiles?: number
  readonly seriesEnded?: boolean
  readonly emptyFolder?: boolean
  readonly failedTask?: boolean
  readonly evidence: JsonValue
}

export interface SmartAction {
  readonly id: string
  readonly type: SmartActionType
  readonly status: 'suggested' | 'ready' | 'executing' | 'done' | 'failed' | 'dismissed'
  readonly subject: SmartFacts['subject']
  readonly title: string
  readonly summary: string
  readonly recommendation: JsonValue
  readonly evidence: JsonValue
  readonly plan: JsonValue
  readonly risk: Risk
  readonly confidenceScore: number
  readonly confidence: Confidence
  readonly verification: JsonValue
  readonly createdAt: string
  readonly updatedAt: string
}

export interface SmartActionExecutor {
  readonly execute: (action: SmartAction, signal: AbortSignal) => Promise<JsonValue>
  readonly verify: (action: SmartAction, result: JsonValue, signal: AbortSignal) => Promise<JsonValue>
}

interface RuleOutput {
  readonly type: SmartActionType
  readonly score: number
  readonly risk: Risk
  readonly title: string
  readonly summary: string
  readonly recommendation: JsonValue
  readonly plan: JsonValue
  readonly verification: JsonValue
}

interface SmartActionRow {
  readonly id: string
  readonly action_type: SmartActionType
  readonly status: SmartAction['status']
  readonly subject: SmartFacts['subject']
  readonly title: string
  readonly summary: string
  readonly recommendation: JsonValue
  readonly evidence: JsonValue
  readonly plan: JsonValue
  readonly risk: { readonly level: Risk; readonly confidenceScore: number }
  readonly verification: JsonValue
  readonly created_at: Date
  readonly updated_at: Date
}

function confidence(score: number): Confidence {
  if (score >= 85) return 'high'
  if (score >= 60) return 'medium'
  return 'suggestion'
}

function rules(facts: SmartFacts): readonly RuleOutput[] {
  const output: RuleOutput[] = []
  if (facts.resourceCandidate === true && facts.alreadyInLibrary !== true) output.push({
    type: 'transfer_add_new', score: 88, risk: 'high', title: '转存新资源', summary: '资源候选未出现在媒体库',
    recommendation: { action: 'resource.add_new' }, plan: { candidateRequired: true }, verification: { resourceVisible: true, embyVisible: true },
  })
  if ((facts.missingEpisodes ?? 0) > 0) output.push({
    type: 'transfer_update_series', score: Math.min(95, 70 + (facts.missingEpisodes ?? 0) * 3), risk: 'high', title: '补齐追更缺集', summary: `检测到 ${String(facts.missingEpisodes)} 个缺集`,
    recommendation: { action: 'series.update' }, plan: { missingEpisodes: facts.missingEpisodes ?? 0 }, verification: { gapsReduced: true },
  })
  if ((facts.duplicateCount ?? 0) > 1) {
    const score = facts.dedupConfidence ?? 50
    output.push({
      type: score >= 85 ? 'dedup_remove_old' : 'dedup_review', score, risk: score >= 85 ? 'critical' : 'low',
      title: score >= 85 ? '删除重复旧版本' : '复核重复版本', summary: `检测到 ${String(facts.duplicateCount)} 个同源版本`,
      recommendation: { action: score >= 85 ? 'dedup.delete' : 'review' }, plan: { duplicateCount: facts.duplicateCount ?? 0 }, verification: { oneVersionRetained: true },
    })
  }
  if (facts.posterMismatch === true) output.push({
    type: 'poster_fix', score: 90, risk: 'medium', title: '修复海报匹配', summary: 'Emby 元数据与 TMDB 候选不一致',
    recommendation: { action: 'poster.fix_batch' }, plan: { remoteSearch: true }, verification: { providerIdMatches: true },
  })
  if (facts.noRating === true) output.push({
    type: 'metadata_refresh', score: 75, risk: 'medium', title: '刷新无评分元数据', summary: '项目缺少评分信息',
    recommendation: { action: 'metadata.refresh' }, plan: { recursive: true }, verification: { itemVisible: true },
  })
  if ((facts.pendingMediaFiles ?? 0) > 0) output.push({
    type: 'library_scan', score: 85, risk: 'medium', title: '扫描新增媒体', summary: `检测到 ${String(facts.pendingMediaFiles)} 个待收录文件`,
    recommendation: { action: 'library.scan' }, plan: { pendingFiles: facts.pendingMediaFiles ?? 0 }, verification: { embyVisible: true },
  })
  if (facts.seriesEnded === true && (facts.missingEpisodes ?? 0) === 0) output.push({
    type: 'archive_series', score: 86, risk: 'high', title: '归档已完结剧集', summary: '剧集已完结且没有缺集',
    recommendation: { action: 'series.archive' }, plan: { moveCloud: true, moveStrm: true }, verification: { archived: true },
  })
  if (facts.emptyFolder === true) output.push({
    type: 'cleanup_empty_folder', score: 80, risk: 'critical', title: '清理空目录', summary: '目录不含媒体或恢复所需文件',
    recommendation: { action: 'cleanup.execute' }, plan: { requireSnapshot: true }, verification: { absent: true },
  })
  if (facts.failedTask === true) output.push({
    type: 'task_retry_or_diagnose', score: 65, risk: 'low', title: '诊断失败任务', summary: '任务进入错误或中断状态',
    recommendation: { action: 'diagnose-before-retry' }, plan: { retryOnlyAfterDiagnosis: true }, verification: { diagnosisRecorded: true },
  })
  return output
}

function actionFromRow(row: SmartActionRow): SmartAction {
  const score = row.risk.confidenceScore
  return {
    id: row.id,
    type: row.action_type,
    status: row.status,
    subject: row.subject,
    title: row.title,
    summary: row.summary,
    recommendation: row.recommendation,
    evidence: row.evidence,
    plan: row.plan,
    risk: row.risk.level,
    confidenceScore: score,
    confidence: confidence(score),
    verification: row.verification,
    createdAt: row.created_at.toISOString(),
    updatedAt: row.updated_at.toISOString(),
  }
}

export class SmartActionEngine {
  constructor(
    private readonly database: Database,
    private readonly executors: Readonly<Partial<Record<SmartActionType, SmartActionExecutor>>>,
  ) {}

  evaluate(facts: SmartFacts): readonly Omit<SmartAction, 'id' | 'status' | 'createdAt' | 'updatedAt'>[] {
    return rules(facts).map(rule => ({
      type: rule.type,
      subject: facts.subject,
      title: rule.title,
      summary: rule.summary,
      recommendation: rule.recommendation,
      evidence: facts.evidence,
      plan: rule.plan,
      risk: rule.risk,
      confidenceScore: rule.score,
      confidence: confidence(rule.score),
      verification: rule.verification,
    }))
  }

  async refresh(facts: readonly SmartFacts[]): Promise<readonly SmartAction[]> {
    const output: SmartAction[] = []
    for (const subject of facts) {
      for (const proposal of this.evaluate(subject)) {
        const id = randomUUID()
        const status = proposal.confidence === 'suggestion' ? 'suggested' : 'ready'
        const inserted = await this.database.query<SmartActionRow>(
          `INSERT INTO smart_action_runs(
            id,action_type,status,subject,title,summary,recommendation,evidence,plan,risk,policy,verification
          ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'{}',$11) RETURNING *`,
          [id, proposal.type, status, JSON.stringify(proposal.subject), proposal.title, proposal.summary, JSON.stringify(proposal.recommendation), JSON.stringify(proposal.evidence), JSON.stringify(proposal.plan), JSON.stringify({ level: proposal.risk, confidenceScore: proposal.confidenceScore }), JSON.stringify(proposal.verification)],
        )
        output.push(actionFromRow(inserted.rows[0]!))
      }
    }
    return output
  }

  async list(status?: SmartAction['status']): Promise<readonly SmartAction[]> {
    const rows = await this.database.query<SmartActionRow>(
      `SELECT * FROM smart_action_runs ${status === undefined ? '' : 'WHERE status=$1'} ORDER BY updated_at DESC LIMIT 500`,
      status === undefined ? [] : [status],
    )
    return rows.rows.map(actionFromRow)
  }

  async get(id: string): Promise<SmartAction> {
    const row = (await this.database.query<SmartActionRow>('SELECT * FROM smart_action_runs WHERE id=$1', [id])).rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', 'Smart Action not found')
    return actionFromRow(row)
  }

  async policies(): Promise<readonly { key: string; enabled: boolean; mode: string; maxRisk: string; params: JsonValue }[]> {
    const rows = await this.database.query<{ key: string; enabled: boolean; mode: string; max_risk: string; params: JsonValue }>('SELECT key,enabled,mode,max_risk,params FROM smart_action_policies ORDER BY key')
    return rows.rows.map(row => ({ key: row.key, enabled: row.enabled, mode: row.mode, maxRisk: row.max_risk, params: row.params }))
  }

  async updatePolicy(key: SmartActionType, enabled: boolean, mode: 'confirm' | 'suggest' | 'auto', maxRisk: Risk, params: JsonValue): Promise<void> {
    await this.database.query(
      `INSERT INTO smart_action_policies(key,enabled,mode,max_risk,params,updated_at) VALUES ($1,$2,$3,$4,$5,now())
       ON CONFLICT(key) DO UPDATE SET enabled=EXCLUDED.enabled,mode=EXCLUDED.mode,max_risk=EXCLUDED.max_risk,params=EXCLUDED.params,updated_at=now()`,
      [key, enabled, mode, maxRisk, JSON.stringify(params)],
    )
  }

  async dismiss(id: string, reason: string | undefined): Promise<SmartAction> {
    const updated = await this.database.query<SmartActionRow>(
      `UPDATE smart_action_runs SET status='dismissed',result=$2,updated_at=now()
       WHERE id=$1 AND status IN ('suggested','ready') RETURNING *`,
      [id, JSON.stringify({ reason: reason ?? '' })],
    )
    if (updated.rows[0] === undefined) throw new EmbymediaError('CONFLICT', 'Smart Action cannot be dismissed in its current state')
    return actionFromRow(updated.rows[0])
  }

  async execute(id: string, signal: AbortSignal): Promise<SmartAction> {
    const action = await this.get(id)
    if (action.status !== 'ready') throw new EmbymediaError('CONFLICT', 'Smart Action is not ready')
    const executor = this.executors[action.type]
    if (executor === undefined) throw new EmbymediaError('POLICY_DENIED', 'Smart Action has no deterministic executor')
    await this.database.query("UPDATE smart_action_runs SET status='executing',updated_at=now() WHERE id=$1", [id])
    try {
      const execution = await executor.execute(action, signal)
      const verification = await executor.verify(action, execution, signal)
      const record = typeof verification === 'object' && verification !== null && !Array.isArray(verification)
        ? verification as Record<string, JsonValue>
        : undefined
      if (record?.ok !== true) throw new EmbymediaError('VERIFICATION_FAILED', 'Smart Action verification failed', verification)
      const updated = await this.database.query<SmartActionRow>(
        "UPDATE smart_action_runs SET status='done',result=$2,verification=$3,updated_at=now() WHERE id=$1 RETURNING *",
        [id, JSON.stringify(execution), JSON.stringify(verification)],
      )
      return actionFromRow(updated.rows[0]!)
    } catch (error) {
      await this.database.query(
        "UPDATE smart_action_runs SET status='failed',error=$2,updated_at=now() WHERE id=$1",
        [id, JSON.stringify({ message: error instanceof Error ? error.message : String(error) })],
      )
      throw error
    }
  }

  async executeBatch(ids: readonly string[], signal: AbortSignal): Promise<readonly SmartAction[]> {
    const output: SmartAction[] = []
    for (const id of ids) {
      signal.throwIfAborted()
      output.push(await this.execute(id, signal))
    }
    return output
  }

  async fromTask(taskId: string): Promise<readonly SmartAction[]> {
    const task = (await this.database.query<{ id: string; label: string; status: string; error: JsonValue | null }>('SELECT id,label,status,error FROM task_runs WHERE id=$1', [taskId])).rows[0]
    if (task === undefined) throw new EmbymediaError('NOT_FOUND', 'task not found')
    return this.refresh([{
      subject: { kind: 'task', id: task.id, label: task.label },
      failedTask: ['error', 'interrupted', 'partial'].includes(task.status),
      evidence: { status: task.status, error: task.error },
    }])
  }

  fromNextAction(facts: SmartFacts): readonly Omit<SmartAction, 'id' | 'status' | 'createdAt' | 'updatedAt'>[] {
    return this.evaluate(facts)
  }

  async workbench(): Promise<{ actions: readonly SmartAction[]; counts: Readonly<Record<string, number>> }> {
    const actions = await this.list()
    const counts: Record<string, number> = {}
    for (const action of actions) counts[action.status] = (counts[action.status] ?? 0) + 1
    return { actions, counts }
  }
}

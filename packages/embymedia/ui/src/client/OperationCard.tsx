import { useMemo, useState } from 'react'
import type { ToolCallViewProps } from '@deepseek-ai/dsh-client-ui-tool/client'
import css from './OperationCard.module.css'

interface StageCredential {
  (sessionId: string, planId: string, value: string, signal: AbortSignal): Promise<{ readonly ok: boolean; readonly error?: { readonly message?: string } }>
}

interface OperationCardProps extends ToolCallViewProps {
  readonly stageCredential: StageCredential
}

type CardState = 'running' | 'done' | 'error' | 'stopped'

interface CardModel {
  readonly state: CardState
  readonly args: Record<string, unknown> | undefined
  readonly value: Record<string, unknown> | undefined
  readonly raw: string
  readonly title: string
  readonly subtitle: string
  readonly risk?: string
  readonly status?: string
}

const TOOL_LABELS: Readonly<Record<string, string>> = {
  embymedia_health: '系统健康',
  embymedia_library: '媒体库',
  embymedia_resource: '115 资源',
  embymedia_series: '追更与缺集',
  embymedia_analyze: '运营分析',
  embymedia_task: '任务队列',
  embymedia_audit: '审计与撤销',
  embymedia_user: '用户与策略',
  embymedia_schedule: '计划任务',
  embymedia_config: '系统配置',
  embymedia_plan: '操作预览',
  embymedia_execute: '执行操作',
  embymedia_verify: '独立验证',
}

const OPERATION_LABELS: Readonly<Record<string, string>> = {
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

const ACTION_LABELS: Readonly<Record<string, string>> = {
  dashboard: '运营总览',
  list: '列表',
  get: '详情',
  cancel: '取消任务',
  export: '导出配置',
  diagnostics: '系统诊断',
  credential_status: '凭据状态',
  logs: '运行日志',
  audit: '审计记录',
  undo: '撤销记录',
  get_policy: '读取策略',
  list_libraries: '媒体库列表',
  summary: '媒体库概览',
  list_items: '媒体条目',
  count_items: '媒体数量',
  list_strm: 'STRM 列表',
  gaps_series: '剧集缺集',
  gaps_library: '媒体库缺集',
  search: '搜索资源',
  parse_share: '解析分享',
  snapshot_share: '分享快照',
  transfer_preview: '转存预检',
  status: '当前状态',
  workbench: '工作台',
  smart_summary: '智能动作概览',
  smart_list: '智能动作列表',
  smart_get: '智能动作详情',
  smart_policies: '智能动作策略',
  smart_verify: '智能动作验证',
  dedup: '重复项分析',
  poster_mismatch: '海报异常',
  poster_search: '海报搜索',
  cleanup: '清理分析',
  cleanup_verify: '清理验证',
}

const STATUS_LABELS: Readonly<Record<string, string>> = {
  previewed: '待审批', approved: '已批准', queued: '排队中', running: '执行中', verifying: '验证中',
  done: '已完成', partial: '部分完成', failed: '失败', error: '错误', cancelled: '已取消', expired: '已过期',
  interrupted: '已中断', stopped: '已停止', starting: '启动中', ready: '已就绪', ok: '正常', active: '启用',
  disabled: '停用', configured: '已配置', healthy: '健康', 'disabled-for-cutover': '切换期已停用',
}

const RISK_LABELS: Readonly<Record<string, string>> = {
  low: '低风险', medium: '中风险', high: '高风险', critical: '严重风险',
}

const FIELD_LABELS: Readonly<Record<string, string>> = {
  capabilityId: '能力标识', id: '计划 ID', kind: '操作类型', status: '状态', risk: '风险', taskId: '任务 ID',
  correlationId: '关联 ID', expiresAt: '过期时间', targets: '目标数量', input: '输入', steps: '执行步骤',
  verification: '验证条件', destructive: '破坏性操作', reversible: '可撤销', settings: '设置', undoId: '撤销 ID',
  keys: '设置项', matches: '匹配', restored: '已恢复', action: '动作', data: '结果数据', warnings: '警告',
}

const CREDENTIAL_LABELS: Readonly<Record<string, string>> = {
  'emby-api-key': 'Emby API 密钥',
  'c115-cookie': '115 Cookie',
  'tmdb-api-key': 'TMDB API 密钥',
  'resource-api-token': '资源 API Token',
  'outbound-proxy-url': '出站代理 URL',
}

function bilingual(value: string, labels: Readonly<Record<string, string>>): string {
  const label = labels[value]
  return label === undefined ? value : `${label} / ${value}`
}

function toolLabel(toolName: string): string {
  return bilingual(toolName, TOOL_LABELS)
}

function displayValue(value: string): string {
  if (OPERATION_LABELS[value] !== undefined) return bilingual(value, OPERATION_LABELS)
  if (STATUS_LABELS[value] !== undefined) return bilingual(value, STATUS_LABELS)
  if (RISK_LABELS[value] !== undefined) return bilingual(value, RISK_LABELS)
  if (ACTION_LABELS[value] !== undefined) return bilingual(value, ACTION_LABELS)
  return value
}

function localizeForDisplay(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(localizeForDisplay)
  if (typeof value === 'object' && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [
      FIELD_LABELS[key] === undefined ? key : `${FIELD_LABELS[key]} / ${key}`,
      localizeForDisplay(item),
    ]))
  }
  return typeof value === 'string' ? displayValue(value) : value
}

function localizedJson(value: unknown): string {
  return JSON.stringify(localizeForDisplay(value), null, 2)
}

function parseObject(text: string): Record<string, unknown> | undefined {
  try {
    const value: unknown = JSON.parse(text)
    return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
  } catch {
    return undefined
  }
}

function resultText(block: ToolCallViewProps['block']): string {
  if (!('kind' in block)) return ''
  return block.content.map(item => item.type === 'text' ? item.text : JSON.stringify(item)).join('\n')
}

function model(toolName: string, block: ToolCallViewProps['block']): CardModel {
  const settled = 'kind' in block
  const argsRaw = (settled ? block.call?.argsRaw : block.argsRaw) ?? ''
  const raw = settled ? resultText(block) : ''
  const args = parseObject(argsRaw)
  const value = parseObject(raw)
  const state: CardState = !settled ? 'running' : block.error?.code === 'interrupted' ? 'stopped' : block.isError ? 'error' : 'done'
  const kind = typeof value?.kind === 'string' ? value.kind : typeof args?.kind === 'string' ? args.kind : undefined
  const action = typeof args?.action === 'string' ? args.action : undefined
  const status = typeof value?.status === 'string' ? value.status : undefined
  const risk = typeof value?.risk === 'string' ? value.risk : undefined
  return {
    state,
    args,
    value,
    raw,
    title: kind === undefined ? toolLabel(toolName) : bilingual(kind, OPERATION_LABELS),
    subtitle: status === undefined
      ? action === undefined ? bilingual(state, STATUS_LABELS) : bilingual(action, ACTION_LABELS)
      : bilingual(status, STATUS_LABELS),
    ...(risk === undefined ? {} : { risk }),
    ...(status === undefined ? {} : { status }),
  }
}

function previewRows(value: Record<string, unknown> | undefined): Array<[string, string]> {
  if (value === undefined) return []
  const rows: Array<[string, string]> = []
  for (const key of ['capabilityId', 'id', 'kind', 'status', 'risk', 'taskId', 'correlationId', 'expiresAt']) {
    const item = value[key]
    if (typeof item === 'string' || typeof item === 'number' || typeof item === 'boolean') rows.push([key, typeof item === 'string' ? displayValue(item) : String(item)])
  }
  const targets = value.targets
  if (Array.isArray(targets)) rows.push(['targets', String(targets.length)])
  return rows
}

function credentialPlan(model: CardModel): { planId: string; credential: string } | undefined {
  if (model.value?.kind !== 'config.credential_rotate' || typeof model.value.id !== 'string') return undefined
  const confirmation = model.value.confirmation
  if (typeof confirmation !== 'object' || confirmation === null || Array.isArray(confirmation)) return undefined
  const input = (confirmation as Record<string, unknown>).input
  if (typeof input !== 'object' || input === null || Array.isArray(input)) return undefined
  const credential = (input as Record<string, unknown>).credential
  if (typeof credential !== 'string' || credential === 'clouddrive-webhook-secret') return undefined
  return { planId: model.value.id, credential }
}

export function OperationCard(props: OperationCardProps) {
  const view = useMemo(() => model(props.toolName, props.block), [props.toolName, props.block])
  const rows = previewRows(view.value)
  const credential = credentialPlan(view)
  const [secret, setSecret] = useState('')
  const [stageState, setStageState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [stageError, setStageError] = useState('')

  const stage = async (): Promise<void> => {
    if (credential === undefined || secret.length === 0) return
    setStageState('saving')
    setStageError('')
    const result = await props.stageCredential(props.sessionId, credential.planId, secret, new AbortController().signal)
    if (result.ok) {
      setSecret('')
      setStageState('saved')
    } else {
      setStageState('error')
      setStageError(result.error?.message ?? '凭据暂存失败')
    }
  }

  if ((view.args === undefined && !('kind' in props.block)) || (view.raw !== '' && view.value === undefined)) {
    return (
      <section className={`${css.card} ${css.fallback}`} data-state={view.state}>
        <header><strong>{toolLabel(props.toolName)}</strong><span>兼容视图 / Fallback</span></header>
        <pre>{view.raw || ('kind' in props.block ? props.block.call?.argsRaw : props.block.argsRaw)}</pre>
      </section>
    )
  }

  return (
    <section className={css.card} data-state={view.state} data-risk={view.risk}>
      <div className={css.rail} aria-hidden />
      <header className={css.header}>
        <div>
          <span className={css.eyebrow}>{toolLabel(props.toolName)}</span>
          <h3>{view.title}</h3>
        </div>
        <div className={css.badges}>
          {view.risk !== undefined ? <span className={css.risk}>{bilingual(view.risk, RISK_LABELS)}</span> : null}
          <span className={css.status}>{view.subtitle}</span>
        </div>
      </header>

      {rows.length > 0 ? (
        <dl className={css.facts}>
          {rows.map(([key, value]) => <div key={key}><dt>{FIELD_LABELS[key] ?? key}<small> / {key}</small></dt><dd>{value}</dd></div>)}
        </dl>
      ) : null}

      {credential !== undefined ? (
        <div className={css.credential}>
          <label htmlFor={`credential-${props.callId}`}>{CREDENTIAL_LABELS[credential.credential] ?? credential.credential}<small> / {credential.credential}</small></label>
          <div className={css.credentialRow}>
            <input
              id={`credential-${props.callId}`}
              type="password"
              value={secret}
              autoComplete="new-password"
              onChange={(event) => { setSecret(event.target.value); setStageState('idle') }}
              placeholder="仅暂存到 Host，不进入聊天记录"
            />
            <button type="button" disabled={secret.length === 0 || stageState === 'saving'} onClick={() => { void stage() }}>
              {stageState === 'saving' ? '暂存中' : stageState === 'saved' ? '已暂存' : '暂存凭据'}
            </button>
          </div>
          {stageState === 'error' ? <p className={css.stageError}>{stageError}</p> : null}
        </div>
      ) : null}

      {view.value?.confirmation !== undefined ? (
        <details className={css.details}>
          <summary>批准内容</summary>
          <pre>{localizedJson(view.value.confirmation)}</pre>
        </details>
      ) : view.value?.data !== undefined ? (
        <details className={css.details}>
          <summary>结构化结果</summary>
          <pre>{localizedJson(view.value.data)}</pre>
        </details>
      ) : null}

      {props.inspect !== undefined ? <button type="button" className={css.inspect} onClick={props.inspect}>检查轨迹</button> : null}
    </section>
  )
}

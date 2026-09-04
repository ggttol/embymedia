import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import css from './EmbyWorkspace.module.css'

type ModuleId = 'dashboard' | 'libraries' | 'resources' | 'metadata' | 'tasks' | 'users' | 'configuration' | 'audit'
type JsonRecord = Record<string, unknown>

interface LibraryRow { id: string; name: string; collectionType?: string; locations: string[] }
interface UserRow { id: string; name: string; disabled: boolean }
interface CredentialRow { id: string; configured: boolean; writable: boolean }
interface CredentialCheckState { running: boolean; ok?: boolean; message?: string; latencyMs?: number }
interface TaskRow { id: string; kind: string; label: string; status: string; progress: number; total: number; statusText: string; updatedAt: string }
interface AuditRow { id: string; action: string; actor: string; destructive: boolean; createdAt: string }
interface Snapshot {
  generatedAt: string
  health: { status: string; writeMode: string; scheduler: string }
  libraries: LibraryRow[]
  users: UserRow[]
  tasks: { counts: Record<string, number>; recent: TaskRow[] }
  schedules: JsonRecord[]
  audit: AuditRow[]
  settings: JsonRecord
  credentials: CredentialRow[]
  supported: string[]
}

export interface EmbyWorkspaceProps {
  loadSnapshot(signal: AbortSignal): Promise<unknown>
  checkCredential(id: string, signal: AbortSignal): Promise<unknown>
  saveCredential?(id: string, value: string, signal: AbortSignal): Promise<void>
  sendPrompt(text: string): Promise<void>
}

const MODULES: Array<{ id: ModuleId; code: string; label: string; note: string }> = [
  { id: 'dashboard', code: '01', label: '总览', note: '信号与运行态' },
  { id: 'libraries', code: '02', label: '媒体库', note: 'Emby 与 STRM' },
  { id: 'resources', code: '03', label: '资源入库', note: '115 一条龙' },
  { id: 'metadata', code: '04', label: '元数据', note: '海报与刷新' },
  { id: 'tasks', code: '05', label: '任务', note: '队列与结果' },
  { id: 'users', code: '06', label: '用户', note: 'Emby 策略' },
  { id: 'configuration', code: '07', label: '配置中心', note: '端点与凭据' },
  { id: 'audit', code: '08', label: '审计', note: '批准与写入' },
]

const METADATA_ACTIONS = [
  { kind: 'poster.apply', title: '应用指定 TMDB', note: '对单条 Emby item 应用远程搜索并复验 ProviderId' },
  { kind: 'poster.fix_batch', title: '批量修复海报', note: '最多 100 个显式 item；逐项验证' },
  { kind: 'metadata.refresh', title: '刷新元数据', note: '刷新图片与元数据并确认 item 仍可见' },
] as const

const CREDENTIAL_LABELS: Record<string, { title: string; note: string }> = {
  'emby-api-key': { title: 'Emby API Key', note: '媒体库与用户管理' },
  'c115-cookie': { title: '115 Cookie', note: '分享预检与转存' },
  'tmdb-api-key': { title: 'TMDB API Key', note: '元数据与海报搜索' },
  'resource-api-token': { title: '资源 API Token', note: 'TG Resource 搜索' },
  'outbound-proxy-url': { title: '出站代理 URL', note: 'TMDB 与资源 API' },
  'clouddrive-webhook-secret': { title: 'CloudDrive Webhook', note: '需维护 canary 流程' },
}

function record(value: unknown): JsonRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as JsonRecord : {}
}
function text(value: unknown, fallback = ''): string { return typeof value === 'string' ? value : fallback }
function num(value: unknown): number { return typeof value === 'number' ? value : Number(value) || 0 }
function bool(value: unknown): boolean { return value === true }
function rows(value: unknown): JsonRecord[] { return Array.isArray(value) ? value.map(record) : [] }

function parseSnapshot(value: unknown): Snapshot {
  const root = record(value)
  const health = record(root.health)
  const taskRoot = record(root.tasks)
  const counts = record(taskRoot.counts)
  const operations = record(root.operations)
  return {
    generatedAt: text(root.generatedAt),
    health: { status: text(health.status, 'unknown'), writeMode: text(health.writeMode, 'unknown'), scheduler: text(health.scheduler, 'unknown') },
    libraries: rows(root.libraries).map((item) => {
      const collectionType = text(item.collectionType)
      return {
        id: text(item.id), name: text(item.name), ...(collectionType === '' ? {} : { collectionType }),
        locations: Array.isArray(item.locations) ? item.locations.map(value => text(value)).filter(Boolean) : [],
      }
    }),
    users: rows(root.users).map(item => ({ id: text(item.Id), name: text(item.Name), disabled: bool(record(item.Policy).IsDisabled) })),
    tasks: {
      counts: Object.fromEntries(Object.entries(counts).map(([key, value]) => [key, num(value)])),
      recent: rows(taskRoot.recent).map(item => ({
        id: text(item.id), kind: text(item.kind), label: text(item.label), status: text(item.status),
        progress: num(item.progress), total: num(item.total), statusText: text(item.status_text), updatedAt: text(item.updated_at),
      })),
    },
    schedules: rows(root.schedules),
    audit: rows(root.audit).map(item => ({ id: text(item.id), action: text(item.action), actor: text(item.actor), destructive: bool(item.destructive), createdAt: text(item.created_at) })),
    settings: record(root.settings),
    credentials: rows(root.credentials).map(item => ({ id: text(item.id), configured: bool(item.configured), writable: bool(item.writable) })),
    supported: Array.isArray(operations.supported) ? operations.supported.map(value => text(value)).filter(Boolean) : [],
  }
}

function statusTone(status: string): 'good' | 'warn' | 'bad' | 'muted' {
  if (['ok', 'done', 'healthy', 'configured', 'active'].includes(status)) return 'good'
  if (['running', 'queued', 'verifying', 'partial', 'enabled'].includes(status)) return 'warn'
  if (['failed', 'error', 'cancelled', 'interrupted'].includes(status)) return 'bad'
  return 'muted'
}

const STATUS_LABELS: Readonly<Record<string, string>> = {
  ok: '正常', done: '已完成', healthy: '健康', configured: '已配置', active: '启用', running: '执行中',
  queued: '排队中', verifying: '验证中', partial: '部分完成', enabled: '已启用', failed: '失败', error: '错误',
  cancelled: '已取消', interrupted: '已中断', disabled: '已停用', missing: '未配置', unknown: '未知',
}

function statusLabel(value: string): string {
  const label = STATUS_LABELS[value]
  return label === undefined ? value : `${label} / ${value}`
}

function Status({ value }: { value: string }) {
  return <span className={css.status} data-tone={statusTone(value)}><i />{statusLabel(value)}</span>
}

function Empty({ children }: { children: string }) { return <div className={css.empty}>{children}</div> }

function SettingEditor({ label, settingKey, value, onPrompt }: { label: string; settingKey: string; value: unknown; onPrompt(prompt: string): void }) {
  const initial = typeof value === 'string' || typeof value === 'number' ? String(value) : ''
  const [draft, setDraft] = useState(initial)
  const serialized = settingKey.endsWith('_seconds') ? String(Number(draft) || 0) : JSON.stringify(draft)
  return <div className={css.settingRow}>
    <div><strong>{label}</strong><code>{settingKey}</code></div>
    <input value={draft} onChange={(event) => { setDraft(event.target.value) }} aria-label={label} />
    <button onClick={() => { onPrompt(`创建 config.update 计划，settings={"${settingKey}":${serialized}}。只生成计划并等待我审批。`) }}>生成变更计划</button>
  </div>
}

export function EmbyWorkspace({ loadSnapshot, checkCredential, saveCredential, sendPrompt }: EmbyWorkspaceProps) {
  const [open, setOpen] = useState(() => localStorage.getItem('embymedia.workspace.open') !== 'false')
  const [module, setModule] = useState<ModuleId>('dashboard')
  const [snapshot, setSnapshot] = useState<Snapshot>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [action, setAction] = useState('')
  const [credentialChecks, setCredentialChecks] = useState<Record<string, CredentialCheckState>>({})
  const [editingCred, setEditingCred] = useState<string | null>(null)
  const [credDraft, setCredDraft] = useState('')
  const [credSaving, setCredSaving] = useState(false)
  const refresh = () => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    void loadSnapshot(controller.signal).then((value) => { setSnapshot(parseSnapshot(value)) }).catch((reason) => {
      setError(reason instanceof Error ? reason.message : String(reason))
    }).finally(() => { setLoading(false) })
    return () => { controller.abort() }
  }

  useEffect(() => {
    if (!open) return
    const cancel = refresh()
    const timer = window.setInterval(refresh, 30_000)
    return () => { cancel(); window.clearInterval(timer) }
  }, [open])

  const runCredentialCheck = async (id: string) => {
    setCredentialChecks(current => ({ ...current, [id]: { running: true } }))
    const controller = new AbortController()
    try {
      const result = record(await checkCredential(id, controller.signal))
      const ok = result.ok === true
      setCredentialChecks(current => ({ ...current, [id]: {
        running: false,
        ok,
        message: text(result.message, ok ? '检查通过' : '检查失败'),
        latencyMs: num(result.latencyMs),
      } }))
    } catch (reason) {
      setCredentialChecks(current => ({ ...current, [id]: {
        running: false,
        ok: false,
        message: reason instanceof Error ? reason.message : String(reason),
      } }))
    }
  }

  const prompt = async (value: string) => {
    setAction(value)
    setError('')
    try {
      await sendPrompt(value)
      localStorage.setItem('embymedia.workspace.open', 'false')
      setOpen(false)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setAction('')
    }
  }

  const close = () => { localStorage.setItem('embymedia.workspace.open', 'false'); setOpen(false) }
  const show = () => { localStorage.setItem('embymedia.workspace.open', 'true'); setOpen(true) }

  if (!open) return <button className={css.launcher} onClick={show}><span>EM</span>打开 Emby 运营台</button>

  const saveDirectCredential = async (id: string) => {
    if (!credDraft.trim()) return
    setCredSaving(true)
    setError('')
    try {
      if (saveCredential) {
        await saveCredential(id, credDraft.trim(), new AbortController().signal)
      } else {
        throw new Error('未支持直接保存凭据')
      }
      setEditingCred(null)
      setCredDraft('')
      refresh()
      void runCredentialCheck(id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setCredSaving(false)
    }
  }

  const data = snapshot
  const activeTasks = (data?.tasks.counts.running ?? 0) + (data?.tasks.counts.queued ?? 0) + (data?.tasks.counts.verifying ?? 0)
  const configured = data?.credentials.filter(item => item.configured).length ?? 0
  const supported = new Set(data?.supported ?? [])
  const checksRunning = Object.values(credentialChecks).some(check => check.running)
  const runAllCredentialChecks = async () => {
    for (const credential of data?.credentials ?? []) {
      if (credential.configured) await runCredentialCheck(credential.id)
    }
  }

  return createPortal(<section className={css.workspace} aria-label="Emby 运营工作台">
    <aside className={css.rail}>
      <header className={css.brand}>
        <div className={css.brandMark}><span>EM</span><i /></div>
        <div><strong>EmbyMedia</strong><small>PROJECTION CONTROL</small></div>
      </header>
      <nav aria-label="运营模块">
        {MODULES.map(item => <button key={item.id} data-active={module === item.id} onClick={() => { setModule(item.id) }}>
          <b>{item.code}</b><span>{item.label}<small>{item.note}</small></span>
        </button>)}
      </nav>
      <footer>
        <button className={css.chatButton} onClick={close}>AI 对话 / 审批</button>
        <small>数据只读 · 写入经 Workspace Write</small>
      </footer>
    </aside>

    <main className={css.canvas}>
      <header className={css.topbar}>
        <div><span className={css.kicker}>EMBY OPERATIONS / {module.toUpperCase()}</span><h1>{MODULES.find(item => item.id === module)?.label}</h1></div>
        <div className={css.topActions}>
          <span>{data?.generatedAt ? new Date(data.generatedAt).toLocaleTimeString('zh-CN', { hour12: false }) : '—'}</span>
          <button onClick={refresh} disabled={loading}>{loading ? '同步中' : '刷新状态'}</button>
          <button onClick={close}>返回 AI 对话</button>
        </div>
      </header>
      {error && <div className={css.error}>{error}</div>}

      {module === 'dashboard' && <>
        <div className={css.metrics}>
          <div><small>系统状态</small><strong>{data?.health.status === undefined ? '—' : statusLabel(data.health.status)}</strong><Status value={data?.health.status ?? 'unknown'} /></div>
          <div><small>媒体库</small><strong>{data?.libraries.length ?? '—'}</strong><span>Emby 标准库 / canonical</span></div>
          <div><small>活动任务</small><strong>{activeTasks}</strong><span>{data?.tasks.counts.done ?? 0} 已完成</span></div>
          <div><small>上游凭据</small><strong>{configured}/{data?.credentials.length ?? 0}</strong><span>仅显示配置状态</span></div>
        </div>
        <section className={css.signalPanel}>
          <div className={css.sectionHeading}><div><small>实时信号链 / LIVE SIGNAL PATH</small><h2>媒体信号脊柱</h2></div><p>从云端目录到播放客户端；状态来自 Host snapshot。</p></div>
          <div className={css.signalSpine}>
            {[
              ['01', 'CloudDrive2', 'CloudFS / 115'], ['02', 'STRM', `${data?.libraries.length ?? 0} 个媒体库`],
              ['03', 'Emby', data?.health.status ?? 'unknown'], ['04', 'NPS / Client', '公网与 LAN'],
            ].map(([code, label, note], index) => <div key={code} className={css.signalNode} data-active={index < 3}><b>{code}</b><i /><strong>{label}</strong><span>{note}</span></div>)}
          </div>
        </section>
        <section className={css.split}>
          <div className={css.taskStrip}><div className={css.sectionHeading}><div><small>最近运行 / RECENT RUNS</small><h2>最近任务</h2></div></div>{data?.tasks.recent.slice(0, 6).map(task => <div className={css.taskRow} key={task.id}><Status value={task.status} /><span><strong>{task.label || task.kind}</strong><small>{task.statusText || task.kind}</small></span><time>{task.updatedAt ? new Date(task.updatedAt).toLocaleTimeString('zh-CN', { hour12: false }) : '—'}</time></div>) ?? <Empty>暂无任务</Empty>}</div>
          <div className={css.commandDeck}><small>可执行写入 / WRITE PATHS</small><strong>{data?.supported.length ?? 0}</strong><p>项操作具备预览 / 执行 / 验证三件套。</p><button onClick={() => { void prompt('检查当前系统健康、媒体库和未完成任务，只读，不执行任何写入。') }}>让 AI 做运营巡检</button></div>
        </section>
      </>}

      {module === 'libraries' && <section className={css.dataSection}>
        <div className={css.sectionHeading}><div><small>Emby 媒体库 / LIBRARIES</small><h2>媒体库与扫描边界</h2></div><button onClick={() => { void prompt('列出所有 Emby 媒体库、STRM 状态和缺集摘要，只读。') }}>AI 全库检查</button></div>
        <div className={css.table}><div className={css.tableHead}><span>媒体库</span><span>类型</span><span>路径</span><span>操作</span></div>{data?.libraries.map(library => <div className={css.tableRow} key={library.id}><span><strong>{library.name}</strong><code>{library.id}</code></span><span>{library.collectionType || 'mixed'}</span><span>{library.locations.join(', ') || '—'}</span><span><button disabled={!supported.has('library.scan')} onClick={() => { void prompt(`为 Emby 库 ${library.name}（libraryId=${library.id}）创建 library.scan canonical plan；先确认对应 CloudDrive mediaFolder，只生成计划并等待审批。`) }}>扫描计划</button></span></div>) ?? <Empty>暂无媒体库</Empty>}</div>
      </section>}

      {module === 'resources' && <section className={css.resourcePage}>
        <div className={css.resourceHero}><small>资源入库 / ONBOARDING</small><h2>一条龙入库，不跳步骤。</h2><p>115 预检、CID 映射、CloudDrive 可见性、STRM、Emby 扫描和独立验证在同一计划中完成。</p><button disabled={!supported.has('resource.add_new')} onClick={() => { void prompt('我要添加一个新的 115 影视资源。先询问我分享链接和目标媒体库，然后创建 resource.add_new canonical plan，展示 targets 后等待审批。') }}>添加 115 资源</button></div>
        <div className={css.pipeline}>{['候选预检', '115 转存', 'CloudDrive 可见', 'STRM', 'Emby 扫描', '独立验证'].map((label, index) => <div key={label}><b>{String(index + 1).padStart(2, '0')}</b><span>{label}</span></div>)}</div>
        <div className={css.supportMatrix}><div><strong>可执行</strong><p>resource.add_new</p></div><div><strong>只读查询</strong><p>搜索、分享解析、snapshot、库上下文</p></div><div data-disabled><strong>待补齐</strong><p>offline 独立验收、单独 save_share</p></div></div>
      </section>}

      {module === 'metadata' && <section className={css.dataSection}>
        <div className={css.sectionHeading}><div><small>元数据工作台 / METADATA LAB</small><h2>海报与元数据</h2></div></div>
        <div className={css.actionIndex}>{METADATA_ACTIONS.map((item, index) => <button key={item.kind} disabled={!supported.has(item.kind)} onClick={() => { void prompt(`准备执行 ${item.kind}。先询问所需 itemId/TMDB id，创建 canonical plan 并等待审批。`) }}><b>{String(index + 1).padStart(2, '0')}</b><span><strong>{item.title}</strong><small>{item.note}</small></span><em>{supported.has(item.kind) ? '可用' : '未接线'}</em></button>)}</div>
      </section>}

      {module === 'tasks' && <section className={css.dataSection}>
        <div className={css.sectionHeading}><div><small>任务账本 / TASK LEDGER</small><h2>任务与队列</h2></div><span>{activeTasks} 进行中 / active</span></div>
        <div className={css.taskList}>{data?.tasks.recent.map(task => <div className={css.taskRow} key={task.id}><Status value={task.status} /><span><strong>{task.label || task.kind}</strong><small>{task.kind} · {task.statusText}</small></span><progress max={Math.max(task.total, 1)} value={task.progress} /><time>{task.updatedAt ? new Date(task.updatedAt).toLocaleString('zh-CN', { hour12: false }) : '—'}</time></div>) ?? <Empty>暂无任务</Empty>}</div>
      </section>}

      {module === 'users' && <section className={css.dataSection}>
        <div className={css.sectionHeading}><div><small>Emby 账户 / ACCOUNTS</small><h2>用户与策略</h2></div><button disabled={!supported.has('user.create')} onClick={() => { void prompt('创建一个新的 Emby 用户。先询问名称与策略，创建 user.create plan 并等待审批；不要在聊天中收集密码。') }}>新用户计划</button></div>
        <div className={css.table}><div className={css.tableHead}><span>用户</span><span>状态</span><span>ID</span><span>操作</span></div>{data?.users.map(user => <div className={css.tableRow} key={user.id}><span><strong>{user.name}</strong></span><span><Status value={user.disabled ? 'disabled' : 'active'} /></span><span><code>{user.id}</code></span><span><button onClick={() => { void prompt(`检查 Emby 用户 ${user.name}（${user.id}）的 policy，只读。`) }}>检查策略</button></span></div>) ?? <Empty>暂无用户</Empty>}</div>
      </section>}

      {module === 'configuration' && <section className={css.configPage}>
        <div className={css.sectionHeading}><div><small>配置矩阵 / CONFIGURATION</small><h2>配置与上游凭据</h2></div><p>秘密只写入，不显示原值。</p></div>
        <div className={css.configBlock}><h3>服务端点</h3>
          <SettingEditor label="资源 API Base URL" settingKey="resource_api_base_url" value={data?.settings.resource_api_base_url} onPrompt={(value) => { void prompt(value) }} />
          <SettingEditor label="TMDB Base URL" settingKey="tmdb_base_url" value={data?.settings.tmdb_base_url ?? 'https://api.themoviedb.org'} onPrompt={(value) => { void prompt(value) }} />
          <SettingEditor label="TMDB 超时 / Timeout" settingKey="tmdb_timeout_seconds" value={data?.settings.tmdb_timeout_seconds ?? 45} onPrompt={(value) => { void prompt(value) }} />
        </div>
        <div className={css.configBlock}>
          <div className={css.configBlockHeading}><h3>只写凭据 / Write-only credentials</h3><button disabled={checksRunning || !data?.credentials.some(item => item.configured)} onClick={() => { void runAllCredentialChecks() }}>{checksRunning ? '检查中…' : '全部检查'}</button></div>
          {data?.credentials.map((credential) => {
            const meta = CREDENTIAL_LABELS[credential.id] ?? { title: credential.id, note: '' }
            const check = credentialChecks[credential.id]
            const isEditing = editingCred === credential.id
            return <div className={css.credentialRow} key={credential.id} style={{ flexWrap: 'wrap' }}>
              <span className={css.credentialGlyph}>{credential.configured ? '●' : '○'}</span>
              <div><strong>{meta.title}</strong><small>{meta.note}</small><code>{credential.id}</code>{check && <span className={css.credentialCheck} data-tone={check.running ? 'running' : check.ok ? 'ok' : 'error'} aria-live="polite">{check.running ? '正在检查…' : `${check.message ?? '检查完成'}${check.latencyMs === undefined ? '' : ` · ${String(check.latencyMs)} ms`}`}</span>}</div>
              <Status value={credential.configured ? 'configured' : 'missing'} />
              <div className={css.credentialActions}>
                <button disabled={!credential.configured || check?.running === true} onClick={() => { void runCredentialCheck(credential.id) }}>{check?.running ? '检查中…' : '检查可用性'}</button>
                <button disabled={!credential.writable} onClick={() => {
                  if (isEditing) {
                    setEditingCred(null)
                    setCredDraft('')
                  } else {
                    setEditingCred(credential.id)
                    setCredDraft('')
                  }
                }}>{isEditing ? '取消' : '粘贴配置'}</button>
              </div>
              {isEditing && <div style={{ width: '100%', marginTop: '10px', display: 'flex', gap: '8px' }}>
                <input
                  type={credential.id.includes('key') || credential.id.includes('secret') ? 'password' : 'text'}
                  placeholder={`直接在此粘贴新的 ${meta.title}…`}
                  value={credDraft}
                  onChange={(e) => { setCredDraft(e.target.value) }}
                  style={{ flex: 1, padding: '8px 12px', background: '#1c1917', border: '1px solid #44403c', color: '#f5f5f4', borderRadius: '4px', fontSize: '13px' }}
                  autoFocus
                />
                <button
                  disabled={credSaving || !credDraft.trim()}
                  onClick={() => { void saveDirectCredential(credential.id) }}
                  style={{ padding: '8px 16px', background: '#eab308', color: '#000', fontWeight: 'bold', border: 'none', borderRadius: '4px', cursor: 'pointer', whiteSpace: 'nowrap' }}
                >
                  {credSaving ? '保存中…' : '直接保存'}
                </button>
              </div>}
            </div>
          }) ?? <Empty>凭据状态不可用</Empty>}
        </div>
        <div className={css.configNote}><strong>115 API 说明</strong><p>当前运营插件使用 115 Cookie，不等同于 CloudDrive2 的 115 Open API。Open API OAuth 尚未接入，界面不会假装已支持。</p></div>
      </section>}

      {module === 'audit' && <section className={css.dataSection}>
        <div className={css.sectionHeading}><div><small>不可变审计 / IMMUTABLE TRACE</small><h2>审计流水</h2></div><span>{data?.audit.length ?? 0} 条最近记录 / recent</span></div>
        <div className={css.auditList}>{data?.audit.map(row => <div key={row.id}><time>{row.createdAt ? new Date(row.createdAt).toLocaleString('zh-CN', { hour12: false }) : '—'}</time><strong>{row.action}</strong><span>{row.actor}</span>{row.destructive && <em>破坏性 / DESTRUCTIVE</em>}</div>) ?? <Empty>暂无审计记录</Empty>}</div>
      </section>}
      <div className={css.actionState} data-visible={Boolean(action)}>{action ? '已提交到 AI 对话' : ''}</div>
    </main>
  </section>, document.body)
}

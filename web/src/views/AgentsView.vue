<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { CheckCircle2, FileCode, Key, Plus, RefreshCw, Trash2, XCircle } from 'lucide-vue-next'
import CopyButton from '../components/CopyButton.vue'
import UiDialog from '../components/UiDialog.vue'
import AuditLogQuery from '../components/AuditLogQuery.vue'
import { PRODUCT_VERSION } from '@/version'

type ProbeState = 'untested' | 'checking' | 'online' | 'error'
interface Probe { state: ProbeState; detail: string; checkedAt: string }
interface Token { id: string; name: string; scopes?: string[]; role?: string; rate_limit?: number; last_used_at?: string }
interface AuditLog { id: string; action: string; caller: string; agent_name?: string; ip?: string; status: string; latency_ms: number; created_at: string }
interface DestructiveApproval { id: string; account_id: string; parent_cid: string; requested_by: string; expires_at: string; targets: Array<{ file_id: string; name: string; is_folder: boolean; size: number }> }
const tokens = ref<Token[] | null>(null)
const auditLogs = ref<AuditLog[] | null>(null)
const tokenError = ref('')
const auditError = ref('')
const revokeError = ref('')
const approvals = ref<DestructiveApproval[] | null>(null)
const approvalError = ref('')
const selectedApproval = ref<DestructiveApproval | null>(null)
const approvalBusy = ref(false)
const showCreateModal = ref(false)
const tokenName = ref('')
const selectedScopes = ref<string[]>(['read', 'write'])
const tokenRateLimit = ref(120)
const creationMessage = ref('')
const createdTokenSecret = ref<string | null>(null)
const creating = ref(false)
const checkedAt = ref('')
const discoveredTools = ref<string[]>([])
const mcpProbe = ref<Probe>({ state: 'untested', detail: '尚未检测', checkedAt: '' })
const openAPIProbe = ref<Probe>({ state: 'untested', detail: '尚未检测', checkedAt: '' })
const phases = ref([
  { name: '协议初始化', state: 'untested' as ProbeState, detail: '尚未检测' },
  { name: '工具目录发现', state: 'untested' as ProbeState, detail: '尚未检测' },
  { name: '安全只读调用', state: 'untested' as ProbeState, detail: '尚未检测' },
])
const mcpTools = [
  ['c115_list_accounts', '读取 115 账号状态与容量'], ['c115_list_files', '列出指定 CID 下的文件与目录'], ['c115_search_files', '搜索账号内已有文件与目录'], ['c115_search', '按关键词搜索 115 分享资源'], ['c115_snapshot_share', '转存前检查 115 分享内容'], ['c115_save_share', '解析 115 分享链接并转存'], ['c115_list_offline', '读取 115 离线下载任务'], ['c115_move', '移动文件或目录'], ['c115_rename', '重命名文件或目录'], ['c115_mkdir', '在指定目录下创建文件夹'], ['c115_get_share_link', '创建文件或目录的 115 分享链接'], ['c115_request_delete', '提交目标绑定的删除确认请求'], ['c115_execute_delete', '执行用户已确认的删除请求'],
  ['cd2_mount_status', '读取并验证 CloudDrive2 挂载清单'], ['cd2_remount', '检查没有活跃播放后自动重新挂载'],
  ['emby_get_libraries', '读取 Emby 媒体库列表'], ['emby_search_items', '搜索 Emby 并取得准确条目 ID'], ['emby_inspect_item', '按 ID 检查元数据与图片'], ['emby_list_sessions', '读取当前播放会话'], ['emby_missing_posters', '列出缺少主海报的条目'], ['emby_refresh_library', '触发 Emby 媒体库扫描'],
  ['task_submit', '提交后台任务'], ['task_list', '列出最近后台任务'], ['task_query', '查询任务状态、进度与结果'], ['task_cancel', '取消等待中或执行中的任务'], ['task_retry', '重试失败或取消的任务'], ['task_get_logs', '读取执行记录与日志'],
  ['schedule_list', '列出自动任务计划'], ['schedule_upsert', '创建、更新、暂停或启用自动任务'], ['schedule_run', '立即执行自动任务'], ['schedule_delete', '删除自动任务计划'], ['system_get_config', '读取脱敏系统设置与目录映射'], ['system_update_config', '更新系统设置但不能更改删除开关'], ['system_health', '检查依赖服务与挂载状态'],
] as const
const mcpEndpoint = computed(() => `${window.location.origin}/mcp`)
const localMcpEndpoint = 'http://127.0.0.1:3080/mcp'
const openApiEndpoint = computed(() => `${window.location.origin}/api/v1/openapi.json`)
const hermesCommands = `hermes mcp add embymedia --url ${localMcpEndpoint} --auth header\nhermes mcp test embymedia`
const allToolsAvailable = computed(() => discoveredTools.value.length === mcpTools.length && mcpTools.every(([name]) => discoveredTools.value.includes(name)))
const diagnosticsRunning = computed(() => mcpProbe.value.state === 'checking' || openAPIProbe.value.state === 'checking')
const probeLabel = (state: ProbeState) => ({ untested: '未检测', checking: '检测中', online: '正常', error: '异常' }[state])
const stateClass = (state: ProbeState) => state === 'online' ? 'text-ok' : state === 'error' ? 'text-danger' : state === 'checking' ? 'text-annotation' : 'text-text-faint'
const toolNames = new Set(mcpTools.map(([name]) => name))
const latestToolCalls = computed(() => {
  const latest: Record<string, AuditLog> = {}
  for (const log of auditLogs.value ?? []) {
    if (toolNames.has(log.action as typeof mcpTools[number][0]) && !latest[log.action]) latest[log.action] = log
  }
  return latest
})
const auditedToolCount = computed(() => Object.keys(latestToolCalls.value).length)
const toolCallLabel = (name: string) => latestToolCalls.value[name]?.status === 'success' ? '最近调用成功' : latestToolCalls.value[name]?.status === 'denied' ? '最近调用被拒绝' : latestToolCalls.value[name] ? '最近调用失败' : discoveredTools.value.includes(name) ? '已发现，暂无调用记录' : '未发现'
const toolCallClass = (name: string) => latestToolCalls.value[name]?.status === 'success' ? 'text-ok' : latestToolCalls.value[name] ? 'text-danger' : 'text-text-faint'
const callActor = (log: AuditLog) => log.agent_name || (log.caller === 'mcp-stdio' ? '本机 MCP（身份未知）' : '身份未知')
let connectionTimer: number | undefined
let auditTimer: number | undefined
async function responseError(response: Response, fallback: string) { try { const data = await response.json(); return data.error || fallback } catch { return fallback } }
async function fetchTokens() { tokenError.value = ''; try { const response = await fetch('/api/v1/tokens'); if (!response.ok) throw new Error(await responseError(response, `令牌读取失败（HTTP ${response.status}）`)); const data = await response.json(); if (!Array.isArray(data.tokens)) throw new Error('令牌数据格式不完整'); tokens.value = data.tokens } catch (error) { tokenError.value = error instanceof Error ? error.message : '令牌读取失败' } }
async function fetchAuditLogs() { auditError.value = ''; try { const response = await fetch('/api/v1/audit-logs?limit=100'); if (!response.ok) throw new Error(await responseError(response, `访问记录读取失败（HTTP ${response.status}）`)); const data = await response.json(); if (!Array.isArray(data.logs)) throw new Error('访问记录数据格式不完整'); auditLogs.value = data.logs } catch (error) { auditError.value = error instanceof Error ? error.message : '访问记录读取失败' } }
async function fetchApprovals() { approvalError.value = ''; try { const response = await fetch('/api/v1/destructive-approvals'); if (!response.ok) throw new Error(await responseError(response, `删除确认读取失败（HTTP ${response.status}）`)); const data = await response.json(); if (!Array.isArray(data.approvals)) throw new Error('删除确认数据格式不完整'); approvals.value = data.approvals } catch (error) { approvalError.value = error instanceof Error ? error.message : '删除确认读取失败' } }
async function readJSONRPC(response: Response) { const body = await response.text(); if (!response.ok) throw new Error(`${response.status} ${body.slice(0, 120)}`.trim()); if (!body) return null; if (response.headers.get('content-type')?.includes('text/event-stream')) { const dataLine = body.split('\n').reverse().find((line) => line.startsWith('data:')); if (!dataLine) throw new Error('MCP 响应缺少 data 事件'); return JSON.parse(dataLine.slice(5).trim()) } return JSON.parse(body) }
async function postMCP(payload: object, sessionId = '') { const headers: Record<string, string> = { Accept: 'application/json, text/event-stream', 'Content-Type': 'application/json', 'MCP-Protocol-Version': '2025-03-26' }; if (sessionId) headers['Mcp-Session-Id'] = sessionId; const response = await fetch('/mcp', { method: 'POST', headers, body: JSON.stringify(payload) }); return { response, result: await readJSONRPC(response) } }
async function probeMCP(runSafeCalls: boolean) {
  mcpProbe.value = { state: 'checking', detail: '正在执行协议初始化', checkedAt: mcpProbe.value.checkedAt }; discoveredTools.value = []; phases.value[0] = { name: '协议初始化', state: 'checking', detail: '正在发送 initialize' }; phases.value[1] = { name: '工具目录发现', state: 'untested', detail: '等待协议初始化' }
  if (runSafeCalls) phases.value[2] = { name: '安全只读调用', state: 'untested', detail: '等待工具目录发现' }
  try {
    const initialized = await postMCP({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'embymedia-web-diagnostic', version: PRODUCT_VERSION } } }); if (initialized.result?.error) throw new Error(initialized.result.error.message); const sessionId = initialized.response.headers.get('Mcp-Session-Id') ?? ''; await postMCP({ jsonrpc: '2.0', method: 'notifications/initialized', params: {} }, sessionId); phases.value[0] = { name: '协议初始化', state: 'online', detail: 'initialize 与 initialized 已完成' }
    phases.value[1] = { name: '工具目录发现', state: 'checking', detail: '正在调用 tools/list' }; const listed = await postMCP({ jsonrpc: '2.0', id: 2, method: 'tools/list', params: {} }, sessionId); if (listed.result?.error) throw new Error(listed.result.error.message); discoveredTools.value = (listed.result?.result?.tools ?? []).map((tool: { name: string }) => tool.name); phases.value[1] = { name: '工具目录发现', state: allToolsAvailable.value ? 'online' : 'error', detail: `发现 ${discoveredTools.value.length} / ${mcpTools.length} 个工具` }
    if (runSafeCalls) { phases.value[2] = { name: '安全只读调用', state: 'checking', detail: '正在检查 4 项只读工具' }; const smokeTools = ['system_get_config', 'system_health', 'emby_get_libraries', 'cd2_mount_status']; const checks = await Promise.all(smokeTools.map(async (name, index) => { const called = await postMCP({ jsonrpc: '2.0', id: 10 + index, method: 'tools/call', params: { name, arguments: {} } }, sessionId); return [name, Boolean(called.result?.error) || !called.result?.result || called.result.result.isError === true ? 'error' : 'ok'] as const })); const passed = checks.filter(([, state]) => state === 'ok').length; phases.value[2] = { name: '安全只读调用', state: passed === checks.length ? 'online' : 'error', detail: `${passed} / ${checks.length} 项调用成功 · ${new Date().toLocaleTimeString()}` } }
    const connected = phases.value[0]?.state === 'online' && phases.value[1]?.state === 'online'; const now = new Date().toLocaleString(); mcpProbe.value = { state: connected ? 'online' : 'error', detail: connected ? '协议与工具目录正常' : '协议或工具目录检测失败', checkedAt: now }
  } catch (error) { const phase = phases.value.find((item) => item.state === 'checking'); if (phase) { phase.state = 'error'; phase.detail = error instanceof Error ? error.message : '检测失败' } mcpProbe.value = { state: 'error', detail: error instanceof Error ? error.message : 'MCP 连接失败', checkedAt: new Date().toLocaleString() } }
}
async function probeOpenAPI() { openAPIProbe.value = { state: 'checking', detail: '正在读取 Schema', checkedAt: openAPIProbe.value.checkedAt }; try { const response = await fetch('/api/v1/openapi.json'); if (!response.ok) throw new Error(`HTTP ${response.status}`); const schema = await response.json(); if (schema.openapi !== '3.1.0' || !schema.paths || typeof schema.paths !== 'object') throw new Error('未返回有效的 OpenAPI 3.1 Schema'); const pathCount = Object.keys(schema.paths).length; openAPIProbe.value = { state: 'online', detail: `OpenAPI ${schema.openapi} · ${pathCount} 条 REST 路径`, checkedAt: new Date().toLocaleString() } } catch (error) { openAPIProbe.value = { state: 'error', detail: error instanceof Error ? error.message : 'OpenAPI 读取失败', checkedAt: new Date().toLocaleString() } } }
async function runDiagnostics(runSafeCalls = true) { if (diagnosticsRunning.value || document.hidden) return; await Promise.all([probeMCP(runSafeCalls), probeOpenAPI()]); checkedAt.value = new Date().toLocaleString(); await fetchAuditLogs() }
function refreshConnection() { if (!document.hidden) void runDiagnostics(false) }
function refreshAudits() { if (!document.hidden) void Promise.all([fetchAuditLogs(), fetchApprovals()]) }
async function createToken() { if (!tokenName.value.trim() || !selectedScopes.value.length || creating.value) return; creationMessage.value = ''; creating.value = true; try { const response = await fetch('/api/v1/tokens', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: tokenName.value.trim(), permissions: selectedScopes.value, rate_limit: tokenRateLimit.value }) }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '创建失败'); createdTokenSecret.value = data.token; creationMessage.value = '令牌已创建。明文只显示一次，请立即复制保存。'; tokenName.value = ''; tokenRateLimit.value = 120; await Promise.all([fetchTokens(), fetchAuditLogs()]) } catch (error) { creationMessage.value = error instanceof Error ? error.message : '无法连接服务，请稍后重试。' } finally { creating.value = false } }
async function deleteToken(id: string) { if (!window.confirm('确认撤销这个访问令牌？已配置的 Agent 将立即失去访问能力。')) return; revokeError.value = ''; try { const response = await fetch(`/api/v1/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' }); if (!response.ok) throw new Error(await responseError(response, `撤销失败（HTTP ${response.status}）`)); await Promise.all([fetchTokens(), fetchAuditLogs()]) } catch (error) { revokeError.value = error instanceof Error ? error.message : '撤销令牌失败' } }
async function decideApproval(approval: DestructiveApproval, decision: 'approve' | 'reject') {
  if (approvalBusy.value) return
  approvalBusy.value = true
  approvalError.value = ''
  try {
    const response = await fetch(`/api/v1/destructive-approvals/${encodeURIComponent(approval.id)}/${decision}`, { method: 'POST' })
    if (!response.ok) throw new Error(await responseError(response, `删除确认操作失败（HTTP ${response.status}）`))
    selectedApproval.value = null
    await fetchApprovals()
  } catch (error) {
    approvalError.value = error instanceof Error ? error.message : '删除确认操作失败'
  } finally {
    approvalBusy.value = false
  }
}
function closeModal() { if (creating.value) return; showCreateModal.value = false; createdTokenSecret.value = null; creationMessage.value = '' }
onMounted(() => { void Promise.all([fetchTokens(), fetchApprovals(), runDiagnostics()]); connectionTimer = window.setInterval(refreshConnection, 30_000); auditTimer = window.setInterval(refreshAudits, 10_000) })
onBeforeUnmount(() => { window.clearInterval(connectionTimer); window.clearInterval(auditTimer) })
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-4 pb-5 border-b border-border"><div><h1 class="font-serif text-3xl font-bold">Agent 接入控制台</h1><p class="text-sm text-text-muted mt-2">先确认连接，再配置地址与令牌。检测请求不代表写操作可用。</p></div><div class="flex flex-col sm:flex-row gap-2"><button type="button" class="min-h-11 px-4 border border-border bg-surface" :disabled="diagnosticsRunning" @click="runDiagnostics()"><RefreshCw class="inline w-4 h-4 mr-2" :class="{ 'animate-spin': diagnosticsRunning }" />{{ diagnosticsRunning ? '检测中' : '重新检测' }}</button><button type="button" class="min-h-11 px-4 bg-accent text-accent-contrast" @click="showCreateModal = true"><Plus class="inline w-4 h-4 mr-2" />创建访问令牌</button></div></header>

    <section aria-labelledby="connection-title" class="rounded-xl border border-border bg-surface p-5"><div class="flex flex-wrap justify-between gap-3"><div><h2 id="connection-title" class="font-serif text-xl font-semibold">连接状态</h2><p class="text-xs text-text-faint mt-1">最近连接检测：{{ checkedAt || '尚未完成' }} · 页面可见时每 30 秒自动更新</p></div><span class="font-mono text-sm" :class="stateClass(mcpProbe.state)">MCP {{ probeLabel(mcpProbe.state) }}</span></div><ol class="grid sm:grid-cols-3 gap-3 mt-4"><li v-for="phase in phases" :key="phase.name" class="border border-border p-4"><div class="flex justify-between gap-2"><h3 class="font-semibold text-sm">{{ phase.name }}</h3><span class="text-xs" :class="stateClass(phase.state)">{{ probeLabel(phase.state) }}</span></div><p class="text-xs text-text-muted mt-2">{{ phase.detail }}</p></li></ol><div class="mt-4 pt-4 border-t border-border flex gap-3"><CheckCircle2 v-if="openAPIProbe.state === 'online'" class="w-5 h-5 text-ok" /><XCircle v-else class="w-5 h-5 text-danger" /><div><p class="text-sm font-semibold">OpenAPI Schema · {{ probeLabel(openAPIProbe.state) }}</p><p class="text-xs text-text-muted">{{ openAPIProbe.detail }} · {{ openAPIProbe.checkedAt || '尚未检测' }}</p></div></div></section>

    <section aria-labelledby="configuration-title" class="rounded-xl border border-border bg-surface p-5"><h2 id="configuration-title" class="font-serif text-xl font-semibold">Hermes 连接配置</h2><p class="text-sm text-text-muted mt-1">同机 Hermes 使用本机 3080 端口的 <code>/mcp</code>；访问受限端点时使用本页令牌。</p><div class="mt-4 grid gap-3"><div><h3 class="text-xs font-mono text-text-faint">推荐命令</h3><div class="mt-2 flex items-start gap-2"><pre class="min-w-0 flex-1 overflow-x-auto bg-text text-bg p-4 text-xs"><code>{{ hermesCommands }}</code></pre><CopyButton :text="hermesCommands" label="复制 Hermes 命令" compact /></div></div><div class="grid lg:grid-cols-2 gap-3"><div><h3 class="text-xs text-text-faint">本机 MCP</h3><div class="flex gap-2 mt-2"><code class="min-w-0 flex-1 overflow-x-auto border border-border p-3 text-xs">{{ localMcpEndpoint }}</code><CopyButton :text="localMcpEndpoint" label="复制本机 MCP 地址" compact /></div></div><div><h3 class="text-xs text-text-faint">当前浏览器 MCP</h3><div class="flex gap-2 mt-2"><code class="min-w-0 flex-1 overflow-x-auto border border-border p-3 text-xs">{{ mcpEndpoint }}</code><CopyButton :text="mcpEndpoint" label="复制当前 MCP 地址" compact /></div></div></div><a :href="openApiEndpoint" target="_blank" rel="noreferrer" class="inline-flex min-h-11 items-center gap-2 text-sm text-accent underline"><FileCode class="w-4 h-4" />打开 OpenAPI 3.1 Schema</a></div></section>

    <section aria-labelledby="approvals-title"><div class="flex items-center gap-2"><h2 id="approvals-title" class="font-serif text-xl font-semibold">待确认删除</h2><span class="text-xs text-text-faint">{{ approvals === null ? '数量未知' : `${approvals.length} 项` }}</span></div><p class="mt-1 text-sm text-text-muted">Agent 只能提交包含准确文件 ID、名称和父目录的请求；你确认后，它才能执行一次。</p><p v-if="approvalError" role="alert" class="mt-3 border border-danger/30 p-3 text-sm text-danger">{{ approvalError }}</p><div v-if="approvals?.length" class="mt-3 divide-y divide-border border border-border bg-surface"><article v-for="approval in approvals" :key="approval.id" class="p-4 sm:flex sm:items-center sm:justify-between sm:gap-4"><div class="min-w-0"><h3 class="font-semibold">{{ approval.targets.map(target => target.name).join('、') }}</h3><p class="mt-1 text-xs text-text-faint">由 {{ approval.requested_by }} 请求 · 目录 CID {{ approval.parent_cid }} · {{ new Date(approval.expires_at).toLocaleString() }} 失效</p></div><div class="mt-3 flex gap-2 sm:mt-0"><button type="button" class="min-h-11 border border-border px-4" :disabled="approvalBusy" @click="decideApproval(approval, 'reject')">拒绝</button><button type="button" class="min-h-11 border border-danger bg-danger/5 px-4 text-danger" :disabled="approvalBusy" @click="selectedApproval = approval">查看并确认</button></div></article></div><p v-else-if="approvals" class="mt-3 border border-border bg-surface p-5 text-sm text-text-muted">没有等待确认的删除请求。</p></section>
    <section aria-labelledby="tokens-title"><div class="flex items-center gap-2"><Key class="w-4 h-4 text-accent" /><h2 id="tokens-title" class="font-serif text-xl font-semibold">访问令牌</h2><span class="text-xs text-text-faint">{{ tokens === null ? '数量未知' : `${tokens.length} 个` }}</span></div><p class="mt-1 text-sm text-text-muted">自主运行令牌默认同时允许读取和写入，每分钟 120 次请求；删除媒体仍需单独确认。</p><p v-if="tokenError || revokeError" class="mt-3 p-3 border border-danger/30 text-sm text-danger" role="alert">{{ revokeError || tokenError }}</p><div class="mt-3 rounded-xl border border-border bg-surface overflow-x-auto"><table class="w-full text-left text-sm"><thead class="border-b border-border"><tr><th class="p-3">名称</th><th class="p-3">权限</th><th class="p-3">请求上限</th><th class="p-3">最近使用</th><th class="p-3 text-right">操作</th></tr></thead><tbody class="divide-y divide-border"><tr v-for="token in tokens || []" :key="token.id"><td class="p-3 font-semibold">{{ token.name }}</td><td class="p-3">{{ (token.scopes || []).map(scope => scope === 'read' ? '读取' : scope === 'write' ? '写入' : scope).join('、') || token.role || '未知' }}</td><td class="p-3">{{ token.rate_limit ? `${token.rate_limit} 次/分钟` : '未知' }}</td><td class="p-3">{{ token.last_used_at ? new Date(token.last_used_at).toLocaleString() : '尚未使用' }}</td><td class="p-3 text-right"><button type="button" class="min-h-11 min-w-11 text-text-faint hover:text-danger" aria-label="撤销令牌" @click="deleteToken(token.id)"><Trash2 class="inline w-4 h-4" /></button></td></tr><tr v-if="tokens?.length === 0"><td colspan="5" class="p-6 text-center text-text-faint">暂无访问令牌</td></tr><tr v-if="tokens === null && !tokenError"><td colspan="5" class="p-6 text-center text-text-faint">正在读取令牌…</td></tr></tbody></table></div></section>

    <p v-if="auditError" class="p-4 border border-danger/30 text-sm text-danger" role="alert">工具目录调用状态更新失败：{{ auditError }}</p>
    <details class="rounded-xl border border-border bg-surface"><summary class="min-h-11 cursor-pointer px-5 py-3 font-serif text-lg font-semibold">工具目录与最近真实调用</summary><div class="border-t border-border p-5"><p class="text-sm text-text-muted">页面可见时每 10 秒同步审计记录。绿点表示最近调用成功，红点表示最近调用失败，空心点表示仅发现但最近 100 条审计记录中没有调用。</p><p class="text-xs mt-2" :class="stateClass(mcpProbe.state)">{{ discoveredTools.length }} / {{ mcpTools.length }} 个工具已发现 · {{ auditedToolCount }} / {{ mcpTools.length }} 个工具有最近调用记录</p><ol class="grid lg:grid-cols-2 mt-3"><li v-for="tool in mcpTools" :key="tool[0]" class="grid grid-cols-[minmax(0,1fr)_auto] gap-3 py-3 border-t border-border"><div><code class="text-xs text-accent break-all">{{ tool[0] }}</code><p class="text-sm text-text-muted">{{ tool[1] }}</p><p v-if="latestToolCalls[tool[0]]" class="mt-1 text-xs text-text-faint">{{ callActor(latestToolCalls[tool[0]]!) }} · {{ new Date(latestToolCalls[tool[0]]!.created_at).toLocaleString() }} · {{ latestToolCalls[tool[0]]!.latency_ms }} ms</p></div><div class="flex items-start gap-2"><span class="mt-1.5 h-2 w-2 rounded-full border" :class="latestToolCalls[tool[0]]?.status === 'success' ? 'border-ok bg-ok' : latestToolCalls[tool[0]] ? 'border-danger bg-danger' : 'border-border'" /><span class="text-xs" :class="toolCallClass(tool[0])">{{ toolCallLabel(tool[0]) }}</span></div></li></ol></div></details>

    <AuditLogQuery />

    <UiDialog v-if="selectedApproval" title="确认删除 115 内容" :busy="approvalBusy" @close="selectedApproval = null"><p class="text-sm text-danger">确认后，Agent 只能把下列已绑定对象移入 115 回收站一次。文件夹会连同其中内容一起移入回收站。</p><ul class="my-4 divide-y divide-border border border-border"><li v-for="target in selectedApproval.targets" :key="target.file_id" class="p-3"><strong class="block break-all">{{ target.name }}</strong><span class="text-xs text-text-faint">{{ target.is_folder ? '文件夹（包含其中内容）' : '文件' }} · ID {{ target.file_id }}</span></li></ul><p class="text-xs text-text-faint">请求方：{{ selectedApproval.requested_by }} · 父目录 CID：{{ selectedApproval.parent_cid }} · {{ new Date(selectedApproval.expires_at).toLocaleString() }} 失效</p><div class="mt-5 flex justify-end gap-2"><button type="button" class="min-h-11 border border-border px-4" :disabled="approvalBusy" @click="selectedApproval = null">取消</button><button type="button" class="min-h-11 bg-danger px-4 text-white" :disabled="approvalBusy" @click="decideApproval(selectedApproval, 'approve')">{{ approvalBusy ? '确认中…' : '确认删除这些内容' }}</button></div></UiDialog>

    <UiDialog v-if="showCreateModal" title="创建访问令牌" :busy="creating" @close="closeModal"><p class="text-sm text-text-muted mb-4">按最小权限签发；明文只显示一次。</p><p v-if="creationMessage" class="p-3 border border-border bg-bg text-sm mb-4" role="status">{{ creationMessage }}</p><div v-if="createdTokenSecret"><label class="block text-sm mb-2">令牌明文</label><div class="flex gap-2"><input readonly :value="createdTokenSecret" class="min-w-0 flex-1 min-h-11 px-3 border border-border bg-bg text-xs font-mono" /><CopyButton :text="createdTokenSecret" label="复制令牌" /></div></div><form v-else class="space-y-4" @submit.prevent="createToken"><div><label for="token-name" class="block text-sm mb-2">名称</label><input id="token-name" v-model="tokenName" required class="w-full min-h-11 px-3 border border-border bg-bg" placeholder="例如：Debian Hermes" /></div><div><label for="token-rate" class="block text-sm mb-2">每分钟请求上限</label><input id="token-rate" v-model.number="tokenRateLimit" type="number" min="1" max="600" required class="w-full min-h-11 px-3 border border-border bg-bg" /></div><fieldset><legend class="text-sm mb-2">权限</legend><label class="inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="read" />读取</label><label class="ml-5 inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="write" />写入</label></fieldset><div class="flex justify-end gap-2"><button type="button" class="min-h-11 px-4 border border-border" @click="closeModal">取消</button><button type="submit" class="min-h-11 px-4 bg-accent text-accent-contrast disabled:opacity-50" :disabled="creating || !tokenName.trim() || !selectedScopes.length">{{ creating ? '创建中' : '创建令牌' }}</button></div></form></UiDialog>
  </div>
</template>

<style scoped>
.grid > * { min-width: 0; }
</style>

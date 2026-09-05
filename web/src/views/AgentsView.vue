<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Activity,
  AlertCircle,
  Bot,
  Check,
  CheckCircle2,
  Copy,
  FileCode,
  Key,
  Plus,
  RefreshCw,
  Server,
  ShieldCheck,
  Terminal,
  Trash2,
  XCircle,
} from 'lucide-vue-next'

type ProbeState = 'checking' | 'online' | 'error'

interface Probe {
  state: ProbeState
  detail: string
}

const tokens = ref<any[]>([])
const auditLogs = ref<any[]>([])
const showCreateModal = ref(false)
const tokenName = ref('')
const selectedScopes = ref<string[]>(['read'])
const tokenRateLimit = ref(20)
const creationMessage = ref('')
const createdTokenSecret = ref<string | null>(null)
const copiedKey = ref('')
const checkedAt = ref('')
const discoveredTools = ref<string[]>([])
const toolExecution = ref<Record<string, 'ok' | 'error'>>({})
const mcpProbe = ref<Probe>({ state: 'checking', detail: '等待检测' })
const openAPIProbe = ref<Probe>({ state: 'checking', detail: '等待检测' })

const mcpTools = [
  ['c115_list_files', '列出指定 CID 下的文件与目录'],
  ['c115_search', '按关键词搜索 115 网盘资源'],
  ['c115_save_share', '解析 115 分享链接并转存'],
  ['c115_move', '移动文件或目录'],
  ['c115_rename', '重命名文件或目录'],
  ['c115_mkdir', '在指定目录下创建文件夹'],
  ['c115_get_share_link', '创建文件或目录的 115 分享链接'],
  ['cd2_mount_status', '读取并验证 CloudDrive2 挂载清单'],
  ['cd2_remount', '停止播放并明确确认后通过 gRPC 重新挂载'],
  ['emby_refresh_library', '触发 Emby 媒体库扫描'],
  ['emby_get_libraries', '读取 Emby 媒体库列表'],
  ['emby_inspect_item', '按 ID 精确检查元数据与图片'],
  ['task_submit', '提交受支持的真实后台任务'],
  ['task_query', '查询任务状态、进度与结果'],
  ['task_cancel', '取消等待中或执行中的任务'],
  ['task_get_logs', '读取持久化执行记录与日志'],
  ['system_get_config', '读取脱敏系统设置与目录映射'],
  ['system_health', '检查依赖服务与实际挂载状态'],
] as const

const mcpEndpoint = computed(() => `${window.location.origin}/mcp`)
const localMcpEndpoint = 'http://127.0.0.1:3080/mcp'
const openApiEndpoint = computed(() => `${window.location.origin}/api/v1/openapi.json`)
const hermesCommands = `hermes mcp add embymedia --url ${localMcpEndpoint} --auth header
hermes mcp test embymedia`
const allToolsAvailable = computed(() =>
  discoveredTools.value.length === mcpTools.length
  && mcpTools.every(([name]) => discoveredTools.value.includes(name)),
)

async function fetchTokens() {
  const response = await fetch('/api/v1/tokens')
  if (!response.ok) return
  const data = await response.json()
  tokens.value = data.tokens ?? []
}

async function fetchAuditLogs() {
  const response = await fetch('/api/v1/audit-logs?limit=8')
  if (!response.ok) return
  const data = await response.json()
  auditLogs.value = data.logs ?? []
}

async function readJSONRPC(response: Response) {
  const body = await response.text()
  if (!response.ok) throw new Error(`${response.status} ${body.slice(0, 120)}`.trim())
  if (!body) return null
  if (response.headers.get('content-type')?.includes('text/event-stream')) {
    const dataLine = body.split('\n').reverse().find((line) => line.startsWith('data:'))
    if (!dataLine) throw new Error('MCP 响应缺少 data 事件')
    return JSON.parse(dataLine.slice(5).trim())
  }
  return JSON.parse(body)
}

async function postMCP(payload: object, sessionId = '') {
  const headers: Record<string, string> = {
    Accept: 'application/json, text/event-stream',
    'Content-Type': 'application/json',
    'MCP-Protocol-Version': '2025-03-26',
  }
  if (sessionId) headers['Mcp-Session-Id'] = sessionId
  const response = await fetch('/mcp', {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
  })
  return { response, result: await readJSONRPC(response) }
}

async function probeMCP() {
  mcpProbe.value = { state: 'checking', detail: '正在执行 initialize' }
  try {
    const initialized = await postMCP({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: '2025-03-26',
        capabilities: {},
        clientInfo: { name: 'embymedia-web-diagnostic', version: '2.0.0' },
      },
    })
    const sessionId = initialized.response.headers.get('Mcp-Session-Id') ?? ''
    if (initialized.result?.error) throw new Error(initialized.result.error.message)
    await postMCP({
      jsonrpc: '2.0',
      method: 'notifications/initialized',
      params: {},
    }, sessionId)
    const listed = await postMCP({
      jsonrpc: '2.0',
      id: 2,
      method: 'tools/list',
      params: {},
    }, sessionId)
    if (listed.result?.error) throw new Error(listed.result.error.message)
    discoveredTools.value = (listed.result?.result?.tools ?? []).map((tool: any) => tool.name)
    const smokeTools = ['system_get_config', 'system_health', 'emby_get_libraries', 'cd2_mount_status']
    const checks = await Promise.all(smokeTools.map(async (name, index) => {
      const called = await postMCP({ jsonrpc: '2.0', id: 10 + index, method: 'tools/call', params: { name, arguments: {} } }, sessionId)
      const failed = Boolean(called.result?.error) || !called.result?.result || called.result.result.isError === true
      return [name, failed ? 'error' : 'ok'] as const
    }))
    toolExecution.value = Object.fromEntries(checks)
    mcpProbe.value = {
      state: allToolsAvailable.value ? 'online' : 'error',
      detail: `协议握手成功 · 发现 ${discoveredTools.value.length} 个工具 · 实际调用 ${Object.values(toolExecution.value).filter((state) => state === 'ok').length} 项`,
    }
  } catch (error) {
    discoveredTools.value = []
    toolExecution.value = {}
    mcpProbe.value = {
      state: 'error',
      detail: error instanceof Error ? error.message : 'MCP 连接失败',
    }
  }
}

async function probeOpenAPI() {
  openAPIProbe.value = { state: 'checking', detail: '正在读取 Schema' }
  try {
    const response = await fetch('/api/v1/openapi.json')
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const schema = await response.json()
    const pathCount = Object.keys(schema.paths ?? {}).length
    if (schema.openapi !== '3.1.0') throw new Error('版本不是 OpenAPI 3.1.0')
    openAPIProbe.value = { state: 'online', detail: `OpenAPI ${schema.openapi} · ${pathCount} 条 REST 路径` }
  } catch (error) {
    openAPIProbe.value = {
      state: 'error',
      detail: error instanceof Error ? error.message : 'OpenAPI 读取失败',
    }
  }
}

async function runDiagnostics() {
  checkedAt.value = ''
  await Promise.all([probeMCP(), probeOpenAPI()])
  checkedAt.value = new Date().toLocaleTimeString()
}
async function createToken() {
  if (!tokenName.value.trim() || selectedScopes.value.length === 0) return
  creationMessage.value = ''
  try {
    const response = await fetch('/api/v1/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
	  body: JSON.stringify({ name: tokenName.value.trim(), permissions: selectedScopes.value, rate_limit: tokenRateLimit.value }),
    })
    const data = await response.json()
    if (!response.ok) {
      creationMessage.value = data.error || '创建失败'
      return
    }
    createdTokenSecret.value = data.token
    creationMessage.value = '令牌已创建。明文只显示一次，请立即复制保存。'
    tokenName.value = ''
	  tokenRateLimit.value = 20
    await Promise.all([fetchTokens(), fetchAuditLogs()])
  } catch {
    creationMessage.value = '无法连接服务，请稍后重试。'
  }
}

async function deleteToken(id: string) {
  if (!window.confirm('确认撤销这个访问令牌？已配置的 Agent 将立即失去访问能力。')) return
  const response = await fetch(`/api/v1/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (response.ok) await Promise.all([fetchTokens(), fetchAuditLogs()])
}

async function copyText(key: string, text: string) {
  await navigator.clipboard.writeText(text)
  copiedKey.value = key
  window.setTimeout(() => (copiedKey.value = ''), 1600)
}

function closeModal() {
  showCreateModal.value = false
  createdTokenSecret.value = null
  creationMessage.value = ''
}

onMounted(() => {
  void Promise.all([fetchTokens(), fetchAuditLogs(), runDiagnostics()])
})
</script>

<template>
  <div class="space-y-8">
    <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-3xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">AGENT GATEWAY / LIVE HANDSHAKE</p>
        <h1 class="font-serif text-3xl font-bold text-text">Agent 接入控制台</h1>
		<p class="text-sm text-text-muted mt-2">现场检测 MCP 与 OpenAPI；Hermes 会提示输入 API key / Bearer token，请使用本页签发的令牌。</p>
      </div>
      <div class="flex flex-col sm:flex-row gap-2">
        <button
          type="button"
          class="flex min-h-11 items-center justify-center gap-2 px-4 border border-border bg-surface text-xs font-mono hover:border-accent"
          :disabled="mcpProbe.state === 'checking' || openAPIProbe.state === 'checking'"
          @click="runDiagnostics"
        >
          <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': mcpProbe.state === 'checking' || openAPIProbe.state === 'checking' }" />
          重新检测
        </button>
        <button type="button" class="flex min-h-11 items-center justify-center gap-2 px-4 bg-accent text-accent-contrast text-xs font-mono font-medium hover:bg-accent-strong" @click="showCreateModal = true">
          <Plus class="w-4 h-4" />创建访问令牌
        </button>
      </div>
    </div>

    <section class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm" aria-labelledby="connection-title">
      <div class="grid xl:grid-cols-[1.3fr_0.7fr]">
        <div class="p-6 sm:p-8 border-b xl:border-b-0 xl:border-r border-border">
          <div class="flex flex-wrap items-start justify-between gap-4 mb-8">
            <div>
              <p class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint">LIVE SIGNAL PATH</p>
              <h2 id="connection-title" class="mt-2 font-serif text-2xl font-semibold text-text">Hermes 到工具执行链</h2>
            </div>
            <span class="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border text-xs font-mono" :class="allToolsAvailable ? 'border-ok/30 bg-ok/10 text-ok' : 'border-danger/30 bg-danger/10 text-danger'">
              <span class="w-2 h-2 rounded-full" :class="allToolsAvailable ? 'bg-ok' : 'bg-danger'"></span>
              {{ allToolsAvailable ? '协议目录完整' : '工具目录不完整' }}
            </span>
          </div>

          <ol class="grid grid-cols-1 sm:grid-cols-4 gap-0" aria-label="Agent 连接路径">
            <li class="relative border-l sm:border-l-0 sm:border-t border-accent pl-5 pb-6 sm:pl-0 sm:pb-0 sm:pt-5">
              <span class="absolute -left-1.5 sm:left-0 sm:-top-1.5 w-3 h-3 rounded-full bg-accent"></span>
              <Terminal class="w-5 h-5 text-accent mb-2" />
              <strong class="block text-sm text-text">Debian Hermes</strong>
              <span class="text-xs text-text-faint">v0.21.0</span>
            </li>
            <li class="relative border-l sm:border-l-0 sm:border-t border-accent pl-5 pb-6 sm:pl-0 sm:pb-0 sm:pt-5">
              <span class="absolute -left-1.5 sm:left-0 sm:-top-1.5 w-3 h-3 rounded-full bg-accent"></span>
              <Activity class="w-5 h-5 text-accent mb-2" />
              <strong class="block text-sm text-text">Streamable HTTP</strong>
              <span class="text-xs text-text-faint">同机回环地址</span>
            </li>
            <li class="relative border-l sm:border-l-0 sm:border-t border-accent pl-5 pb-6 sm:pl-0 sm:pb-0 sm:pt-5">
              <span class="absolute -left-1.5 sm:left-0 sm:-top-1.5 w-3 h-3 rounded-full bg-accent"></span>
              <Server class="w-5 h-5 text-accent mb-2" />
              <strong class="block text-sm text-text">Go MCP Core</strong>
              <span class="text-xs text-text-faint">协议初始化完成</span>
            </li>
            <li class="relative border-l sm:border-l-0 sm:border-t border-accent pl-5 sm:pl-0 sm:pt-5">
              <span class="absolute -left-1.5 sm:left-0 sm:-top-1.5 w-3 h-3 rounded-full" :class="allToolsAvailable ? 'bg-ok' : 'bg-danger'"></span>
              <Bot class="w-5 h-5 text-accent mb-2" />
              <strong class="block text-sm text-text">{{ discoveredTools.length || '—' }} 个工具</strong>
              <span class="text-xs text-text-faint">动态发现结果</span>
            </li>
          </ol>
        </div>

        <div class="p-6 sm:p-8 bg-accent text-accent-contrast">
          <ShieldCheck class="w-7 h-7 mb-8 opacity-80" />
          <p class="font-serif text-2xl leading-snug">先检测，再复制配置。</p>
          <p class="mt-4 text-sm leading-7 opacity-80">3081 的旧 SSE 端口未对公网开放。Debian 上的 Hermes 应连接本机 3080 端口的 <code>/mcp</code>，无需绕行公网。</p>
          <p v-if="checkedAt" class="mt-6 pt-5 border-t border-white/20 text-xs font-mono opacity-70">最近检测 {{ checkedAt }}</p>
        </div>
      </div>
    </section>

    <section class="grid lg:grid-cols-[1fr_1.25fr] gap-5" aria-label="连接诊断与配置">
      <div class="rounded-xl border border-border bg-surface overflow-hidden">
        <div class="p-5 border-b border-border flex items-center justify-between gap-3">
          <h2 class="font-serif text-xl font-semibold">实时诊断</h2>
          <span class="text-[10px] font-mono text-text-faint">ACTUAL REQUESTS</span>
        </div>
        <dl>
          <div class="p-5 border-b border-border flex items-start gap-3">
            <CheckCircle2 v-if="mcpProbe.state === 'online'" class="w-5 h-5 text-ok shrink-0" />
            <XCircle v-else-if="mcpProbe.state === 'error'" class="w-5 h-5 text-danger shrink-0" />
            <RefreshCw v-else class="w-5 h-5 text-accent animate-spin shrink-0" />
            <div class="min-w-0"><dt class="text-sm font-semibold">MCP 协议握手</dt><dd class="mt-1 text-xs font-mono text-text-muted break-words">{{ mcpProbe.detail }}</dd></div>
          </div>
          <div class="p-5 flex items-start gap-3">
            <CheckCircle2 v-if="openAPIProbe.state === 'online'" class="w-5 h-5 text-ok shrink-0" />
            <XCircle v-else-if="openAPIProbe.state === 'error'" class="w-5 h-5 text-danger shrink-0" />
            <RefreshCw v-else class="w-5 h-5 text-accent animate-spin shrink-0" />
            <div class="min-w-0"><dt class="text-sm font-semibold">OpenAPI Schema</dt><dd class="mt-1 text-xs font-mono text-text-muted break-words">{{ openAPIProbe.detail }}</dd></div>
          </div>
        </dl>
      </div>

      <div class="rounded-xl border border-border bg-surface overflow-hidden">
        <div class="p-5 border-b border-border"><h2 class="font-serif text-xl font-semibold">Debian / Hermes 推荐配置</h2><p class="mt-1 text-xs text-text-muted">在安装 Hermes 的 gaotao.cc 主机执行。</p></div>
        <div class="p-5">
          <div class="relative">
            <pre class="overflow-x-auto bg-text text-bg p-4 pr-12 text-xs font-mono leading-6"><code>{{ hermesCommands }}</code></pre>
            <button type="button" class="absolute top-2 right-2 p-2 border border-white/20 text-white hover:border-white/60" aria-label="复制 Hermes 命令" @click="copyText('hermes', hermesCommands)">
              <Check v-if="copiedKey === 'hermes'" class="w-4 h-4" /><Copy v-else class="w-4 h-4" />
            </button>
          </div>
          <dl class="mt-5 space-y-4">
            <div><dt class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint">本机 MCP（推荐）</dt><dd class="mt-2 flex gap-2"><code class="min-w-0 flex-1 overflow-x-auto border border-border bg-bg px-3 py-2 text-xs font-mono">{{ localMcpEndpoint }}</code><button type="button" class="p-2 border border-border hover:border-accent" aria-label="复制本机 MCP 地址" @click="copyText('local', localMcpEndpoint)"><Check v-if="copiedKey === 'local'" class="w-4 h-4 text-ok" /><Copy v-else class="w-4 h-4" /></button></dd></div>
            <div><dt class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint">当前浏览器 MCP</dt><dd class="mt-2 flex gap-2"><code class="min-w-0 flex-1 overflow-x-auto border border-border bg-bg px-3 py-2 text-xs font-mono">{{ mcpEndpoint }}</code><button type="button" class="p-2 border border-border hover:border-accent" aria-label="复制当前 MCP 地址" @click="copyText('mcp', mcpEndpoint)"><Check v-if="copiedKey === 'mcp'" class="w-4 h-4 text-ok" /><Copy v-else class="w-4 h-4" /></button></dd></div>
            <div><dt class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint">OPENAPI 3.1</dt><dd class="mt-2"><a :href="openApiEndpoint" target="_blank" rel="noreferrer" class="inline-flex min-h-11 items-center gap-2 text-sm font-mono text-accent underline underline-offset-4"><FileCode class="w-4 h-4" />{{ openApiEndpoint }}</a></dd></div>
          </dl>
        </div>
      </div>
    </section>

    <section>
      <div class="flex items-end justify-between gap-4 mb-4">
        <div><p class="text-[10px] font-mono text-text-faint tracking-[0.14em]">SERVER-DISCOVERED CATALOG</p><h2 class="mt-1 font-serif font-semibold text-xl text-text">工具目录</h2></div>
        <span class="font-mono text-xs" :class="allToolsAvailable ? 'text-ok' : 'text-danger'">{{ discoveredTools.length }} / {{ mcpTools.length }}</span>
      </div>
      <p class="mb-4 text-xs leading-5 text-text-faint">绿点表示已完成一次真实只读调用；红点表示调用返回错误；空心点仅表示协议已发现。写操作由各自的参数校验、权限与上游结果决定。</p>
      <ol class="grid grid-cols-1 lg:grid-cols-2 border-t border-border">
        <li v-for="(tool, index) in mcpTools" :key="tool[0]" class="grid grid-cols-[2.25rem_minmax(0,1fr)_auto] gap-3 py-4 border-b border-border lg:odd:pr-6 lg:even:pl-6">
          <span class="font-mono text-xs text-annotation">{{ String(index + 1).padStart(2, '0') }}</span>
          <div class="min-w-0"><code class="text-xs font-semibold text-accent break-all">{{ tool[0] }}</code><p class="mt-1 text-sm text-text-muted">{{ tool[1] }}</p></div>
          <span class="mt-1 w-2 h-2 rounded-full border" :class="toolExecution[tool[0]] === 'ok' ? 'bg-ok border-ok' : toolExecution[tool[0]] === 'error' ? 'bg-danger border-danger' : discoveredTools.includes(tool[0]) ? 'border-accent' : 'border-border'" :aria-label="toolExecution[tool[0]] === 'ok' ? '调用成功' : toolExecution[tool[0]] === 'error' ? '调用失败' : discoveredTools.includes(tool[0]) ? '已发现但未调用' : '未发现'"></span>
        </li>
      </ol>
    </section>

    <section class="grid xl:grid-cols-[1.15fr_0.85fr] gap-5">
      <div class="min-w-0">
        <div class="flex items-center justify-between gap-3 mb-4"><div class="flex items-center gap-2"><Key class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-xl">访问令牌</h2></div><span class="text-xs font-mono text-text-faint">{{ tokens.length }} 个</span></div>
        <div class="rounded-xl border border-border bg-surface overflow-x-auto">
          <table class="w-full text-left text-xs font-mono">
            <thead class="border-b border-border bg-bg-muted/50 text-text-muted"><tr><th class="py-3 px-5">名称</th><th class="py-3 px-4">权限</th><th class="py-3 px-4">最近使用</th><th class="py-3 px-4 text-right">操作</th></tr></thead>
            <tbody class="divide-y divide-border/60">
              <tr v-for="token in tokens" :key="token.id"><td class="py-3 px-5 font-semibold">{{ token.name }}</td><td class="py-3 px-4">{{ (token.scopes || []).join(' + ') || token.role }}</td><td class="py-3 px-4">{{ token.last_used_at ? new Date(token.last_used_at).toLocaleString() : '尚未使用' }}</td><td class="py-3 px-4 text-right"><button type="button" class="p-2 text-text-faint hover:text-danger" aria-label="撤销令牌" @click="deleteToken(token.id)"><Trash2 class="w-4 h-4" /></button></td></tr>
              <tr v-if="tokens.length === 0"><td colspan="4" class="py-8 px-5 text-center text-text-faint">暂无访问令牌</td></tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="min-w-0">
        <div class="flex items-center gap-2 mb-4"><Activity class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-xl">最近访问记录</h2></div>
        <div class="rounded-xl border border-border bg-surface divide-y divide-border min-h-32">
          <div v-for="log in auditLogs" :key="log.id" class="p-4 flex items-start justify-between gap-4">
            <div class="min-w-0"><code class="text-xs text-accent break-all">{{ log.action }}</code><p class="mt-1 text-xs text-text-faint">{{ log.caller }} · {{ log.ip || '本机' }}</p></div>
            <time class="text-[10px] font-mono text-text-faint whitespace-nowrap">{{ new Date(log.created_at).toLocaleTimeString() }}</time>
          </div>
          <p v-if="auditLogs.length === 0" class="p-8 text-center text-sm text-text-faint">暂无写操作记录</p>
        </div>
      </div>
    </section>

    <div v-if="showCreateModal" class="fixed inset-0 bg-black/45 flex items-center justify-center z-50 p-4" @click.self="closeModal">
      <div class="w-full max-w-md p-6 rounded-xl border border-border bg-surface shadow-xl space-y-5" role="dialog" aria-modal="true" aria-labelledby="token-dialog-title">
        <div><h3 id="token-dialog-title" class="font-serif font-semibold text-xl text-text">创建访问令牌</h3><p class="text-sm text-text-muted mt-1">令牌按最小权限签发，明文只显示一次。</p></div>
        <div v-if="creationMessage" class="p-3 border border-border bg-bg text-sm flex items-start gap-2"><AlertCircle class="w-4 h-4 mt-0.5 shrink-0 text-annotation" /><span>{{ creationMessage }}</span></div>
        <div v-if="createdTokenSecret" class="space-y-2"><label class="block text-xs font-mono text-text-muted">令牌明文</label><div class="flex gap-2"><input readonly :value="createdTokenSecret" class="min-w-0 flex-1 px-3 py-2 border border-border bg-bg text-xs font-mono" /><button type="button" class="px-3 border border-accent bg-accent text-accent-contrast" @click="copyText('token', createdTokenSecret)">复制</button></div></div>
        <template v-if="!creationMessage">
          <div><label for="token-name" class="block text-xs font-mono text-text-muted mb-2">名称</label><input id="token-name" v-model="tokenName" type="text" placeholder="例如：Debian Hermes" class="w-full min-h-11 px-3 border border-border bg-bg text-sm focus:border-accent" /></div>
          <div><label for="token-rate" class="block text-xs font-mono text-text-muted mb-2">每分钟请求上限</label><input id="token-rate" v-model.number="tokenRateLimit" type="number" min="1" max="600" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono" /></div>
          <fieldset><legend class="block text-xs font-mono text-text-muted mb-2">权限</legend><label class="inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="read" />读取</label><label class="ml-5 inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="write" />写入</label></fieldset>
        </template>
        <div class="flex flex-col-reverse sm:flex-row justify-end gap-2 pt-2"><button type="button" class="min-h-11 px-4 border border-border text-sm" @click="closeModal">关闭</button><button v-if="!creationMessage" type="button" class="min-h-11 px-4 bg-accent text-accent-contrast text-sm disabled:opacity-50" :disabled="!tokenName.trim() || selectedScopes.length === 0" @click="createToken">创建令牌</button></div>
      </div>
    </div>
  </div>
</template>

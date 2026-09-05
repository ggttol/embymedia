<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

interface AuditLog {
  id: string
  action: string
  caller: string
  agent_name?: string
  token_id?: string
  ip?: string
  status: string
  latency_ms: number
  created_at: string
  input?: unknown
  output?: unknown
}
interface AuditSummary { total: number; success: number; error: number; denied: number; avg_latency_ms: number }
interface ToolSummary { action: string; total: number; error: number; denied: number; avg_latency_ms: number }
interface AuditResponse { logs: AuditLog[]; total: number; summary: AuditSummary; tools: ToolSummary[]; agents: string[] }
interface Filters { protocol: 'mcp' | 'all'; agent: string; action: string; status: string; from: string; to: string; q: string }
const defaultFilters = (): Filters => ({ protocol: 'mcp', agent: '', action: '', status: '', from: '', to: '', q: '' })
const filters = ref(defaultFilters())
const appliedFilters = ref(defaultFilters())
const result = ref<AuditResponse | null>(null)
const agents = ref<string[]>([])
const loading = ref(false)
const error = ref('')
const filterError = ref('')
const offset = ref(0)
const limit = 25
const updatedAt = ref('')
let appliedParams = new URLSearchParams({ protocol: 'mcp' })
let controller: AbortController | undefined
let requestId = 0
const pageCount = computed(() => Math.max(1, Math.ceil((result.value?.total ?? 0) / limit)))
const pageNumber = computed(() => Math.floor(offset.value / limit) + 1)
const appliedDescription = computed(() => {
  const query = appliedFilters.value
  return [query.protocol === 'mcp' ? '仅 MCP 调用' : '全部协议', query.agent && `Agent：${query.agent}`, query.action && `动作：${query.action}`, query.status && `状态：${statusLabel(query.status)}`, query.from && `从 ${query.from.replace('T', ' ')}`, query.to && `至 ${query.to.replace('T', ' ')}`, query.q && `关键词：${query.q}`].filter(Boolean).join(' · ')
})
const statusLabels: Record<string, string> = { success: '成功', error: '失败', denied: '拒绝' }
const statusLabel = (status: string) => statusLabels[status] ?? status
const statusClass = (status: string) => status === 'success' ? 'text-ok' : status === 'denied' ? 'text-annotation' : status === 'error' ? 'text-danger' : 'text-text-muted'
const actor = (log: AuditLog) => log.agent_name || (log.caller === 'mcp-stdio' ? '本机 MCP（身份未知）' : '身份未知')
const timestamp = (value: string) => new Date(value).toLocaleString()
const rate = (count: number, total: number) => total > 0 ? `${(count / total * 100).toFixed(1)}%` : '—'
const latency = (value: number, total: number) => total > 0 ? `${value.toFixed(1)} ms` : '—'
const payload = (value: unknown) => value === undefined || value === null ? '未记录' : typeof value === 'string' ? value : JSON.stringify(value, null, 2)

async function loadPage(nextOffset: number) {
  controller?.abort()
  const activeController = new AbortController()
  controller = activeController
  const activeRequest = ++requestId
  loading.value = true
  error.value = ''
  offset.value = nextOffset
  const params = new URLSearchParams(appliedParams)
  params.set('limit', String(limit))
  params.set('offset', String(nextOffset))
  try {
    const response = await fetch(`/api/v1/audit-logs?${params}`, { signal: activeController.signal })
    if (!response.ok) {
      let message = `调用记录读取失败（HTTP ${response.status}）`
      try { const body = await response.json(); if (typeof body.error === 'string') message = body.error } catch { /* Non-JSON failures retain the HTTP status. */ }
      throw new Error(message)
    }
    const data: AuditResponse = await response.json()
    if (!Array.isArray(data.logs) || !Array.isArray(data.tools) || !Array.isArray(data.agents) || !Number.isInteger(data.total) || data.total < 0 || !data.summary || !['total', 'success', 'error', 'denied', 'avg_latency_ms'].every((key) => typeof data.summary[key as keyof AuditSummary] === 'number')) throw new Error('调用记录数据格式不完整')
    if (activeRequest !== requestId) return
    const lastOffset = Math.max(0, Math.ceil(data.total / limit) - 1) * limit
    if (nextOffset > lastOffset) { await loadPage(lastOffset); return }
    result.value = data
    agents.value = data.agents
    offset.value = nextOffset
    updatedAt.value = new Date().toLocaleTimeString()
  } catch (cause) {
    if (activeRequest !== requestId || activeController.signal.aborted) return
    result.value = null
    error.value = cause instanceof Error ? cause.message : '调用记录读取失败，请重试。'
  } finally {
    if (activeRequest === requestId) loading.value = false
  }
}

function submitQuery() {
  filterError.value = ''
  const query = { ...filters.value }
  const params = new URLSearchParams({ protocol: query.protocol })
  for (const key of ['agent', 'action', 'status', 'q'] as const) if (query[key]) params.set(key, query[key])
  for (const key of ['from', 'to'] as const) {
    if (!query[key]) continue
    const date = new Date(query[key])
    if (!Number.isFinite(date.getTime())) { filterError.value = '请输入有效的起止时间。'; return }
    params.set(key, date.toISOString())
  }
  if (query.from && query.to && new Date(query.from) > new Date(query.to)) { filterError.value = '开始时间不能晚于结束时间。'; return }
  appliedFilters.value = query
  appliedParams = params
  void loadPage(0)
}
function resetQuery() { filters.value = defaultFilters(); submitQuery() }
function changePage(direction: -1 | 1) {
  if (loading.value || !result.value) return
  const nextOffset = offset.value + direction * limit
  if (nextOffset < 0 || nextOffset >= result.value.total) return
  void loadPage(nextOffset)
}
onMounted(() => { void loadPage(0) })
onBeforeUnmount(() => { requestId++; controller?.abort() })
</script>

<template>
  <section aria-labelledby="audit-query-title" class="audit-query rounded-xl border border-border bg-surface">
    <header class="p-4 sm:p-5 border-b border-border">
      <h2 id="audit-query-title" class="font-serif text-xl font-semibold">MCP 调用记录查询</h2>
      <p class="mt-2 text-sm text-text-muted">查看工具调用、参数与返回摘要，不包含用户原始对话或 Agent 思考过程。输入与输出仅展示服务端保存的脱敏记录，可能被截断。</p>
    </header>
    <form class="p-4 sm:p-5 border-b border-border" @submit.prevent="submitQuery">
      <div class="filter-grid">
        <label class="audit-field"><span>协议范围</span><select v-model="filters.protocol"><option value="mcp">仅 MCP 调用</option><option value="all">全部协议</option></select></label>
        <label class="audit-field"><span>Agent 身份</span><select v-model="filters.agent"><option value="">全部身份（含未知）</option><option v-for="name in agents" :key="name" :value="name">{{ name }}</option></select></label>
        <label class="audit-field"><span>工具 / 动作（精确名称）</span><input v-model="filters.action" type="text" placeholder="例如 system_health" /></label>
        <label class="audit-field"><span>调用状态</span><select v-model="filters.status"><option value="">全部状态</option><option value="success">成功</option><option value="error">失败</option><option value="denied">拒绝</option></select></label>
        <label class="audit-field"><span>开始时间（本地时区）</span><input v-model="filters.from" type="datetime-local" step="1" /></label>
        <label class="audit-field"><span>结束时间（本地时区）</span><input v-model="filters.to" type="datetime-local" step="1" /></label>
        <label class="audit-field keyword-field"><span>关键词（按字面包含匹配）</span><input v-model="filters.q" type="search" placeholder="搜索动作、Agent、脱敏输入或输出" /></label>
      </div>
      <p class="mt-3 text-xs text-text-faint">Agent 选项来自全部 MCP 记录；无身份的调用不会推断为某个客户端。筛选条件提交后生效，查询结果不自动刷新。</p>
      <p v-if="filterError" class="mt-3 text-sm text-danger" role="alert">{{ filterError }}</p>
      <div class="mt-4 flex flex-wrap gap-2">
        <button type="submit" class="audit-button primary">{{ loading ? '重新查询' : '查询记录' }}</button>
        <button type="button" class="audit-button" @click="resetQuery">重置筛选</button>
        <button type="button" class="audit-button" :disabled="loading" @click="loadPage(offset)">刷新当前页</button>
      </div>
    </form>
    <div class="p-4 sm:p-5" :aria-busy="loading">
      <p class="text-xs text-text-muted break-words">已提交条件：{{ appliedDescription }}</p>
      <p class="mt-2 text-sm text-text-muted" role="status" aria-live="polite">{{ loading ? '正在查询调用记录…' : error ? '查询未完成。' : result ? `共 ${result.total} 条匹配记录 · ${updatedAt} 更新` : '' }}</p>
      <div v-if="error" class="mt-4 border border-danger/30 p-4 text-sm text-danger" role="alert"><p>{{ error }}</p><button type="button" class="audit-button mt-3" @click="loadPage(offset)">重试查询</button></div>
      <div v-if="result && !loading && !error" class="mt-5 space-y-5">
        <section aria-labelledby="audit-summary-title">
          <h3 id="audit-summary-title" class="font-semibold">匹配范围统计</h3>
          <p class="mt-1 text-xs text-text-muted">统计与工具汇总均覆盖全部匹配记录，不限当前页。失败率 = 失败数 ÷ 匹配总数；拒绝率 = 拒绝数 ÷ 匹配总数，拒绝不计入失败。</p>
          <dl class="summary-grid mt-3">
            <div><dt>调用总数</dt><dd>{{ result.summary.total }}</dd></div>
            <div><dt>成功</dt><dd class="text-ok">{{ result.summary.success }}</dd></div>
            <div><dt>失败 / 失败率</dt><dd class="text-danger">{{ result.summary.error }} <small>/ {{ rate(result.summary.error, result.summary.total) }}</small></dd></div>
            <div><dt>拒绝 / 拒绝率</dt><dd class="text-annotation">{{ result.summary.denied }} <small>/ {{ rate(result.summary.denied, result.summary.total) }}</small></dd></div>
            <div><dt>平均耗时（全部状态）</dt><dd>{{ latency(result.summary.avg_latency_ms, result.summary.total) }}</dd></div>
          </dl>
        </section>
        <section aria-labelledby="audit-tools-title">
          <h3 id="audit-tools-title" class="font-semibold">工具 / 动作汇总</h3>
          <p class="mt-1 text-xs text-text-muted">比较调用量、失败与拒绝次数以及平均耗时，定位需要优化的工具。</p>
          <div v-if="result.tools.length" class="table-scroll mt-3" tabindex="0" role="region" aria-label="全部匹配记录的工具汇总，可横向滚动">
            <table class="w-full text-left text-sm">
              <caption class="sr-only">全部匹配记录的工具汇总，平均耗时包含全部状态</caption>
              <thead><tr><th scope="col">工具 / 动作</th><th scope="col">调用数</th><th scope="col">失败</th><th scope="col">拒绝</th><th scope="col">平均耗时</th></tr></thead>
              <tbody><tr v-for="tool in result.tools" :key="tool.action"><th scope="row" class="font-mono font-normal break-all">{{ tool.action }}</th><td>{{ tool.total }}</td><td>{{ tool.error }}</td><td>{{ tool.denied }}</td><td>{{ latency(tool.avg_latency_ms, tool.total) }}</td></tr></tbody>
            </table>
          </div>
          <p v-else class="mt-3 text-sm text-text-faint">没有匹配的工具记录。</p>
        </section>
        <section aria-labelledby="audit-records-title">
          <h3 id="audit-records-title" class="font-semibold">调用明细</h3>
          <p v-if="!result.logs.length" class="mt-3 border border-border p-6 text-center text-sm text-text-muted">没有符合条件的调用记录。可放宽时间范围或重置筛选。</p>
          <div v-else class="mt-3 space-y-2">
            <details v-for="log in result.logs" :key="log.id" class="log-details border border-border">
              <summary class="cursor-pointer p-3 sm:p-4">
                <span class="font-mono text-sm text-accent break-all">{{ log.action }}</span>
                <span class="ml-2 text-xs font-semibold" :class="statusClass(log.status)">{{ statusLabel(log.status) }}</span>
                <span class="block mt-1 text-xs text-text-muted break-words"><time :datetime="log.created_at">{{ timestamp(log.created_at) }}</time> · {{ actor(log) }} · {{ log.latency_ms }} ms</span>
                <span class="block mt-1 text-xs text-text-faint">展开 / 收起脱敏详情</span>
              </summary>
              <div class="border-t border-border p-3 sm:p-4 space-y-4">
                <dl class="log-metadata text-xs">
                  <div><dt>记录 ID</dt><dd>{{ log.id }}</dd></div><div><dt>时间戳</dt><dd>{{ log.created_at }}</dd></div>
                  <div><dt>Agent 身份</dt><dd>{{ actor(log) }}</dd></div><div><dt>来源类型</dt><dd>{{ log.caller || '未记录' }}</dd></div>
                  <div><dt>来源 IP</dt><dd>{{ log.ip || '未记录' }}</dd></div><div><dt>令牌 ID（非密钥）</dt><dd>{{ log.token_id || '未记录' }}</dd></div>
                </dl>
                <div class="payload-grid"><div><h4 class="text-xs font-semibold mb-2">输入（脱敏记录）</h4><pre tabindex="0" aria-label="脱敏输入">{{ payload(log.input) }}</pre></div><div><h4 class="text-xs font-semibold mb-2">输出（脱敏摘要）</h4><pre tabindex="0" aria-label="脱敏输出摘要">{{ payload(log.output) }}</pre></div></div>
              </div>
            </details>
          </div>
        </section>
      </div>
      <nav class="mt-5 pt-4 border-t border-border flex flex-wrap items-center justify-between gap-3" aria-label="调用记录分页">
        <p class="text-xs text-text-muted">{{ loading ? '正在读取分页…' : result ? `第 ${pageNumber} / ${pageCount} 页` : '暂无可显示页' }} · 每页 {{ limit }} 条</p>
        <div class="flex gap-2"><button type="button" class="audit-button" :disabled="loading || !result || offset === 0" @click="changePage(-1)">上一页</button><button type="button" class="audit-button" :disabled="loading || !result || offset + limit >= result.total" @click="changePage(1)">下一页</button></div>
      </nav>
    </div>
  </section>
</template>

<style scoped>
.audit-query, .filter-grid > *, .summary-grid > *, .payload-grid > *, .log-metadata > * { min-width: 0; }
.audit-query { overflow-wrap: anywhere; }
.filter-grid, .summary-grid, .payload-grid, .log-metadata { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.audit-field { display: flex; min-width: 0; flex-direction: column; gap: 6px; font-size: 12px; }
.audit-field input, .audit-field select { width: 100%; min-width: 0; min-height: 44px; padding: 8px 10px; border: 1px solid var(--border); background: var(--bg); color: var(--text); font-size: 14px; }
.audit-button { min-height: 44px; padding: 8px 14px; border: 1px solid var(--border); background: var(--surface); font-size: 13px; }
.audit-button.primary { background: var(--accent); border-color: var(--accent); color: var(--accent-contrast); }
.audit-button:disabled { opacity: 0.45; cursor: not-allowed; }
.summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.summary-grid > div { border: 1px solid var(--border); padding: 12px; background: var(--bg); }
.summary-grid dt, .log-metadata dt { color: var(--text-muted); font-size: 12px; }
.summary-grid dd { margin-top: 6px; font-size: 20px; font-weight: 600; }
.summary-grid small { font-size: 12px; font-weight: 400; }
.table-scroll { max-width: 100%; overflow-x: auto; border: 1px solid var(--border); }
.table-scroll:focus-visible, pre:focus-visible { outline: 2px solid var(--annotation); outline-offset: 3px; }
.table-scroll table { min-width: 510px; table-layout: fixed; }
.table-scroll th, .table-scroll td { padding: 10px 12px; border-bottom: 1px solid var(--border); vertical-align: top; }
.table-scroll th:first-child { width: 36%; }
.table-scroll thead { background: var(--bg); }
.log-details summary { min-height: 44px; }
.log-details[open] summary { background: var(--accent-soft); }
.log-metadata dd { margin-top: 3px; overflow-wrap: anywhere; }
pre { max-width: 100%; max-height: 320px; overflow-y: auto; white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; background: var(--bg); border: 1px solid var(--border); padding: 12px; font-size: 12px; }
@media (min-width: 640px) { .filter-grid, .log-metadata { grid-template-columns: repeat(2, minmax(0, 1fr)); } .keyword-field { grid-column: span 2; } }
@media (min-width: 1024px) { .filter-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } .summary-grid { grid-template-columns: repeat(5, minmax(0, 1fr)); } .payload-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>

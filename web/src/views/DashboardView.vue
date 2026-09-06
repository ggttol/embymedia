<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { ArrowUpRight, RefreshCw } from 'lucide-vue-next'

type SourceKey = 'summary' | 'trends' | 'accounts' | 'libraries' | 'tasks' | 'mounts'
type LoadState = 'untested' | 'loading' | 'success' | 'error'
interface Source { key: SourceKey; name: string; url: string; route: string; state: LoadState; error: string; checkedAt: string }
interface Task { id: string; name: string; type: string; status: string; error?: string }
interface Account { id: string; name: string; status: string }
interface Mount { name?: string; mount_path: string; status: string; error?: string }
const sources = ref<Source[]>([
  { key: 'summary', name: '资源摘要', url: '/api/v1/home/summary', route: '/resources', state: 'untested', error: '', checkedAt: '' },
  { key: 'accounts', name: '115 账号', url: '/api/v1/accounts', route: '/settings', state: 'untested', error: '', checkedAt: '' },
  { key: 'libraries', name: 'Emby 媒体库', url: '/api/v1/emby/libraries', route: '/settings', state: 'untested', error: '', checkedAt: '' },
  { key: 'tasks', name: '后台任务', url: '/api/v1/async-tasks', route: '/tasks', state: 'untested', error: '', checkedAt: '' },
  { key: 'mounts', name: 'CloudDrive 挂载', url: '/api/v1/mounts', route: '/settings', state: 'untested', error: '', checkedAt: '' },
  { key: 'trends', name: '搜索趋势', url: '/api/v1/trends', route: '/resources', state: 'untested', error: '', checkedAt: '' },
])
const stats = ref({ totalIndexedLinks: null as number | null, validLinks: null as number | null, todayUpdated: null as number | null, driveAccounts: null as number | null, embyLibraries: null as number | null, activeTasks: null as number | null, mountedMounts: null as number | null, totalMounts: null as number | null })
const trends = ref<string[]>([])
const tasks = ref<Task[]>([])
const accounts = ref<Account[]>([])
const mounts = ref<Mount[]>([])
const loading = ref(false)
const checkedAt = ref('')
const source = (key: SourceKey) => sources.value.find((item) => item.key === key)!
const readErrors = computed(() => sources.value.filter((item) => item.state === 'error'))
const recentTasks = computed(() => {
  const priority: Record<string, number> = { running: 0, failed: 1, pending: 2 }
  return [...tasks.value].sort((a, b) => (priority[a.status] ?? 3) - (priority[b.status] ?? 3)).slice(0, 6)
})
const serviceExceptions = computed(() => [
  ...(source('accounts').state === 'success' ? accounts.value.filter((item) => item.status !== 'active').map((item) => ({ key: item.id, name: item.name || '115 账号', detail: item.status === 'expired' ? '凭据已过期，请更新账号配置。' : item.status === 'error' ? '账号记录异常，请检查凭据。' : '账号状态未知，请检查配置。', route: '/settings' })) : []),
  ...(source('mounts').state === 'success' ? mounts.value.filter((item) => item.status !== 'mounted').map((item) => ({ key: item.mount_path, name: item.name || item.mount_path, detail: item.error || '挂载尚未就绪，请检查 CloudDrive 配置。', route: '/settings' })) : []),
])
const taskStatus = (status: string) => ({ running: '运行中', failed: '失败', pending: '等待中', completed: '已完成', paused: '已暂停', idle: '空闲', cancelled: '已取消' }[status] ?? '未知状态')
const taskDefinition = (type: string) => ({
  emby_refresh: ['同步媒体并刷新 Emby', '先生成和校验 STRM，再跟踪 Emby 全库扫描到完成。'],
  emby_missing_posters: ['检查缺失海报', '检查没有主海报的 Emby 媒体条目。'],
  emby_match: ['修正媒体匹配', '将指定 Emby 条目匹配到明确的 TMDB 条目。'],
  strm_sync: ['同步 STRM 文件', '根据媒体源目录创建或更新 STRM 文件。'],
  strm_verify: ['检查 STRM 链接', '检查 STRM 是否仍指向媒体源目录中的有效文件。'],
  c115_save_share: ['转存 115 分享', '把 115 分享内容转存到指定网盘目录。'],
  c115_offline_download: ['提交 115 离线下载', '把下载地址提交到指定 115 网盘目录。'],
}[type] ?? ['后台任务', '执行已提交的媒体运维工作。'])
const taskDetail = (task: Task) => {
  const [label, description] = taskDefinition(task.type)
  const customName = task.name && task.name !== task.type && task.name !== label ? `任务名称：${task.name}。` : ''
  return { label, description: `${customName}${description}` }
}
const count = (value: number | null) => value === null ? '—' : value.toLocaleString()
const sourceLabel = (key: SourceKey) => {
  const item = source(key)
  if (item.state === 'loading') return item.checkedAt ? '刷新中 · 显示上次数据' : '读取中'
  if (item.state === 'error') return item.checkedAt ? '读取失败 · 显示上次数据' : '读取失败 · 数据未知'
  return item.state === 'success' ? `上次读取成功 ${item.checkedAt}` : '尚未读取'
}
function collection<T>(data: Record<string, unknown>, key: string): T[] {
  if (!(key in data) || (data[key] !== null && !Array.isArray(data[key]))) throw new Error('服务返回的数据格式不完整')
  return (data[key] ?? []) as T[]
}
async function fetchHomeData() {
  if (loading.value) return
  loading.value = true
  await Promise.all(sources.value.map(async (item) => {
    item.state = 'loading'
    item.error = ''
    try {
      const response = await fetch(item.url)
      if (!response.ok) throw new Error(`读取失败（HTTP ${response.status}）`)
      const data = await response.json()
      if (!data || typeof data !== 'object') throw new Error('服务返回的数据格式不完整')
      switch (item.key) {
        case 'summary': {
          const summary = data.summary?.summary ?? data.summary
          if (!summary || !['links', 'health_valid', 'today_updated'].every((key) => typeof summary[key] === 'number')) throw new Error('资源摘要缺少统计数据')
          stats.value.totalIndexedLinks = summary.links
          stats.value.validLinks = summary.health_valid
          stats.value.todayUpdated = summary.today_updated
          break
        }
        case 'trends': {
          const raw = data.data?.trends?.trends ?? data.data?.trends ?? data.trends
          if (!Array.isArray(raw) && raw !== null) throw new Error('搜索趋势数据格式不完整')
          trends.value = (raw ?? []).map((trend: string | { keyword?: string; name?: string }) => typeof trend === 'string' ? trend : trend.keyword || trend.name || '').filter(Boolean).slice(0, 10)
          break
        }
        case 'accounts': accounts.value = collection<Account>(data, 'accounts'); stats.value.driveAccounts = accounts.value.length; break
        case 'libraries': stats.value.embyLibraries = collection(data, 'libraries').length; break
        case 'tasks': tasks.value = collection<Task>(data, 'tasks'); stats.value.activeTasks = tasks.value.filter((task) => task.status === 'running').length; break
        case 'mounts': mounts.value = collection<Mount>(data, 'mounts'); stats.value.totalMounts = mounts.value.length; stats.value.mountedMounts = mounts.value.filter((mount) => mount.status === 'mounted').length; break
      }
      item.state = 'success'
      item.checkedAt = new Date().toLocaleString()
    } catch (error) {
      item.state = 'error'
      item.error = error instanceof Error ? error.message : '无法连接服务，请稍后重试。'
    }
  }))
  if (sources.value.every((item) => item.state === 'success')) checkedAt.value = new Date().toLocaleString()
  loading.value = false
}
onMounted(fetchHomeData)
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-4 pb-5 border-b border-border">
      <div><h1 class="font-serif text-3xl font-bold text-text">媒体系统运行台</h1><p class="text-sm text-text-muted mt-2">先处理服务异常与后台任务，再查看资源库存。</p><p class="text-xs text-text-faint mt-2">{{ checkedAt ? `全部数据上次读取成功：${checkedAt}` : '尚未完成全部数据读取' }} · 账号状态来自已保存记录</p></div>
      <button type="button" @click="fetchHomeData" :disabled="loading" class="flex min-h-11 items-center justify-center gap-2 px-4 rounded-lg border border-border bg-surface hover:border-accent text-sm disabled:opacity-50"><RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />{{ loading ? '刷新中' : '刷新数据' }}</button>
    </header>

    <section aria-labelledby="exceptions-title" class="rounded-xl border border-border bg-surface p-5">
      <h2 id="exceptions-title" class="font-serif text-xl font-semibold">需要关注</h2>
      <div aria-live="polite" class="mt-3 divide-y divide-border">
        <div v-for="item in readErrors" :key="item.key" class="py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-2"><div><h3 class="text-sm font-semibold text-danger">{{ item.name }}读取失败</h3><p class="text-sm text-text-muted mt-1">{{ item.error }}</p><p class="text-xs text-text-faint mt-1">{{ item.checkedAt ? `上次读取成功：${item.checkedAt}` : '尚无成功读取记录' }}</p></div><RouterLink :to="item.route" class="inline-flex min-h-11 items-center text-sm text-accent underline">前往处理 →</RouterLink></div>
        <div v-for="item in serviceExceptions" :key="item.key" class="py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-2"><div><h3 class="text-sm font-semibold text-annotation break-all">{{ item.name }}</h3><p class="text-sm text-text-muted mt-1">{{ item.detail }}</p></div><RouterLink :to="item.route" class="inline-flex min-h-11 items-center text-sm text-accent underline">检查设置 →</RouterLink></div>
        <p v-if="!readErrors.length && !serviceExceptions.length" class="pt-3 text-sm text-text-muted">{{ loading ? '正在读取服务状态…' : sources.every(item => item.state === 'success') ? '本次读取未发现账号或挂载异常；任务状态见下方。' : '服务状态尚未读取。' }}</p>
      </div>
    </section>

    <section aria-labelledby="recent-tasks-title" class="rounded-xl border border-border bg-surface p-5">
      <div class="flex items-center justify-between gap-3"><h2 id="recent-tasks-title" class="font-serif text-xl font-semibold">任务动态</h2><RouterLink to="/tasks" class="inline-flex min-h-11 items-center text-sm text-accent underline">全部任务 →</RouterLink></div>
      <p class="text-xs text-text-faint">运行中与失败任务优先 · {{ sourceLabel('tasks') }}</p>
      <div v-if="recentTasks.length" class="mt-3 divide-y divide-border">
        <RouterLink v-for="task in recentTasks" :key="task.id" to="/tasks" class="flex min-h-11 items-start justify-between gap-4 py-4 text-sm hover:text-accent"><div class="min-w-0"><h3 class="font-semibold break-words">{{ taskDetail(task).label }}</h3><p class="mt-1 break-words text-text-muted">{{ taskDetail(task).description }}</p><p v-if="task.error" class="text-danger mt-1 break-words">失败原因：{{ task.error }}</p></div><span class="shrink-0" :class="task.status === 'failed' ? 'text-danger' : task.status === 'running' ? 'text-annotation' : 'text-text-muted'">{{ taskStatus(task.status) }}</span></RouterLink>
      </div>
      <p v-else class="pt-4 text-sm text-text-muted">{{ source('tasks').state === 'success' ? '暂无后台任务记录。' : source('tasks').state === 'error' ? '任务读取失败，请前往任务页或重试。' : source('tasks').state === 'loading' ? '正在读取任务…' : '任务尚未读取。' }}</p>
    </section>

    <section aria-labelledby="inventory-title">
      <h2 id="inventory-title" class="font-serif text-xl font-semibold mb-3">资源库存</h2>
      <div class="grid sm:grid-cols-2 xl:grid-cols-3 gap-3">
        <article class="p-4 rounded-xl border border-border bg-surface"><h3 class="text-sm text-text-muted">全库资源索引</h3><p class="font-mono text-2xl font-bold mt-2">{{ count(stats.totalIndexedLinks) }}</p><p class="text-xs text-text-muted mt-2">有效 {{ count(stats.validLinks) }} · 今日更新 {{ count(stats.todayUpdated) }}</p><p class="text-xs text-text-faint mt-2">{{ sourceLabel('summary') }}</p></article>
        <article class="p-4 rounded-xl border border-border bg-surface"><h3 class="text-sm text-text-muted">115 纳管账号</h3><p class="font-mono text-2xl font-bold mt-2">{{ count(stats.driveAccounts) }}</p><p class="text-xs text-text-faint mt-2">{{ sourceLabel('accounts') }}</p><RouterLink to="/files" class="inline-flex min-h-11 items-center text-sm text-accent underline">管理文件 →</RouterLink></article>
        <article class="p-4 rounded-xl border border-border bg-surface"><h3 class="text-sm text-text-muted">CloudDrive 挂载就绪 / 总数</h3><p class="font-mono text-2xl font-bold mt-2">{{ count(stats.mountedMounts) }} / {{ count(stats.totalMounts) }}</p><p class="text-xs text-text-faint mt-2">{{ sourceLabel('mounts') }}</p></article>
        <article class="p-4 rounded-xl border border-border bg-surface"><h3 class="text-sm text-text-muted">Emby 媒体库</h3><p class="font-mono text-2xl font-bold mt-2">{{ count(stats.embyLibraries) }}</p><p class="text-xs text-text-faint mt-2">{{ sourceLabel('libraries') }}</p></article>
        <article class="p-4 rounded-xl border border-border bg-surface"><h3 class="text-sm text-text-muted">运行中任务</h3><p class="font-mono text-2xl font-bold mt-2">{{ count(stats.activeTasks) }}</p><p class="text-xs text-text-faint mt-2">{{ sourceLabel('tasks') }}</p></article>
      </div>
    </section>

    <section aria-labelledby="trends-title" class="rounded-xl border border-border bg-surface p-5">
      <h2 id="trends-title" class="font-serif text-xl font-semibold">搜索趋势</h2><p class="text-xs text-text-faint mt-2">{{ sourceLabel('trends') }}</p>
      <div v-if="trends.length" class="flex flex-wrap gap-2 mt-4"><RouterLink v-for="keyword in trends" :key="keyword" :to="`/resources?q=${encodeURIComponent(keyword)}`" class="inline-flex min-h-11 items-center gap-1 px-3 rounded-lg border border-border hover:border-accent text-sm">{{ keyword }}<ArrowUpRight class="w-4 h-4" /></RouterLink></div>
      <p v-else class="text-sm text-text-muted mt-4">{{ source('trends').state === 'success' ? '暂无搜索趋势。' : source('trends').state === 'error' ? '搜索趋势读取失败，请稍后重试。' : source('trends').state === 'loading' ? '正在读取搜索趋势…' : '搜索趋势尚未读取。' }}</p>
    </section>
  </div>
</template>

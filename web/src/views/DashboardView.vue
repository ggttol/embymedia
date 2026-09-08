<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import {
  ArrowUpRight,
  RefreshCw,
  ArrowRight,
  AlertCircle,
  HardDrive,
  Film,
  Layers,
  CheckCircle2,
  Activity,
  ChevronRight,
  Sparkles,
} from 'lucide-vue-next'

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

const stats = ref({
  totalIndexedLinks: null as number | null,
  validLinks: null as number | null,
  todayUpdated: null as number | null,
  driveAccounts: null as number | null,
  embyLibraries: null as number | null,
  activeTasks: null as number | null,
  mountedMounts: null as number | null,
  totalMounts: null as number | null,
})

const trends = ref<string[]>([])
const tasks = ref<Task[]>([])
const accounts = ref<Account[]>([])
const mounts = ref<Mount[]>([])
const loading = ref(false)
const checkedAt = ref('')

const editionDate = computed(() => {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
      weekday: 'long',
    }).format(new Date())
  } catch {
    return new Date().toLocaleDateString()
  }
})

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

const taskBadgeClass = (status: string) => {
  switch (status) {
    case 'running':
      return 'bg-accent-soft text-accent border-accent/20'
    case 'failed':
      return 'bg-danger/10 text-danger border-danger/20'
    case 'completed':
      return 'bg-ok/10 text-ok border-ok/20'
    default:
      return 'bg-bg-muted text-text-secondary border-border/60'
  }
}

const taskDefinition = (type: string) => ({
  series_auto_fill: ['自动补集', '检查追更库已播缺集，精确转存匹配集并在 Emby 扫描后复查。'],
  emby_refresh: ['同步媒体并刷新 Emby', '先生成和校验 STRM，再跟踪 Emby 全库扫描到完成。'],
  emby_missing_posters: ['检查并修复海报', '刷新缺少主海报的电影和剧集，并复查实际修复结果。'],
  emby_metadata_repair: ['检查并修复元数据', '为缺少 TMDB 身份的条目寻找候选，仅应用安全唯一匹配。'],
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
  <div class="space-y-8 pb-10">
    <!-- Editorial Masthead Header -->
    <header class="border-b border-border/80 pb-6">
      <!-- Publication Eyebrow Bar -->
      <div class="flex flex-wrap items-center justify-between gap-3 text-xs tracking-wider text-text-faint font-mono border-b border-border/50 pb-3 mb-5">
        <div class="flex items-center gap-2.5">
          <span class="font-serif italic font-normal text-text-secondary text-sm">EmbyMedia Dispatch</span>
          <span class="w-1 h-1 rounded-full bg-accent/60"></span>
          <span>运维晨间简报 · {{ editionDate }}</span>
        </div>
        <div class="flex items-center gap-2">
          <span>{{ checkedAt ? '已同步最新状态' : '就绪' }}</span>
          <span class="w-1.5 h-1.5 rounded-full" :class="loading ? 'bg-warn animate-pulse' : 'bg-ok'"></span>
        </div>
      </div>

      <!-- Main Headline & Actions -->
      <div class="flex flex-col md:flex-row md:items-end justify-between gap-6">
        <div>
          <h1 class="font-serif text-3xl sm:text-4xl lg:text-[40px] font-normal tracking-tight text-text leading-tight">
            媒体系统运行台
          </h1>
          <p class="text-sm sm:text-base text-text-secondary mt-2.5 max-w-2xl font-sans leading-relaxed">
            统一汇聚云端网盘、本地挂载、媒体库及后台流水线。优先处理异常关注项，确保流媒体链路全天候稳定高效。
          </p>
          <div class="flex flex-wrap items-center gap-3 text-xs text-text-faint mt-3 font-mono">
            <span>快照周期：实时按需探测</span>
            <span class="text-border-strong">/</span>
            <span>{{ checkedAt ? `全量校验就绪：${checkedAt}` : '尚未完成全部数据读取' }}</span>
          </div>
        </div>

        <button
          type="button"
          @click="fetchHomeData"
          :disabled="loading"
          class="group relative inline-flex items-center justify-center gap-2.5 px-4 py-2.5 rounded-xl border border-border bg-surface hover:bg-bg-elevated hover:border-border-strong text-sm font-medium text-text shadow-xs hover:shadow-card active:scale-[0.98] transition-all duration-200 disabled:opacity-50 cursor-pointer self-start md:self-end shrink-0"
        >
          <RefreshCw
            class="w-4 h-4 text-text-secondary group-hover:text-accent transition-transform duration-700 ease-claude"
            :class="{ 'animate-spin': loading, 'group-hover:rotate-180': !loading }"
          />
          <span>{{ loading ? '数据同步中…' : '刷新数据' }}</span>
        </button>
      </div>
    </header>

    <!-- KPI Metric Cards Grid -->
    <section aria-labelledby="kpi-overview-title" class="space-y-3">
      <div class="flex items-center justify-between">
        <h2 id="kpi-overview-title" class="font-serif text-lg font-normal text-text-secondary tracking-normal">
          核心指标总览
        </h2>
        <span class="text-xs font-mono text-text-faint">TABULAR METRICS</span>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
        <!-- Metric 1: Total Indexed Links -->
        <article class="bg-surface border border-border rounded-2xl p-5 shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-300 relative group flex flex-col justify-between">
          <div>
            <div class="flex items-center justify-between gap-2 mb-3">
              <span class="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium tracking-wide bg-bg-muted text-text-secondary border border-border/40">
                资源索引
              </span>
              <Layers class="w-4 h-4 text-text-faint group-hover:text-accent transition-colors duration-200" />
            </div>
            <p class="font-serif tabular-nums text-3xl sm:text-4xl font-normal tracking-tight text-text leading-tight my-1">
              {{ count(stats.totalIndexedLinks) }}
            </p>
            <p class="text-xs text-text-secondary font-sans mt-2">
              有效 {{ count(stats.validLinks) }} · 今日更新 {{ count(stats.todayUpdated) }}
            </p>
          </div>
          <div class="pt-3 mt-4 border-t border-border/40 text-[11px] text-text-faint font-mono truncate">
            {{ sourceLabel('summary') }}
          </div>
        </article>

        <!-- Metric 2: Managed Accounts -->
        <article class="bg-surface border border-border rounded-2xl p-5 shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-300 relative group flex flex-col justify-between">
          <div>
            <div class="flex items-center justify-between gap-2 mb-3">
              <span class="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium tracking-wide bg-bg-muted text-text-secondary border border-border/40">
                115 网盘
              </span>
              <HardDrive class="w-4 h-4 text-text-faint group-hover:text-accent transition-colors duration-200" />
            </div>
            <p class="font-serif tabular-nums text-3xl sm:text-4xl font-normal tracking-tight text-text leading-tight my-1">
              {{ count(stats.driveAccounts) }}
            </p>
            <p class="text-xs text-text-secondary font-sans mt-2">
              已纳管云存储凭据
            </p>
          </div>
          <div class="pt-3 mt-4 border-t border-border/40 text-[11px] text-text-faint flex items-center justify-between">
            <span class="font-mono truncate">{{ sourceLabel('accounts') }}</span>
            <RouterLink to="/files" class="inline-flex items-center gap-1 text-xs font-medium text-accent hover:text-accent-strong group/link shrink-0 ml-1">
              <span>文件库</span>
              <ArrowRight class="w-3 h-3 transition-transform duration-200 group-hover/link:translate-x-0.5" />
            </RouterLink>
          </div>
        </article>

        <!-- Metric 3: CloudDrive Mounts -->
        <article class="bg-surface border border-border rounded-2xl p-5 shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-300 relative group flex flex-col justify-between">
          <div>
            <div class="flex items-center justify-between gap-2 mb-3">
              <span class="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium tracking-wide bg-bg-muted text-text-secondary border border-border/40">
                CloudDrive
              </span>
              <CheckCircle2 class="w-4 h-4 text-text-faint group-hover:text-accent transition-colors duration-200" />
            </div>
            <p class="font-serif tabular-nums text-3xl sm:text-4xl font-normal tracking-tight text-text leading-tight my-1">
              {{ count(stats.mountedMounts) }}
              <span class="text-lg sm:text-xl font-normal text-text-faint">/ {{ count(stats.totalMounts) }}</span>
            </p>
            <p class="text-xs text-text-secondary font-sans mt-2">
              本地就绪挂载点
            </p>
          </div>
          <div class="pt-3 mt-4 border-t border-border/40 text-[11px] text-text-faint font-mono truncate">
            {{ sourceLabel('mounts') }}
          </div>
        </article>

        <!-- Metric 4: Emby Libraries -->
        <article class="bg-surface border border-border rounded-2xl p-5 shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-300 relative group flex flex-col justify-between">
          <div>
            <div class="flex items-center justify-between gap-2 mb-3">
              <span class="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium tracking-wide bg-bg-muted text-text-secondary border border-border/40">
                Emby 服务
              </span>
              <Film class="w-4 h-4 text-text-faint group-hover:text-accent transition-colors duration-200" />
            </div>
            <p class="font-serif tabular-nums text-3xl sm:text-4xl font-normal tracking-tight text-text leading-tight my-1">
              {{ count(stats.embyLibraries) }}
            </p>
            <p class="text-xs text-text-secondary font-sans mt-2">
              媒体库分类目录
            </p>
          </div>
          <div class="pt-3 mt-4 border-t border-border/40 text-[11px] text-text-faint font-mono truncate">
            {{ sourceLabel('libraries') }}
          </div>
        </article>

        <!-- Metric 5: Active Tasks -->
        <article class="bg-surface border border-border rounded-2xl p-5 shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-300 relative group flex flex-col justify-between">
          <div>
            <div class="flex items-center justify-between gap-2 mb-3">
              <span class="inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium tracking-wide bg-bg-muted text-text-secondary border border-border/40">
                活跃任务
              </span>
              <Activity class="w-4 h-4 text-text-faint group-hover:text-accent transition-colors duration-200" />
            </div>
            <p class="font-serif tabular-nums text-3xl sm:text-4xl font-normal tracking-tight text-text leading-tight my-1" :class="stats.activeTasks ? 'text-accent' : ''">
              {{ count(stats.activeTasks) }}
            </p>
            <p class="text-xs text-text-secondary font-sans mt-2">
              队列流水执行中
            </p>
          </div>
          <div class="pt-3 mt-4 border-t border-border/40 text-[11px] text-text-faint font-mono truncate">
            {{ sourceLabel('tasks') }}
          </div>
        </article>
      </div>
    </section>

    <!-- Main Editorial Two-Column Section: Exceptions & Tasks + Trends & System Probes -->
    <div class="grid grid-cols-1 lg:grid-cols-12 gap-8">
      <!-- Left Editorial Column: Exceptions & Background Tasks (col-span-7) -->
      <div class="lg:col-span-7 space-y-8">
        <!-- Exceptions Section -->
        <section aria-labelledby="exceptions-title" class="rounded-2xl border border-border bg-surface p-6 shadow-xs">
          <div class="flex items-center justify-between pb-4 border-b border-border/70">
            <div>
              <h2 id="exceptions-title" class="font-serif text-xl font-normal text-text">
                需要关注
              </h2>
              <p class="text-xs text-text-secondary mt-1">
                账号状态凭据失效或脱机挂载需优先处理
              </p>
            </div>
            <span
              v-if="readErrors.length || serviceExceptions.length"
              class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-warn/10 text-warn border border-warn/20"
            >
              <AlertCircle class="w-3.5 h-3.5" />
              {{ readErrors.length + serviceExceptions.length }} 项待处理
            </span>
          </div>

          <div aria-live="polite" class="mt-2 divide-y divide-border/50">
            <!-- Read Errors -->
            <div
              v-for="item in readErrors"
              :key="item.key"
              class="py-4 px-3 -mx-3 rounded-xl hover:bg-bg-muted/40 transition-all duration-200 flex flex-col sm:flex-row sm:items-center justify-between gap-3 group"
            >
              <div class="flex items-start gap-3">
                <span class="w-2 h-2 rounded-full bg-danger/80 mt-1.5 shrink-0"></span>
                <div>
                  <h3 class="text-sm font-medium text-text group-hover:text-danger transition-colors">
                    {{ item.name }} 读取异常
                  </h3>
                  <p class="text-xs text-text-secondary mt-1 font-sans leading-relaxed">
                    {{ item.error }}
                  </p>
                  <p class="text-[11px] text-text-faint mt-1 font-mono">
                    {{ item.checkedAt ? `上次成功时间：${item.checkedAt}` : '暂无成功历史记录' }}
                  </p>
                </div>
              </div>

              <RouterLink
                :to="item.route"
                class="group/link inline-flex items-center gap-1.5 text-xs font-medium text-accent hover:text-accent-strong py-1.5 px-3 rounded-lg hover:bg-accent-soft/50 transition-all self-start sm:self-center shrink-0"
              >
                <span>前往处理</span>
                <ArrowRight class="w-3.5 h-3.5 transition-transform duration-200 ease-claude group-hover/link:translate-x-1" />
              </RouterLink>
            </div>

            <!-- Service Status Exceptions -->
            <div
              v-for="item in serviceExceptions"
              :key="item.key"
              class="py-4 px-3 -mx-3 rounded-xl hover:bg-bg-muted/40 transition-all duration-200 flex flex-col sm:flex-row sm:items-center justify-between gap-3 group"
            >
              <div class="flex items-start gap-3">
                <span class="w-2 h-2 rounded-full bg-warn/80 mt-1.5 shrink-0"></span>
                <div>
                  <h3 class="text-sm font-medium text-text group-hover:text-accent transition-colors break-all">
                    {{ item.name }}
                  </h3>
                  <p class="text-xs text-text-secondary mt-1 font-sans leading-relaxed">
                    {{ item.detail }}
                  </p>
                </div>
              </div>

              <RouterLink
                :to="item.route"
                class="group/link inline-flex items-center gap-1.5 text-xs font-medium text-accent hover:text-accent-strong py-1.5 px-3 rounded-lg hover:bg-accent-soft/50 transition-all self-start sm:self-center shrink-0"
              >
                <span>检查配置</span>
                <ArrowRight class="w-3.5 h-3.5 transition-transform duration-200 ease-claude group-hover/link:translate-x-1" />
              </RouterLink>
            </div>

            <!-- Empty State -->
            <div
              v-if="!readErrors.length && !serviceExceptions.length"
              class="py-6 flex items-center gap-3 text-sm text-text-secondary"
            >
              <div class="w-6 h-6 rounded-full bg-ok/10 flex items-center justify-center shrink-0">
                <CheckCircle2 class="w-3.5 h-3.5 text-ok" />
              </div>
              <p class="text-xs sm:text-sm font-sans">
                {{ loading ? '正在探测各服务状态…' : sources.every(item => item.state === 'success') ? '系统状态优良，本次巡检未发现账号过期或挂载脱机异常。' : '服务状态监测就绪。' }}
              </p>
            </div>
          </div>
        </section>

        <!-- Background Tasks Section -->
        <section aria-labelledby="recent-tasks-title" class="rounded-2xl border border-border bg-surface p-6 shadow-xs">
          <div class="flex items-center justify-between pb-4 border-b border-border/70">
            <div>
              <h2 id="recent-tasks-title" class="font-serif text-xl font-normal text-text">
                任务动态
              </h2>
              <p class="text-xs text-text-secondary mt-1">
                近期后台运维流水 · {{ sourceLabel('tasks') }}
              </p>
            </div>
            <RouterLink
              to="/tasks"
              class="group/all inline-flex items-center gap-1.5 text-xs font-medium text-accent hover:text-accent-strong py-1.5 px-3 rounded-lg hover:bg-accent-soft/50 transition-all"
            >
              <span>查看全部任务</span>
              <ArrowRight class="w-3.5 h-3.5 transition-transform duration-200 ease-claude group-hover/all:translate-x-1" />
            </RouterLink>
          </div>

          <div v-if="recentTasks.length" class="mt-2 divide-y divide-border/40">
            <RouterLink
              v-for="task in recentTasks"
              :key="task.id"
              to="/tasks"
              class="group flex items-start justify-between gap-4 py-4 px-3 -mx-3 rounded-xl hover:bg-bg-muted/50 transition-all duration-200"
            >
              <div class="min-w-0 pr-2">
                <div class="flex items-center gap-2">
                  <h3 class="text-sm font-medium text-text group-hover:text-accent transition-colors truncate">
                    {{ taskDetail(task).label }}
                  </h3>
                </div>
                <p class="mt-1 text-xs text-text-secondary line-clamp-1 font-sans">
                  {{ taskDetail(task).description }}
                </p>
                <p v-if="task.error" class="text-xs text-danger mt-1 font-mono">
                  失败详情：{{ task.error }}
                </p>
              </div>

              <div class="flex items-center gap-3 shrink-0">
                <span
                  class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium tracking-wide border"
                  :class="taskBadgeClass(task.status)"
                >
                  {{ taskStatus(task.status) }}
                </span>
                <ChevronRight class="w-4 h-4 text-text-faint group-hover:text-text-secondary transition-transform duration-200 ease-claude group-hover:translate-x-1" />
              </div>
            </RouterLink>
          </div>

          <div v-else class="py-6 text-sm text-text-secondary text-center">
            {{ source('tasks').state === 'success' ? '暂无近期任务记录。' : source('tasks').state === 'error' ? '任务队列读取失败，请检查服务连接。' : source('tasks').state === 'loading' ? '正在拉取任务队列…' : '任务尚未读取。' }}
          </div>
        </section>
      </div>

      <!-- Right Editorial Column: Search Trends & Source Probe Cards (col-span-5) -->
      <div class="lg:col-span-5 space-y-8">
        <!-- Search Trends Section -->
        <section aria-labelledby="trends-title" class="rounded-2xl border border-border bg-surface p-6 shadow-xs">
          <div class="flex items-center justify-between pb-4 border-b border-border/70">
            <div>
              <h2 id="trends-title" class="font-serif text-xl font-normal text-text flex items-center gap-2">
                <span>热搜趋势</span>
                <Sparkles class="w-4 h-4 text-accent" />
              </h2>
              <p class="text-xs text-text-secondary mt-1">
                近期热门影视资源与检索关键词
              </p>
            </div>
            <span class="text-[11px] font-mono text-text-faint">
              {{ sourceLabel('trends') }}
            </span>
          </div>

          <div v-if="trends.length" class="flex flex-wrap gap-2 mt-4">
            <RouterLink
              v-for="keyword in trends"
              :key="keyword"
              :to="`/resources?q=${encodeURIComponent(keyword)}`"
              class="group inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border/80 bg-surface hover:bg-bg-elevated hover:border-accent/40 text-xs font-medium text-text-secondary hover:text-accent transition-all duration-200 shadow-xs hover:shadow-sm"
            >
              <span>{{ keyword }}</span>
              <ArrowUpRight class="w-3.5 h-3.5 text-text-faint group-hover:text-accent transition-transform duration-200 group-hover:-translate-y-0.5 group-hover:translate-x-0.5" />
            </RouterLink>
          </div>

          <div v-else class="py-6 text-sm text-text-secondary text-center">
            {{ source('trends').state === 'success' ? '暂无搜索趋势推荐。' : source('trends').state === 'error' ? '搜索趋势拉取失败，请稍后重试。' : source('trends').state === 'loading' ? '正在解析搜索趋势…' : '搜索趋势尚未读取。' }}
          </div>
        </section>

        <!-- System Source Probes Bulletin Section -->
        <section aria-labelledby="probes-title" class="rounded-2xl border border-border bg-surface p-6 shadow-xs">
          <div class="pb-3 border-b border-border/70">
            <h2 id="probes-title" class="font-serif text-lg font-normal text-text">
              数据源巡检记录
            </h2>
            <p class="text-xs text-text-secondary mt-0.5">
              底层 REST API 与微服务链路连通性
            </p>
          </div>

          <div class="mt-3 divide-y divide-border/40 text-xs font-mono">
            <div
              v-for="src in sources"
              :key="src.key"
              class="py-2.5 flex items-center justify-between gap-3"
            >
              <div class="flex items-center gap-2">
                <span
                  class="w-1.5 h-1.5 rounded-full"
                  :class="src.state === 'success' ? 'bg-ok' : src.state === 'loading' ? 'bg-warn animate-pulse' : src.state === 'error' ? 'bg-danger' : 'bg-text-faint'"
                ></span>
                <span class="font-sans font-medium text-text">{{ src.name }}</span>
              </div>
              <span class="text-text-faint text-[11px] truncate max-w-[180px]">
                {{ src.checkedAt ? src.checkedAt.replace(/^\d{4}\//, '') : '待探测' }}
              </span>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { HardDrive, Tv, ListTodo, TrendingUp, ArrowUpRight, Sparkles, RefreshCw, FolderSync, ArrowRight } from 'lucide-vue-next'

const stats = ref({
  totalIndexedLinks: null as number | null,
  validLinks: null as number | null,
  driveAccounts: null as number | null,
  embyLibraries: null as number | null,
  activeTasks: null as number | null,
})
const trends = ref<string[]>([])
const recentTasks = ref<any[]>([])
const loading = ref(false)

async function fetchHomeData() {
  loading.value = true
  try {
    const [summaryResult, trendsResult, accountsResult, librariesResult, tasksResult] = await Promise.allSettled([
      fetch('/api/v1/home/summary'),
      fetch('/api/v1/trends'),
      fetch('/api/v1/accounts'),
      fetch('/api/v1/emby/libraries'),
      fetch('/api/v1/tasks'),
    ])
    if (summaryResult.status === 'fulfilled' && summaryResult.value.ok) {
      const data = await summaryResult.value.json()
      const summary = data.summary?.summary ?? data.summary ?? {}
      stats.value.totalIndexedLinks = summary.links ?? null
      stats.value.validLinks = summary.health_valid ?? null
      stats.value.todayUpdated = summary.today_updated ?? null
    }
    if (trendsResult.status === 'fulfilled' && trendsResult.value.ok) {
      const data = await trendsResult.value.json()
      let rawList = data.data?.trends?.trends ?? data.data?.trends ?? data.trends ?? []
      if (rawList && typeof rawList === 'object' && !Array.isArray(rawList)) {
        rawList = rawList.trends ?? Object.values(rawList)
      }
      trends.value = (Array.isArray(rawList) ? rawList : [])
        .map((trend: any) => (typeof trend === 'string' ? trend : (trend.keyword || trend.name || '')))
        .filter(Boolean)
        .slice(0, 10)
    }
    if (accountsResult.status === 'fulfilled' && accountsResult.value.ok) {
      const data = await accountsResult.value.json()
      stats.value.driveAccounts = Array.isArray(data.accounts) ? data.accounts.length : 0
    }
    if (librariesResult.status === 'fulfilled' && librariesResult.value.ok) {
      const data = await librariesResult.value.json()
      stats.value.embyLibraries = Array.isArray(data.libraries) ? data.libraries.length : 0
    }
    if (tasksResult.status === 'fulfilled' && tasksResult.value.ok) {
      const data = await tasksResult.value.json()
      const tasks = Array.isArray(data.tasks) ? data.tasks : []
      stats.value.activeTasks = tasks.filter((task: any) => task.status === 'running').length
      recentTasks.value = tasks.slice(0, 4)
    }
  } catch (error) {
    console.error(error)
  } finally {
    loading.value = false
  }
}

onMounted(fetchHomeData)
</script>

<template>
  <div class="space-y-8">
    <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-2xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">SYSTEM DESK / LIVE DATA</p>
        <h1 class="font-serif text-3xl font-bold text-text">媒体系统运行台</h1>
        <p class="text-sm text-text-muted mt-2">汇总资源索引、115 账号、Emby 媒体库与后台任务的当前状态。</p>
      </div>
      <button @click="fetchHomeData" :disabled="loading" class="flex min-h-11 items-center justify-center gap-2 px-4 py-2 rounded-lg border border-border bg-surface hover:border-accent text-xs font-mono text-text transition-colors">
        <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
        <span>刷新实时数据</span>
      </button>
    </div>

    <section aria-label="核心管理指标" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
      <article class="p-6 rounded-2xl border border-border bg-gradient-to-br from-surface to-bg shadow-sm hover:border-accent/40 transition-colors">
        <div class="flex items-start justify-between">
          <div>
            <div class="flex items-center gap-2 mb-2">
              <span class="w-1.5 h-6 bg-accent rounded-full"></span>
              <span class="text-sm font-mono text-text-muted">全库资源索引</span>
            </div>
            <div class="text-4xl font-bold font-mono tracking-tight text-text mt-4">{{ stats.totalIndexedLinks === null ? '—' : stats.totalIndexedLinks.toLocaleString() }}</div>
          </div>
          <TrendingUp class="w-10 h-10 text-accent/20" />
        </div>
        <div class="flex items-center justify-between mt-6 pt-4 border-t border-border/50 text-xs font-mono">
          <span class="text-text-muted">有效数：{{ stats.validLinks === null ? '—' : stats.validLinks.toLocaleString() }}</span>
          <span v-if="stats.todayUpdated !== null" class="px-2 py-0.5 rounded-full bg-accent-soft text-accent">今日 +{{ stats.todayUpdated.toLocaleString() }}</span>
        </div>
      </article>

      <article class="p-6 rounded-2xl border border-border bg-gradient-to-br from-surface to-bg shadow-sm hover:border-accent/40 transition-colors">
        <div class="flex items-start justify-between">
          <div>
            <div class="flex items-center gap-2 mb-2">
              <span class="w-1.5 h-6 bg-accent rounded-full"></span>
              <span class="text-sm font-mono text-text-muted">115 云端纳管</span>
            </div>
            <div class="text-4xl font-bold font-mono tracking-tight text-text mt-4">{{ stats.driveAccounts === null ? '—' : stats.driveAccounts }}<span class="text-lg text-text-faint ml-2 font-serif font-normal">个账号</span></div>
          </div>
          <HardDrive class="w-10 h-10 text-accent/20" />
        </div>
        <div class="flex items-center justify-between mt-6 pt-4 border-t border-border/50 text-xs font-mono">
          <span class="text-text-muted">实时挂载 / 存储在线</span>
          <RouterLink to="/files" class="text-accent hover:underline flex items-center gap-1">管理文件 <ArrowRight class="w-3 h-3" /></RouterLink>
        </div>
      </article>
    </section>

    <section aria-label="应用与流水" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
      <article class="p-6 rounded-2xl border border-border bg-surface shadow-sm hover:border-accent/40 transition-colors flex items-center gap-6">
        <div class="w-12 h-12 rounded-xl bg-accent-soft flex items-center justify-center shrink-0">
          <Tv class="w-6 h-6 text-accent" />
        </div>
        <div class="min-w-0 flex-1">
           <div class="text-sm font-mono text-text-muted mb-1">Emby 媒体库</div>
           <div class="flex items-baseline gap-2">
             <span class="text-2xl font-bold font-mono tracking-tight text-text">{{ stats.embyLibraries === null ? '—' : stats.embyLibraries }}</span>
             <span class="text-xs text-text-faint">个挂载源已接驳</span>
           </div>
        </div>
      </article>

      <article class="p-6 rounded-2xl border border-border bg-surface shadow-sm hover:border-accent/40 transition-colors flex items-center gap-6">
        <div class="w-12 h-12 rounded-xl bg-orange-100 dark:bg-orange-900/30 flex items-center justify-center shrink-0">
          <ListTodo class="w-6 h-6 text-orange-600 dark:text-orange-400" />
        </div>
        <div class="min-w-0 flex-1">
           <div class="text-sm font-mono text-text-muted mb-1">活跃后台任务</div>
           <div class="flex items-baseline gap-2">
             <span class="text-2xl font-bold font-mono tracking-tight text-text">{{ stats.activeTasks === null ? '—' : stats.activeTasks }}</span>
             <span class="text-xs text-text-faint">个自动机运行中</span>
           </div>
        </div>
      </article>
    </section>

    <div class="grid grid-cols-1 xl:grid-cols-[minmax(280px,360px)_minmax(0,1fr)] gap-5">
      <section class="p-6 rounded-2xl border border-border bg-surface shadow-sm hover:border-accent/30 transition-colors">
        <div class="flex items-center gap-2 pb-4 mb-4 border-b border-border">
          <Sparkles class="w-4 h-4 text-annotation" />
          <h2 class="font-serif font-semibold text-lg text-text">实时搜索趋势</h2>
        </div>
        <div v-if="trends.length > 0" class="flex flex-wrap gap-2">
          <RouterLink v-for="keyword in trends" :key="keyword" :to="`/resources?q=${encodeURIComponent(keyword)}`" class="px-3 py-2 rounded-lg border border-border hover:border-accent bg-bg hover:bg-accent-soft text-xs font-mono text-text hover:text-accent transition-colors flex items-center gap-1 group">
            <span>{{ keyword }}</span><ArrowUpRight class="w-3 h-3 text-text-faint group-hover:text-accent" />
          </RouterLink>
        </div>
        <p v-else class="text-sm text-text-faint">上游暂未返回趋势数据。</p>
      </section>

      <section class="p-6 rounded-xl border border-border bg-surface shadow-sm">
        <div class="flex items-center justify-between pb-4 mb-1 border-b border-border">
          <div class="flex items-center gap-2"><FolderSync class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-lg text-text">最近任务</h2></div>
          <RouterLink to="/tasks" class="text-xs font-mono text-accent hover:underline">全部任务 →</RouterLink>
        </div>
        <div v-if="recentTasks.length > 0" class="divide-y divide-border">
          <div v-for="task in recentTasks" :key="task.id" class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 py-4">
            <div class="flex min-w-0 items-start gap-3">
              <span class="w-2 h-2 mt-1.5 rounded-full shrink-0" :class="task.status === 'running' ? 'bg-warn animate-pulse' : task.status === 'failed' ? 'bg-danger' : 'bg-ok'"></span>
              <div class="min-w-0"><div class="text-sm font-semibold text-text break-words">{{ task.name }}</div><div class="text-xs font-mono text-text-faint mt-1">{{ task.type }}</div></div>
            </div>
            <span class="text-xs font-mono text-text-muted sm:text-right">{{ task.status }}</span>
          </div>
        </div>
        <p v-else class="py-5 text-sm text-text-faint">暂无后台任务记录。</p>
      </section>
    </div>
  </div>
</template>

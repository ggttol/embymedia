<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import {
  ListTodo,
  Play,
  RotateCcw,
  CheckCircle2,
  AlertCircle,
  Loader2,
  Layers,
  Activity
} from 'lucide-vue-next'

const tasks = ref<any[]>([])
const asyncTasks = ref<any[]>([])
const loading = ref(false)
const executingId = ref<string | null>(null)
let pollTimer: number | undefined

async function fetchTasks() {
  loading.value = true
  try {
    const [cronRes, asyncRes] = await Promise.all([
      fetch('/api/v1/tasks'),
      fetch('/api/v1/async-tasks')
    ])
    if (cronRes.ok) {
      const data = await cronRes.json()
      tasks.value = data.tasks ?? []
    }
    if (asyncRes.ok) {
      const data = await asyncRes.json()
      asyncTasks.value = data.tasks ?? []
    }
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

async function runTask(id: string) {
  executingId.value = id
  try {
    const res = await fetch(`/api/v1/tasks/${id}/run`, { method: 'POST' })
    if (res.ok) {
      await fetchTasks()
    }
  } catch (e) {
    console.error(e)
  } finally {
    executingId.value = null
  }
}

function asyncTone(status: string) {
  if (status === 'completed') return 'bg-ok'
  if (status === 'running') return 'bg-warn animate-pulse'
  if (status === 'failed') return 'bg-danger'
  return 'bg-text-faint'
}

onMounted(() => {
  fetchTasks()
  pollTimer = window.setInterval(fetchTasks, 5000)
})

onUnmounted(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-center justify-between pb-6 border-b border-border">
      <div>
        <h1 class="font-serif text-2xl font-bold text-text">后台任务与定时引擎</h1>
        <p class="text-sm text-text-muted mt-1 font-mono">
          Cron 定时作业调度 · 异步队列并发执行 · 任务审计回溯
        </p>
      </div>

      <button
        @click="fetchTasks"
        :disabled="loading"
        class="flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border bg-surface hover:bg-bg-muted text-xs font-mono text-text transition-colors"
      >
        <RotateCcw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
        <span>刷新列表</span>
      </button>
    </div>


    <!-- Async Task Ledger -->
    <div class="space-y-3">
      <div class="flex items-center justify-between px-1">
        <div class="flex items-center gap-2">
          <Activity class="w-4 h-4 text-annotation" />
          <h2 class="font-serif font-semibold text-lg text-text">异步任务流水</h2>
          <span class="text-xs font-mono text-text-faint">5s 自动刷新</span>
        </div>
      </div>

      <div v-if="asyncTasks.length === 0" class="p-8 rounded-xl border border-dashed border-border bg-surface text-center text-text-faint text-xs font-mono">
        暂无异步任务记录。离线下载、批量操作等后台任务会在此显示进度。
      </div>

      <div
        v-for="task in asyncTasks"
        :key="task.id"
        class="p-4 rounded-xl border border-border bg-surface shadow-sm space-y-3"
      >
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div class="flex items-center gap-2.5 min-w-0">
            <span class="w-2.5 h-2.5 rounded-full shrink-0" :class="asyncTone(task.status)"></span>
            <span class="text-xs font-mono text-text-muted">{{ task.type }}</span>
            <span class="font-mono text-[10px] text-text-faint truncate max-w-48">{{ task.id }}</span>
          </div>
          <div class="flex items-center gap-3 text-xs font-mono">
            <span class="text-text-faint">{{ new Date(task.created_at).toLocaleString() }}</span>
            <span
              class="px-2 py-0.5 rounded font-semibold"
              :class="{
                'bg-ok/10 text-ok': task.status === 'completed',
                'bg-warn/10 text-warn': task.status === 'running',
                'bg-danger/10 text-danger': task.status === 'failed',
                'bg-bg-muted text-text-faint': task.status === 'pending'
              }"
            >
              {{ task.status }}
            </span>
          </div>
        </div>

        <div v-if="task.status === 'running' || task.progress > 0" class="flex items-center gap-3">
          <div class="flex-1 h-1.5 rounded-full bg-bg-muted overflow-hidden">
            <div
              class="h-full rounded-full transition-all"
              :class="task.status === 'failed' ? 'bg-danger' : 'bg-accent'"
              :style="{ width: Math.min(100, task.progress || 0) + '%' }"
            ></div>
          </div>
          <span class="text-xs font-mono text-text-muted w-12 text-right">{{ Math.round(task.progress || 0) }}%</span>
        </div>

        <div v-if="task.error" class="text-xs font-mono text-danger pt-1 border-t border-border/60">
          {{ task.error }}
        </div>
      </div>
    </div>

    <!-- Cron Scheduled Tasks -->
    <div class="space-y-3">
      <div class="flex items-center gap-2 px-1">
        <ListTodo class="w-4 h-4 text-accent" />
        <h2 class="font-serif font-semibold text-lg text-text">Cron 定时作业</h2>
      </div>
      <div
        v-for="task in tasks"
        :key="task.id"
        class="p-5 rounded-xl border border-border bg-surface shadow-sm hover:border-border-strong transition-all flex flex-col md:flex-row md:items-center justify-between gap-4"
      >
        <div class="space-y-2 flex-1">
          <div class="flex items-center gap-2.5 flex-wrap">
            <span
              class="w-2.5 h-2.5 rounded-full"
              :class="{
                'bg-ok': task.status === 'idle' || task.status === 'completed',
                'bg-warn animate-pulse': task.status === 'running',
                'bg-danger': task.status === 'failed',
                'bg-text-faint': task.status === 'paused'
              }"
            ></span>
            <span class="font-semibold text-sm text-text">{{ task.name }}</span>
            <span class="px-2 py-0.5 rounded bg-bg-muted font-mono text-xs text-text-muted">
              类型: {{ task.type }}
            </span>
            <span v-if="task.cron_expr" class="px-2 py-0.5 rounded bg-accent-soft text-accent font-mono text-xs">
              Cron: {{ task.cron_expr }}
            </span>
          </div>

          <div class="text-xs font-mono text-text-faint flex items-center gap-4 flex-wrap">
            <span v-if="task.last_run_at">上次执行: {{ new Date(task.last_run_at).toLocaleString() }}</span>
            <span v-if="task.next_run_at">预计下次: {{ new Date(task.next_run_at).toLocaleString() }}</span>
            <span v-if="task.error" class="text-danger">异常: {{ task.error }}</span>
          </div>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <button
            @click="runTask(task.id)"
            :disabled="executingId === task.id"
            class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg border border-border hover:border-accent bg-bg text-xs font-mono text-text hover:text-accent transition-colors"
          >
            <Loader2 v-if="executingId === task.id" class="w-3.5 h-3.5 animate-spin text-accent" />
            <Play v-else class="w-3.5 h-3.5" />
            <span>立即触发</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

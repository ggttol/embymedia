<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  ListTodo,
  Play,
  Pause,
  RotateCcw,
  Clock,
  CheckCircle2,
  AlertCircle,
  Loader2,
  Calendar,
  Layers
} from 'lucide-vue-next'

const tasks = ref<any[]>([])
const loading = ref(false)
const executingId = ref<string | null>(null)

async function fetchTasks() {
  loading.value = true
  try {
    const res = await fetch('/api/v1/tasks')
    if (res.ok) {
      const data = await res.json()
      tasks.value = data.tasks ?? []
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

onMounted(() => {
  fetchTasks()
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

    <!-- Tasks Grid -->
    <div class="space-y-3">
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

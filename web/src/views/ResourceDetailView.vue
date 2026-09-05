<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowLeft,
  Bookmark,
  Copy,
  Check,
  Download,
  ShieldCheck,
  Clock,
  Radio,
  Loader2,
  FolderInput
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import { getDiskLabel, getDiskColor, getHealthLabel } from '@/utils/resourceMeta'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()

const linkId = route.params.id as string
const loading = ref(true)
const resource = ref<any>(null)
const copied = ref(false)
const importing = ref(false)
const importResult = ref<string | null>(null)
const loadError = ref('')

const cidMap = ref<Record<string, string>>({})
const defaultCid = ref('0')
const savedCidKey = 'embymedia_default_target_cid'

async function fetchDetail() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await fetch(`/api/v1/links/${linkId}`)
    const data = await res.json()
    if (!res.ok) throw new Error(data.error || data.message || '资源不存在')
    resource.value = data.data
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '读取资源失败'
  } finally {
    loading.value = false
  }
}

function copyUrl() {
  if (!resource.value) return
  navigator.clipboard.writeText(resource.value.url)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}

async function fetchCidMap() {
  try {
    const res = await fetch('/api/v1/cid-map')
    if (res.ok) {
      const data = await res.json()
      cidMap.value = data.map ?? {}
    }
  } catch (e) {
    console.error(e)
  }
  const stored = localStorage.getItem(savedCidKey)
  defaultCid.value = stored || Object.values(cidMap.value)[0] || '0'
}

async function triggerSave() {
  if (!resource.value) return
  importing.value = true
  importResult.value = null
  try {
    const res = await fetch(`/api/v1/links/${linkId}/save`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_cid: defaultCid.value })
    })
    const data = await res.json()
    if (res.ok) {
      importResult.value = data.method === 'share_save'
        ? `已转存「${data.title || resource.value.title}」${data.count} 项到目标目录`
        : '已推送到 115 离线下载队列'
    } else {
      importResult.value = data.error || '操作失败'
    }
  } catch (e) {
    importResult.value = '网络请求失败'
  } finally {
    importing.value = false
  }
}

onMounted(async () => {
  await fetchCidMap()
  fetchDetail()
})
</script>

<template>
  <div class="space-y-6">
    <!-- Breadcrumb back -->
    <div class="flex items-center gap-3">
      <button
        @click="router.back()"
        class="flex items-center gap-1 text-xs font-mono text-text-muted hover:text-text transition-colors"
      >
		<ArrowLeft class="w-4 h-4" />
		<span>返回</span>
      </button>
      <span class="text-text-faint">/</span>
      <span class="text-xs font-mono text-text-faint">资源档案 #{{ linkId }}</span>
    </div>

    <!-- Loading skeleton -->
    <div v-if="loading" class="p-8 rounded-xl border border-border bg-surface text-center">
      <Loader2 class="w-6 h-6 animate-spin text-accent mx-auto mb-2" />
      <div class="text-xs font-mono text-text-muted">正在加载档案详情...</div>
    </div>

    <div v-else-if="loadError" class="p-8 rounded-xl border border-danger/30 bg-surface text-center text-danger">{{ loadError }}</div>

    <!-- Main Detail Card -->
    <div v-else-if="resource" class="space-y-6">
      <div class="p-8 rounded-xl border border-border bg-surface shadow-sm space-y-6">
        <!-- Title and Disk Badge -->
        <div class="space-y-3">
          <div class="flex items-center gap-2 flex-wrap">
            <span
              class="px-2.5 py-1 rounded text-xs font-mono font-medium text-white shadow-xs"
              :style="{ backgroundColor: getDiskColor(resource.disk_type) }"
            >
              {{ getDiskLabel(resource.disk_type) }}
            </span>
            <span
              class="px-2.5 py-1 rounded text-xs font-mono font-medium"
              :class="resource.health_status === 'valid' ? 'bg-ok/10 text-ok' : 'bg-danger/10 text-danger'"
            >
              {{ getHealthLabel(resource.health_status) }}
            </span>
            <span class="font-mono text-xs text-text-faint">ID: {{ resource.id }}</span>
          </div>

          <h1 class="font-serif text-2xl font-bold text-text leading-snug">
            {{ resource.title }}
          </h1>
        </div>

        <!-- Action Panel -->
        <div class="p-5 rounded-lg border border-border bg-bg space-y-4">
          <div class="flex items-center justify-between gap-4 flex-wrap">
            <div class="font-mono text-xs text-text-muted break-all flex-1 min-w-0">
              <span class="text-text-faint">资源链接: </span>
              <span class="text-text selection:bg-accent-soft select-all">{{ resource.url }}</span>
            </div>
            <div class="flex items-center gap-2 shrink-0 flex-wrap">
              <div class="flex items-center gap-1.5">
                <FolderInput class="w-3.5 h-3.5 text-text-faint" />
                <select
                  v-model="defaultCid"
                  class="px-2.5 py-2 rounded-lg border border-border bg-surface text-text text-xs font-mono focus:outline-none focus:border-accent min-h-9"
                  title="转存目标目录"
                >
                  <option value="0">根目录</option>
                  <option v-for="(cid, name) in cidMap" :key="cid" :value="cid">{{ name }}</option>
                </select>
              </div>
              <button
                @click="copyUrl"
                class="flex items-center gap-1.5 px-3.5 py-2 rounded-lg border border-border hover:border-text-muted bg-surface text-xs font-mono text-text transition-colors"
              >
                <Check v-if="copied" class="w-4 h-4 text-ok" />
                <Copy v-else class="w-4 h-4" />
                <span>{{ copied ? '已复制' : '复制链接' }}</span>
              </button>

              <button
                @click="toggleFavorite({ id: resource.id, title: resource.title, url: resource.url, disk_type: resource.disk_type, password: resource.password, first_source: resource.first_source })"
                class="flex items-center gap-1.5 px-3.5 py-2 rounded-lg border border-border hover:border-text-muted bg-surface text-xs font-mono text-text transition-colors"
                :class="{ 'text-annotation border-annotation bg-annotation-soft': isFavorite(resource.id) }"
              >
                <Bookmark class="w-4 h-4" :class="{ 'fill-current': isFavorite(resource.id) }" />
                <span>{{ isFavorite(resource.id) ? '已收藏' : '收藏' }}</span>
              </button>

              <button
                @click="triggerSave"
                :disabled="importing"
                class="flex items-center gap-1.5 px-4 py-2 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors shadow-sm"
              >
                <Loader2 v-if="importing" class="w-4 h-4 animate-spin" />
                <Download v-else class="w-4 h-4" />
                <span>转存到 115</span>
              </button>
            </div>
          </div>

          <div v-if="importResult" class="text-xs font-mono text-accent pt-1">
            {{ importResult }}
          </div>

          <div v-if="resource.password" class="pt-2 border-t border-border flex items-center gap-3">
            <span class="text-xs font-mono text-text-faint">提取密码:</span>
            <span class="px-2 py-0.5 rounded bg-surface border border-border font-mono text-sm font-semibold text-text">
              {{ resource.password }}
            </span>
          </div>
        </div>

        <!-- Evidence & Metadata Grid -->
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4 pt-4 border-t border-border">
          <div class="p-4 rounded-lg border border-border bg-surface">
            <div class="flex items-center gap-2 text-xs font-mono text-text-muted mb-2">
              <ShieldCheck class="w-4 h-4 text-ok" />
              <span>健康检查证据</span>
            </div>
            <div class="text-sm font-medium text-text">
              状态: {{ getHealthLabel(resource.health_status) }}
            </div>
            <div class="text-xs font-mono text-text-faint mt-1">
              原因: {{ resource.health_reason || '正常' }}
            </div>
          </div>

          <div class="p-4 rounded-lg border border-border bg-surface">
            <div class="flex items-center gap-2 text-xs font-mono text-text-muted mb-2">
              <Radio class="w-4 h-4 text-accent" />
              <span>发现来源</span>
            </div>
            <div class="text-sm font-medium text-text truncate">
              {{ resource.first_source || '网络抓取' }}
            </div>
            <div class="text-xs font-mono text-text-faint mt-1">
              累计来源频道: {{ resource.source_count || 1 }} 个
            </div>
          </div>

          <div class="p-4 rounded-lg border border-border bg-surface">
            <div class="flex items-center gap-2 text-xs font-mono text-text-muted mb-2">
              <Clock class="w-4 h-4 text-annotation" />
              <span>更新时间</span>
            </div>
            <div class="text-sm font-medium text-text">
              {{ resource.last_seen_at ? new Date(resource.last_seen_at).toLocaleString() : '最近' }}
            </div>
            <div class="text-xs font-mono text-text-faint mt-1">
              最新消息: {{ resource.latest_message_time ? new Date(resource.latest_message_time).toLocaleDateString() : '未知' }}
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

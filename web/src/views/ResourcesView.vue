<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Search,
  Bookmark,
  ExternalLink,
  Copy,
  Check,
  Download,
  AlertCircle,
  Loader2,
  Calendar,
  Radio,
  SlidersHorizontal
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import { getDiskLabel, getDiskColor, getHealthLabel } from '@/utils/resourceMeta'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()

const keyword = ref((route.query.q as string) || '')
const selectedDisk = ref((route.query.disk as string) || '')
const selectedChannel = ref((route.query.channel as string) || '')
const healthFilter = ref((route.query.health as string) || '')
const offset = ref(0)
const limit = 30

const loading = ref(false)
const results = ref<any[]>([])
const totalHits = ref(0)
const hasMore = ref(false)
const diskTypes = ref<{ disk_type: string; count: number }[]>([])
const copiedId = ref<number | null>(null)
const importingId = ref<number | null>(null)
const importMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)

async function doSearch(resetPage = true) {
  if (resetPage) {
    offset.value = 0
  }
  loading.value = true
  try {
    const params = new URLSearchParams()
    if (keyword.value) params.set('q', keyword.value)
    if (selectedDisk.value) params.set('disk_type', selectedDisk.value)
    if (selectedChannel.value) params.set('channel', selectedChannel.value)
    if (healthFilter.value) params.set('health_status', healthFilter.value)
    params.set('offset', String(offset.value))
    params.set('limit', String(limit))

    const res = await fetch(`/search?${params.toString()}`)
    if (res.ok) {
      const data = await res.json()
      if (resetPage) {
        results.value = data.links || []
      } else {
        results.value.push(...(data.links || []))
      }
      totalHits.value = data.total || results.value.length
      hasMore.value = data.has_more ?? false
      if (data.disk_types) {
        diskTypes.value = data.disk_types
      }
    }
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

function handleSearchSubmit() {
  router.push({
    path: '/resources',
    query: {
      ...(keyword.value ? { q: keyword.value } : {}),
      ...(selectedDisk.value ? { disk: selectedDisk.value } : {}),
      ...(selectedChannel.value ? { channel: selectedChannel.value } : {}),
      ...(healthFilter.value ? { health: healthFilter.value } : {}),
    }
  })
  doSearch(true)
}

function copyToClipboard(text: string, id: number) {
  navigator.clipboard.writeText(text)
  copiedId.value = id
  setTimeout(() => {
    if (copiedId.value === id) copiedId.value = null
  }, 2000)
}

async function triggerOfflineImport(link: any) {
  importingId.value = link.id
  importMessage.value = null
  try {
    const res = await fetch('/api/v1/offline/download', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        urls: [link.url],
        target_cid: '0'
      })
    })
    if (res.ok) {
      importMessage.value = { id: link.id, text: '已加入 115 离线下载队列！', ok: true }
    } else {
      const err = await res.json()
      importMessage.value = { id: link.id, text: err.message || '离线失败', ok: false }
    }
  } catch (e) {
    importMessage.value = { id: link.id, text: '网络请求错误', ok: false }
  } finally {
    importingId.value = null
  }
}

watch(
  () => route.query,
  () => {
    keyword.value = (route.query.q as string) || ''
    selectedDisk.value = (route.query.disk as string) || ''
    selectedChannel.value = (route.query.channel as string) || ''
    healthFilter.value = (route.query.health as string) || ''
    doSearch(true)
  }
)

onMounted(() => {
  doSearch(true)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Top Search Bar Form -->
    <div class="p-6 rounded-xl border border-border bg-surface shadow-sm">
      <form @submit.prevent="handleSearchSubmit" class="space-y-4">
        <div class="relative flex items-center">
          <Search class="w-5 h-5 absolute left-4 text-text-faint" />
          <input
            v-model="keyword"
            type="text"
            placeholder="搜索全网公开网盘资源（电影、剧集、动漫、纪录片、4K原盘）..."
            class="w-full pl-12 pr-28 py-3.5 rounded-lg border border-border bg-bg text-text placeholder:text-text-faint focus:outline-none focus:border-accent text-sm font-sans"
          />
          <button
            type="submit"
            :disabled="loading"
            class="absolute right-2 px-5 py-2 rounded-md bg-accent text-accent-contrast font-medium text-xs hover:bg-accent-strong transition-colors flex items-center gap-1.5 shadow-sm"
          >
            <Loader2 v-if="loading" class="w-3.5 h-3.5 animate-spin" />
            <span>检索</span>
          </button>
        </div>

        <!-- Filter Chips Bar -->
        <div class="flex flex-wrap items-center gap-3 pt-2 text-xs font-mono">
          <div class="flex items-center gap-1.5 text-text-muted">
            <SlidersHorizontal class="w-3.5 h-3.5" />
            <span>网盘筛选:</span>
          </div>

          <button
            type="button"
            @click="selectedDisk = ''; handleSearchSubmit()"
            class="px-2.5 py-1 rounded-md border transition-colors"
            :class="selectedDisk === '' ? 'border-accent bg-accent-soft text-accent font-semibold' : 'border-border bg-bg text-text-muted hover:border-text-muted'"
          >
            全部网盘
          </button>

          <button
            v-for="d in diskTypes"
            :key="d.disk_type"
            type="button"
            @click="selectedDisk = d.disk_type; handleSearchSubmit()"
            class="px-2.5 py-1 rounded-md border transition-colors flex items-center gap-1.5"
            :class="selectedDisk === d.disk_type ? 'border-accent bg-accent-soft text-accent font-semibold' : 'border-border bg-bg text-text-muted hover:border-text-muted'"
          >
            <span class="w-2 h-2 rounded-full" :style="{ backgroundColor: getDiskColor(d.disk_type) }"></span>
            <span>{{ getDiskLabel(d.disk_type) }}</span>
            <span class="text-text-faint">({{ d.count }})</span>
          </button>
        </div>
      </form>
    </div>

    <!-- Results List -->
    <div class="space-y-4">
      <div class="flex items-center justify-between px-1 text-xs font-mono text-text-muted">
        <div>
          共检索到 <span class="text-text font-semibold">{{ totalHits }}</span> 条相关资源
        </div>
        <div v-if="loading" class="flex items-center gap-1 text-accent">
          <Loader2 class="w-3.5 h-3.5 animate-spin" />
          <span>正在检索索引库...</span>
        </div>
      </div>

      <!-- Empty State -->
      <div
        v-if="!loading && results.length === 0"
        class="p-12 rounded-xl border border-dashed border-border text-center bg-surface"
      >
        <AlertCircle class="w-8 h-8 text-text-faint mx-auto mb-3" />
        <h3 class="font-serif font-semibold text-base text-text">未检索到匹配的公开资源</h3>
        <p class="text-xs text-text-muted mt-1 font-mono">
          建议尝试更换关键词，或缩减网盘类型过滤条件
        </p>
      </div>

      <!-- Cards Grid -->
      <div class="grid grid-cols-1 gap-3.5">
        <div
          v-for="item in results"
          :key="item.id"
          class="p-5 rounded-xl border border-border bg-surface hover:border-border-strong transition-all shadow-sm flex flex-col md:flex-row md:items-center justify-between gap-4"
        >
          <!-- Title and metadata -->
          <div class="space-y-2 flex-1 min-w-0">
            <div class="flex items-center gap-2.5 flex-wrap">
              <!-- Disk badge -->
              <span
                class="px-2 py-0.5 rounded text-[11px] font-mono font-medium text-white shadow-xs"
                :style="{ backgroundColor: getDiskColor(item.disk_type) }"
              >
                {{ getDiskLabel(item.disk_type) }}
              </span>

              <!-- Health badge -->
              <span
                class="px-2 py-0.5 rounded text-[11px] font-mono"
                :class="item.health_status === 'valid' ? 'bg-ok/10 text-ok' : item.health_status === 'invalid' ? 'bg-danger/10 text-danger' : 'bg-bg-muted text-text-faint'"
              >
                {{ getHealthLabel(item.health_status) }}
              </span>

              <!-- Title link to detail view -->
              <RouterLink
                :to="`/resource/${item.id}`"
                class="font-medium text-sm text-text hover:text-accent transition-colors truncate block"
              >
                {{ item.title }}
              </RouterLink>
            </div>

            <!-- Source & Date Info -->
            <div class="flex items-center gap-4 text-xs font-mono text-text-faint flex-wrap">
              <span v-if="item.first_source" class="flex items-center gap-1">
                <Radio class="w-3.5 h-3.5" />
                <span>{{ item.first_source }}</span>
              </span>
              <span v-if="item.last_seen_at" class="flex items-center gap-1">
                <Calendar class="w-3.5 h-3.5" />
                <span>{{ new Date(item.last_seen_at).toLocaleDateString() }}</span>
              </span>
              <span v-if="item.password" class="px-1.5 py-0.5 rounded bg-bg-muted border border-border text-text font-semibold">
                密码: {{ item.password }}
              </span>
            </div>

            <!-- Inline feedback alert if any -->
            <div
              v-if="importMessage && importMessage.id === item.id"
              class="text-xs font-mono pt-1"
              :class="importMessage.ok ? 'text-ok' : 'text-danger'"
            >
              {{ importMessage.text }}
            </div>
          </div>

          <!-- Actions -->
          <div class="flex items-center gap-2 shrink-0">
            <!-- Copy Link -->
            <button
              @click="copyToClipboard(item.url, item.id)"
              class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border hover:border-text-muted bg-bg text-xs font-mono text-text transition-colors"
              title="复制原链接"
            >
              <Check v-if="copiedId === item.id" class="w-3.5 h-3.5 text-ok" />
              <Copy v-else class="w-3.5 h-3.5" />
              <span>{{ copiedId === item.id ? '已复制' : '复制' }}</span>
            </button>

            <!-- Favorite toggle -->
            <button
              @click="toggleFavorite({ id: item.id, title: item.title, url: item.url, disk_type: item.disk_type, password: item.password, first_source: item.first_source })"
              class="p-2 rounded-lg border border-border hover:border-text-muted bg-bg text-text transition-colors"
              :class="{ 'text-annotation border-annotation bg-annotation-soft': isFavorite(item.id) }"
              title="收藏资源"
            >
              <Bookmark class="w-3.5 h-3.5" :class="{ 'fill-current': isFavorite(item.id) }" />
            </button>

            <!-- Push to 115 offline -->
            <button
              @click="triggerOfflineImport(item)"
              :disabled="importingId === item.id"
              class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors shadow-xs"
            >
              <Loader2 v-if="importingId === item.id" class="w-3.5 h-3.5 animate-spin" />
              <Download v-else class="w-3.5 h-3.5" />
              <span>推送到 115</span>
            </button>

            <!-- View detail button -->
            <RouterLink
              :to="`/resource/${item.id}`"
              class="p-2 rounded-lg border border-border hover:border-text-muted bg-bg text-text-muted hover:text-text transition-colors"
            >
              <ExternalLink class="w-3.5 h-3.5" />
            </RouterLink>
          </div>
        </div>
      </div>

      <!-- Load more button -->
      <div v-if="hasMore" class="text-center pt-4">
        <button
          @click="offset += limit; doSearch(false)"
          :disabled="loading"
          class="px-6 py-2.5 rounded-lg border border-border bg-surface hover:bg-bg-muted text-xs font-mono text-text transition-colors"
        >
          加载更多资源...
        </button>
      </div>
    </div>
  </div>
</template>

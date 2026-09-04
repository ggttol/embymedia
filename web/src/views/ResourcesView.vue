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
  FolderInput
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import { getHealthLabel } from '@/utils/resourceMeta'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()

const keyword = ref((route.query.q as string) || '')
const selectedChannel = ref((route.query.channel as string) || '')
const healthFilter = ref((route.query.health as string) || '')
const offset = ref(0)
const limit = 30

const loading = ref(false)
const results = ref<any[]>([])
const totalHits = ref(0)
const hasMore = ref(false)
const copiedId = ref<number | null>(null)
const importingId = ref<number | null>(null)
const importMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)

const cidMap = ref<Record<string, string>>({})
const defaultCid = ref('0')
const savedCidKey = 'embymedia_default_target_cid'

async function doSearch(resetPage = true) {
  if (resetPage) {
    offset.value = 0
  }
  loading.value = true
  try {
    const params = new URLSearchParams()
    if (keyword.value) params.set('q', keyword.value)
    params.set('disk_type', '115')
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

function onDefaultCidChange() {
  localStorage.setItem(savedCidKey, defaultCid.value)
}

async function triggerSave(link: any) {
  importingId.value = link.id
  importMessage.value = null
  try {
    const res = await fetch(`/api/v1/links/${link.id}/save`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_cid: defaultCid.value })
    })
    const data = await res.json()
    if (res.ok) {
      importMessage.value = { id: link.id, text: `已转存「${data.title || link.title}」${data.count} 项到目标目录`, ok: true }
    } else {
      importMessage.value = { id: link.id, text: data.error || '转存失败', ok: false }
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
    selectedChannel.value = (route.query.channel as string) || ''
    healthFilter.value = (route.query.health as string) || ''
    doSearch(true)
  }
)

onMounted(async () => {
  await fetchCidMap()
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
            placeholder="搜索 115 网盘公开分享资源（电影、剧集、动漫、纪录片、4K原盘）..."
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

        <!-- Save target selector -->
        <div class="flex flex-wrap items-center gap-3 pt-1 text-xs font-mono">
          <div class="flex items-center gap-1.5 text-text-muted">
            <FolderInput class="w-3.5 h-3.5" />
            <span>转存目标目录:</span>
          </div>
          <select
            v-model="defaultCid"
            @change="onDefaultCidChange"
            class="px-2.5 py-1.5 rounded-md border border-border bg-bg text-text text-xs font-mono focus:outline-none focus:border-accent min-h-8"
          >
            <option value="0">根目录</option>
            <option v-for="(cid, name) in cidMap" :key="cid" :value="cid">{{ name }}</option>
          </select>
          <span class="text-text-faint">检索结果一键转存至所选目录</span>
        </div>
      </form>
    </div>

    <!-- Results List -->
    <div class="space-y-4">
      <div class="flex items-center justify-between px-1 text-xs font-mono text-text-muted">
        <div>
          共检索到 <span class="text-text font-semibold">{{ totalHits }}</span> 条 115 资源
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
        <h3 class="font-serif font-semibold text-base text-text">未检索到匹配的 115 资源</h3>
        <p class="text-xs text-text-muted mt-1 font-mono">
          建议尝试更换关键词后重新检索
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
                style="background-color: var(--disk-115)"
              >
                115
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

            <!-- Save share to 115 -->
            <button
              @click="triggerSave(item)"
              :disabled="importingId === item.id"
              class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors shadow-xs"
            >
              <Loader2 v-if="importingId === item.id" class="w-3.5 h-3.5 animate-spin" />
              <Download v-else class="w-3.5 h-3.5" />
              <span>转存到 115</span>
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

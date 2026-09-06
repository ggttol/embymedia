<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlertCircle, Bookmark, Calendar, Download, ExternalLink, FolderInput, Loader2, Radio, Search } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { useFavorites } from '@/stores/favorites'
import { getDiskColor, getDiskLabel, getHealthLabel } from '@/utils/resourceMeta'
import { getResourceSnapshot, resourceCanvas, resourceQueryKey, resourceReturnTo, restoreResourceScroll, setResourceSnapshot } from '@/stores/resourceSearch'
import { saveResource, useTransferTarget } from '@/stores/transferTarget'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()
const target = useTransferTarget()
const keyword = ref(typeof route.query.q === 'string' ? route.query.q : '')
const selectedChannel = ref(typeof route.query.channel === 'string' ? route.query.channel : '')
const healthFilter = ref(typeof route.query.health === 'string' ? route.query.health : '')
const offset = ref(0)
const limit = 30
const loading = ref(false)
const results = ref<any[]>([])
const totalHits = ref(0)
const hasMore = ref(false)
const importingId = ref<number | null>(null)
const importMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)
const searchError = ref('')
const searchTechnical = ref('')
const receivedAt = ref('')
const restored = ref(false)
let hasSuccessfulResult = false
let searchGeneration = 0
let activeKey = resourceQueryKey(route.query)

function currentQuery() {
  return {
    ...(keyword.value ? { q: keyword.value } : {}),
    ...(selectedChannel.value ? { channel: selectedChannel.value } : {}),
    ...(healthFilter.value ? { health: healthFilter.value } : {}),
  }
}

function preserveSnapshot() {
  if (!hasSuccessfulResult) return
  setResourceSnapshot({ key: activeKey, results: results.value, total: totalHits.value, hasMore: hasMore.value, offset: offset.value, scrollTop: resourceCanvas()?.scrollTop || 0, receivedAt: receivedAt.value })
}

async function doSearch(reset = true) {
  const key = resourceQueryKey(route.query)
  const requestedOffset = reset ? 0 : offset.value + limit
  loading.value = true
  searchError.value = ''
  if (reset) {
    hasSuccessfulResult = false
    restored.value = false
    receivedAt.value = ''
    setResourceSnapshot(null)
    results.value = []
    totalHits.value = 0
    hasMore.value = false
  }
  searchTechnical.value = ''
  const generation = ++searchGeneration
  try {
    const params = new URLSearchParams()
    if (keyword.value) params.set('q', keyword.value)
    params.set('disk_type', '115')
    if (selectedChannel.value) params.set('channel', selectedChannel.value)
    if (healthFilter.value) params.set('health_status', healthFilter.value)
    params.set('offset', String(requestedOffset))
    params.set('limit', String(limit))
    const response = await fetch(`/search?${params}`)
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
    if (generation !== searchGeneration || key !== resourceQueryKey(route.query)) return false
    results.value = reset ? (data.links || []) : [...results.value, ...(data.links || [])]
    offset.value = requestedOffset
    totalHits.value = data.total || results.value.length
    hasMore.value = data.has_more === true
    activeKey = key
    hasSuccessfulResult = true
    receivedAt.value = new Date().toLocaleTimeString('zh-CN')
    preserveSnapshot()
    return true
  } catch (cause) {
    if (generation !== searchGeneration) return false
    searchError.value = '暂时无法检索资源，请稍后重试。'
    searchTechnical.value = cause instanceof Error ? cause.message : String(cause)
    return false
  } finally {
    if (generation === searchGeneration) loading.value = false
  }
}

async function handleSearchSubmit() {
  const query = currentQuery()
  const nextKey = resourceQueryKey(query)
  if (nextKey === resourceQueryKey(route.query)) await doSearch(true)
  else await router.push({ path: '/resources', query })
}

async function loadMore() {
  await doSearch(false)
}

async function triggerSave(link: any) {
  const destination = target.destination()
  if (!destination) return
  importingId.value = link.id
  importMessage.value = null
  try {
    const message = await saveResource(link.id, destination)
    importMessage.value = { id: link.id, text: `「${link.title}」${message}`, ok: true }
  } catch (cause) {
    importMessage.value = { id: link.id, text: `「${link.title}」转存至 ${destination.label} 失败：${cause instanceof Error ? cause.message : '请求失败'}`, ok: false }
  } finally {
    importingId.value = null
  }
}

onMounted(async () => {
  target.loadTargets()
  const saved = getResourceSnapshot(activeKey)
  if (saved) {
    hasSuccessfulResult = true
    receivedAt.value = saved.receivedAt
    restored.value = true
    results.value = saved.results
    totalHits.value = saved.total
    hasMore.value = saved.hasMore
    offset.value = saved.offset
    await restoreResourceScroll(saved.scrollTop)
  } else await doSearch(true)
})
onBeforeUnmount(() => {
  preserveSnapshot()
  searchGeneration++
})

function detailTo(id: number) {
  return { path: `/resources/${id}`, query: { returnTo: resourceReturnTo(route.fullPath) } }
}
</script>

<template>
  <div class="space-y-7">
    <header class="pb-6 border-b border-border/70">
      <h1 class="font-serif text-3xl sm:text-4xl font-normal text-text tracking-tight">115 资源检索</h1>
      <p class="mt-2 text-sm text-text-muted leading-relaxed">筛选公开分享资源，一键转存至云端指定目录或本地挂载媒体源。</p>
    </header>

    <section class="p-6 sm:p-7 rounded-2xl border border-border/80 bg-surface shadow-xs" aria-labelledby="resource-search-heading">
      <div class="flex items-center justify-between pb-3 mb-4 border-b border-border/60">
        <h2 id="resource-search-heading" class="font-serif text-lg font-medium text-text">检索条件</h2>
        <span class="text-xs font-mono text-text-faint">DISK / 115 INDEX</span>
      </div>

      <form @submit.prevent="handleSearchSubmit" class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <label class="md:col-span-3 text-sm text-text">
          <span class="block mb-2 text-xs font-medium text-text-secondary">检索关键词</span>
          <span class="relative flex items-center">
            <Search class="w-5 h-5 absolute left-4 text-text-faint pointer-events-none" />
            <input
              v-model="keyword"
              data-global-search-input
              type="search"
              class="w-full min-h-12 pl-12 pr-4 rounded-xl border border-border/80 bg-bg/70 hover:bg-bg focus:bg-surface text-text text-sm sm:text-base placeholder:text-text-faint transition-all duration-200 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/15"
              placeholder="搜索电影、剧集名称、导演或演员…"
            />
          </span>
        </label>

        <label class="text-sm text-text">
          <span class="block mb-2 text-xs font-medium text-text-secondary">来源频道</span>
          <input
            v-model="selectedChannel"
            class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm placeholder:text-text-faint focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/15 transition-all"
            placeholder="全部频道（支持模糊匹配）"
          />
        </label>

        <label class="text-sm text-text">
          <span class="block mb-2 text-xs font-medium text-text-secondary">资源健康状态</span>
          <select
            v-model="healthFilter"
            class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/15 transition-all"
          >
            <option value="">全部状态</option>
            <option value="valid">有效可用</option>
            <option value="invalid">已失效</option>
            <option value="unknown">未检测</option>
          </select>
        </label>

        <button
          type="submit"
          :disabled="loading"
          class="self-end min-h-11 px-6 rounded-xl bg-accent text-accent-contrast font-medium text-sm flex items-center justify-center gap-2 hover:bg-accent-strong disabled:opacity-60 shadow-xs transition-all duration-200 cursor-pointer"
        >
          <Loader2 v-if="loading" class="w-4 h-4 animate-spin" />
          <span>{{ loading ? '检索中…' : '执行检索' }}</span>
        </button>
      </form>

      <div class="mt-6 pt-5 border-t border-border/70">
        <label class="text-sm font-medium text-text flex items-center gap-2" for="resources-transfer-target">
          <FolderInput class="w-4 h-4 text-accent" />
          <span>转存目标目录</span>
        </label>
        <div class="mt-2 flex flex-col sm:flex-row sm:items-center gap-3">
          <select
            id="resources-transfer-target"
            aria-label="转存目标目录"
            v-model="target.targetCid.value"
            :disabled="target.loading.value"
            class="w-full sm:w-auto min-w-[240px] min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/15 transition-all"
          >
            <option value="0">根目录（CID: 0）</option>
            <option v-for="option in target.options.value" :key="option.cid" :value="option.cid">{{ option.name }}（CID: {{ option.cid }}）</option>
          </select>
          <span v-if="target.ready.value" class="text-xs text-text-muted">当前选择：{{ target.targetLabel.value }}</span>
        </div>

        <p v-if="target.error.value" class="mt-2.5 text-sm text-danger flex items-center gap-1.5">
          <span>无法读取目录配置。转存已停用，避免误存到根目录。</span>
          <button type="button" class="underline hover:text-danger/80" @click="target.loadTargets">重试</button>
        </p>
        <p v-else-if="!target.available.value" class="mt-2.5 text-sm text-danger">已保存的目标 {{ target.targetLabel.value }} 当前不可用。请重新选择后再转存。</p>
        <p v-if="target.storageError.value" role="status" class="mt-2.5 text-sm text-warn">{{ target.storageError.value }}</p>
      </div>
    </section>

    <section class="space-y-4" aria-labelledby="resource-results-heading">
      <div class="flex items-center justify-between gap-3 px-1">
        <div>
          <h2 id="resource-results-heading" class="font-serif text-xl font-medium text-text">检索结果</h2>
          <p v-if="restored" role="status" class="text-xs text-text-muted mt-0.5">
            已恢复上次快照（{{ receivedAt }}）
            <button type="button" class="underline ml-1 hover:text-accent" :disabled="loading" @click="doSearch(true)">刷新</button>
          </p>
        </div>
        <span class="text-xs font-mono text-text-muted">{{ receivedAt && !searchError ? `共 ${totalHits} 条` : '数量未知' }}</span>
      </div>

      <div v-if="searchError" role="alert" class="p-6 rounded-2xl border border-danger/30 bg-danger/5">
        <p class="text-sm font-medium text-danger">{{ searchError }}</p>
        <button class="mt-3 min-h-10 px-4 border border-danger/40 text-xs font-medium rounded-xl hover:bg-danger/10 text-danger transition-colors" @click="doSearch(true)">重新检索</button>
        <details v-if="searchTechnical" class="mt-3 text-xs text-text-muted">
          <summary class="cursor-pointer">查看技术详情</summary>
          <pre class="mt-2 p-3 rounded-lg border border-border/60 bg-bg font-mono text-xs whitespace-pre-wrap">{{ searchTechnical }}</pre>
        </details>
      </div>

      <div v-else-if="loading && results.length === 0" class="p-14 rounded-2xl border border-dashed border-border/80 text-center bg-surface">
        <Loader2 class="w-6 h-6 animate-spin mx-auto text-accent" />
        <p class="mt-3 text-sm text-text-muted">正在检索索引库…</p>
      </div>

      <div v-else-if="results.length === 0" class="p-14 rounded-2xl border border-dashed border-border/80 text-center bg-surface">
        <AlertCircle class="w-8 h-8 mx-auto text-text-faint" />
        <h3 class="mt-3 font-serif font-medium text-text">未检索到匹配资源</h3>
        <p class="mt-1 text-sm text-text-muted">请调整关键词、频道过滤或健康筛选后重试。</p>
      </div>

      <div class="space-y-4">
        <article
          v-for="item in results"
          :key="item.id"
          class="p-5 sm:p-6 rounded-2xl border border-border/80 bg-surface shadow-xs transition-all duration-300 hover:shadow-card hover:-translate-y-0.5 hover:border-border-strong group"
        >
          <div class="flex flex-col md:flex-row md:items-start justify-between gap-5">
            <div class="min-w-0 flex-1">
              <RouterLink :to="detailTo(item.id)" class="font-serif text-lg font-medium text-text hover:text-accent break-words transition-colors leading-snug">
                {{ item.title }}
              </RouterLink>

              <div class="mt-3 flex flex-wrap items-center gap-2">
                <!-- 微光网盘胶囊 -->
                <span
                  class="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border shadow-xs"
                  :style="{
                    backgroundColor: getDiskColor(item.disk_type || '115') + '15',
                    borderColor: getDiskColor(item.disk_type || '115') + '38',
                    color: getDiskColor(item.disk_type || '115')
                  }"
                >
                  <span class="w-1.5 h-1.5 rounded-full" :style="{ backgroundColor: getDiskColor(item.disk_type || '115') }"></span>
                  {{ getDiskLabel(item.disk_type || '115') }}
                </span>

                <!-- 健康状态胶囊 -->
                <span
                  class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium border"
                  :class="{
                    'bg-[#edf4ee] text-[#2e5e43] border-[#d3e3d7]': item.health_status === 'valid',
                    'bg-[#faeeee] text-[#9e3939] border-[#f3d3d3]': item.health_status === 'invalid',
                    'bg-bg-muted/70 text-text-muted border-border/60': item.health_status !== 'valid' && item.health_status !== 'invalid'
                  }"
                >
                  {{ getHealthLabel(item.health_status) }}
                </span>

                <span v-if="item.first_source" class="text-xs text-text-muted flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-bg-muted/40">
                  <Radio class="w-3.5 h-3.5 text-text-faint" />
                  <span>{{ item.first_source }}</span>
                </span>
              </div>

              <div class="mt-3 flex flex-wrap gap-3.5 text-xs text-text-faint font-mono">
                <span v-if="item.last_seen_at" class="flex items-center gap-1">
                  <Calendar class="w-3.5 h-3.5" />
                  <span>{{ new Date(item.last_seen_at).toLocaleDateString() }}</span>
                </span>
                <span v-if="item.password" class="px-2 py-0.5 rounded bg-bg-muted/60 text-text-secondary">提取码：{{ item.password }}</span>
              </div>

              <p v-if="importMessage && importMessage.id === item.id" role="status" class="mt-3 text-sm font-medium" :class="importMessage.ok ? 'text-ok' : 'text-danger'">
                {{ importMessage.text }}
              </p>
            </div>

            <div class="flex flex-wrap gap-2 md:justify-end items-center shrink-0">
              <CopyButton :text="item.url" label="复制链接" />
              <button
                class="min-h-10 px-3 border border-border/80 bg-surface hover:bg-bg-muted rounded-xl transition-colors"
                :aria-pressed="isFavorite(item.id)"
                @click="toggleFavorite({ id: item.id, title: item.title, url: item.url, disk_type: item.disk_type, password: item.password, first_source: item.first_source })"
              >
                <Bookmark class="w-4 h-4" :class="{ 'fill-current text-annotation': isFavorite(item.id) }" />
                <span class="sr-only">{{ isFavorite(item.id) ? '取消收藏' : '收藏' }} {{ item.title }}</span>
              </button>
              <button
                :disabled="importingId === item.id || !target.ready.value"
                class="min-h-10 px-4 rounded-xl bg-accent text-accent-contrast text-xs font-medium flex items-center gap-2 hover:bg-accent-strong disabled:opacity-50 shadow-xs transition-colors cursor-pointer"
                @click="triggerSave(item)"
              >
                <Loader2 v-if="importingId === item.id" class="w-3.5 h-3.5 animate-spin" />
                <Download v-else class="w-3.5 h-3.5" />
                <span>转存至 {{ target.targetLabel.value }}</span>
              </button>
              <RouterLink :to="detailTo(item.id)" class="min-h-10 px-3 border border-border/80 bg-surface hover:bg-bg-muted rounded-xl flex items-center transition-colors">
                <ExternalLink class="w-4 h-4 text-text-secondary" />
                <span class="sr-only">查看 {{ item.title }} 详情</span>
              </RouterLink>
            </div>
          </div>
        </article>
      </div>

      <div class="pt-2 text-center" v-if="hasMore && !searchError">
        <button
          :disabled="loading"
          class="min-h-11 px-7 border border-border/80 bg-surface hover:bg-bg-muted rounded-xl text-sm font-medium shadow-xs disabled:opacity-60 transition-all duration-200 cursor-pointer"
          @click="loadMore"
        >
          {{ loading ? '加载中…' : '加载更多资源' }}
        </button>
      </div>
    </section>
  </div>
</template>

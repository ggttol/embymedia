<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlertCircle, Bookmark, Calendar, Download, ExternalLink, FolderInput, Loader2, Radio, Search } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { useFavorites } from '@/stores/favorites'
import { getHealthLabel } from '@/utils/resourceMeta'
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
  <div class="space-y-6">
    <header>
      <h1 class="font-serif text-3xl font-bold text-text">115 资源检索</h1>
      <p class="mt-2 text-sm text-text-muted">筛选公开分享资源并转存到指定目录。</p>
    </header>
    <section class="p-6 rounded-xl border border-border bg-surface shadow-sm" aria-labelledby="resource-search-heading">
      <h2 id="resource-search-heading" class="font-serif text-lg font-semibold text-text">检索条件</h2>
      <form @submit.prevent="handleSearchSubmit" class="mt-4 grid grid-cols-1 md:grid-cols-3 gap-4">
        <label class="md:col-span-3 text-sm text-text"><span class="block mb-1.5 font-medium">关键词</span><span class="relative flex items-center"><Search class="w-5 h-5 absolute left-4 text-text-faint"/><input v-model="keyword" data-global-search-input type="search" class="w-full min-h-11 pl-12 pr-4 rounded-lg border border-border bg-bg text-text" placeholder="电影、剧集或资源名称"></span></label>
        <label class="text-sm text-text"><span class="block mb-1.5 font-medium">来源频道</span><input v-model="selectedChannel" class="w-full min-h-11 px-3 rounded-lg border border-border bg-bg" placeholder="全部频道"></label>
        <label class="text-sm text-text"><span class="block mb-1.5 font-medium">资源健康状态</span><select v-model="healthFilter" class="w-full min-h-11 px-3 rounded-lg border border-border bg-bg"><option value="">全部状态</option><option value="valid">有效</option><option value="invalid">失效</option><option value="unknown">未检测</option></select></label>
        <button type="submit" :disabled="loading" class="self-end min-h-11 px-5 rounded-lg bg-accent text-accent-contrast flex items-center justify-center gap-2 disabled:opacity-60"><Loader2 v-if="loading" class="w-4 h-4 animate-spin"/>检索</button>
      </form>
      <div class="mt-5 pt-4 border-t border-border">
        <label class="text-sm font-medium text-text flex items-center gap-2"><FolderInput class="w-4 h-4"/>转存目标目录</label>
        <select aria-label="转存目标目录" v-model="target.targetCid.value" :disabled="target.loading.value" class="mt-2 w-full md:w-auto min-h-11 px-3 rounded-lg border border-border bg-bg"><option value="0">根目录（CID: 0）</option><option v-for="option in target.options.value" :key="option.cid" :value="option.cid">{{ option.name }}（CID: {{ option.cid }}）</option></select>
        <p v-if="target.error.value" class="mt-2 text-sm text-danger">无法读取目录配置。转存已停用，避免误存到根目录。<button type="button" class="underline ml-1" @click="target.loadTargets">重试</button></p>
        <p v-else-if="!target.available.value" class="mt-2 text-sm text-danger">已保存的目标 {{ target.targetLabel.value }} 当前不可用。请重新选择后再转存。</p>
        <p v-else class="mt-2 text-xs text-text-muted">当前目标：{{ target.targetLabel.value }}</p>
        <p v-if="target.storageError.value" role="status" class="mt-2 text-sm text-warn">{{ target.storageError.value }}</p>
      </div>
    </section>

    <section class="space-y-4" aria-labelledby="resource-results-heading">
      <div class="flex items-center justify-between gap-3"><h2 id="resource-results-heading" class="font-serif text-xl font-semibold text-text">检索结果</h2><span class="text-xs font-mono text-text-muted">{{ receivedAt && !searchError ? `共 ${totalHits} 条` : '数量未知' }}</span></div>
      <p v-if="restored" role="status" class="text-sm text-text-muted">已恢复上次结果（{{ receivedAt }}）。<button type="button" class="underline min-h-11" :disabled="loading" @click="doSearch(true)">刷新结果</button></p>
      <div v-if="searchError" role="alert" class="p-5 rounded-lg border border-danger/30 bg-surface"><p class="text-sm text-danger">{{ searchError }}</p><button class="mt-3 min-h-11 px-4 border border-border rounded-lg" @click="doSearch(true)">重新检索</button><details v-if="searchTechnical" class="mt-3 text-xs text-text-muted"><summary>技术详情</summary><pre class="mt-2 whitespace-pre-wrap">{{ searchTechnical }}</pre></details></div>
      <div v-else-if="loading && results.length === 0" class="p-12 border border-dashed border-border text-center"><Loader2 class="w-6 h-6 animate-spin mx-auto"/><p class="mt-2 text-sm">正在检索索引库…</p></div>
      <div v-else-if="results.length === 0" class="p-12 rounded-xl border border-dashed border-border text-center bg-surface"><AlertCircle class="w-8 h-8 mx-auto text-text-faint"/><h3 class="mt-3 font-serif font-semibold">未检索到匹配资源</h3><p class="mt-1 text-sm text-text-muted">调整关键词或筛选条件后重试。</p></div>
      <article v-for="item in results" :key="item.id" class="p-5 rounded-xl border border-border bg-surface shadow-sm">
        <div class="flex flex-col md:flex-row md:items-start justify-between gap-5">
          <div class="min-w-0 flex-1"><RouterLink :to="detailTo(item.id)" class="font-serif text-lg font-semibold text-text hover:text-accent break-words">{{ item.title }}</RouterLink><div class="mt-2 flex flex-wrap gap-2"><span class="px-2 py-1 text-xs text-white" style="background:var(--disk-115)">115</span><span class="px-2 py-1 text-xs bg-bg-muted">{{ getHealthLabel(item.health_status) }}</span><span v-if="item.first_source" class="text-xs text-text-muted flex items-center gap-1"><Radio class="w-3.5 h-3.5"/>{{ item.first_source }}</span></div><div class="mt-3 flex flex-wrap gap-3 text-xs text-text-faint"><span v-if="item.last_seen_at" class="flex items-center gap-1"><Calendar class="w-3.5 h-3.5"/>{{ new Date(item.last_seen_at).toLocaleDateString() }}</span><span v-if="item.password">提取码：{{ item.password }}</span></div><p v-if="importMessage && importMessage.id === item.id" role="status" class="mt-3 text-sm" :class="importMessage.ok ? 'text-ok' : 'text-danger'">{{ importMessage.text }}</p></div>
          <div class="flex flex-wrap gap-2 md:justify-end"><CopyButton :text="item.url" label="复制链接"/><button class="min-h-11 px-3 border border-border rounded-lg" :aria-pressed="isFavorite(item.id)" @click="toggleFavorite({ id:item.id,title:item.title,url:item.url,disk_type:item.disk_type,password:item.password,first_source:item.first_source })"><Bookmark class="w-4 h-4" :class="{ 'fill-current text-annotation': isFavorite(item.id) }"/><span class="sr-only">{{ isFavorite(item.id) ? '取消收藏' : '收藏' }} {{ item.title }}</span></button><button :disabled="importingId === item.id || !target.ready.value" class="min-h-11 px-4 rounded-lg bg-accent text-accent-contrast flex items-center gap-2 disabled:opacity-50" @click="triggerSave(item)"><Loader2 v-if="importingId === item.id" class="w-4 h-4 animate-spin"/><Download v-else class="w-4 h-4"/>转存至 {{ target.targetLabel.value }}</button><RouterLink :to="detailTo(item.id)" class="min-h-11 px-3 border border-border rounded-lg flex items-center"><ExternalLink class="w-4 h-4"/><span class="sr-only">查看 {{ item.title }} 详情</span></RouterLink></div>
        </div>
      </article>
      <button v-if="hasMore && !searchError" :disabled="loading" class="min-h-11 px-6 border border-border bg-surface rounded-lg disabled:opacity-60" @click="loadMore">{{ loading ? '加载中…' : '加载更多资源' }}</button>
    </section>
  </div>
</template>

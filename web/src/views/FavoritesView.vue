<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Bookmark, CheckSquare, Download, FolderInput, Loader2, Search, Square, Trash2, Undo2 } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import UiDialog from '@/components/UiDialog.vue'
import { type FavoriteItem, useFavorites } from '@/stores/favorites'
import { saveResource, useTransferTarget } from '@/stores/transferTarget'
import { getDiskColor, getDiskLabel } from '@/utils/resourceMeta'

const { favorites, count, removeFavorite, restoreFavorite } = useFavorites()
const target = useTransferTarget()
const searchQuery = ref('')
const selectedDisk = ref('')
const selectedIds = ref<Set<number>>(new Set())
const pushingId = ref<number | null>(null)
const pushMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)
const batchRunning = ref(false)
const batchDialog = ref(false)
const batchResults = ref<Array<{ title: string; text: string; ok: boolean }>>([])
const removed = ref<FavoriteItem | null>(null)
const filteredFavorites = computed(() => favorites.value.filter(item => (!searchQuery.value || item.title.toLowerCase().includes(searchQuery.value.toLowerCase())) && (!selectedDisk.value || item.disk_type === selectedDisk.value)))
const selectedTargets = computed(() => filteredFavorites.value.filter(item => item.disk_type === '115' && selectedIds.value.has(item.id)))

watch([searchQuery, selectedDisk], () => { selectedIds.value = new Set() })
onMounted(target.loadTargets)

function toggleSelect(id: number) {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}

function selectAllVisible() {
  selectedIds.value = new Set(filteredFavorites.value.filter(item => item.disk_type === '115').map(item => item.id))
}

function clearFilters() {
  searchQuery.value = ''
  selectedDisk.value = ''
}

function remove(item: FavoriteItem) {
  removeFavorite(item.id)
  removed.value = item
  selectedIds.value.delete(item.id)
  selectedIds.value = new Set(selectedIds.value)
}

function undoRemoval() {
  if (!removed.value) return
  restoreFavorite(removed.value)
  removed.value = null
}

async function triggerSave(item: FavoriteItem) {
  const destination = target.destination()
  if (!destination) return
  pushingId.value = item.id
  pushMessage.value = null
  try {
    pushMessage.value = { id: item.id, text: `「${item.title}」${await saveResource(item.id, destination)}`, ok: true }
  } catch (cause) {
    pushMessage.value = { id: item.id, text: `「${item.title}」转存至 ${destination.label} 失败：${cause instanceof Error ? cause.message : '请求失败'}`, ok: false }
  } finally {
    pushingId.value = null
  }
}

async function triggerBatchSave() {
  const destination = target.destination()
  const items = [...selectedTargets.value]
  if (!destination || items.length === 0) return
  batchRunning.value = true
  batchResults.value = []
  for (const item of items) {
    try {
      batchResults.value.push({ title: item.title, text: await saveResource(item.id, destination), ok: true })
    } catch (cause) {
      batchResults.value.push({ title: item.title, text: `转存至 ${destination.label} 失败：${cause instanceof Error ? cause.message : '请求失败'}`, ok: false })
    }
  }
  batchRunning.value = false
  batchDialog.value = false
  selectedIds.value = new Set()
}
</script>

<template>
  <div class="space-y-6">
    <header class="pb-6 border-b border-border"><h1 class="font-serif text-3xl font-bold text-text">收藏资源</h1><p class="mt-2 text-sm text-text-muted">此浏览器已保存 {{ count }} 条资源。</p></header>
    <section v-if="count" class="p-4 rounded-xl border border-border bg-surface space-y-4" aria-labelledby="favorite-filters"><h2 id="favorite-filters" class="font-serif text-lg font-semibold">筛选与转存</h2><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><label class="text-sm"><span class="block mb-1">收藏关键词</span><span class="relative flex items-center"><Search class="absolute left-3 w-4 h-4 text-text-faint"/><input v-model="searchQuery" type="search" class="w-full min-h-11 pl-9 pr-3 border border-border bg-bg rounded-lg" placeholder="资源名称"></span></label><label class="text-sm"><span class="block mb-1">网盘类型</span><select v-model="selectedDisk" class="w-full min-h-11 px-3 border border-border bg-bg rounded-lg"><option value="">全部网盘</option><option value="115">115</option><option value="quark">夸克</option><option value="baidu">百度网盘</option><option value="aliyun">阿里云盘</option></select></label><label class="text-sm"><span class="mb-1 flex items-center gap-1"><FolderInput class="w-4 h-4"/>转存目标目录</span><select v-model="target.targetCid.value" :disabled="target.loading.value" class="w-full min-h-11 px-3 border border-border bg-bg rounded-lg"><option value="0">根目录（CID: 0）</option><option v-for="option in target.options.value" :key="option.cid" :value="option.cid">{{ option.name }}（CID: {{ option.cid }}）</option></select></label></div><p v-if="target.error.value" class="text-sm text-danger">无法读取目录配置。转存已停用，避免误存到根目录。<button class="underline ml-1" @click="target.loadTargets">重试</button></p><p v-else-if="!target.available.value" class="text-sm text-danger">已保存的目标 {{ target.targetLabel.value }} 当前不可用，请重新选择。</p><p v-else class="text-xs text-text-muted">当前目标：{{ target.targetLabel.value }}</p></section>

    <div v-if="removed" role="status" class="p-4 border border-border bg-surface flex flex-wrap items-center justify-between gap-3"><span>已移除「{{ removed.title }}」</span><button class="min-h-11 px-4 border border-border rounded-lg flex items-center gap-2" @click="undoRemoval"><Undo2 class="w-4 h-4"/>撤销</button></div>
    <div v-if="selectedTargets.length" class="p-4 border border-accent bg-accent-soft flex flex-wrap items-center justify-between gap-3"><span>已选 {{ selectedTargets.length }} 项；目标：{{ target.targetLabel.value }}</span><div class="flex gap-2"><button :disabled="!target.ready.value" class="min-h-11 px-4 bg-accent text-accent-contrast rounded-lg disabled:opacity-50" @click="batchDialog = true">确认批量转存</button><button class="min-h-11 px-4 border border-border rounded-lg" @click="selectedIds = new Set()">取消选择</button></div></div>
    <div v-if="batchResults.length" class="p-4 border border-border bg-surface"><h2 class="font-serif font-semibold">批量转存结果</h2><ul class="mt-3 space-y-2"><li v-for="result in batchResults" :key="result.title" :class="result.ok ? 'text-ok' : 'text-danger'"><strong>{{ result.title }}</strong>：{{ result.text }}</li></ul></div>

    <section v-if="count === 0" class="p-16 border border-dashed border-border bg-surface text-center"><Bookmark class="w-8 h-8 mx-auto text-text-faint"/><h2 class="mt-3 font-serif text-lg font-semibold">暂无收藏</h2><p class="mt-2 text-sm text-text-muted">在检索结果中收藏资源后，会显示在这里。</p><RouterLink to="/resources" class="mt-4 inline-flex min-h-11 items-center px-5 bg-accent text-accent-contrast rounded-lg">前往资源检索</RouterLink></section>
    <section v-else-if="filteredFavorites.length === 0" class="p-12 border border-dashed border-border bg-surface text-center"><h2 class="font-serif text-lg font-semibold">当前筛选没有匹配收藏</h2><p class="mt-2 text-sm text-text-muted">收藏仍保留在浏览器中。</p><button class="mt-4 min-h-11 px-5 border border-border rounded-lg" @click="clearFilters">清除筛选</button></section>
    <section v-else class="space-y-3" aria-labelledby="favorite-list"><div class="flex justify-between items-center"><h2 id="favorite-list" class="font-serif text-xl font-semibold">收藏列表</h2><button class="min-h-11 flex items-center gap-2 text-sm" @click="selectAllVisible"><CheckSquare class="w-4 h-4"/>选择当前 115 资源</button></div><article v-for="item in filteredFavorites" :key="item.id" class="p-5 border bg-surface rounded-xl" :class="selectedIds.has(item.id) ? 'border-accent' : 'border-border'"><div class="flex flex-col md:flex-row md:items-start justify-between gap-4"><div class="flex gap-3 min-w-0"><button v-if="item.disk_type === '115'" class="min-w-11 min-h-11 flex items-center justify-center" :aria-pressed="selectedIds.has(item.id)" @click="toggleSelect(item.id)"><CheckSquare v-if="selectedIds.has(item.id)" class="w-5 h-5 text-accent"/><Square v-else class="w-5 h-5"/><span class="sr-only">选择 {{ item.title }}</span></button><div class="min-w-0"><RouterLink :to="{ path:`/resources/${item.id}`, query:{ returnTo:'/favorites' } }" class="font-serif text-lg font-semibold hover:text-accent break-words">{{ item.title }}</RouterLink><div class="mt-2 flex flex-wrap gap-2 text-xs text-text-muted"><span class="px-2 py-1 text-white" :style="{ backgroundColor:getDiskColor(item.disk_type) }">{{ getDiskLabel(item.disk_type) }}</span><span>收藏于 {{ new Date(item.savedAt).toLocaleDateString() }}</span><span v-if="item.first_source">来源：{{ item.first_source }}</span></div><p v-if="pushMessage?.id === item.id" class="mt-3 text-sm" :class="pushMessage.ok ? 'text-ok' : 'text-danger'">{{ pushMessage.text }}</p></div></div><div class="flex flex-wrap gap-2"><CopyButton :text="item.url" label="复制链接"/><button v-if="item.disk_type === '115'" :disabled="pushingId === item.id || !target.ready.value" class="min-h-11 px-4 bg-accent text-accent-contrast rounded-lg disabled:opacity-50 flex items-center gap-2" @click="triggerSave(item)"><Loader2 v-if="pushingId === item.id" class="w-4 h-4 animate-spin"/><Download v-else class="w-4 h-4"/>转存至 {{ target.targetLabel.value }}</button><button class="min-h-11 min-w-11 border border-border rounded-lg flex items-center justify-center hover:text-danger" @click="remove(item)"><Trash2 class="w-4 h-4"/><span class="sr-only">移除 {{ item.title }}</span></button></div></div></article></section>

    <UiDialog v-if="batchDialog" title="确认批量转存" :busy="batchRunning" @close="batchDialog = false"><p>将 {{ selectedTargets.length }} 项转存至 <strong>{{ target.targetLabel.value }}</strong>。转存开始后，请在结果区域核对每一项。</p><div class="mt-5 flex flex-wrap justify-end gap-2"><button class="min-h-11 px-4 border border-border rounded-lg" :disabled="batchRunning" @click="batchDialog = false">取消</button><button class="min-h-11 px-4 bg-accent text-accent-contrast rounded-lg flex items-center gap-2" :disabled="batchRunning" @click="triggerBatchSave"><Loader2 v-if="batchRunning" class="w-4 h-4 animate-spin"/>开始转存</button></div></UiDialog>
  </div>
</template>

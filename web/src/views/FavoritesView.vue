<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  Bookmark,
  Trash2,
  Download,
  Copy,
  Check,
  Search,
  FolderInput,
  Loader2,
  CheckSquare,
  Square
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import { getDiskLabel, getDiskColor } from '@/utils/resourceMeta'

const { favorites, count, removeFavorite } = useFavorites()
const searchQuery = ref('')
const selectedDisk = ref('')
const copiedId = ref<number | null>(null)
const pushingId = ref<number | null>(null)
const pushMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)
const batchRunning = ref(false)
const batchMessage = ref<string | null>(null)
const selectedIds = ref<Set<number>>(new Set())
const cidMap = ref<Record<string, string>>({})
const defaultCid = ref('0')
const savedCidKey = 'embymedia_default_target_cid'

const filteredFavorites = computed(() => {
  return favorites.value.filter(item => {
    const matchQuery = !searchQuery.value || item.title.toLowerCase().includes(searchQuery.value.toLowerCase())
    const matchDisk = !selectedDisk.value || item.disk_type === selectedDisk.value
    return matchQuery && matchDisk
  })
})

const is115 = (item: any) => item.disk_type === '115'
const selectedTargets = computed(() => filteredFavorites.value.filter(item => is115(item) && selectedIds.value.has(item.id)))
const selectedCount = computed(() => selectedTargets.value.length)

function toggleSelect(id: number) {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}

function selectAllVisible() {
  const next = new Set(selectedIds.value)
	filteredFavorites.value.filter(is115).forEach(item => next.add(item.id))
  selectedIds.value = next
}

function clearSelection() {
  selectedIds.value = new Set()
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

async function saveOne(link: any, targetCid: string): Promise<string> {
  const res = await fetch(`/api/v1/links/${link.id}/save`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target_cid: targetCid })
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || '转存失败')
  return `已转存「${data.title || link.title}」${data.count} 项`
}

async function triggerSave(link: any) {
  pushingId.value = link.id
  pushMessage.value = null
  try {
    const message = await saveOne(link, defaultCid.value)
    pushMessage.value = { id: link.id, text: message, ok: true }
  } catch (e) {
    pushMessage.value = { id: link.id, text: e instanceof Error ? e.message : '请求失败', ok: false }
  } finally {
    pushingId.value = null
  }
}

async function triggerBatchSave() {
	const targets = selectedTargets.value
  if (targets.length === 0) return
  batchRunning.value = true
  batchMessage.value = null
  let ok = 0
  const failures: string[] = []
  for (const item of targets) {
    try {
      await saveOne(item, defaultCid.value)
      ok++
    } catch (e) {
      failures.push(`${item.title}: ${e instanceof Error ? e.message : '失败'}`)
    }
  }
  batchMessage.value = `批量转存完成：成功 ${ok} / ${targets.length}` + (failures.length ? `；失败: ${failures.slice(0, 3).join('；')}` : '')
  batchRunning.value = false
  selectedIds.value = new Set()
}

onMounted(fetchCidMap)
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-center justify-between pb-6 border-b border-border">
      <div>
        <h1 class="font-serif text-3xl font-bold text-text">收藏资源</h1>
        <p class="text-sm text-text-muted mt-2">
          此浏览器已保存 <span class="font-semibold text-text">{{ count }}</span> 条资源。
        </p>
      </div>
    </div>

    <!-- Filter Tools -->
    <div v-if="count > 0" class="flex flex-col md:flex-row items-center gap-3 p-4 rounded-xl border border-border bg-surface">
      <div class="relative flex-1 w-full">
        <Search class="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-text-faint" />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="在收藏中筛选..."
          class="w-full pl-9 pr-3 py-2 rounded-lg border border-border bg-bg text-text placeholder:text-text-faint text-xs font-mono focus:outline-none focus:border-accent"
        />
      </div>
      <div class="flex items-center gap-1.5 text-xs font-mono text-text-muted shrink-0">
        <FolderInput class="w-3.5 h-3.5" />
        <span>转存目录:</span>
        <select
          v-model="defaultCid"
          class="px-2.5 py-1.5 rounded-md border border-border bg-bg text-text text-xs font-mono focus:outline-none focus:border-accent min-h-8"
        >
          <option value="0">根目录</option>
          <option v-for="(cid, name) in cidMap" :key="cid" :value="cid">{{ name }}</option>
        </select>
      </div>
    </div>

    <!-- Batch actions -->
    <div v-if="selectedCount > 0" class="flex items-center justify-between gap-3 p-4 rounded-xl border border-accent/40 bg-accent-soft">
      <div class="text-xs font-mono text-text">
        已选 <span class="font-bold">{{ selectedCount }}</span> 项
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="triggerBatchSave"
          :disabled="batchRunning"
          class="flex items-center gap-1.5 px-4 py-2 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors disabled:opacity-60"
        >
          <Loader2 v-if="batchRunning" class="w-3.5 h-3.5 animate-spin" />
          <Download v-else class="w-3.5 h-3.5" />
          <span>{{ batchRunning ? '批量转存中...' : '批量转存到 115' }}</span>
        </button>
        <button
          @click="clearSelection"
          class="px-3 py-2 rounded-lg border border-border bg-surface text-xs font-mono text-text-muted hover:text-text transition-colors"
        >
          取消选择
        </button>
      </div>
    </div>

    <div v-if="batchMessage" class="px-4 py-2.5 rounded-lg border border-border bg-surface text-xs font-mono text-text-muted">
      {{ batchMessage }}
    </div>

    <!-- Empty State -->
    <div
      v-if="count === 0"
      class="p-16 rounded-xl border border-dashed border-border text-center bg-surface space-y-4"
    >
      <div class="w-12 h-12 rounded-full bg-bg-muted flex items-center justify-center mx-auto text-text-faint">
        <Bookmark class="w-6 h-6" />
      </div>
      <div>
        <h3 class="font-serif font-semibold text-lg text-text">暂无收藏</h3>
        <p class="text-sm text-text-muted mt-2">
          在检索结果中使用收藏按钮，资源会保存在当前浏览器。
        </p>
      </div>
      <RouterLink
        to="/resources"
        class="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-accent text-accent-contrast font-medium text-xs shadow-sm hover:bg-accent-strong transition-colors"
      >
        前往检索资源库
      </RouterLink>
    </div>

    <!-- Favorites List -->
    <div v-else class="space-y-3">
      <div class="flex items-center gap-3 px-1">
        <button
          @click="selectAllVisible"
          class="flex items-center gap-1 text-xs font-mono text-text-muted hover:text-accent transition-colors"
        >
          <CheckSquare class="w-3.5 h-3.5" />
          <span>全选当前列表</span>
        </button>
      </div>
      <div
        v-for="item in filteredFavorites"
        :key="item.id"
        class="p-4 rounded-xl border bg-surface transition-all flex flex-col md:flex-row md:items-center justify-between gap-4"
        :class="selectedIds.has(item.id) ? 'border-accent' : 'border-border hover:border-border-strong'"
      >
        <div class="flex items-start gap-3 flex-1 min-w-0">
          <button
            v-if="is115(item)"
            @click="toggleSelect(item.id)"
            class="mt-0.5 text-text-faint hover:text-accent transition-colors shrink-0"
            :title="selectedIds.has(item.id) ? '取消选择' : '加入批量转存'"
          >
            <CheckSquare v-if="selectedIds.has(item.id)" class="w-4 h-4 text-accent" />
            <Square v-else class="w-4 h-4" />
          </button>
          <div class="space-y-1.5 flex-1 min-w-0">
            <div class="flex items-center gap-2">
              <span
                class="px-2 py-0.5 rounded text-[11px] font-mono font-medium text-white shrink-0 shadow-xs"
                :style="{ backgroundColor: getDiskColor(item.disk_type) }"
              >
                {{ getDiskLabel(item.disk_type) }}
              </span>
              <RouterLink
				:to="`/resources/${item.id}`"
                class="text-sm font-medium text-text hover:text-accent truncate block"
              >
                {{ item.title }}
              </RouterLink>
            </div>
            <div class="text-xs font-mono text-text-faint flex items-center gap-3">
              <span>保存时间: {{ new Date(item.savedAt).toLocaleDateString() }}</span>
              <span v-if="item.password">密码: {{ item.password }}</span>
              <span v-if="item.first_source">来源: {{ item.first_source }}</span>
            </div>

            <div
              v-if="pushMessage && pushMessage.id === item.id"
              class="text-xs font-mono pt-1"
              :class="pushMessage.ok ? 'text-ok' : 'text-danger'"
            >
              {{ pushMessage.text }}
            </div>
          </div>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <button
            @click="copyToClipboard(item.url, item.id)"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border hover:border-text-muted bg-bg text-xs font-mono text-text transition-colors"
          >
            <Check v-if="copiedId === item.id" class="w-3.5 h-3.5 text-ok" />
            <Copy v-else class="w-3.5 h-3.5" />
            <span>{{ copiedId === item.id ? '已复制' : '复制' }}</span>
          </button>

          <button
            v-if="is115(item)"
            @click="triggerSave(item)"
            :disabled="pushingId === item.id"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors shadow-xs"
          >
            <Loader2 v-if="pushingId === item.id" class="w-3.5 h-3.5 animate-spin" />
            <Download v-else class="w-3.5 h-3.5" />
            <span>转存到 115</span>
          </button>

          <button
            @click="removeFavorite(item.id)"
            class="p-2 rounded-lg border border-border hover:border-danger hover:text-danger bg-bg text-text-faint transition-colors"
            title="移除收藏"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

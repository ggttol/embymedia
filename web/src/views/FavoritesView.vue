<script setup lang="ts">
import { ref, computed } from 'vue'
import {
  Bookmark,
  Trash2,
  Download,
  Copy,
  Check,
  Search,
  ExternalLink,
  SlidersHorizontal
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import { getDiskLabel, getDiskColor } from '@/utils/resourceMeta'

const { favorites, count, removeFavorite } = useFavorites()
const searchQuery = ref('')
const selectedDisk = ref('')
const copiedId = ref<number | null>(null)
const pushingId = ref<number | null>(null)
const pushMessage = ref<{ id: number; text: string; ok: boolean } | null>(null)

const filteredFavorites = computed(() => {
  return favorites.value.filter(item => {
    const matchQuery = !searchQuery.value || item.title.toLowerCase().includes(searchQuery.value.toLowerCase())
    const matchDisk = !selectedDisk.value || item.disk_type === selectedDisk.value
    return matchQuery && matchDisk
  })
})

function copyToClipboard(text: string, id: number) {
  navigator.clipboard.writeText(text)
  copiedId.value = id
  setTimeout(() => {
    if (copiedId.value === id) copiedId.value = null
  }, 2000)
}

async function triggerOfflineImport(link: any) {
  pushingId.value = link.id
  pushMessage.value = null
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
      pushMessage.value = { id: link.id, text: '已推送至 115 离线任务！', ok: true }
    } else {
      const err = await res.json()
      pushMessage.value = { id: link.id, text: err.message || '离线失败', ok: false }
    }
  } catch (e) {
    pushMessage.value = { id: link.id, text: '请求失败', ok: false }
  } finally {
    pushingId.value = null
  }
}
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
      <div
        v-for="item in filteredFavorites"
        :key="item.id"
        class="p-4 rounded-xl border border-border bg-surface hover:border-border-strong transition-all flex flex-col md:flex-row md:items-center justify-between gap-4"
      >
        <div class="space-y-1.5 flex-1 min-w-0">
          <div class="flex items-center gap-2">
            <span
              class="px-2 py-0.5 rounded text-[11px] font-mono font-medium text-white shrink-0 shadow-xs"
              :style="{ backgroundColor: getDiskColor(item.disk_type) }"
            >
              {{ getDiskLabel(item.disk_type) }}
            </span>
            <RouterLink
              :to="`/resource/${item.id}`"
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
            @click="triggerOfflineImport(item)"
            :disabled="pushingId === item.id"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-accent bg-accent hover:bg-accent-strong text-accent-contrast text-xs font-mono transition-colors shadow-xs"
          >
            <Download class="w-3.5 h-3.5" />
            <span>推送到 115</span>
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

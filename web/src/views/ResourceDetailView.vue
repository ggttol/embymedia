<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Bookmark, Clock, Download, FolderInput, Loader2, Radio, ShieldCheck } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { useFavorites } from '@/stores/favorites'
import { resourceReturnTo } from '@/stores/resourceSearch'
import { saveResource, useTransferTarget } from '@/stores/transferTarget'
import { getDiskColor, getDiskLabel, getHealthLabel } from '@/utils/resourceMeta'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()
const target = useTransferTarget()
const linkId = route.params.id as string
const loading = ref(true)
const resource = ref<any>(null)
const importing = ref(false)
const importResult = ref<{ text: string; ok: boolean } | null>(null)
const loadError = ref('')
const loadTechnical = ref('')
const returnTo = resourceReturnTo(typeof route.query.returnTo === 'string' ? route.query.returnTo : '/resources')

function returnToResources() {
  if (router.options.history.state.back === returnTo) router.back()
  else router.replace(returnTo)
}

async function fetchDetail() {
  loading.value = true
  loadError.value = ''
  loadTechnical.value = ''
  try {
    const response = await fetch(`/api/v1/links/${linkId}`)
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || data.message || `HTTP ${response.status}`)
    resource.value = data.data
  } catch (cause) {
    loadError.value = '暂时无法读取这条资源。'
    loadTechnical.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}

async function triggerSave() {
  const destination = target.destination()
  if (!resource.value || !destination) return
  importing.value = true
  importResult.value = null
  try {
    const message = await saveResource(resource.value.id, destination)
    importResult.value = { text: `「${resource.value.title}」${message}`, ok: true }
  } catch (cause) {
    importResult.value = { text: `「${resource.value.title}」转存至 ${destination.label} 失败：${cause instanceof Error ? cause.message : '请求失败'}`, ok: false }
  } finally {
    importing.value = false
  }
}

onMounted(() => {
  target.loadTargets()
  fetchDetail()
})
</script>

<template>
  <div class="space-y-6">
    <button class="min-h-11 inline-flex items-center gap-2 text-sm text-text-muted hover:text-text" @click="returnToResources"><ArrowLeft class="w-4 h-4"/>返回资源列表</button>
    <div v-if="loading" class="p-12 border border-border bg-surface text-center"><Loader2 class="w-6 h-6 animate-spin mx-auto"/><p class="mt-2">正在加载资源详情…</p></div>
    <div v-else-if="loadError" role="alert" class="p-8 border border-danger/30 bg-surface"><h1 class="font-serif text-2xl font-bold">资源详情</h1><p class="mt-3 text-danger">{{ loadError }}</p><button class="mt-4 min-h-11 px-4 border border-border rounded-lg" @click="fetchDetail">重试</button><details class="mt-3 text-xs text-text-muted"><summary>技术详情</summary><pre class="mt-2 whitespace-pre-wrap">{{ loadTechnical }}</pre></details></div>
    <main v-else-if="resource" class="space-y-6">
      <article class="p-8 rounded-xl border border-border bg-surface shadow-sm space-y-6">
        <header><div class="flex flex-wrap gap-2 mb-3"><span class="px-2.5 py-1 text-xs text-white" :style="{ backgroundColor:getDiskColor(resource.disk_type) }">{{ getDiskLabel(resource.disk_type) }}</span><span class="px-2.5 py-1 text-xs bg-bg-muted">{{ getHealthLabel(resource.health_status) }}</span><span class="text-xs text-text-faint self-center">资源编号 {{ resource.id }}</span></div><h1 class="font-serif text-2xl font-bold text-text leading-snug">{{ resource.title }}</h1></header>
        <section class="p-5 rounded-lg border border-border bg-bg space-y-5" aria-labelledby="resource-actions"><h2 id="resource-actions" class="font-serif text-lg font-semibold">资源操作</h2><div><span class="block text-sm font-medium mb-1">资源链接</span><p class="text-sm break-all select-all">{{ resource.url }}</p></div><div class="flex flex-wrap gap-2"><CopyButton :text="resource.url" label="复制链接"/><CopyButton v-if="resource.password" :text="resource.password" label="复制提取码"/><button class="min-h-11 px-4 border border-border rounded-lg flex items-center gap-2" :aria-pressed="isFavorite(resource.id)" @click="toggleFavorite({ id:resource.id,title:resource.title,url:resource.url,disk_type:resource.disk_type,password:resource.password,first_source:resource.first_source })"><Bookmark class="w-4 h-4" :class="{ 'fill-current text-annotation':isFavorite(resource.id) }"/>{{ isFavorite(resource.id) ? '取消收藏' : '收藏' }}</button></div>
          <div class="pt-4 border-t border-border"><label class="text-sm font-medium flex items-center gap-2" for="detail-target"><FolderInput class="w-4 h-4"/>转存目标目录</label><select id="detail-target" v-model="target.targetCid.value" :disabled="target.loading.value" class="mt-2 w-full min-h-11 px-3 border border-border bg-surface rounded-lg"><option value="0">根目录（CID: 0）</option><option v-for="option in target.options.value" :key="option.cid" :value="option.cid">{{ option.name }}（CID: {{ option.cid }}）</option></select><p v-if="target.error.value" class="mt-2 text-sm text-danger">无法读取目录配置。转存已停用，避免误存到根目录。<button class="underline ml-1" @click="target.loadTargets">重试</button></p><p v-else-if="!target.available.value" class="mt-2 text-sm text-danger">已保存的目标 {{ target.targetLabel.value }} 当前不可用，请重新选择。</p><button :disabled="importing || !target.ready.value" class="mt-3 min-h-11 px-5 bg-accent text-accent-contrast rounded-lg flex items-center gap-2 disabled:opacity-50" @click="triggerSave"><Loader2 v-if="importing" class="w-4 h-4 animate-spin"/><Download v-else class="w-4 h-4"/>转存至 {{ target.targetLabel.value }}</button><p v-if="importResult" class="mt-3 text-sm" :class="importResult.ok ? 'text-ok' : 'text-danger'">{{ importResult.text }}</p></div>
        </section>
        <section class="grid grid-cols-1 md:grid-cols-3 gap-4" aria-labelledby="resource-evidence"><h2 id="resource-evidence" class="sr-only">资源证据与元数据</h2><div class="p-4 border border-border"><p class="text-xs text-text-muted flex gap-2"><ShieldCheck class="w-4 h-4"/>健康检查</p><p class="mt-2 font-medium">{{ getHealthLabel(resource.health_status) }}</p><p class="mt-1 text-xs text-text-faint">{{ resource.health_reason || '未提供检查说明' }}</p></div><div class="p-4 border border-border"><p class="text-xs text-text-muted flex gap-2"><Radio class="w-4 h-4"/>发现来源</p><p class="mt-2 font-medium">{{ resource.first_source || '网络抓取' }}</p><p class="mt-1 text-xs text-text-faint">累计 {{ resource.source_count || 1 }} 个来源频道</p></div><div class="p-4 border border-border"><p class="text-xs text-text-muted flex gap-2"><Clock class="w-4 h-4"/>更新时间</p><p class="mt-2 font-medium">{{ resource.last_seen_at ? new Date(resource.last_seen_at).toLocaleString() : '未知' }}</p></div></section>
      </article>
    </main>
  </div>
</template>

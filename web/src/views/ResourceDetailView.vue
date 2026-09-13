<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Bookmark, Clock, Download, FolderInput, Loader2, Radio, ShieldCheck } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { useFavorites } from '@/stores/favorites'
import { getDiskColor, getDiskLabel, getHealthLabel, normalizeResourceProvider } from '@/utils/resourceMeta'
import { resourceReturnTo } from '@/stores/resourceSearch'
import { saveResource, useTransferTarget } from '@/stores/transferTarget'

const route = useRoute()
const router = useRouter()
const { isFavorite, toggleFavorite } = useFavorites()
const target = useTransferTarget()
const linkId = route.params.id as string
const loading = ref(true)
const resource = ref<any>(null)
const resourceProvider = ref<'115' | 'quark'>(normalizeResourceProvider(route.query.provider) === 'quark' ? 'quark' : '115')
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
    resourceProvider.value = resource.value?.disk_type === 'quark' ? 'quark' : '115'
  } catch (cause) {
    loadError.value = '暂时无法读取这条资源。'
    loadTechnical.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}

async function triggerSave() {
  if (!resource.value) return
  if (resourceProvider.value === 'quark') {
    await router.push({ path: '/files', query: { resource_id: String(resource.value.id) } })
    return
  }
  const destination = target.destination()
  if (!destination) return
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
    <button class="min-h-11 inline-flex items-center gap-2 text-sm text-text-muted hover:text-text transition-colors group cursor-pointer" @click="returnToResources">
      <ArrowLeft class="w-4 h-4 transition-transform duration-200 group-hover:-translate-x-1" />
      <span>返回资源列表</span>
    </button>

    <div v-if="loading" class="p-14 rounded-2xl border border-border/80 bg-surface text-center shadow-xs">
      <Loader2 class="w-6 h-6 animate-spin mx-auto text-accent" />
      <p class="mt-3 text-sm text-text-muted">正在加载资源详情…</p>
    </div>

    <div v-else-if="loadError" role="alert" class="p-8 rounded-2xl border border-danger/30 bg-danger/5 shadow-xs">
      <h1 class="font-serif text-2xl font-bold text-text">资源详情</h1>
      <p class="mt-3 text-sm font-medium text-danger">{{ loadError }}</p>
      <button class="mt-4 min-h-10 px-4 border border-danger/40 text-xs font-medium rounded-xl hover:bg-danger/10 text-danger transition-colors" @click="fetchDetail">重试</button>
      <details v-if="loadTechnical" class="mt-3 text-xs text-text-muted">
        <summary class="cursor-pointer">查看技术详情</summary>
        <pre class="mt-2 p-3 rounded-lg border border-border/60 bg-bg font-mono text-xs whitespace-pre-wrap">{{ loadTechnical }}</pre>
      </details>
    </div>

    <main v-else-if="resource" class="space-y-6">
      <article class="p-6 sm:p-8 rounded-2xl border border-border/80 bg-surface shadow-card space-y-7">
        <header>
          <div class="flex flex-wrap items-center gap-2.5 mb-3">
            <!-- 微光网盘胶囊 -->
            <span
              class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium border shadow-xs"
              :style="{
                backgroundColor: getDiskColor(resource.disk_type) + '15',
                borderColor: getDiskColor(resource.disk_type) + '38',
                color: getDiskColor(resource.disk_type)
              }"
            >
              <span class="w-1.5 h-1.5 rounded-full" :style="{ backgroundColor: getDiskColor(resource.disk_type) }"></span>
              {{ getDiskLabel(resource.disk_type) }}
            </span>

            <!-- 健康状态胶囊 -->
            <span
              class="inline-flex items-center px-3 py-1 rounded-full text-xs font-medium border"
              :class="{
                'bg-[#edf4ee] text-[#2e5e43] border-[#d3e3d7]': resource.health_status === 'valid',
                'bg-[#faeeee] text-[#9e3939] border-[#f3d3d3]': resource.health_status === 'invalid',
                'bg-bg-muted/70 text-text-muted border-border/60': resource.health_status !== 'valid' && resource.health_status !== 'invalid'
              }"
            >
              {{ getHealthLabel(resource.health_status) }}
            </span>

            <span class="text-xs font-mono text-text-faint self-center ml-1">资源 ID: {{ resource.id }}</span>
          </div>

          <h1 class="font-serif text-2xl sm:text-3xl font-medium text-text leading-snug tracking-tight">{{ resource.title }}</h1>
        </header>

        <section class="p-5 sm:p-6 rounded-xl border border-border/70 bg-bg-muted/30 space-y-5" aria-labelledby="resource-actions">
          <h2 id="resource-actions" class="font-serif text-lg font-medium text-text">资源操作与链接</h2>
          <div>
            <span class="block text-xs font-medium text-text-secondary mb-1.5">公开分享链接</span>
            <p class="text-sm font-mono break-all select-all p-3 rounded-lg border border-border/60 bg-surface text-text">{{ resource.url }}</p>
          </div>

          <div class="flex flex-wrap gap-2.5 items-center">
            <CopyButton :text="resource.url" label="复制链接" />
            <CopyButton v-if="resource.password" :text="resource.password" label="复制提取码" />
            <button
              class="min-h-10 px-4 border border-border/80 bg-surface hover:bg-bg-muted rounded-xl text-xs font-medium flex items-center gap-2 transition-colors cursor-pointer"
              :aria-pressed="isFavorite(resource.id)"
              @click="toggleFavorite({ id: resource.id, title: resource.title, url: resource.url, disk_type: resource.disk_type, password: resource.password, first_source: resource.first_source })"
            >
              <Bookmark class="w-4 h-4" :class="{ 'fill-current text-annotation': isFavorite(resource.id) }" />
              <span>{{ isFavorite(resource.id) ? '已收藏' : '收藏' }}</span>
            </button>
          </div>

          <div class="pt-5 border-t border-border/60">
            <template v-if="resourceProvider === 'quark'">
              <p class="text-sm text-text-muted">夸克资源将在文件管理页预加载分享信息，确认夸克保存目录后发送到 115 的固定整理目录。</p>
              <button
                :disabled="importing"
                class="mt-3 min-h-11 px-6 bg-accent text-accent-contrast rounded-xl text-sm font-medium flex items-center justify-center gap-2 hover:bg-accent-strong disabled:opacity-50 shadow-xs transition-colors cursor-pointer"
                @click="triggerSave"
              >
                <Download class="w-4 h-4" />
                <span>转存并发送到 115</span>
              </button>
            </template>
            <template v-else>
              <label class="text-sm font-medium flex items-center gap-2 text-text" for="detail-target">
                <FolderInput class="w-4 h-4 text-accent" />
                <span>转存目标目录</span>
              </label>
              <div class="mt-2 flex flex-col sm:flex-row sm:items-center gap-3">
                <select
                  id="detail-target"
                  v-model="target.targetCid.value"
                  :disabled="target.loading.value"
                  class="w-full sm:w-auto min-w-[260px] min-h-11 px-3.5 border border-border/80 bg-surface rounded-xl text-sm focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/15 transition-all"
                >
                  <option value="0">根目录（CID: 0）</option>
                  <option v-for="option in target.options.value" :key="option.cid" :value="option.cid">{{ option.name }}（CID: {{ option.cid }}）</option>
                </select>
                <button
                  :disabled="importing || !target.ready.value"
                  class="min-h-11 px-6 bg-accent text-accent-contrast rounded-xl text-sm font-medium flex items-center justify-center gap-2 hover:bg-accent-strong disabled:opacity-50 shadow-xs transition-colors cursor-pointer"
                  @click="triggerSave"
                >
                  <Loader2 v-if="importing" class="w-4 h-4 animate-spin" />
                  <Download v-else class="w-4 h-4" />
                  <span>转存至 {{ target.targetLabel.value }}</span>
                </button>
              </div>

              <p v-if="target.error.value" class="mt-2.5 text-sm text-danger flex items-center gap-1">
                <span>无法读取目录配置。转存已停用，避免误存到根目录。</span>
                <button class="underline hover:text-danger/80" @click="target.loadTargets">重试</button>
              </p>
              <p v-else-if="!target.available.value" class="mt-2.5 text-sm text-danger">已保存的目标 {{ target.targetLabel.value }} 当前不可用，请重新选择。</p>
            </template>
            <p v-if="importResult" class="mt-3 text-sm font-medium p-3 rounded-lg border" :class="importResult.ok ? 'border-ok/30 bg-ok/5 text-ok' : 'border-danger/30 bg-danger/5 text-danger'">{{ importResult.text }}</p>
          </div>
        </section>

        <section class="grid grid-cols-1 md:grid-cols-3 gap-4" aria-labelledby="resource-evidence">
          <h2 id="resource-evidence" class="sr-only">资源证据与元数据</h2>
          <div class="p-4 rounded-xl border border-border/70 bg-bg-muted/20">
            <p class="text-xs text-text-muted flex items-center gap-1.5">
              <ShieldCheck class="w-4 h-4 text-accent" />
              <span>健康检查</span>
            </p>
            <p class="mt-2 font-medium text-text">{{ getHealthLabel(resource.health_status) }}</p>
            <p class="mt-1 text-xs text-text-faint">{{ resource.health_reason || '未提供检查说明' }}</p>
          </div>

          <div class="p-4 rounded-xl border border-border/70 bg-bg-muted/20">
            <p class="text-xs text-text-muted flex items-center gap-1.5">
              <Radio class="w-4 h-4 text-accent" />
              <span>发现来源</span>
            </p>
            <p class="mt-2 font-medium text-text">{{ resource.first_source || '网络抓取' }}</p>
            <p class="mt-1 text-xs text-text-faint">累计 {{ resource.source_count || 1 }} 个来源频道</p>
          </div>

          <div class="p-4 rounded-xl border border-border/70 bg-bg-muted/20">
            <p class="text-xs text-text-muted flex items-center gap-1.5">
              <Clock class="w-4 h-4 text-accent" />
              <span>更新时间</span>
            </p>
            <p class="mt-2 font-medium text-text font-mono text-sm">{{ resource.last_seen_at ? new Date(resource.last_seen_at).toLocaleString() : '未知' }}</p>
          </div>
        </section>
      </article>
    </main>
  </div>
</template>

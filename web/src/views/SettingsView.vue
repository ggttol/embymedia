<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Check, CircleAlert, FileCode, HardDrive, Loader2, Radio, RefreshCw, Save, Tv } from 'lucide-vue-next'

const emptySettings: Record<string, string> = {
  '115_cookie': '',
  'emby_url': '',
  'emby_api_key': '',
  'clouddrive_url': '',
  'clouddrive_mount_path': '',
  'clouddrive_api_token': '',
  'clouddrive_source_path': '',
  'clouddrive_webhook_secret': '',
  'clouddrive_webhook_debounce_seconds': '5',
  'media_root': '',
  'strm_root': '',
  'emby_media_prefix': '/media',
  'resource_api_url': '',
  'resource_api_token': '',
  'dangerous_actions_enabled': 'false',
}
const settings = ref({ ...emptySettings })
const configured = ref<Record<string, boolean>>({ c115: false, emby: false, clouddrive: false, resource: false })
const health = ref<Record<string, { status: string; message: string; latency?: number; details?: string }>>({})
const checking = ref<Record<string, boolean>>({ c115: false, emby: false, clouddrive: false, resource: false })
const baseline = ref(JSON.stringify(settings.value))
const loaded = ref(false)
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const feedback = ref('')
const readError = ref('')
const dirty = computed(() => loaded.value && JSON.stringify(settings.value) !== baseline.value)
const configuredCount = computed(() => Object.values(configured.value).filter(Boolean).length)
const integrationItems = [
  {
    key: 'c115',
    label: '一一五网盘',
  },
  {
    key: 'emby',
    label: '媒体服务器',
  },
  {
    key: 'clouddrive',
    label: '挂载服务',
  },
  {
    key: 'resource',
    label: '资源索引',
  },
]
const c115Placeholder = '粘贴浏览器凭据'
const embyKeyPlaceholder = '输入接口密钥'
const resourceTokenPlaceholder = '留空时保留现有值（如有）'

function secretPlaceholder(key: string, fallback: string) {
  return configured.value[key] ? '已保存；留空不会替换现有凭据' : fallback
}

async function fetchSettings() {
  saveState.value = 'saving'
  readError.value = ''
  try {
    const response = await fetch('/api/v1/settings')
    if (!response.ok) throw new Error('读取设置失败')
    const data = await response.json()
    settings.value = { ...emptySettings, ...(data.settings ?? {}) }
    configured.value = { ...configured.value, ...(data.configured ?? {}) }
    health.value = data.health ?? {}
    baseline.value = JSON.stringify(settings.value)
    saveState.value = 'idle'
    loaded.value = true
    readError.value = ''
  } catch (error) {
    saveState.value = 'idle'
    readError.value = error instanceof Error ? error.message : '无法读取设置'
  }
}

async function checkComponent(key: string) {
  if (!loaded.value || checking.value[key]) return
  checking.value[key] = true
  health.value = { ...health.value, [key]: { status: 'checking', message: '正在检查连接' } }
  try {
    const response = await fetch('/api/v1/settings/check', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ component: key }),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '连接检查失败')
    health.value = { ...health.value, [key]: data.health ?? { status: 'error', message: '服务未返回检查结果' } }
  } catch (error) {
    health.value = { ...health.value, [key]: { status: 'error', message: error instanceof Error ? error.message : '连接检查失败' } }
  } finally {
    checking.value[key] = false
  }
}

async function saveSettings() {
  if (!dirty.value || saveState.value === 'saving') return
  saveState.value = 'saving'
  feedback.value = ''
  try {
    const response = await fetch('/api/v1/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settings.value),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '保存设置失败')
    settings.value = { ...settings.value, ...(data.settings ?? {}) }
    configured.value = { ...configured.value, ...(data.configured ?? {}) }
    if (data.health) health.value = data.health
    baseline.value = JSON.stringify(settings.value)
    saveState.value = 'saved'
    feedback.value = '设置已保存并同步状态。'
  } catch (error) {
    saveState.value = 'error'
    feedback.value = error instanceof Error ? error.message : '保存设置失败'
  }
}
watch(settings, () => {
  if (dirty.value && saveState.value === 'saved') {
    saveState.value = 'idle'
    feedback.value = ''
  }
}, { deep: true })

function warnBeforeUnload(event: BeforeUnloadEvent) {
  if (!dirty.value) return
  event.preventDefault()
  event.returnValue = ''
}

onBeforeRouteLeave(() => !dirty.value || window.confirm('设置尚未保存，确认离开？'))
onMounted(() => {
  window.addEventListener('beforeunload', warnBeforeUnload)
  void fetchSettings()
})
onBeforeUnmount(() => window.removeEventListener('beforeunload', warnBeforeUnload))
</script>

<template>
  <div class="space-y-7 max-w-5xl">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border/80">
      <div class="max-w-2xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">CONFIGURATION / PERSISTED STATE</p>
        <h1 class="font-serif text-3xl font-bold text-text tracking-tight">系统设置</h1>
        <p class="text-sm text-text-muted mt-2">管理服务地址与凭据。秘密值读取时始终保持隐藏。</p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-3 text-sm">
        <span class="font-mono text-xs text-text-faint">{{ loaded ? `${configuredCount} / 4 已配置` : '配置状态未知' }}</span>
        <span class="w-2 h-2 rounded-full" :class="loaded ? (configuredCount === 4 ? 'bg-ok' : 'bg-warn') : 'bg-text-faint'"></span>
      </div>
    </header>
    <div v-if="readError" role="alert" class="rounded-xl border-l-4 border-danger bg-danger/5 p-4 text-sm text-danger shadow-xs">
      <p class="font-medium">{{ readError }}</p>
      <p class="mt-1 text-text-muted">为避免覆盖已保存的设置，读取成功前不能编辑或检查连接。</p>
      <button type="button" :disabled="saveState === 'saving'" class="mt-3 min-h-10 rounded-lg border border-danger/40 px-4 text-sm font-medium hover:bg-danger/10 transition-colors disabled:opacity-50" @click="fetchSettings">{{ saveState === 'saving' ? '正在重试' : '重新读取设置' }}</button>
    </div>
    <div v-else-if="!loaded" role="status" class="flex items-center gap-2 rounded-xl border border-border/70 bg-surface p-4 text-sm text-text-muted shadow-xs"><Loader2 class="w-4 h-4 animate-spin text-accent" />正在读取系统设置…</div>

    <div class="grid grid-cols-2 lg:grid-cols-4 border border-border/70 bg-surface rounded-2xl overflow-hidden shadow-xs">
      <div v-for="item in integrationItems" :key="item.key" class="p-4 border-b border-r border-border/60 last:border-r-0 lg:border-b-0 flex flex-col justify-between">
        <div class="flex items-center justify-between">
          <span class="text-xs font-mono uppercase tracking-wider text-text-muted font-medium">{{ item.label }}</span>
          <button type="button" :disabled="!loaded || checking[item.key]" class="inline-flex min-h-8 min-w-8 items-center justify-center text-text-faint hover:text-text rounded-lg hover:bg-bg-muted transition-colors disabled:opacity-45" :aria-label="`测试${item.label}连接`" @click="checkComponent(item.key)">
            <Loader2 v-if="checking[item.key]" class="w-3.5 h-3.5 animate-spin text-accent" />
            <RefreshCw v-else class="w-3.5 h-3.5" />
          </button>
        </div>
        <div class="mt-2.5">
          <div class="flex items-center gap-2 text-xs font-mono">
            <span v-if="!loaded" class="inline-flex items-center gap-1.5 text-text-faint">
              <span class="w-2 h-2 rounded-full bg-text-faint"></span>
              状态未知
            </span>
            <span v-else-if="health[item.key]?.status === 'ok'" class="inline-flex items-center gap-1.5 text-ok font-semibold">
              <span class="w-2 h-2 rounded-full bg-ok"></span>
              正常在线
            </span>
            <span v-else-if="health[item.key]?.status === 'error'" class="inline-flex items-center gap-1.5 text-danger font-semibold">
              <span class="w-2 h-2 rounded-full bg-danger"></span>
              异常
            </span>
            <span v-else-if="configured[item.key]" class="inline-flex items-center gap-1.5 text-ok">
              <Check class="w-3.5 h-3.5" />
              已配置
            </span>
            <span v-else class="inline-flex items-center gap-1.5 text-warn">
              <CircleAlert class="w-3.5 h-3.5" />
              待配置
            </span>
          </div>
          <p v-if="health[item.key]?.message" class="mt-2 break-words text-xs leading-5" :class="health[item.key]?.status === 'error' ? 'text-danger' : 'text-text-faint'">
            {{ health[item.key]?.message }}
          </p>
          <p v-if="health[item.key]?.details" class="mt-1 break-words text-xs leading-5 text-text-muted">{{ health[item.key]?.details }}</p>
          <p v-if="!loaded" class="mt-2 text-xs leading-5 text-text-faint">读取设置后才能检查连接。</p>
        </div>
      </div>
    </div>

    <form id="settings-form" class="border border-border/70 bg-surface rounded-2xl shadow-xs overflow-hidden" @submit.prevent="saveSettings">
      <fieldset :disabled="!loaded || saveState === 'saving'">
      <div class="border-b border-border/60 bg-bg-muted/40 px-5 py-4 sm:px-7"><h2 class="font-serif text-xl font-semibold text-text tracking-tight">服务连接</h2><p class="mt-1 text-sm text-text-muted">配置网盘、媒体服务器、挂载服务与资源索引的访问方式。</p></div>
      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border/60">
        <div>
          <div class="flex items-center gap-2"><HardDrive class="w-4 h-4 text-accent" /><h3 class="font-serif font-semibold text-lg text-text">115 网盘</h3></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">用于账号鉴权。保存后不会再次返回明文。</p>
        </div>
        <div>
          <label for="c115-cookie" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">账号浏览器凭据</label>
          <input id="c115-cookie" v-model="settings['115_cookie']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('c115', c115Placeholder)" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" />
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border/60">
        <div>
          <div class="flex items-center gap-2"><Tv class="w-4 h-4 text-accent" /><h3 class="font-serif font-semibold text-lg text-text">Emby</h3></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">媒体库读取与刷新所需的服务地址和密钥。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="emby-url" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">服务端地址</label><input id="emby-url" v-model="settings['emby_url']" type="url" placeholder="http://127.0.0.1:8096" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="emby-key" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">接口密钥</label><input id="emby-key" v-model="settings['emby_api_key']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('emby', embyKeyPlaceholder)" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border/60">
        <div>
          <div class="flex items-center gap-2"><Radio class="w-4 h-4 text-accent" /><h3 class="font-serif font-semibold text-lg text-text">CloudDrive2 连接</h3></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">使用官方 gRPC API 读取和重新挂载；API Token 读取时不会返回明文。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="clouddrive-url" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">gRPC 地址</label><input id="clouddrive-url" v-model="settings['clouddrive_url']" type="url" placeholder="http://127.0.0.1:19798" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="clouddrive-token" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">API Token</label><input id="clouddrive-token" v-model="settings['clouddrive_api_token']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('clouddrive', '输入 CloudDrive2 API Token')" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border/60">
        <div><div class="flex items-center gap-2"><HardDrive class="w-4 h-4 text-accent" /><h3 class="font-serif font-semibold text-lg text-text">媒体与路径映射</h3></div><p class="mt-2 text-xs leading-5 text-text-faint">对应 CloudDrive 来源、本地挂载、STRM 输出与 Emby 可见路径。</p></div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="mount-path" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">本地挂载目录</label><input id="mount-path" v-model="settings['clouddrive_mount_path']" type="text" placeholder="/srv/clouddrive/CloudDrive" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="source-path" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">CloudDrive 源目录</label><input id="source-path" v-model="settings['clouddrive_source_path']" type="text" placeholder="115://Media" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="media-root" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">媒体源根目录</label><input id="media-root" v-model="settings['media_root']" type="text" placeholder="/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="strm-root" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">STRM 输出根目录</label><input id="strm-root" v-model="settings['strm_root']" type="text" placeholder="/srv/embymedia/data/strm" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div class="md:col-span-2"><label for="emby-media-prefix" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">Emby 媒体路径前缀</label><input id="emby-media-prefix" v-model="settings['emby_media_prefix']" type="text" placeholder="/media" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7">
        <div>
          <div class="flex items-center gap-2"><FileCode class="w-4 h-4 text-annotation" /><h3 class="font-serif font-semibold text-lg text-text">公共资源索引</h3></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">资源搜索上游地址；授权令牌按部署需要选填。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="resource-url" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">服务地址</label><input id="resource-url" v-model="settings['resource_api_url']" type="url" placeholder="http://gaotao.cc:8100" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          <div><label for="resource-token" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">授权令牌（可选）</label><input id="resource-token" v-model="settings['resource_api_token']" type="password" autocomplete="new-password" :placeholder="resourceTokenPlaceholder" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
        </div>
      </section>

      <details class="border-t border-border/60">
        <summary class="flex min-h-11 cursor-pointer items-center px-5 py-4 font-serif text-lg font-semibold text-text sm:px-7 hover:bg-bg-muted/30 transition-colors">高级设置</summary>
        <div class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 border-t border-border/60 p-5 sm:p-7">
          <div><div class="flex items-center gap-2"><Radio class="w-4 h-4 text-annotation" /><h3 class="font-serif font-semibold text-lg text-text">Webhook 与调优</h3></div><p class="mt-2 text-xs leading-5 text-text-faint">仅在 CloudDrive2 主动通知文件变化时需要。防抖用于合并短时间内连续到达的通知。</p></div>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div><label for="webhook-secret" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">Webhook Secret</label><input id="webhook-secret" v-model="settings['clouddrive_webhook_secret']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('clouddrive', '输入 Webhook Secret')" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
            <div><label for="webhook-delay" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-2 font-medium">防抖秒数</label><input id="webhook-delay" v-model="settings['clouddrive_webhook_debounce_seconds']" type="number" min="1" max="300" class="w-full min-h-11 px-3.5 rounded-xl border border-border/80 bg-bg text-sm font-mono focus:border-accent focus:outline-none transition-colors" /></div>
          </div>
        </div>
      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-t border-border/60">
        <div>
          <div class="flex items-center gap-2"><CircleAlert class="w-4 h-4 text-annotation" /><h3 class="font-serif font-semibold text-lg text-text">破坏性操作</h3></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">关闭时，文件删除 API 会以 403 拒绝，不会触达 115。</p>
        </div>
        <label class="inline-flex min-h-11 items-center gap-3 text-sm text-text cursor-pointer">
          <input v-model="settings['dangerous_actions_enabled']" type="checkbox" true-value="true" false-value="false" class="w-4 h-4 accent-accent" />
          <span>允许将 115 文件移入回收站</span>
        </label>
      </section>
      </details>
      </fieldset>

      <footer class="action-dock sticky bottom-0 z-10 flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-5 sm:px-7 border-t border-border/80 bg-surface/95 backdrop-blur-md shadow-[0_-8px_24px_rgba(31,30,29,0.06)]">
        <div class="min-h-6 text-sm">
          <span v-if="saveState === 'saved'" class="inline-flex items-start gap-2 text-ok"><Check class="w-4 h-4 mt-0.5 shrink-0" />{{ feedback }}</span>
          <span v-else-if="saveState === 'error'" class="inline-flex items-start gap-2 text-danger"><CircleAlert class="w-4 h-4 mt-0.5 shrink-0" />{{ feedback }}</span>
          <span v-else-if="dirty" class="text-text-muted">有尚未保存的修改。</span>
          <span v-else-if="!loaded" class="text-text-faint">读取设置后可编辑和保存。</span>
          <span v-else class="text-text-faint">当前页面与已保存设置一致。</span>
        </div>
        <button type="submit" :disabled="!loaded || !dirty || saveState === 'saving'" class="flex min-h-11 shrink-0 items-center justify-center gap-2 px-6 rounded-xl bg-accent hover:bg-accent-strong text-accent-contrast text-sm font-medium shadow-xs hover:shadow-sm transition-all disabled:cursor-not-allowed disabled:opacity-45" :title="!loaded ? '设置尚未读取成功' : !dirty ? '没有需要保存的修改' : undefined">
          <Loader2 v-if="saveState === 'saving'" class="w-4 h-4 animate-spin" />
          <Save v-else class="w-4 h-4" />
          <span>{{ saveState === 'saving' ? '正在保存' : '保存修改' }}</span>
        </button>
      </footer>
    </form>
  </div>
</template>

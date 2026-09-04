<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Check, CircleAlert, FileCode, HardDrive, Loader2, Radio, RefreshCw, Save, Tv } from 'lucide-vue-next'

const emptySettings: Record<string, string> = {
  '115_cookie': '',
  'emby_url': '',
  'emby_api_key': '',
  'clouddrive_url': '',
  'clouddrive_mount_path': '',
  'resource_api_url': '',
  'resource_api_token': '',
}
const settings = ref({ ...emptySettings })
const configured = ref<Record<string, boolean>>({ c115: false, emby: false, clouddrive: false, resource: false })
const health = ref<Record<string, { status: string; message: string; latency?: number; details?: string }>>({})
const checking = ref<Record<string, boolean>>({ c115: false, emby: false, clouddrive: false, resource: false })
const baseline = ref(JSON.stringify(settings.value))
const loaded = ref(false)
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const feedback = ref('')
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
  feedback.value = ''
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
  } catch (error) {
    saveState.value = 'error'
    feedback.value = error instanceof Error ? error.message : '无法读取设置'
  }
}

async function checkComponent(key: string) {
  checking.value[key] = true
  try {
    const response = await fetch('/api/v1/settings/check', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ component: key }),
    })
    if (response.ok) {
      const data = await response.json()
      if (data.health) {
        health.value = { ...health.value, [key]: data.health }
      }
    }
  } catch (e) {
    console.error(e)
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
  if (loaded.value && saveState.value === 'saved') {
    saveState.value = 'idle'
    feedback.value = ''
  }
}, { deep: true })

onMounted(fetchSettings)
</script>

<template>
  <div class="space-y-7 max-w-5xl">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-2xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">CONFIGURATION / PERSISTED STATE</p>
        <h1 class="font-serif text-3xl font-bold text-text">系统设置</h1>
        <p class="text-sm text-text-muted mt-2">管理服务地址与凭据。秘密值读取时始终保持隐藏。</p>
      </div>
      <div class="flex items-center gap-3 text-sm">
        <span class="font-mono text-xs text-text-faint">{{ configuredCount }} / 4 已配置</span>
        <span class="w-2 h-2 rounded-full" :class="configuredCount === 4 ? 'bg-ok' : 'bg-warn'"></span>
      </div>
    </header>

    <div class="grid grid-cols-2 lg:grid-cols-4 border border-border bg-surface rounded-xl overflow-hidden">
      <div v-for="item in integrationItems" :key="item.key" class="p-4 border-b border-r border-border last:border-r-0 lg:border-b-0 flex flex-col justify-between">
        <div class="flex items-center justify-between">
          <span class="text-xs font-medium text-text-muted">{{ item.label }}</span>
          <button type="button" :disabled="checking[item.key]" class="p-1 text-text-faint hover:text-text rounded transition-colors" title="测试连接" @click="checkComponent(item.key)">
            <Loader2 v-if="checking[item.key]" class="w-3 h-3 animate-spin text-accent" />
            <RefreshCw v-else class="w-3 h-3" />
          </button>
        </div>
        <div class="mt-2.5">
          <div class="flex items-center gap-2 text-xs font-mono">
            <span v-if="health[item.key]?.status === 'ok'" class="inline-flex items-center gap-1.5 text-ok font-semibold">
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
          <p v-if="health[item.key]?.message" class="text-[11px] text-text-faint mt-1 truncate" :title="health[item.key]?.message">
            {{ health[item.key]?.message }}
          </p>
        </div>
      </div>
    </div>

    <form class="border border-border bg-surface rounded-xl overflow-hidden" @submit.prevent="saveSettings">
      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border">
        <div>
          <div class="flex items-center gap-2"><HardDrive class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-lg">115 网盘</h2></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">用于账号鉴权。保存后不会再次返回明文。</p>
        </div>
        <div>
          <label for="c115-cookie" class="block text-xs font-mono text-text-muted mb-2">账号浏览器凭据</label>
          <input id="c115-cookie" v-model="settings['115_cookie']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('c115', c115Placeholder)" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" />
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border">
        <div>
          <div class="flex items-center gap-2"><Tv class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-lg">Emby</h2></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">媒体库读取与刷新所需的服务地址和密钥。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="emby-url" class="block text-xs font-mono text-text-muted mb-2">服务端地址</label><input id="emby-url" v-model="settings['emby_url']" type="url" placeholder="http://127.0.0.1:8096" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
          <div><label for="emby-key" class="block text-xs font-mono text-text-muted mb-2">接口密钥</label><input id="emby-key" v-model="settings['emby_api_key']" type="password" autocomplete="new-password" :placeholder="secretPlaceholder('emby', embyKeyPlaceholder)" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7 border-b border-border">
        <div>
          <div class="flex items-center gap-2"><Radio class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-lg">CloudDrive2</h2></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">本地挂载服务地址与媒体文件挂载目录。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="clouddrive-url" class="block text-xs font-mono text-text-muted mb-2">接口地址</label><input id="clouddrive-url" v-model="settings['clouddrive_url']" type="url" placeholder="http://127.0.0.1:19798" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
          <div><label for="mount-path" class="block text-xs font-mono text-text-muted mb-2">挂载目录</label><input id="mount-path" v-model="settings['clouddrive_mount_path']" type="text" placeholder="/Volumes/115" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
        </div>
      </section>

      <section class="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-6 p-5 sm:p-7">
        <div>
          <div class="flex items-center gap-2"><FileCode class="w-4 h-4 text-annotation" /><h2 class="font-serif font-semibold text-lg">公共资源索引</h2></div>
          <p class="mt-2 text-xs leading-5 text-text-faint">资源搜索上游地址；授权令牌按部署需要选填。</p>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="resource-url" class="block text-xs font-mono text-text-muted mb-2">服务地址</label><input id="resource-url" v-model="settings['resource_api_url']" type="url" placeholder="http://gaotao.cc:8100" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
          <div><label for="resource-token" class="block text-xs font-mono text-text-muted mb-2">授权令牌（可选）</label><input id="resource-token" v-model="settings['resource_api_token']" type="password" autocomplete="new-password" :placeholder="resourceTokenPlaceholder" class="w-full min-h-11 px-3 border border-border bg-bg text-sm font-mono focus:border-accent" /></div>
        </div>
      </section>

      <footer class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-5 sm:px-7 border-t border-border bg-bg-muted/45">
        <div class="min-h-6 text-sm">
          <span v-if="saveState === 'saved'" class="inline-flex items-start gap-2 text-ok"><Check class="w-4 h-4 mt-0.5 shrink-0" />{{ feedback }}</span>
          <span v-else-if="saveState === 'error'" class="inline-flex items-start gap-2 text-danger"><CircleAlert class="w-4 h-4 mt-0.5 shrink-0" />{{ feedback }}</span>
          <span v-else-if="dirty" class="text-text-muted">有尚未保存的修改。</span>
          <span v-else class="text-text-faint">当前页面与已保存设置一致。</span>
        </div>
        <button type="submit" :disabled="!dirty || saveState === 'saving'" class="flex min-h-11 shrink-0 items-center justify-center gap-2 px-5 bg-accent text-accent-contrast text-sm font-medium disabled:cursor-not-allowed disabled:opacity-45">
          <Loader2 v-if="saveState === 'saving'" class="w-4 h-4 animate-spin" />
          <Save v-else class="w-4 h-4" />
          <span>{{ saveState === 'saving' ? '正在保存' : '保存修改' }}</span>
        </button>
      </footer>
    </form>
  </div>
</template>

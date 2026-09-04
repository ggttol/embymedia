<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Bot, Key, Plus, Copy, Check, Terminal, FileCode, AlertCircle } from 'lucide-vue-next'

const tokens = ref<any[]>([])
const showCreateModal = ref(false)
const tokenName = ref('')
const selectedScopes = ref<string[]>(['read'])
const creationMessage = ref('')
const createdTokenSecret = ref<string | null>(null)
const copied = ref(false)

const mcpTools = [
  ['c115_list_files', '列出指定 CID 下的文件与目录'],
  ['c115_search', '按关键词搜索 115 网盘公开分享资源'],
  ['c115_save_share', '解析 115 分享链接并转存到指定目录'],
  ['c115_move', '移动文件或目录'],
  ['c115_rename', '重命名文件或目录'],
  ['c115_mkdir', '在指定目录下创建文件夹'],
  ['c115_get_share_link', '生成文件或目录分享链接'],
  ['cd2_mount_status', '检查 CloudDrive2 挂载状态'],
  ['cd2_remount', '重新加载 CloudDrive2 挂载'],
  ['emby_refresh_library', '触发 Emby 媒体库扫描'],
  ['emby_get_libraries', '读取 Emby 媒体库列表'],
  ['emby_inspect_item', '检查媒体条目元数据与图片'],
  ['task_submit', '提交异步后台任务'],
  ['task_query', '查询任务状态与进度'],
  ['task_cancel', '取消等待中或执行中的任务'],
  ['task_get_logs', '读取任务执行日志'],
  ['system_get_config', '读取系统设置与目录映射'],
  ['system_health', '检查各服务健康状态'],
]

const mcpEndpoint = computed(() => `${window.location.protocol}//${window.location.hostname}:3081/sse`)
const openApiEndpoint = computed(() => `${window.location.origin}/openapi.json`)

async function fetchTokens() {
  try {
    const response = await fetch('/api/v1/tokens')
    if (response.ok) {
      const data = await response.json()
      tokens.value = data.tokens ?? []
    }
  } catch (error) {
    console.error(error)
  }
}

async function createToken() {
  if (!tokenName.value.trim()) return
  creationMessage.value = ''
  try {
    const response = await fetch('/api/v1/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: tokenName.value.trim(), permissions: selectedScopes.value }),
    })
    if (!response.ok) {
      creationMessage.value = '创建失败，请检查服务日志。'
      return
    }
    const data = await response.json()
    createdTokenSecret.value = data.token ?? null
    creationMessage.value = createdTokenSecret.value
      ? '令牌已创建。明文只显示一次，请立即保存。'
      : '访问记录已创建，但当前 API 未返回令牌明文。'
    tokenName.value = ''
    await fetchTokens()
  } catch (error) {
    creationMessage.value = '无法连接服务，请稍后重试。'
  }
}

async function copyText(text: string) {
  await navigator.clipboard.writeText(text)
  copied.value = true
  window.setTimeout(() => (copied.value = false), 1600)
}

function closeModal() {
  showCreateModal.value = false
  createdTokenSecret.value = null
  creationMessage.value = ''
}

onMounted(fetchTokens)
</script>

<template>
  <div class="space-y-8">
    <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-3xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">AGENT GATEWAY / 18 TOOLS</p>
        <h1 class="font-serif text-3xl font-bold text-text">Agent 接入控制台</h1>
        <p class="text-sm text-text-muted mt-2">查看 MCP 与 OpenAPI 入口，核对工具清单并管理访问记录。</p>
      </div>
      <button @click="showCreateModal = true" class="flex min-h-11 items-center justify-center gap-2 px-4 py-2 rounded-lg bg-accent text-accent-contrast text-xs font-mono font-medium hover:bg-accent-strong transition-colors">
        <Plus class="w-4 h-4" /><span>创建访问记录</span>
      </button>
    </div>

    <section class="grid grid-cols-1 xl:grid-cols-[1.15fr_0.85fr] border border-border bg-surface rounded-xl overflow-hidden">
      <div class="p-6 sm:p-8 border-b xl:border-b-0 xl:border-r border-border">
        <div class="flex items-center gap-2 mb-5"><Terminal class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-lg">连接地址</h2></div>
        <dl class="space-y-5">
          <div>
            <dt class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint mb-2">MCP SSE</dt>
            <dd class="flex items-center gap-2 min-w-0"><code class="flex-1 min-w-0 overflow-x-auto border border-border bg-bg px-3 py-2 text-xs font-mono">{{ mcpEndpoint }}</code><button class="p-2 border border-border bg-bg hover:border-accent" aria-label="复制 MCP 地址" @click="copyText(mcpEndpoint)"><Check v-if="copied" class="w-4 h-4 text-ok" /><Copy v-else class="w-4 h-4" /></button></dd>
          </div>
          <div>
            <dt class="text-[10px] font-mono font-bold tracking-[0.14em] text-text-faint mb-2">OPENAPI 3.1</dt>
            <dd><a :href="openApiEndpoint" target="_blank" rel="noreferrer" class="inline-flex min-h-11 items-center gap-2 text-sm font-mono text-accent underline underline-offset-4"><FileCode class="w-4 h-4" />{{ openApiEndpoint }}</a></dd>
          </div>
        </dl>
      </div>
      <div class="p-6 sm:p-8 bg-accent text-accent-contrast">
        <Bot class="w-7 h-7 mb-8 opacity-80" />
        <p class="font-serif text-2xl leading-snug">同一业务能力，分别通过 MCP 与 HTTP 暴露。</p>
        <p class="mt-4 text-sm leading-7 opacity-80">客户端所需配置格式由对应客户端决定；此处只提供当前实例的真实入口。</p>
      </div>
    </section>

    <section>
      <div class="flex items-center justify-between gap-4 mb-4"><h2 class="font-serif font-semibold text-xl text-text">工具目录</h2><span class="font-mono text-xs text-text-faint">18 / 18</span></div>
      <ol class="grid grid-cols-1 lg:grid-cols-2 border-t border-border">
        <li v-for="(tool, index) in mcpTools" :key="tool[0]" class="grid grid-cols-[2.25rem_minmax(0,1fr)] gap-3 py-4 border-b border-border lg:odd:pr-6 lg:even:pl-6">
          <span class="font-mono text-xs text-annotation">{{ String(index + 1).padStart(2, '0') }}</span>
          <div class="min-w-0"><code class="text-xs font-semibold text-accent break-all">{{ tool[0] }}</code><p class="mt-1 text-sm text-text-muted">{{ tool[1] }}</p></div>
        </li>
      </ol>
    </section>

    <section>
      <div class="flex items-center gap-2 mb-4"><Key class="w-4 h-4 text-accent" /><h2 class="font-serif font-semibold text-xl text-text">访问记录</h2></div>
      <div class="rounded-xl border border-border bg-surface overflow-x-auto">
        <table class="w-full text-left text-xs font-mono">
          <thead class="border-b border-border bg-bg-muted/50 text-text-muted"><tr><th class="py-3 px-5">名称</th><th class="py-3 px-4">角色</th><th class="py-3 px-4">权限</th><th class="py-3 px-4">限流</th><th class="py-3 px-4">创建时间</th></tr></thead>
          <tbody class="divide-y divide-border/60">
            <tr v-for="token in tokens" :key="token.id"><td class="py-3 px-5 font-semibold">{{ token.name }}</td><td class="py-3 px-4">{{ token.role }}</td><td class="py-3 px-4">{{ (token.scopes || []).join(', ') || '—' }}</td><td class="py-3 px-4">{{ token.rate_limit }} 次/分</td><td class="py-3 px-4">{{ token.created_at ? new Date(token.created_at).toLocaleDateString() : '—' }}</td></tr>
            <tr v-if="tokens.length === 0"><td colspan="5" class="py-8 px-5 text-center text-text-faint">暂无访问记录</td></tr>
          </tbody>
        </table>
      </div>
    </section>

    <div v-if="showCreateModal" class="fixed inset-0 bg-black/45 flex items-center justify-center z-50 p-4" @click.self="closeModal">
      <div class="w-full max-w-md p-6 rounded-xl border border-border bg-surface shadow-xl space-y-5" role="dialog" aria-modal="true" aria-labelledby="token-dialog-title">
        <div><h3 id="token-dialog-title" class="font-serif font-semibold text-xl text-text">创建访问记录</h3><p class="text-sm text-text-muted mt-1">记录名称与最小必要权限。</p></div>
        <div v-if="creationMessage" class="p-3 border border-border bg-bg text-sm flex items-start gap-2"><AlertCircle class="w-4 h-4 mt-0.5 shrink-0 text-annotation" /><span>{{ creationMessage }}</span></div>
        <div v-if="createdTokenSecret" class="space-y-2"><label class="block text-xs font-mono text-text-muted">令牌明文</label><div class="flex gap-2"><input readonly :value="createdTokenSecret" class="min-w-0 flex-1 px-3 py-2 border border-border bg-bg text-xs font-mono" /><button class="px-3 border border-accent bg-accent text-accent-contrast" @click="copyText(createdTokenSecret)">复制</button></div></div>
        <template v-if="!creationMessage">
          <div><label for="token-name" class="block text-xs font-mono text-text-muted mb-2">记录名称</label><input id="token-name" v-model="tokenName" type="text" placeholder="例如：桌面客户端" class="w-full min-h-11 px-3 border border-border bg-bg text-sm focus:border-accent" /></div>
          <fieldset><legend class="block text-xs font-mono text-text-muted mb-2">权限</legend><label class="inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="read" />读取</label><label class="ml-5 inline-flex min-h-11 items-center gap-2"><input v-model="selectedScopes" type="checkbox" value="write" />写入</label></fieldset>
        </template>
        <div class="flex flex-col-reverse sm:flex-row justify-end gap-2 pt-2"><button @click="closeModal" class="min-h-11 px-4 border border-border text-sm">关闭</button><button v-if="!creationMessage" @click="createToken" class="min-h-11 px-4 bg-accent text-accent-contrast text-sm">创建记录</button></div>
      </div>
    </div>
  </div>
</template>

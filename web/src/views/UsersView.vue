<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CircleAlert, KeyRound, Loader2, Pencil, ShieldCheck, Trash2, UserPlus, UserRound, Users, X } from 'lucide-vue-next'

interface BrowserUser {
  username: string
  role: 'admin' | 'operator'
  enabled: boolean
  created_at: string
  updated_at: string
}

const users = ref<BrowserUser[]>([])
const currentUser = ref<BrowserUser | null>(null)
const loading = ref(true)
const saving = ref(false)
const message = ref('')
const error = ref('')
const showCreate = ref(false)
const editing = ref<BrowserUser | null>(null)
const createForm = ref({ username: '', password: '', role: 'operator' as BrowserUser['role'] })
const editForm = ref({ role: 'operator' as BrowserUser['role'], enabled: true, password: '' })
const enabledCount = computed(() => users.value.filter((user) => user.enabled).length)
const adminCount = computed(() => users.value.filter((user) => user.enabled && user.role === 'admin').length)

async function readResponse(response: Response) {
  const text = await response.text()
  const body = text ? JSON.parse(text) : {}
  if (response.status === 401) {
    window.location.assign(`/login?rd=${encodeURIComponent('/users')}`)
    throw new Error('登录已失效。')
  }
  if (!response.ok) throw new Error(body.error || `请求失败（HTTP ${response.status}）`)
  return body
}

async function loadUsers() {
  loading.value = true
  error.value = ''
  try {
    const [meResponse, usersResponse] = await Promise.all([
      fetch('/auth/me', { cache: 'no-store' }),
      fetch('/auth/users', { cache: 'no-store' }),
    ])
    const meData = await readResponse(meResponse)
    const usersData = await readResponse(usersResponse)
    currentUser.value = meData.user
    users.value = usersData.users ?? []
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '无法读取用户列表。'
  } finally {
    loading.value = false
  }
}

async function createUser() {
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    const response = await fetch('/auth/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(createForm.value),
    })
    const data = await readResponse(response)
    message.value = `已创建用户“${data.user.username}”。`
    createForm.value = { username: '', password: '', role: 'operator' }
    showCreate.value = false
    await loadUsers()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '创建用户失败。'
  } finally {
    saving.value = false
  }
}

function openEdit(user: BrowserUser) {
  editing.value = user
  editForm.value = { role: user.role, enabled: user.enabled, password: '' }
  message.value = ''
  error.value = ''
}

function closeEdit() {
  editing.value = null
  editForm.value.password = ''
}

async function saveUser() {
  if (!editing.value) return
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    const payload: Record<string, unknown> = {
      role: editForm.value.role,
      enabled: editForm.value.enabled,
    }
    if (editForm.value.password) payload.password = editForm.value.password
    const response = await fetch(`/auth/users/${encodeURIComponent(editing.value.username)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    const data = await readResponse(response)
    message.value = `已更新用户“${data.user.username}”。`
    closeEdit()
    await loadUsers()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '更新用户失败。'
  } finally {
    saving.value = false
  }
}

async function deleteUser(user: BrowserUser) {
  if (!window.confirm(`确认删除用户“${user.username}”？该用户之后无法登录。`)) return
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    const response = await fetch(`/auth/users/${encodeURIComponent(user.username)}`, { method: 'DELETE' })
    await readResponse(response)
    message.value = `已删除用户“${user.username}”。`
    await loadUsers()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '删除用户失败。'
  } finally {
    saving.value = false
  }
}

function roleLabel(role: BrowserUser['role']) {
  return role === 'admin' ? '管理员' : '操作员'
}

function roleDescription(role: BrowserUser['role']) {
  return role === 'admin' ? '可以管理用户，并使用全部媒体运维功能。' : '可以使用媒体运维功能，不能管理用户。'
}

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}

onMounted(() => { void loadUsers() })
</script>

<template>
  <div class="space-y-7 max-w-6xl">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-2xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">ACCESS / USER ROSTER</p>
        <h1 class="font-serif text-3xl font-bold text-text">用户管理</h1>
        <p class="mt-2 text-sm text-text-muted">创建浏览器登录用户，控制访问状态，并在需要时重置密码。</p>
      </div>
      <button type="button" class="inline-flex min-h-11 items-center justify-center gap-2 bg-accent px-4 text-sm font-medium text-accent-contrast" @click="showCreate = !showCreate">
        <X v-if="showCreate" class="w-4 h-4" /><UserPlus v-else class="w-4 h-4" />{{ showCreate ? '收起创建表单' : '创建用户' }}
      </button>
    </header>

    <div class="grid grid-cols-3 border border-border bg-surface">
      <div class="p-4 sm:p-5 border-r border-border"><strong class="block font-serif text-2xl text-text">{{ users.length }}</strong><span class="text-xs text-text-muted">全部用户</span></div>
      <div class="p-4 sm:p-5 border-r border-border"><strong class="block font-serif text-2xl text-ok">{{ enabledCount }}</strong><span class="text-xs text-text-muted">允许登录</span></div>
      <div class="p-4 sm:p-5"><strong class="block font-serif text-2xl text-accent">{{ adminCount }}</strong><span class="text-xs text-text-muted">管理员</span></div>
    </div>

    <p v-if="message" role="status" class="border-l-2 border-ok bg-ok/5 p-3 text-sm text-ok">{{ message }}</p>
    <p v-if="error" role="alert" class="flex items-start gap-2 border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger"><CircleAlert class="w-4 h-4 mt-0.5 shrink-0" />{{ error }}</p>

    <form v-if="showCreate" class="border border-border bg-surface shadow-sm" @submit.prevent="createUser">
      <div class="p-5 sm:p-7 border-b border-border"><div class="flex items-center gap-2"><UserPlus class="w-4 h-4 text-annotation" /><h2 class="font-serif text-xl font-semibold">创建登录用户</h2></div><p class="mt-1 text-sm text-text-muted">密码只用于验证，保存后不会再次显示。</p></div>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4 p-5 sm:p-7">
        <div><label for="new-username" class="block mb-2 text-xs font-medium text-text-muted">用户名</label><input id="new-username" v-model="createForm.username" required minlength="3" maxlength="64" pattern="[A-Za-z0-9._-]+" autocomplete="off" placeholder="例如：family" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
        <div><label for="new-password" class="block mb-2 text-xs font-medium text-text-muted">初始密码</label><input id="new-password" v-model="createForm.password" required minlength="10" maxlength="256" type="password" autocomplete="new-password" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
        <div><label for="new-role" class="block mb-2 text-xs font-medium text-text-muted">权限</label><select id="new-role" v-model="createForm.role" class="w-full min-h-11 border border-border bg-bg px-3 text-sm"><option value="operator">操作员</option><option value="admin">管理员</option></select></div>
      </div>
      <div class="flex justify-end gap-2 border-t border-border bg-bg-muted/60 p-5"><button type="button" class="min-h-11 border border-border bg-surface px-5 text-sm" @click="showCreate = false">取消</button><button type="submit" :disabled="saving" class="inline-flex min-h-11 items-center gap-2 bg-accent px-5 text-sm font-medium text-accent-contrast disabled:opacity-50"><Loader2 v-if="saving" class="w-4 h-4 animate-spin" />创建用户</button></div>
    </form>

    <section class="border border-border bg-surface" aria-labelledby="roster-heading">
      <div class="flex items-center justify-between gap-3 border-b border-border p-5 sm:px-7"><div><div class="flex items-center gap-2"><Users class="w-4 h-4 text-accent" /><h2 id="roster-heading" class="font-serif text-xl font-semibold">登录用户</h2></div><p class="mt-1 text-xs text-text-faint">停用会立即阻止新请求；重置密码会让该用户的新版会话失效。</p></div><span class="text-xs font-mono text-text-faint">{{ users.length }} 人</span></div>
      <div v-if="loading" class="flex items-center justify-center gap-2 p-10 text-sm text-text-muted"><Loader2 class="w-4 h-4 animate-spin" />正在读取用户</div>
      <div v-else-if="users.length === 0" class="p-10 text-center text-sm text-text-faint">还没有可管理的用户。</div>
      <article v-for="user in users" v-else :key="user.username" class="grid gap-4 border-b border-border p-5 last:border-b-0 sm:px-7 lg:grid-cols-[minmax(0,1.2fr)_minmax(180px,.7fr)_minmax(170px,.7fr)_auto] lg:items-center">
        <div class="flex items-center gap-3 min-w-0"><span class="grid w-10 h-10 shrink-0 place-items-center border border-border bg-bg"><ShieldCheck v-if="user.role === 'admin'" class="w-4 h-4 text-accent" /><UserRound v-else class="w-4 h-4 text-text-muted" /></span><div class="min-w-0"><div class="flex items-center gap-2"><strong class="truncate text-sm text-text">{{ user.username }}</strong><span v-if="currentUser?.username === user.username" class="bg-accent-soft px-2 py-0.5 text-[10px] text-accent">当前用户</span></div><p class="mt-1 text-xs text-text-faint">创建于 {{ formatTime(user.created_at) }}</p></div></div>
        <div><span class="text-xs font-medium" :class="user.enabled ? 'text-ok' : 'text-danger'">{{ user.enabled ? '允许登录' : '已停用' }}</span><p class="mt-1 text-xs text-text-faint">{{ user.enabled ? '账户可以建立会话' : '现有会话已被拒绝' }}</p></div>
        <div><strong class="text-sm text-text">{{ roleLabel(user.role) }}</strong><p class="mt-1 text-xs leading-5 text-text-faint">{{ roleDescription(user.role) }}</p></div>
        <div class="flex gap-2 lg:justify-end"><button type="button" class="inline-flex min-h-10 items-center gap-1.5 border border-border px-3 text-xs font-medium" @click="openEdit(user)"><Pencil class="w-3.5 h-3.5" />管理</button><button v-if="currentUser?.username !== user.username" type="button" :disabled="saving" class="inline-flex min-h-10 items-center gap-1.5 border border-danger/40 px-3 text-xs font-medium text-danger" @click="deleteUser(user)"><Trash2 class="w-3.5 h-3.5" />删除</button></div>
      </article>
    </section>

    <div v-if="editing" class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4" @click.self="closeEdit">
      <form class="w-full max-w-lg border border-border bg-surface shadow-xl" role="dialog" aria-modal="true" aria-labelledby="edit-user-title" @submit.prevent="saveUser">
        <div class="flex items-start justify-between gap-4 border-b border-border p-5"><div><h2 id="edit-user-title" class="font-serif text-xl font-semibold">管理 {{ editing.username }}</h2><p class="mt-1 text-sm text-text-muted">修改权限、登录状态，或设置一个新密码。</p></div><button type="button" class="p-2 text-text-faint" aria-label="关闭" @click="closeEdit"><X class="w-4 h-4" /></button></div>
        <div class="space-y-4 p-5 sm:p-7">
          <div><label for="edit-role" class="block mb-2 text-xs font-medium text-text-muted">权限</label><select id="edit-role" v-model="editForm.role" :disabled="currentUser?.username === editing.username" class="w-full min-h-11 border border-border bg-bg px-3 text-sm disabled:opacity-60"><option value="operator">操作员</option><option value="admin">管理员</option></select></div>
          <label class="flex min-h-11 items-center gap-3 border border-border bg-bg px-3 text-sm"><input v-model="editForm.enabled" type="checkbox" :disabled="currentUser?.username === editing.username" /><span>{{ editForm.enabled ? '允许该用户登录' : '停用该用户' }}</span></label>
          <div><label for="reset-password" class="block mb-2 text-xs font-medium text-text-muted"><KeyRound class="inline w-3.5 h-3.5 mr-1" />设置新密码（可选）</label><input id="reset-password" v-model="editForm.password" minlength="10" maxlength="256" type="password" autocomplete="new-password" placeholder="留空时不修改密码" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
        </div>
        <div class="flex justify-end gap-2 border-t border-border bg-bg-muted/60 p-5"><button type="button" class="min-h-11 border border-border bg-surface px-5 text-sm" @click="closeEdit">取消</button><button type="submit" :disabled="saving" class="inline-flex min-h-11 items-center gap-2 bg-accent px-5 text-sm font-medium text-accent-contrast disabled:opacity-50"><Loader2 v-if="saving" class="w-4 h-4 animate-spin" />保存修改</button></div>
      </form>
    </div>
  </div>
</template>

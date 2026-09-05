<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  Folder,
  File,
  HardDrive,
  FolderPlus,
  RefreshCw,
  Trash2,
  Edit2,
  Loader2,
  Users,
  FolderInput,
  UserMinus,
  UserPlus
} from 'lucide-vue-next'
const currentCid = ref('0')
const cidMap = ref<Record<string, string>>({})
const actionError = ref('')
const selectedFiles = ref<Set<string>>(new Set())
const moveTargetCid = ref('')

async function fetchCidMap() {
  try {
    const response = await fetch('/api/v1/cid-map')
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取目录映射失败')
    cidMap.value = data.map ?? {}
    if (!moveTargetCid.value) moveTargetCid.value = Object.values(cidMap.value)[0] || '0'
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '读取目录映射失败'
  }
}

function jumpToCid(name: string, cid: string) {
  currentCid.value = cid
  breadcrumbs.value = [
    { cid: '0', name: '根目录' },
    { cid, name }
  ]
  fetchFiles()
}

async function changeAccount() {
  currentCid.value = '0'
  breadcrumbs.value = [{ cid: '0', name: '根目录' }]
  selectedFiles.value.clear()
  await fetchFiles()
}


function toggleSelection(fileId: string) {
  if (selectedFiles.value.has(fileId)) {
    selectedFiles.value.delete(fileId)
  } else {
    selectedFiles.value.add(fileId)
  }
}

function toggleAll(e: Event) {
  const checked = (e.target as HTMLInputElement).checked
  if (checked) {
    files.value.forEach((f: any) => selectedFiles.value.add(f.file_id || f.cid))
  } else {
    selectedFiles.value.clear()
  }
}

function isSelected(fileId: string) {
  return selectedFiles.value.has(fileId)
}

async function batchDelete() {
  if (selectedFiles.value.size === 0) return
  if (!confirm(`确认批量删除选中的 ${selectedFiles.value.size} 个项目？此操作会移入回收站。`)) return

  loading.value = true
  actionError.value = ''
  try {
    const fileIds = Array.from(selectedFiles.value)
    const res = await fetch(`/api/v1/files/delete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        file_ids: fileIds
      })
    })
    if (res.ok) {
      selectedFiles.value.clear()
      fetchFiles()
    } else {
      const data = await res.json()
      actionError.value = data.error || '批量删除失败'
    }
  } catch (error: any) {
    actionError.value = error.message
  } finally {
    loading.value = false
  }
}

async function batchMove() {
  if (selectedFiles.value.size === 0 || !moveTargetCid.value) return
  if (!confirm(`确认移动选中的 ${selectedFiles.value.size} 个项目？`)) return
  loading.value = true
  actionError.value = ''
  try {
    const response = await fetch('/api/v1/files/move', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        file_ids: Array.from(selectedFiles.value),
        target_cid: moveTargetCid.value,
      }),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '批量移动失败')
    selectedFiles.value.clear()
    await fetchFiles()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '批量移动失败'
  } finally {
    loading.value = false
  }
}

async function deleteFile(file: any) {
  if (!confirm(`确认删除「${file.name}」？此操作会移入回收站。`)) return
  actionError.value = ''
  try {
    const res = await fetch('/api/v1/files/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        file_ids: [file.file_id || file.cid]
      })
    })
    if (!res.ok) {
      const err = await res.json()
      actionError.value = err.error || '删除失败'
      return
    }
    fetchFiles()
  } catch (e) {
    actionError.value = '网络请求错误'
  }
}

async function renameFile(file: any) {
  const newName = prompt('输入新名称', file.name)
  if (!newName || newName === file.name) return
  actionError.value = ''
  try {
    const res = await fetch('/api/v1/files/rename', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        file_id: file.file_id || file.cid,
        new_name: newName
      })
    })
    if (!res.ok) {
      const err = await res.json()
      actionError.value = err.error || '重命名失败'
      return
    }
    fetchFiles()
  } catch (e) {
    actionError.value = '网络请求错误'
  }
}

const breadcrumbs = ref<{ cid: string; name: string }[]>([
  { cid: '0', name: '根目录' }
])
const files = ref<any[]>([])
const accounts = ref<any[]>([])
const currentAccountId = ref('')
const loading = ref(false)
const showNewFolderModal = ref(false)
const newFolderName = ref('')
const showAccountModal = ref(false)
const newAccount = ref({ name: '', cookie: '', is_default: false })
const accountBusy = ref(false)

async function fetchAccounts() {
  try {
    const response = await fetch('/api/v1/accounts')
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取 115 账号失败')
    accounts.value = data.accounts ?? []
    if (accounts.value.length > 0) {
      const defaultAccount = accounts.value.find((account: any) => account.is_default) || accounts.value[0]
      currentAccountId.value = defaultAccount.id
    }
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '读取 115 账号失败'
  }
}

async function createAccount() {
  actionError.value = ''
  accountBusy.value = true
  try {
    const response = await fetch('/api/v1/accounts', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(newAccount.value),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '添加账号失败')
    newAccount.value = { name: '', cookie: '', is_default: false }
    showAccountModal.value = false
    await fetchAccounts()
    currentAccountId.value = data.id
    await changeAccount()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '添加账号失败'
  } finally {
    accountBusy.value = false
  }
}

async function deleteCurrentAccount() {
  if (!currentAccountId.value || !confirm('确认移除当前 115 账号？不会删除网盘文件。')) return
  accountBusy.value = true
  try {
    const response = await fetch(`/api/v1/accounts/${encodeURIComponent(currentAccountId.value)}`, { method: 'DELETE' })
    if (!response.ok) {
      const data = await response.json()
      throw new Error(data.error || '移除账号失败')
    }
    currentAccountId.value = ''
    await fetchAccounts()
    await changeAccount()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '移除账号失败'
  } finally {
    accountBusy.value = false
  }
}

async function fetchFiles() {
  selectedFiles.value.clear()
  loading.value = true
  actionError.value = ''
  try {
    const params = new URLSearchParams()
    if (currentAccountId.value) params.set('account_id', currentAccountId.value)
    params.set('cid', currentCid.value)
    const response = await fetch(`/api/v1/files?${params.toString()}`)
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取目录失败')
    files.value = data.files ?? []
  } catch (error) {
    files.value = []
    actionError.value = error instanceof Error ? error.message : '读取目录失败'
  } finally {
    loading.value = false
  }
}

function enterFolder(file: any) {
  const fileId = file.file_id || file.cid
  currentCid.value = fileId
  breadcrumbs.value.push({ cid: fileId, name: file.name })
  fetchFiles()
}

function navigateToBreadcrumb(idx: number) {
  const target = breadcrumbs.value[idx]
  currentCid.value = target.cid
  breadcrumbs.value = breadcrumbs.value.slice(0, idx + 1)
  fetchFiles()
}

async function createFolder() {
  if (!newFolderName.value.trim()) return
  actionError.value = ''
  try {
    const response = await fetch('/api/v1/files/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ account_id: currentAccountId.value, parent_cid: currentCid.value, name: newFolderName.value.trim() }),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '创建文件夹失败')
    newFolderName.value = ''
    showNewFolderModal.value = false
    await fetchFiles()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : '创建文件夹失败'
  }
}

onMounted(async () => {
  await fetchAccounts()
  await fetchCidMap()
  fetchFiles()
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col lg:flex-row lg:items-center justify-between gap-4 pb-6 border-b border-border">
      <div>
        <h1 class="font-serif text-2xl font-bold text-text">115 网盘文件管理</h1>
        <p class="text-sm text-text-muted mt-1 font-mono">
          多账号支持 · 树形目录浏览 · 离线任务推送 · 挂载点映射
        </p>
      </div>

      <!-- Account Selector & Tools -->
      <div class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-2 bg-surface px-3 py-1.5 rounded-lg border border-border">
          <Users class="w-3.5 h-3.5 text-text-muted" />
          <select
            v-model="currentAccountId"
            @change="changeAccount"
            class="bg-transparent text-xs font-mono text-text focus:outline-none cursor-pointer"
          >
            <option v-for="acc in accounts" :key="acc.id" :value="acc.id">
              {{ acc.name }} {{ acc.is_default ? '(默认)' : '' }}
            </option>
            <option v-if="accounts.length === 0" value="">默认账号 (Cookie)</option>
          </select>
        </div>

		<button type="button" :disabled="accountBusy" @click="showAccountModal = true" class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border bg-surface text-xs font-mono disabled:opacity-50"><UserPlus class="w-3.5 h-3.5" />添加账号</button>
		<button v-if="currentAccountId" type="button" :disabled="accountBusy" @click="deleteCurrentAccount" class="p-2 rounded-lg border border-border bg-surface text-text-faint hover:text-danger disabled:opacity-50" aria-label="移除当前账号"><UserMinus class="w-3.5 h-3.5" /></button>
        <button type="button" @click="showNewFolderModal = true" class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border hover:border-text-muted bg-surface text-xs font-mono text-text transition-colors">
          <FolderPlus class="w-3.5 h-3.5" />
          <span>新建文件夹</span>
        </button>

        <!-- Batch Action: only show if files selected -->
        <button
          v-if="selectedFiles.size > 0"
          @click="batchDelete"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-danger/30 hover:border-danger text-danger bg-danger/5 transition-colors text-xs font-mono"
        >
          <Trash2 class="w-3.5 h-3.5" />
          <span>批量删除 ({{ selectedFiles.size }})</span>
        </button>


        <template v-if="selectedFiles.size > 0">
          <select v-model="moveTargetCid" class="min-h-9 max-w-40 rounded-lg border border-border bg-surface px-2 text-xs font-mono" aria-label="批量移动目标目录">
            <option value="0">根目录</option>
            <option v-for="(cid, name) in cidMap" :key="cid" :value="cid">{{ name }}</option>
          </select>
          <button type="button" @click="batchMove" class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-accent bg-surface text-xs font-mono text-accent">
            <FolderInput class="w-3.5 h-3.5" />批量移动
          </button>
        </template>

        <button
          @click="fetchFiles"
          :disabled="loading"
          class="p-2 rounded-lg border border-border hover:border-text-muted bg-surface text-text-muted hover:text-text transition-colors"
          aria-label="刷新文件列表"
        >
          <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>
    <!-- CID Map Shortcuts -->
    <div v-if="Object.keys(cidMap).length > 0" class="p-4 rounded-xl border border-border bg-surface flex items-center gap-2 flex-wrap text-xs font-mono">
      <div class="flex items-center gap-1.5 text-text-muted">
        <FolderInput class="w-3.5 h-3.5" />
        <span>分类目录:</span>
      </div>
      <button
        v-for="(cid, name) in cidMap"
        :key="cid"
        @click="jumpToCid(String(name), cid)"
        class="px-2.5 py-1 rounded-md border transition-colors"
        :class="currentCid === cid ? 'border-accent bg-accent-soft text-accent font-semibold' : 'border-border bg-bg text-text-muted hover:border-text-muted'"
      >
        {{ name }}
      </button>
    </div>

    <!-- Breadcrumb Path Bar -->
    <div class="p-4 rounded-xl border border-border bg-surface space-y-2">
      <div class="flex items-center gap-2 text-xs font-mono text-text-muted">
        <HardDrive class="w-4 h-4 text-accent shrink-0" />
        <div class="flex items-center gap-2 flex-wrap">
          <template v-for="(b, idx) in breadcrumbs" :key="b.cid">
            <button
              @click="navigateToBreadcrumb(idx)"
              class="hover:text-text hover:underline transition-colors"
              :class="{ 'text-text font-semibold': idx === breadcrumbs.length - 1 }"
            >
              {{ b.name }}
            </button>
            <span v-if="idx < breadcrumbs.length - 1" class="text-text-faint">/</span>
          </template>
        </div>
      </div>
      <div v-if="actionError" class="text-xs font-mono text-danger pl-6">{{ actionError }}</div>
    </div>

    <!-- File Table / Explorer -->
    <div class="rounded-xl border border-border bg-surface overflow-hidden shadow-sm">
      <div v-if="loading" class="p-12 text-center">
        <Loader2 class="w-6 h-6 animate-spin text-accent mx-auto mb-2" />
        <span class="text-xs font-mono text-text-muted">正在加载目录列表...</span>
      </div>

      <div v-else-if="files.length === 0" class="p-12 text-center text-text-faint text-xs font-mono">
        此文件夹为空
      </div>

      <table v-else class="w-full text-left text-xs font-mono">
        <thead class="border-b border-border bg-bg-muted/50 text-text-muted uppercase tracking-wider">
          <tr>
            <th class="py-3 px-5 w-12">
              <input
                type="checkbox"
                :checked="files.length > 0 && selectedFiles.size === files.length"
                @change="toggleAll"
                class="rounded border-border text-accent focus:ring-accent accent-accent bg-bg"
              />
            </th>
            <th class="py-3 px-5">名称</th>
            <th class="py-3 px-4 w-32">大小</th>
            <th class="py-3 px-4 w-44">修改日期</th>
            <th class="py-3 px-4 w-28 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border/60">
          <tr
            v-for="file in files"
            :key="file.file_id || file.cid"
            class="transition-colors group"
            :class="isSelected(file.file_id || file.cid) ? 'bg-accent-soft/30' : 'hover:bg-bg-muted/30'"
          >
            <td class="py-3 px-5 w-12" @click.stop>
              <input
                type="checkbox"
                :checked="isSelected(file.file_id || file.cid)"
                @change="toggleSelection(file.file_id || file.cid)"
                class="rounded border-border text-accent focus:ring-accent accent-accent bg-bg"
              />
            </td>
            <td class="py-3 px-5 flex items-center gap-3">
              <Folder v-if="file.is_folder" class="w-4 h-4 text-accent shrink-0" />
              <File v-else class="w-4 h-4 text-text-faint shrink-0" />

              <button
                v-if="file.is_folder"
                @click="enterFolder(file)"
                class="text-text font-medium hover:text-accent truncate text-left"
              >
                {{ file.name }}
              </button>
              <span v-else class="text-text truncate">{{ file.name }}</span>
            </td>
            <td class="py-3 px-4 text-text-faint">
              {{ file.is_folder ? '-' : (file.size ? (file.size / (1024*1024)).toFixed(1) + ' MB' : '-') }}
            </td>
            <td class="py-3 px-4 text-text-faint">
              {{ file.updated_time ? new Date(file.updated_time).toLocaleDateString() : '-' }}
            </td>
            <td class="py-3 px-4 text-right whitespace-nowrap">
              <button
                @click="renameFile(file)"
				class="p-1 text-text-faint hover:text-accent opacity-100 sm:opacity-0 sm:group-hover:opacity-100 focus:opacity-100 transition-opacity mr-1"
                title="重命名"
              >
                <Edit2 class="w-3.5 h-3.5" />
              </button>
              <button
                @click="deleteFile(file)"
				class="p-1 text-text-faint hover:text-danger opacity-100 sm:opacity-0 sm:group-hover:opacity-100 focus:opacity-100 transition-opacity"
                title="删除"
              >
                <Trash2 class="w-3.5 h-3.5" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- New Folder Modal -->
    <div v-if="showNewFolderModal" class="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4" @click.self="showNewFolderModal = false">
      <div class="w-full max-w-md p-6 rounded-xl border border-border bg-surface shadow-lg space-y-4" role="dialog" aria-modal="true" aria-label="新建文件夹">
        <h3 class="font-serif font-semibold text-base text-text">新建文件夹</h3>
        <input v-model="newFolderName" type="text" placeholder="请输入文件夹名称" class="w-full min-h-11 px-3.5 border border-border bg-bg text-text text-sm" />
        <div class="flex justify-end gap-2"><button type="button" @click="showNewFolderModal = false" class="min-h-11 px-4 border border-border text-sm">取消</button><button type="button" @click="createFolder" class="min-h-11 px-4 bg-accent text-accent-contrast text-sm">创建</button></div>
      </div>
    </div>

    <div v-if="showAccountModal" class="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4" @click.self="showAccountModal = false">
      <form class="w-full max-w-md p-6 rounded-xl border border-border bg-surface shadow-lg space-y-4" role="dialog" aria-modal="true" aria-label="添加 115 账号" @submit.prevent="createAccount">
        <h3 class="font-serif font-semibold text-base text-text">添加 115 账号</h3>
		<div><label for="account-name" class="block mb-2 text-xs font-mono text-text-muted">名称</label><input id="account-name" v-model="newAccount.name" required class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
		<div><label for="account-cookie" class="block mb-2 text-xs font-mono text-text-muted">浏览器 Cookie</label><input id="account-cookie" v-model="newAccount.cookie" type="password" autocomplete="new-password" required class="w-full min-h-11 border border-border bg-bg px-3 text-sm font-mono" /></div>
        <label class="inline-flex items-center gap-2 text-sm"><input v-model="newAccount.is_default" type="checkbox" />设为默认账号</label>
		<div class="flex justify-end gap-2"><button type="button" :disabled="accountBusy" class="min-h-11 px-4 border border-border" @click="showAccountModal = false">取消</button><button type="submit" :disabled="accountBusy" class="min-h-11 px-4 bg-accent text-accent-contrast disabled:opacity-50">{{ accountBusy ? '正在保存' : '保存账号' }}</button></div>
      </form>
    </div>
  </div>
</template>

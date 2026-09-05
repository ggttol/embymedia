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
  FolderInput
} from 'lucide-vue-next'
const currentCid = ref('0')
const cidMap = ref<Record<string, string>>({})
const actionError = ref('')
const selectedFiles = ref<Set<string>>(new Set())

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
}

function jumpToCid(name: string, cid: string) {
  currentCid.value = cid
  breadcrumbs.value = [
    { cid: '0', name: '根目录' },
    { cid, name }
  ]
  fetchFiles()
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

async function fetchAccounts() {
  try {
    const res = await fetch('/api/v1/accounts')
    if (res.ok) {
      const data = await res.json()
      accounts.value = data.accounts ?? []
      if (accounts.value.length > 0) {
        const def = accounts.value.find((account: any) => account.is_default) || accounts.value[0]
        currentAccountId.value = def.id
      }
    }
  } catch (e) {
    console.error(e)
  }
}

async function fetchFiles() {
  loading.value = true
  try {
    const params = new URLSearchParams()
    if (currentAccountId.value) params.set('account_id', currentAccountId.value)
    params.set('cid', currentCid.value)
    const res = await fetch(`/api/v1/files?${params.toString()}`)
    if (res.ok) {
      const data = await res.json()
      files.value = data.files ?? []
    }
  } catch (e) {
    console.error(e)
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
  if (!newFolderName.value) return
  try {
    const res = await fetch('/api/v1/files/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        parent_cid: currentCid.value,
        name: newFolderName.value
      })
    })
    if (res.ok) {
      newFolderName.value = ''
      showNewFolderModal.value = false
      fetchFiles()
    }
  } catch (e) {
    console.error(e)
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
    <div class="flex items-center justify-between pb-6 border-b border-border">
      <div>
        <h1 class="font-serif text-2xl font-bold text-text">115 网盘文件管理</h1>
        <p class="text-sm text-text-muted mt-1 font-mono">
          多账号支持 · 树形目录浏览 · 离线任务推送 · 挂载点映射
        </p>
      </div>

      <!-- Account Selector & Tools -->
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-2 bg-surface px-3 py-1.5 rounded-lg border border-border">
          <Users class="w-3.5 h-3.5 text-text-muted" />
          <select
            v-model="currentAccountId"
            @change="fetchFiles"
            class="bg-transparent text-xs font-mono text-text focus:outline-none cursor-pointer"
          >
            <option v-for="acc in accounts" :key="acc.id" :value="acc.id">
              {{ acc.name }} {{ acc.is_default ? '(默认)' : '' }}
            </option>
            <option v-if="accounts.length === 0" value="">默认账号 (Cookie)</option>
          </select>
        </div>

        <button
          @click="showNewFolderModal = true"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border hover:border-text-muted bg-surface text-xs font-mono text-text transition-colors"
        >
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


        <button
          @click="fetchFiles"
          :disabled="loading"
          class="p-2 rounded-lg border border-border hover:border-text-muted bg-surface text-text-muted hover:text-text transition-colors"
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
                class="p-1 text-text-faint hover:text-accent opacity-0 group-hover:opacity-100 transition-opacity mr-1"
                title="重命名"
              >
                <Edit2 class="w-3.5 h-3.5" />
              </button>
              <button
                @click="deleteFile(file)"
                class="p-1 text-text-faint hover:text-danger opacity-0 group-hover:opacity-100 transition-opacity"
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
    <div
      v-if="showNewFolderModal"
      class="fixed inset-0 bg-black/40 backdrop-blur-xs flex items-center justify-center z-50 p-4"
    >
      <div class="w-full max-w-md p-6 rounded-xl border border-border bg-surface shadow-lg space-y-4">
        <h3 class="font-serif font-semibold text-base text-text">新建文件夹</h3>
        <input
          v-model="newFolderName"
          type="text"
          placeholder="请输入文件夹名称..."
          class="w-full px-3.5 py-2 rounded-lg border border-border bg-bg text-text text-xs font-sans focus:outline-none focus:border-accent"
        />
        <div class="flex justify-end gap-2 pt-2">
          <button
            @click="showNewFolderModal = false"
            class="px-4 py-2 rounded-lg border border-border text-xs font-mono text-text-muted hover:bg-bg-muted"
          >
            取消
          </button>
          <button
            @click="createFolder"
            class="px-4 py-2 rounded-lg bg-accent text-accent-contrast text-xs font-mono hover:bg-accent-strong"
          >
            创建
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

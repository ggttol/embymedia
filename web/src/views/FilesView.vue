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
  Download,
  Upload,
  ArrowLeft,
  Loader2,
  Users
} from 'lucide-vue-next'

const currentCid = ref('0')
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
  currentCid.value = file.cid
  breadcrumbs.value.push({ cid: file.cid, name: file.name })
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

        <button
          @click="fetchFiles"
          :disabled="loading"
          class="p-2 rounded-lg border border-border hover:border-text-muted bg-surface text-text-muted hover:text-text transition-colors"
        >
          <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>

    <!-- Breadcrumb Path Bar -->
    <div class="p-4 rounded-xl border border-border bg-surface flex items-center gap-2 text-xs font-mono text-text-muted">
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
            class="hover:bg-bg-muted/30 transition-colors group"
          >
            <td class="py-3 px-5 flex items-center gap-3">
              <Folder v-if="file.is_dir" class="w-4 h-4 text-accent shrink-0" />
              <File v-else class="w-4 h-4 text-text-faint shrink-0" />

              <button
                v-if="file.is_dir"
                @click="enterFolder(file)"
                class="text-text font-medium hover:text-accent truncate text-left"
              >
                {{ file.name }}
              </button>
              <span v-else class="text-text truncate">{{ file.name }}</span>
            </td>
            <td class="py-3 px-4 text-text-faint">
              {{ file.is_dir ? '-' : (file.size ? (file.size / (1024*1024)).toFixed(1) + ' MB' : '-') }}
            </td>
            <td class="py-3 px-4 text-text-faint">
              {{ file.updated_at ? new Date(file.updated_at).toLocaleDateString() : '-' }}
            </td>
            <td class="py-3 px-4 text-right">
              <button
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

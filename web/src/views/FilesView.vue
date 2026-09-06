<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Folder, File, HardDrive, FolderPlus, RefreshCw, Trash2, Edit2, Loader2, Users, FolderInput, UserMinus, UserPlus, Link2 } from 'lucide-vue-next'
import UiDialog from '../components/UiDialog.vue'

type DriveFile = { file_id?: string; cid?: string; name: string; is_folder: boolean; size?: number; updated_time?: string }
type DriveAccount = { id: string; name: string; is_default: boolean }
type FileOperation = { kind: 'move' | 'delete' | 'rename'; ids: string[]; name: string }

const currentCid = ref('0')
const cidMap = ref<Record<string, string>>({})
const mapError = ref('')
const mapLoading = ref(false)
const selectedFiles = ref<Set<string>>(new Set())
const moveTargetCid = ref('0')
const breadcrumbs = ref([{ cid: '0', name: '根目录' }])
const files = ref<DriveFile[]>([])
const accounts = ref<DriveAccount[]>([])
const currentAccountId = ref('')
const accountsLoading = ref(true)
const accountsError = ref('')
const loading = ref(false)
const filesError = ref('')
const success = ref('')
const accountSuccess = ref('')
const accountActionError = ref('')
const showNewFolderModal = ref(false)
const newFolderName = ref('')
const folderError = ref('')
const folderBusy = ref(false)
const showAccountModal = ref(false)
const newAccount = ref({ name: '', cookie: '', is_default: false })
const accountError = ref('')
const accountBusy = ref(false)
const showShareModal = ref(false)
const shareForm = ref({ url: '', password: '' })
const shareError = ref('')
const shareBusy = ref(false)
const operation = ref<FileOperation | null>(null)
const operationError = ref('')
const operationBusy = ref(false)
const renameValue = ref('')
const busy = computed(() => loading.value || accountsLoading.value || accountBusy.value || folderBusy.value || shareBusy.value || operationBusy.value)
const accountName = computed(() => accounts.value.find(account => account.id === currentAccountId.value)?.name || '未选择账号')
const directoryName = computed(() => breadcrumbs.value.map(item => item.name).join(' / '))
const moveTargetName = computed(() => moveTargetCid.value === '0' ? '根目录' : Object.entries(cidMap.value).find(([, cid]) => cid === moveTargetCid.value)?.[0] || moveTargetCid.value)
const allSelected = computed(() => files.value.length > 0 && selectedFiles.value.size === files.value.length)
const partlySelected = computed(() => selectedFiles.value.size > 0 && !allSelected.value)
const operationTitle = computed(() => operation.value?.kind === 'move' ? '确认移动' : operation.value?.kind === 'delete' ? '确认删除' : '重命名')

function fileId(file: DriveFile) { return String(file.file_id || file.cid || '') }
function fileSize(file: DriveFile) { return file.is_folder ? '文件夹' : file.size == null ? '大小未知' : `${(file.size / (1024 * 1024)).toFixed(1)} MB` }
function fileDate(file: DriveFile) { return file.updated_time ? new Date(file.updated_time).toLocaleDateString() : '日期未知' }
function errorMessage(error: unknown) { return error instanceof Error ? error.message : '网络请求失败，请重试。' }

async function requireOk(response: Response, message: string) {
  if (response.ok) return
  const data = await response.json().catch(() => null)
  throw new Error(data?.error || `${message}（HTTP ${response.status}）`)
}

async function fetchCidMap() {
  mapError.value = ''
  mapLoading.value = true
  try {
    const response = await fetch('/api/v1/cid-map')
    await requireOk(response, '读取分类目录失败')
    const data = await response.json()
    cidMap.value = data.map ?? {}
  } catch (error) {
    mapError.value = errorMessage(error)
  } finally {
    mapLoading.value = false
  }
}

async function fetchAccounts(preferredId = currentAccountId.value) {
  accountsLoading.value = true
  accountsError.value = ''
  try {
    const response = await fetch('/api/v1/accounts')
    await requireOk(response, '读取 115 账号失败')
    const data = await response.json()
    accounts.value = data.accounts ?? []
    const account = accounts.value.find(item => item.id === preferredId) || accounts.value.find(item => item.is_default) || accounts.value[0]
    currentAccountId.value = account?.id || ''
  } catch (error) {
    accountsError.value = errorMessage(error)
  } finally {
    accountsLoading.value = false
  }
}

async function reloadAccounts() {
  await fetchAccounts()
  await changeAccount()
}

async function fetchFiles() {
  selectedFiles.value.clear()
  filesError.value = ''
  files.value = []
  if (!currentAccountId.value || accountsError.value) return
  loading.value = true
  try {
    const params = new URLSearchParams({ account_id: currentAccountId.value, cid: currentCid.value })
    const response = await fetch(`/api/v1/files?${params}`)
    await requireOk(response, '读取目录失败')
    const data = await response.json()
    files.value = data.files ?? []
  } catch (error) {
    filesError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

async function changeAccount() {
  currentCid.value = '0'
  breadcrumbs.value = [{ cid: '0', name: '根目录' }]
  success.value = ''
  await fetchFiles()
}

function jumpToCid(name: string, cid: string) {
  currentCid.value = cid
  breadcrumbs.value = cid === '0' ? [{ cid: '0', name: '根目录' }] : [{ cid: '0', name: '根目录' }, { cid, name }]
  success.value = ''
  fetchFiles()
}

function enterFolder(file: DriveFile) {
  currentCid.value = fileId(file)
  breadcrumbs.value.push({ cid: currentCid.value, name: file.name })
  success.value = ''
  fetchFiles()
}

function navigateToBreadcrumb(index: number) {
  const target = breadcrumbs.value[index]
  if (!target) return
  currentCid.value = target.cid
  breadcrumbs.value = breadcrumbs.value.slice(0, index + 1)
  success.value = ''
  fetchFiles()
}

function toggleSelection(id: string) {
  if (selectedFiles.value.has(id)) selectedFiles.value.delete(id)
  else selectedFiles.value.add(id)
}

function toggleAll(event: Event) {
  selectedFiles.value = (event.target as HTMLInputElement).checked ? new Set(files.value.map(fileId)) : new Set()
}

function openOperation(kind: FileOperation['kind'], file?: DriveFile) {
  const ids = file ? [fileId(file)] : Array.from(selectedFiles.value)
  if (!ids.length) return
  operation.value = { kind, ids, name: file?.name || '' }
  renameValue.value = file?.name || ''
  operationError.value = ''
}

async function submitOperation() {
  const action = operation.value
  if (!action || operationBusy.value) return
  if (action.kind === 'rename' && !renameValue.value.trim()) {
    operationError.value = '请输入新名称。'
    return
  }
  operationBusy.value = true
  operationError.value = ''
  success.value = ''
  try {
    const body = action.kind === 'rename'
      ? { account_id: currentAccountId.value, file_id: action.ids[0], new_name: renameValue.value.trim() }
      : { account_id: currentAccountId.value, file_ids: action.ids, ...(action.kind === 'move' ? { target_cid: moveTargetCid.value } : {}) }
    const response = await fetch(`/api/v1/files/${action.kind}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    await requireOk(response, `${operationTitle.value}失败`)
    success.value = action.kind === 'move'
      ? `已将 ${action.ids.length} 个项目移动到「${accountName.value} / ${moveTargetName.value}」（CID: ${moveTargetCid.value}）。`
      : action.kind === 'delete' ? `已将 ${action.ids.length} 个项目移入「${accountName.value}」的回收站。` : `已将「${action.name}」重命名为「${renameValue.value.trim()}」。`
    operation.value = null
    await fetchFiles()
  } catch (error) {
    operationError.value = errorMessage(error)
  } finally {
    operationBusy.value = false
  }
}

async function createAccount() {
  if (accountBusy.value) return
  accountError.value = ''
  accountSuccess.value = ''
  if (!newAccount.value.name.trim() || !newAccount.value.cookie.trim()) {
    accountError.value = '请输入账号名称和浏览器 Cookie。'
    return
  }
  accountBusy.value = true
  try {
    const response = await fetch('/api/v1/accounts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(newAccount.value) })
    await requireOk(response, '添加账号失败')
    const data = await response.json()
    accountSuccess.value = `已添加账号「${newAccount.value.name.trim()}」。`
    newAccount.value = { name: '', cookie: '', is_default: false }
    showAccountModal.value = false
    await fetchAccounts(data.id)
    await changeAccount()
  } catch (error) {
    accountError.value = errorMessage(error)
  } finally {
    accountBusy.value = false
  }
}

async function deleteCurrentAccount() {
  if (!currentAccountId.value || busy.value || !confirm(`确认移除账号「${accountName.value}」？这只会移除本系统保存的账号，不会删除网盘文件。`)) return
  accountBusy.value = true
  accountActionError.value = ''
  accountSuccess.value = ''
  try {
    const name = accountName.value
    const response = await fetch(`/api/v1/accounts/${encodeURIComponent(currentAccountId.value)}`, { method: 'DELETE' })
    await requireOk(response, '移除账号失败')
    accountSuccess.value = `已移除账号「${name}」，网盘文件未删除。`
    currentAccountId.value = ''
    await fetchAccounts()
    await changeAccount()
  } catch (error) {
    accountActionError.value = errorMessage(error)
  } finally {
    accountBusy.value = false
  }
}

async function createFolder() {
  if (folderBusy.value) return
  folderError.value = ''
  if (!newFolderName.value.trim()) {
    folderError.value = '请输入文件夹名称。'
    return
  }
  folderBusy.value = true
  success.value = ''
  try {
    const name = newFolderName.value.trim()
    const response = await fetch('/api/v1/files/mkdir', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ account_id: currentAccountId.value, parent_cid: currentCid.value, name }) })
    await requireOk(response, '创建文件夹失败')
    success.value = `已在「${directoryName.value}」创建文件夹「${name}」。`
    newFolderName.value = ''
    showNewFolderModal.value = false
    await fetchFiles()
  } catch (error) {
    folderError.value = errorMessage(error)
  } finally {
    folderBusy.value = false
  }
}

function openShareTransfer() {
  shareError.value = ''
  showShareModal.value = true
}

async function saveSharedContent() {
  if (shareBusy.value) return
  const url = shareForm.value.url.trim()
  if (!url) {
    shareError.value = '请填写 115 分享链接。'
    return
  }
  shareBusy.value = true
  shareError.value = ''
  success.value = ''
  try {
    const response = await fetch('/api/v1/files/save_share', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        account_id: currentAccountId.value,
        target_cid: currentCid.value,
        url,
        ...(shareForm.value.password.trim() ? { password: shareForm.value.password.trim() } : {}),
      }),
    })
    await requireOk(response, '转存分享失败')
    const result = await response.json()
    const title = typeof result.title === 'string' && result.title.trim() ? `「${result.title.trim()}」` : '该分享'
    const count = Number.isInteger(result.count) ? `${result.count} 个项目` : '分享内容'
    success.value = `已将 ${title} 中的 ${count} 转存到「${accountName.value} / ${directoryName.value}」。`
    shareForm.value = { url: '', password: '' }
    showShareModal.value = false
    await fetchFiles()
  } catch (error) {
    shareError.value = errorMessage(error)
  } finally {
    shareBusy.value = false
  }
}

onMounted(() => {
  reloadAccounts()
  fetchCidMap()
})
</script>

<template>
  <div class="files-view space-y-6">
    <header class="space-y-4 pb-6 border-b border-border/80">
      <div>
        <h1 class="font-serif text-2xl sm:text-3xl font-bold text-text tracking-tight">115 网盘文件管理</h1>
        <p class="text-sm text-text-muted mt-1.5 leading-relaxed">切换账号浏览目录，将分享链接转存到当前目录，或选择文件进行移动、重命名和删除。</p>
      </div>
      <div class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-2 rounded-xl border border-border/80 bg-surface px-3 min-w-0 max-w-full shadow-xs">
          <Users class="w-4 h-4 shrink-0 text-text-muted" aria-hidden="true" />
          <label for="files-account" class="text-xs font-mono font-medium text-text-muted shrink-0 uppercase tracking-wider">账号</label>
          <select id="files-account" v-model="currentAccountId" :disabled="busy || !!accountsError || !accounts.length" class="min-w-0 bg-transparent text-sm font-medium text-text focus:outline-none" @change="changeAccount">
            <option v-if="accountsLoading" value="">正在读取账号…</option>
            <option v-else-if="accountsError" value="">账号状态未知</option>
            <option v-else-if="!accounts.length" value="">尚未添加账号</option>
            <option v-for="account in accounts" :key="account.id" :value="account.id">{{ account.name }}{{ account.is_default ? '（默认）' : '' }}</option>
          </select>
        </div>
        <button type="button" :disabled="busy" class="file-button" @click="accountError = ''; showAccountModal = true"><UserPlus class="w-4 h-4" aria-hidden="true" />添加账号</button>
        <button v-if="currentAccountId && !accountsError" type="button" :disabled="busy" class="file-button text-danger border-danger/30 hover:border-danger" @click="deleteCurrentAccount"><UserMinus class="w-4 h-4" aria-hidden="true" />移除账号</button>
        <button type="button" :disabled="busy || !currentAccountId || !!accountsError || !!filesError" class="file-button" @click="folderError = ''; showNewFolderModal = true"><FolderPlus class="w-4 h-4" aria-hidden="true" />新建文件夹</button>
        <button type="button" :disabled="busy || !currentAccountId || !!accountsError || !!filesError" class="file-button text-accent border-accent/40 hover:border-accent" @click="openShareTransfer"><Link2 class="w-4 h-4" aria-hidden="true" />转存分享</button>
        <button type="button" :disabled="busy || !currentAccountId || !!accountsError" class="file-button" @click="fetchFiles"><RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" aria-hidden="true" />刷新目录</button>
      </div>
      <p v-if="accountSuccess" role="status" class="text-sm text-accent break-words">{{ accountSuccess }}</p>
      <p v-if="accountActionError" role="alert" class="text-sm text-danger break-words">移除账号失败：{{ accountActionError }} 请重试移除操作。</p>
    </header>

    <section v-if="Object.keys(cidMap).length || mapError" aria-label="分类目录" class="p-4 rounded-2xl border border-border/70 bg-surface shadow-xs space-y-3">
      <div v-if="Object.keys(cidMap).length" class="flex items-center gap-2 flex-wrap">
        <h2 class="text-xs font-mono uppercase tracking-wider text-text-muted font-semibold">分类目录</h2>
        <button v-for="(cid, name) in cidMap" :key="name" type="button" :disabled="busy || !currentAccountId || !!accountsError" class="file-button" :class="currentCid === cid ? 'border-accent text-accent bg-accent-soft' : ''" @click="jumpToCid(String(name), cid)">{{ name }}</button>
      </div>
      <div v-if="mapError" class="space-y-2">
        <p role="alert" class="text-sm text-danger break-words">分类目录读取失败：{{ mapError }} 仍可从根目录浏览文件。</p>
        <button type="button" :disabled="mapLoading" class="file-button" @click="fetchCidMap">{{ mapLoading ? '正在读取…' : '重试分类目录' }}</button>
      </div>
    </section>

    <section aria-label="当前目录" class="p-4 rounded-2xl border border-border/70 bg-surface shadow-xs space-y-3">
      <nav aria-label="目录路径" class="flex items-start gap-2 text-sm text-text-muted">
        <HardDrive class="w-4 h-4 mt-3.5 text-accent shrink-0" aria-hidden="true" />
        <ol class="flex items-center flex-wrap min-w-0">
          <li v-for="(item, index) in breadcrumbs" :key="index" class="flex items-center min-w-0">
            <span v-if="index" class="px-2 text-text-faint" aria-hidden="true">/</span>
            <button type="button" :disabled="busy || !currentAccountId || !!accountsError" :aria-current="index === breadcrumbs.length - 1 ? 'location' : undefined" class="px-1 text-left break-all font-medium transition-colors hover:text-accent" :class="index === breadcrumbs.length - 1 ? 'text-text font-semibold' : 'text-text-muted'" @click="navigateToBreadcrumb(index)">{{ item.name }}</button>
          </li>
        </ol>
      </nav>
      <p v-if="currentAccountId && !accountsError" class="text-xs font-mono text-text-faint break-words">{{ accountName }} · CID: {{ currentCid }}</p>
      <p v-if="success" role="status" class="text-sm text-accent break-words">{{ success }}</p>
    </section>

    <section aria-label="文件列表" :aria-busy="loading || accountsLoading" class="rounded-2xl border border-border/70 bg-surface overflow-hidden shadow-xs">
      <div v-if="accountsLoading" role="status" class="p-8 text-center text-sm text-text-muted">正在读取账号…</div>
      <div v-else-if="accountsError" class="p-6 space-y-3">
        <h2 class="text-lg font-semibold">无法确认账号状态</h2>
        <p role="alert" class="text-sm text-danger break-words">账号列表读取失败：{{ accountsError }}</p>
        <button type="button" :disabled="busy" class="file-button" @click="reloadAccounts">重试读取账号</button>
      </div>
      <div v-else-if="!currentAccountId" class="p-6 space-y-3">
        <h2 class="text-lg font-semibold">尚未添加 115 账号</h2>
        <p class="text-sm text-text-muted">添加账号后即可浏览网盘目录。</p>
        <button type="button" :disabled="busy" class="file-button" @click="accountError = ''; showAccountModal = true">添加账号</button>
      </div>
      <div v-else-if="loading" role="status" class="p-8 text-center text-sm text-text-muted"><Loader2 class="w-6 h-6 animate-spin text-accent mx-auto mb-2" aria-hidden="true" />正在加载目录列表…</div>
      <div v-else-if="filesError" class="p-6 space-y-3">
        <h2 class="text-lg font-semibold">目录读取失败</h2>
        <p class="text-sm text-text-muted break-words">{{ accountName }} / {{ directoryName }}（CID: {{ currentCid }}）</p>
        <p role="alert" class="text-sm text-danger break-words">{{ filesError }}</p>
        <button type="button" :disabled="busy" class="file-button" @click="fetchFiles">重试读取目录</button>
      </div>
      <div v-else-if="!files.length" class="p-8 text-center space-y-2">
        <h2 class="text-lg font-semibold">此文件夹为空</h2>
        <p class="text-sm text-text-muted">可将 115 分享转存到这里、新建文件夹，或切换到其他目录。</p>
      </div>
      <template v-else>
        <div class="flex items-center justify-between gap-3 px-4 border-b border-border/60 bg-bg-muted/30">
          <label class="inline-flex items-center min-h-11 gap-3 text-xs font-mono font-medium uppercase tracking-wider text-text-muted cursor-pointer"><input type="checkbox" :checked="allSelected" :indeterminate="partlySelected" :disabled="busy" aria-label="选择当前目录全部项目" @change="toggleAll" />全选当前目录</label>
          <span class="text-xs font-mono text-text-faint">{{ files.length }} 个项目</span>
        </div>
        <table class="desktop-files w-full text-left text-sm table-fixed">
          <thead class="border-b border-border/60 bg-bg-muted/40 text-[11px] font-mono uppercase tracking-wider text-text-muted"><tr><th scope="col" class="w-16"><span class="sr-only">选择</span></th><th scope="col" class="p-3 font-semibold text-text-muted">名称</th><th scope="col" class="p-3 w-28 font-semibold text-text-muted">大小</th><th scope="col" class="p-3 w-32 font-semibold text-text-muted">修改日期</th><th scope="col" class="p-3 w-28 text-right font-semibold text-text-muted">操作</th></tr></thead>
          <tbody class="divide-y divide-border/40">
            <tr v-for="file in files" :key="fileId(file)" :class="selectedFiles.has(fileId(file)) ? 'bg-accent-soft/30' : 'hover:bg-bg-muted/30 transition-colors'">
              <td class="px-2"><label class="file-selection"><input type="checkbox" :disabled="busy" :checked="selectedFiles.has(fileId(file))" :aria-label="`选择 ${file.name}`" @change="toggleSelection(fileId(file))" /></label></td>
              <td class="p-3"><div class="flex items-center gap-3 min-w-0"><Folder v-if="file.is_folder" class="w-5 h-5 shrink-0 text-accent" aria-hidden="true" /><File v-else class="w-5 h-5 shrink-0 text-text-muted" aria-hidden="true" /><button v-if="file.is_folder" type="button" :disabled="busy" class="text-left break-all text-text hover:text-accent font-medium transition-colors" @click="enterFolder(file)">{{ file.name }}</button><span v-else class="break-all">{{ file.name }}</span></div></td>
              <td class="p-3 text-xs font-mono text-text-muted">{{ fileSize(file) }}</td><td class="p-3 text-xs font-mono text-text-muted">{{ fileDate(file) }}</td>
              <td class="p-2"><div class="flex justify-end gap-1"><button type="button" :disabled="busy" class="file-icon-button text-text-muted hover:text-accent" :aria-label="`重命名 ${file.name}`" @click="openOperation('rename', file)"><Edit2 class="w-4 h-4" aria-hidden="true" /></button><button type="button" :disabled="busy" class="file-icon-button text-danger/80 hover:text-danger" :aria-label="`删除 ${file.name}`" @click="openOperation('delete', file)"><Trash2 class="w-4 h-4" aria-hidden="true" /></button></div></td>
            </tr>
          </tbody>
        </table>
        <ul class="mobile-files divide-y divide-border">
          <li v-for="file in files" :key="fileId(file)" class="p-3" :class="selectedFiles.has(fileId(file)) ? 'bg-accent-soft/30' : ''">
            <div class="flex items-start gap-2">
              <label class="file-selection shrink-0"><input type="checkbox" :checked="selectedFiles.has(fileId(file))" :disabled="busy" :aria-label="`选择 ${file.name}`" @change="toggleSelection(fileId(file))" /></label>
              <div class="min-w-0 flex-1">
                <div class="flex items-start gap-2"><Folder v-if="file.is_folder" class="w-5 h-5 mt-3 shrink-0 text-accent" aria-hidden="true" /><File v-else class="w-5 h-5 mt-3 shrink-0 text-text-muted" aria-hidden="true" /><button v-if="file.is_folder" type="button" :disabled="busy" class="py-2 text-left break-all text-sm font-medium hover:text-accent" @click="enterFolder(file)">{{ file.name }}</button><span v-else class="py-3 text-sm break-all">{{ file.name }}</span></div>
                <p class="text-sm text-text-muted">{{ fileSize(file) }} · {{ fileDate(file) }}</p>
                <div class="flex flex-wrap gap-2 mt-2"><button type="button" :disabled="busy" class="file-button" :aria-label="`重命名 ${file.name}`" @click="openOperation('rename', file)"><Edit2 class="w-4 h-4" aria-hidden="true" />重命名</button><button type="button" :disabled="busy" class="file-button text-danger" :aria-label="`删除 ${file.name}`" @click="openOperation('delete', file)"><Trash2 class="w-4 h-4" aria-hidden="true" />删除</button></div>
              </div>
            </div>
          </li>
        </ul>
      </template>
    </section>

    <section v-if="selectedFiles.size" aria-label="批量操作" class="action-dock sticky z-20 rounded-2xl border border-accent/40 bg-surface/95 backdrop-blur-md p-4 shadow-lg space-y-3">
      <div class="flex flex-wrap items-center justify-between gap-2"><p role="status" class="text-sm font-semibold text-text">已选择 {{ selectedFiles.size }} 个项目 · {{ accountName }}</p><button type="button" :disabled="busy" class="file-button" @click="selectedFiles.clear()">取消选择</button></div>
      <div class="flex flex-wrap items-end gap-3">
        <div class="min-w-0 flex-1"><label for="files-move-target" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-1.5 font-medium">移动到当前账号的目录</label><select id="files-move-target" v-model="moveTargetCid" :disabled="busy" class="w-full rounded-xl border border-border/80 bg-bg px-3 text-sm focus:border-accent focus:outline-none"><option value="0">根目录（CID: 0）</option><option v-for="(cid, name) in cidMap" :key="name" :value="cid">{{ name }}（CID: {{ cid }}）</option></select></div>
        <button type="button" :disabled="busy || !moveTargetCid || moveTargetCid === currentCid" class="file-button text-accent border-accent/40 hover:border-accent" @click="openOperation('move')"><FolderInput class="w-4 h-4" aria-hidden="true" />移动所选</button>
        <button type="button" :disabled="busy" class="file-button text-danger border-danger/30 hover:border-danger" @click="openOperation('delete')"><Trash2 class="w-4 h-4" aria-hidden="true" />删除所选</button>
      </div>
      <p v-if="moveTargetCid === currentCid" class="text-xs text-text-muted">请选择与当前目录不同的移动目标。</p>
    </section>

    <UiDialog v-if="showShareModal" title="转存 115 分享" :busy="shareBusy" @close="showShareModal = false">
      <form class="space-y-4" @submit.prevent="saveSharedContent">
        <div class="rounded-xl border border-border/70 bg-bg-muted/50 p-4">
          <p class="text-xs font-mono uppercase tracking-wider text-text-muted">转存位置</p>
          <p class="mt-1 text-sm font-semibold break-words text-text">{{ accountName }} / {{ directoryName }}</p>
          <p class="mt-1 text-xs font-mono text-text-faint break-all">目录 CID：{{ currentCid }}</p>
        </div>
        <div>
          <label for="share-transfer-url" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">115 分享链接</label>
          <input id="share-transfer-url" v-model="shareForm.url" :disabled="shareBusy" type="text" inputmode="url" autocomplete="off" required autofocus placeholder="https://115.com/s/..." class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" :aria-invalid="!!shareError" :aria-describedby="shareError ? 'share-transfer-error' : 'share-transfer-help'" />
          <p id="share-transfer-help" class="mt-2 text-xs text-text-muted">支持 115.com 与 115cdn.com 分享链接。链接已包含提取码时，下方可以留空。</p>
        </div>
        <div>
          <label for="share-transfer-password" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">提取码（可选）</label>
          <input id="share-transfer-password" v-model="shareForm.password" :disabled="shareBusy" type="text" autocomplete="off" maxlength="128" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm font-mono focus:border-accent focus:outline-none" />
        </div>
        <p v-if="shareError" id="share-transfer-error" role="alert" class="text-sm text-danger break-words">转存失败：{{ shareError }}</p>
        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <button type="button" :disabled="shareBusy" class="file-button" @click="showShareModal = false">取消</button>
          <button type="submit" :disabled="shareBusy || !shareForm.url.trim()" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong"><Loader2 v-if="shareBusy" class="w-4 h-4 animate-spin" aria-hidden="true" />{{ shareBusy ? '正在转存…' : '转存到当前目录' }}</button>
        </div>
      </form>
    </UiDialog>

    <UiDialog v-if="showNewFolderModal" title="新建文件夹" :busy="folderBusy" @close="showNewFolderModal = false">
      <form class="space-y-4" @submit.prevent="createFolder">
        <p class="text-xs font-mono text-text-muted break-words">创建位置：{{ accountName }} / {{ directoryName }}（CID: {{ currentCid }}）</p>
        <div><label for="new-folder-name" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">文件夹名称</label><input id="new-folder-name" v-model="newFolderName" :disabled="folderBusy" required autofocus class="w-full px-3.5 rounded-xl border border-border/80 bg-bg text-sm focus:border-accent focus:outline-none" :aria-invalid="!!folderError" :aria-describedby="folderError ? 'folder-error' : undefined" /></div>
        <p v-if="folderError" id="folder-error" role="alert" class="text-sm text-danger break-words">创建文件夹失败：{{ folderError }}</p>
        <div class="flex justify-end gap-2 pt-2"><button type="button" :disabled="folderBusy" class="file-button" @click="showNewFolderModal = false">取消</button><button type="submit" :disabled="folderBusy" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong">{{ folderBusy ? '正在创建…' : '创建文件夹' }}</button></div>
      </form>
    </UiDialog>

    <UiDialog v-if="showAccountModal" title="添加 115 账号" :busy="accountBusy" @close="showAccountModal = false">
      <form class="space-y-4" @submit.prevent="createAccount">
        <div><label for="account-name" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">账号名称</label><input id="account-name" v-model="newAccount.name" :disabled="accountBusy" required autofocus class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
        <div><label for="account-cookie" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">浏览器 Cookie</label><input id="account-cookie" v-model="newAccount.cookie" :disabled="accountBusy" type="password" autocomplete="new-password" required class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm font-mono focus:border-accent focus:outline-none" /></div>
        <label class="inline-flex min-h-11 items-center gap-3 text-sm text-text cursor-pointer"><input v-model="newAccount.is_default" :disabled="accountBusy" type="checkbox" class="accent-accent" />设为默认账号</label>
        <p v-if="accountError" role="alert" class="text-sm text-danger break-words">添加账号失败：{{ accountError }}</p>
        <div class="flex justify-end gap-2 pt-2"><button type="button" :disabled="accountBusy" class="file-button" @click="showAccountModal = false">取消</button><button type="submit" :disabled="accountBusy" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong">{{ accountBusy ? '正在保存…' : '保存账号' }}</button></div>
      </form>
    </UiDialog>

    <UiDialog v-if="operation" :title="operationTitle" :busy="operationBusy" @close="operation = null">
      <form class="space-y-4" @submit.prevent="submitOperation">
        <p class="text-xs font-mono text-text-muted break-words">账号：{{ accountName }}<br />当前目录：{{ directoryName }}（CID: {{ currentCid }}）</p>
        <p class="text-sm font-medium text-text">{{ operation.name ? `项目：${operation.name}` : `已选择 ${operation.ids.length} 个项目` }}</p>
        <p v-if="operation.kind === 'move'" class="text-sm text-text-muted break-words">将 {{ operation.ids.length }} 个项目移动到「{{ accountName }} / {{ moveTargetName }}」（CID: {{ moveTargetCid }}）。</p>
        <p v-if="operation.kind === 'delete'" class="rounded-xl border border-danger/30 bg-danger/5 p-3.5 text-sm text-danger">删除后，{{ operation.ids.length }} 个项目将从当前目录移入 115 回收站。请确认所选内容；恢复需前往 115 网盘。</p>
        <div v-if="operation.kind === 'rename'"><label for="file-new-name" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">新名称</label><input id="file-new-name" v-model="renameValue" required autofocus :disabled="operationBusy" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
        <p v-if="operationError" role="alert" class="text-sm text-danger break-words">{{ operationTitle }}失败：{{ operationError }}</p>
        <div class="flex flex-wrap justify-end gap-2 pt-2"><button type="button" :disabled="operationBusy" class="file-button" @click="operation = null">取消</button><button type="submit" :disabled="operationBusy || (operation.kind === 'rename' && renameValue.trim() === operation.name)" class="file-button" :class="operation.kind === 'delete' ? 'border-danger/40 text-danger hover:border-danger' : 'bg-accent text-accent-contrast hover:bg-accent-strong'">{{ operationBusy ? '正在处理…' : operation.kind === 'delete' ? `确认删除 ${operation.ids.length} 个项目` : operation.kind === 'move' ? '确认移动' : '保存名称' }}</button></div>
      </form>
    </UiDialog>
  </div>
</template>

<style scoped>
.files-view button,
.files-view select,
input:not([type='checkbox']) { min-height: 40px; }
.files-view button:disabled,
.files-view select:disabled { opacity: 0.5; cursor: not-allowed; }
.file-button {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.5rem 0.875rem;
  border: 1px solid var(--border);
  border-radius: 0.625rem;
  background: var(--surface);
  color: var(--text);
  font-size: 0.875rem;
  font-weight: 500;
  box-shadow: 0 1px 2px rgba(31, 30, 29, 0.04);
  transition: all 0.15s cubic-bezier(0.16, 1, 0.3, 1);
}
.file-button:hover:not(:disabled) {
  background: var(--bg-muted);
  border-color: var(--border-strong);
  color: var(--text);
}
.file-icon-button,
.file-selection { display: inline-flex; align-items: center; justify-content: center; min-width: 40px; min-height: 40px; border-radius: 0.5rem; transition: all 0.15s ease; }
.file-selection { cursor: pointer; }
input[type='checkbox'] { width: 1.125rem; height: 1.125rem; accent-color: var(--accent); }
.mobile-files { display: none; }
@media (max-width: 767px) {
  .desktop-files { display: none; }
  .mobile-files { display: block; }
  .action-dock > div:last-child { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .action-dock > div:last-child > div { grid-column: 1 / -1; }
}
</style>

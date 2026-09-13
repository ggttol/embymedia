<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Folder, File, HardDrive, FolderPlus, RefreshCw, Trash2, Edit2, Loader2, Users, FolderInput, UserMinus, UserPlus, Link2, ArrowRight, CircleStop, RotateCcw } from 'lucide-vue-next'
import UiDialog from '../components/UiDialog.vue'

type Provider = '115' | 'quark'
type DriveFile = { file_id?: string; cid?: string; parent_id?: string; name: string; is_folder: boolean; size?: number; updated_time?: string }
type DriveAccount = { id: string; type: Provider; name: string; is_default: boolean; status: string }
type DriveCapabilities = { browse: boolean; mkdir: boolean; rename: boolean; move: boolean; delete: boolean; share_save: boolean; offline: boolean }
type FileOperation = { kind: 'move' | 'delete' | 'rename'; ids: string[]; name: string }
type AsyncTask = { id: string; status: string; progress: number; error?: string; result?: string }
type ImportDetail = { import: { phase: string; destination_cid?: string; total_files: number; completed_files: number; total_bytes: number; completed_bytes: number; current_file?: string }; items: Array<{ relative_path: string; state: string; size: number; downloaded_bytes: number; error?: string }> }
type ImportStatus = { task: AsyncTask; detail: ImportDetail | null }

const route = useRoute()
const router = useRouter()
const provider = ref<Provider>('115')
const currentCid = ref('0')
const cidMap = ref<Record<string, string>>({})
const mapError = ref('')
const mapLoading = ref(false)
const selectedFiles = ref<Set<string>>(new Set())
const moveTargetCid = ref('0')
const breadcrumbs = ref([{ cid: '0', name: '根目录' }])
const files = ref<DriveFile[]>([])
const accounts = ref<DriveAccount[]>([])
const capabilities = ref<DriveCapabilities>({ browse: true, mkdir: true, rename: true, move: true, delete: true, share_save: true, offline: true })
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
const newAccount = ref({ name: '', cookie: '', token: '', is_default: false })
const editingAccountId = ref('')
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
const importStatus = ref<ImportStatus | null>(null)
const importPollError = ref('')
const importActionBusy = ref(false)
let generation = 0
let requestController = new AbortController()
let importPollTimer: number | undefined

const providerLabel = computed(() => provider.value === '115' ? '115' : '夸克')
const busy = computed(() => loading.value || accountsLoading.value || accountBusy.value || folderBusy.value || shareBusy.value || operationBusy.value || importActionBusy.value)
const accountName = computed(() => accounts.value.find(account => account.id === currentAccountId.value)?.name || '未选择账号')
const directoryName = computed(() => breadcrumbs.value.map(item => item.name).join(' / '))
const moveTargets = computed(() => {
  const values = new Map<string, string>([['0', '根目录']])
  if (provider.value === '115') Object.entries(cidMap.value).forEach(([name, id]) => values.set(id, name))
  breadcrumbs.value.forEach(item => values.set(item.cid, item.name))
  return Array.from(values, ([cid, name]) => ({ cid, name }))
})
const moveTargetName = computed(() => moveTargets.value.find(item => item.cid === moveTargetCid.value)?.name || moveTargetCid.value)
const allSelected = computed(() => files.value.length > 0 && selectedFiles.value.size === files.value.length)
const partlySelected = computed(() => selectedFiles.value.size > 0 && !allSelected.value)
const operationTitle = computed(() => operation.value?.kind === 'move' ? '确认移动' : operation.value?.kind === 'delete' ? '确认删除' : '重命名')
const importActive = computed(() => ['pending', 'running'].includes(importStatus.value?.task.status || ''))
const importTerminal = computed(() => !!importStatus.value && !importActive.value)
const importResult = computed(() => {
  try { return importStatus.value?.task.result ? JSON.parse(importStatus.value.task.result) as Record<string, unknown> : null } catch { return null }
})

function fileId(file: DriveFile) { return String(file.file_id || file.cid || '') }
function fileSize(file: DriveFile) { return file.is_folder ? '文件夹' : file.size == null ? '大小未知' : formatBytes(file.size) }
function fileDate(file: DriveFile) { return file.updated_time ? new Date(file.updated_time).toLocaleDateString() : '日期未知' }
function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return '大小未知'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`
}
function phaseLabel(phase?: string) {
  return ({ saving_share: '正在转存夸克分享', discovering: '正在读取文件清单', downloading: '正在下载到中转目录', uploading: '正在上传到 115', verifying: '正在核对 115 文件', verified: '已完成核对' } as Record<string, string>)[phase || ''] || '等待执行'
}
function errorMessage(error: unknown) { return error instanceof Error ? error.message : '网络请求失败，请重试。' }
async function requireOk(response: Response, message: string) {
  if (response.ok) return
  const data = await response.json().catch(() => null)
  throw new Error(data?.error || `${message}（HTTP ${response.status}）`)
}

function resetProviderState() {
  requestController.abort()
  requestController = new AbortController()
  generation++
  currentCid.value = '0'
  breadcrumbs.value = [{ cid: '0', name: '根目录' }]
  accounts.value = []
  currentAccountId.value = ''
  files.value = []
  selectedFiles.value = new Set()
  moveTargetCid.value = '0'
  editingAccountId.value = ''
  operation.value = null
  showNewFolderModal.value = false
  showAccountModal.value = false
  showShareModal.value = false
  success.value = ''
  filesError.value = ''
  accountsError.value = ''
  accountSuccess.value = ''
  accountActionError.value = ''
  importStatus.value = null
  importPollError.value = ''
  if (importPollTimer) window.clearTimeout(importPollTimer)
}

async function fetchCidMap() {
  if (provider.value !== '115') { cidMap.value = {}; mapError.value = ''; return }
  mapError.value = ''
  mapLoading.value = true
  try {
    const response = await fetch('/api/v1/cid-map', { signal: requestController.signal })
    await requireOk(response, '读取分类目录失败')
    const data = await response.json()
    cidMap.value = data.map ?? {}
  } catch (error) {
    if ((error as Error).name !== 'AbortError') mapError.value = errorMessage(error)
  } finally { mapLoading.value = false }
}

async function fetchAccounts(preferredId = currentAccountId.value) {
  const requestedGeneration = generation
  accountsLoading.value = true
  accountsError.value = ''
  try {
    const response = await fetch(`/api/v1/drive/accounts?provider=${provider.value}`, { signal: requestController.signal })
    await requireOk(response, `读取${providerLabel.value}账号失败`)
    const data = await response.json()
    if (requestedGeneration !== generation) return
    accounts.value = data.accounts ?? []
    capabilities.value = data.capabilities ?? capabilities.value
    const account = accounts.value.find(item => item.id === preferredId) || accounts.value.find(item => item.is_default) || accounts.value[0]
    currentAccountId.value = account?.id || ''
  } catch (error) {
    if ((error as Error).name !== 'AbortError' && requestedGeneration === generation) accountsError.value = errorMessage(error)
  } finally { if (requestedGeneration === generation) accountsLoading.value = false }
}

async function reloadAccounts() {
  await fetchAccounts()
  await changeAccount()
}

async function fetchFiles() {
  const requestedGeneration = generation
  selectedFiles.value = new Set()
  filesError.value = ''
  files.value = []
  if (!currentAccountId.value || accountsError.value) return
  loading.value = true
  try {
    const params = new URLSearchParams({ provider: provider.value, account_id: currentAccountId.value, parent_id: currentCid.value })
    const response = await fetch(`/api/v1/drive/files?${params}`, { signal: requestController.signal })
    await requireOk(response, '读取目录失败')
    const data = await response.json()
    if (requestedGeneration === generation) files.value = data.files ?? []
  } catch (error) {
    if ((error as Error).name !== 'AbortError' && requestedGeneration === generation) filesError.value = errorMessage(error)
  } finally { if (requestedGeneration === generation) loading.value = false }
}

async function switchProvider(next: Provider, syncRoute = true) {
  if (next === provider.value) return
  resetProviderState()
  provider.value = next
  if (syncRoute) await router.replace({ query: { ...route.query, provider: next, add_account: undefined } })
  await Promise.all([fetchAccounts(), fetchCidMap()])
  await fetchFiles()
}

watch(() => route.query.provider, (value) => {
  if ((value === '115' || value === 'quark') && value !== provider.value) void switchProvider(value, false)
})
async function changeAccount() {
  currentCid.value = '0'
  breadcrumbs.value = [{ cid: '0', name: '根目录' }]
  moveTargetCid.value = '0'
  success.value = ''
  importStatus.value = null
  await fetchFiles()
}
function jumpToCid(name: string, cid: string) {
  currentCid.value = cid
  breadcrumbs.value = cid === '0' ? [{ cid: '0', name: '根目录' }] : [{ cid: '0', name: '根目录' }, { cid, name }]
  success.value = ''
  void fetchFiles()
}
function enterFolder(file: DriveFile) {
  currentCid.value = fileId(file)
  breadcrumbs.value.push({ cid: currentCid.value, name: file.name })
  success.value = ''
  void fetchFiles()
}
function navigateToBreadcrumb(index: number) {
  const target = breadcrumbs.value[index]
  if (!target) return
  currentCid.value = target.cid
  breadcrumbs.value = breadcrumbs.value.slice(0, index + 1)
  success.value = ''
  void fetchFiles()
}
function toggleSelection(id: string) {
  const next = new Set(selectedFiles.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  selectedFiles.value = next
}
function toggleAll(event: Event) { selectedFiles.value = (event.target as HTMLInputElement).checked ? new Set(files.value.map(fileId)) : new Set() }
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
  if (action.kind === 'rename' && !renameValue.value.trim()) { operationError.value = '请输入新名称。'; return }
  operationBusy.value = true
  operationError.value = ''
  success.value = ''
  try {
    const common = { provider: provider.value, account_id: currentAccountId.value }
    const body = action.kind === 'rename'
      ? { ...common, file_id: action.ids[0], new_name: renameValue.value.trim() }
      : { ...common, parent_id: currentCid.value, file_ids: action.ids, ...(action.kind === 'move' ? { target_id: moveTargetCid.value } : {}) }
    const response = await fetch(`/api/v1/drive/files/${action.kind}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    await requireOk(response, `${operationTitle.value}失败`)
    success.value = action.kind === 'move' ? `已将 ${action.ids.length} 个项目移动到「${accountName.value} / ${moveTargetName.value}」。` : action.kind === 'delete' ? `已将 ${action.ids.length} 个项目移入「${accountName.value}」的回收站。` : `已将「${action.name}」重命名为「${renameValue.value.trim()}」。`
    operation.value = null
    await fetchFiles()
  } catch (error) { operationError.value = errorMessage(error) } finally { operationBusy.value = false }
}

function openAccountEditor(account?: DriveAccount) {
  editingAccountId.value = account?.id || ''
  newAccount.value = { name: account?.name || '', cookie: '', token: '', is_default: account?.is_default || false }
  accountError.value = ''
  showAccountModal.value = true
}

async function saveAccount() {
  if (accountBusy.value) return
  accountError.value = ''
  accountSuccess.value = ''
  if (!newAccount.value.name.trim() || (!editingAccountId.value && !newAccount.value.cookie.trim())) { accountError.value = editingAccountId.value ? '请输入账号名称。' : '请输入账号名称和浏览器 Cookie。'; return }
  accountBusy.value = true
  try {
    const response = await fetch('/api/v1/drive/accounts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...newAccount.value, ...(editingAccountId.value ? { id: editingAccountId.value } : {}), type: provider.value }) })
    await requireOk(response, editingAccountId.value ? '更新账号失败' : '添加账号失败')
    const data = await response.json()
    accountSuccess.value = editingAccountId.value ? `已更新账号「${newAccount.value.name.trim()}」。` : `已添加账号「${newAccount.value.name.trim()}」。`
    editingAccountId.value = ''
    newAccount.value = { name: '', cookie: '', token: '', is_default: false }
    showAccountModal.value = false
    await fetchAccounts(data.id)
    await changeAccount()
  } catch (error) { accountError.value = errorMessage(error) } finally { accountBusy.value = false }
}

async function deleteCurrentAccount() {
  if (!currentAccountId.value || busy.value || !confirm(`确认移除账号「${accountName.value}」？这只会移除本系统保存的账号，不会删除网盘文件。`)) return
  accountBusy.value = true
  accountActionError.value = ''
  accountSuccess.value = ''
  try {
    const name = accountName.value
    const response = await fetch(`/api/v1/drive/accounts/${encodeURIComponent(currentAccountId.value)}`, { method: 'DELETE' })
    await requireOk(response, '移除账号失败')
    accountSuccess.value = `已移除账号「${name}」，网盘文件未删除。`
    currentAccountId.value = ''
    await fetchAccounts()
    await changeAccount()
  } catch (error) { accountActionError.value = errorMessage(error) } finally { accountBusy.value = false }
}
async function createFolder() {
  if (folderBusy.value) return
  folderError.value = ''
  if (!newFolderName.value.trim()) { folderError.value = '请输入文件夹名称。'; return }
  folderBusy.value = true
  success.value = ''
  try {
    const name = newFolderName.value.trim()
    const response = await fetch('/api/v1/drive/files/mkdir', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ provider: provider.value, account_id: currentAccountId.value, parent_id: currentCid.value, name }) })
    await requireOk(response, '创建文件夹失败')
    success.value = `已在「${directoryName.value}」创建文件夹「${name}」。`
    newFolderName.value = ''
    showNewFolderModal.value = false
    await fetchFiles()
  } catch (error) { folderError.value = errorMessage(error) } finally { folderBusy.value = false }
}
function openShareTransfer() { shareError.value = ''; showShareModal.value = true }

function scheduleImportPoll(delay: number) {
  if (importPollTimer) window.clearTimeout(importPollTimer)
  if (importStatus.value && !importTerminal.value) importPollTimer = window.setTimeout(() => void pollImport(), delay)
}
async function pollImport() {
  const taskId = importStatus.value?.task.id
  if (!taskId) return
  try {
    const response = await fetch(`/api/v1/quark/share-imports/${encodeURIComponent(taskId)}`)
    await requireOk(response, '读取导入进度失败')
    importStatus.value = await response.json()
    importPollError.value = ''
  } catch (error) {
    importPollError.value = errorMessage(error)
  } finally {
    scheduleImportPoll(importActive.value ? 1000 : 5000)
  }
}
async function saveSharedContent() {
  if (shareBusy.value) return
  const url = shareForm.value.url.trim()
  if (!url) { shareError.value = `请填写${providerLabel.value}分享链接。`; return }
  shareBusy.value = true
  shareError.value = ''
  success.value = ''
  try {
    if (provider.value === 'quark') {
      const response = await fetch('/api/v1/quark/share-imports', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ quark_account_id: currentAccountId.value, quark_target_id: currentCid.value, share_url: url, ...(shareForm.value.password.trim() ? { share_password: shareForm.value.password.trim() } : {}) }) })
      await requireOk(response, '提交夸克分享导入失败')
      const queued = await response.json()
      importStatus.value = { task: { id: queued.task_id, status: queued.status, progress: 0 }, detail: null }
      shareForm.value = { url: '', password: '' }
      showShareModal.value = false
      scheduleImportPoll(250)
    } else {
      const response = await fetch('/api/v1/drive/share-save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ provider: '115', account_id: currentAccountId.value, target_cid: currentCid.value, url, ...(shareForm.value.password.trim() ? { password: shareForm.value.password.trim() } : {}) }) })
      await requireOk(response, '转存分享失败')
      const result = await response.json()
      const title = typeof result.title === 'string' && result.title.trim() ? `「${result.title.trim()}」` : '该分享'
      success.value = `已将 ${title} 中的 ${Number.isInteger(result.count) ? `${result.count} 个项目` : '分享内容'} 转存到「${accountName.value} / ${directoryName.value}」。`
      shareForm.value = { url: '', password: '' }
      showShareModal.value = false
      await fetchFiles()
    }
  } catch (error) { shareError.value = errorMessage(error) } finally { shareBusy.value = false }
}
async function cancelImport() {
  const id = importStatus.value?.task.id
  if (!id || importActionBusy.value) return
  importActionBusy.value = true
  try { const response = await fetch(`/api/v1/async-tasks/${encodeURIComponent(id)}/cancel`, { method: 'POST' }); await requireOk(response, '取消导入失败'); await pollImport() } catch (error) { importPollError.value = errorMessage(error) } finally { importActionBusy.value = false }
}
async function retryImport() {
  const id = importStatus.value?.task.id
  if (!id || importActionBusy.value) return
  importActionBusy.value = true
  try {
    const response = await fetch(`/api/v1/async-tasks/${encodeURIComponent(id)}/retry`, { method: 'POST' })
    await requireOk(response, '重试导入失败')
    const task = await response.json()
    importStatus.value = { task, detail: null }
    importPollError.value = ''
    scheduleImportPoll(250)
  } catch (error) { importPollError.value = errorMessage(error) } finally { importActionBusy.value = false }
}
async function showCompletedDestination() {
  const cid = String(importResult.value?.destination_cid || importStatus.value?.detail?.import.destination_cid || '')
  if (!cid) return
  await switchProvider('115')
  jumpToCid('_待整理', cid)
}

onMounted(async () => {
  const requestedProvider = route.query.provider
  if (requestedProvider === '115' || requestedProvider === 'quark') provider.value = requestedProvider
  await Promise.all([fetchAccounts(), fetchCidMap()])
  await fetchFiles()
  if (route.query.add_account === '1') {
    openAccountEditor()
    await router.replace({ query: { ...route.query, provider: provider.value, add_account: undefined } })
  }
})
onUnmounted(() => {
  requestController.abort()
  if (importPollTimer) window.clearTimeout(importPollTimer)
})
</script>

<template>
  <div class="files-view space-y-6">
    <header class="space-y-4 pb-6 border-b border-border/80">
      <div>
        <h1 class="font-serif text-2xl sm:text-3xl font-bold text-text tracking-tight">网盘文件管理</h1>
        <p class="text-sm text-text-muted mt-1.5 leading-relaxed">在 115 与夸克账号间切换，管理目录，并跟踪夸克分享发送到 115 的完整核对过程。</p>
      </div>
      <div class="provider-tabs inline-grid grid-cols-2 rounded-xl border border-border/80 bg-bg-muted/50 p-1" role="tablist" aria-label="网盘提供方">
        <button v-for="item in ([{ id: '115', label: '115' }, { id: 'quark', label: '夸克' }] as const)" :key="item.id" type="button" role="tab" :aria-selected="provider === item.id" :disabled="accountBusy || folderBusy || shareBusy || operationBusy || importActionBusy" class="min-h-10 rounded-lg px-5 text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent" :class="provider === item.id ? 'bg-surface text-text shadow-xs' : 'text-text-muted hover:text-text'" @click="switchProvider(item.id)">{{ item.label }}</button>
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
        <button type="button" :disabled="busy" class="file-button" @click="openAccountEditor()"><UserPlus class="w-4 h-4" aria-hidden="true" />添加账号</button>
        <button v-if="currentAccountId && !accountsError" type="button" :disabled="busy" class="file-button" @click="openAccountEditor(accounts.find(account => account.id === currentAccountId))"><Edit2 class="w-4 h-4" aria-hidden="true" />更新凭据</button>
        <button v-if="currentAccountId && !accountsError" type="button" :disabled="busy" class="file-button text-danger border-danger/30 hover:border-danger" @click="deleteCurrentAccount"><UserMinus class="w-4 h-4" aria-hidden="true" />移除账号</button>
        <button type="button" :disabled="busy || !capabilities.mkdir || !currentAccountId || !!accountsError || !!filesError" class="file-button" @click="folderError = ''; showNewFolderModal = true"><FolderPlus class="w-4 h-4" aria-hidden="true" />新建文件夹</button>
        <button v-if="capabilities.share_save" type="button" :disabled="busy || importActive || !currentAccountId || !!accountsError || !!filesError" class="file-button text-accent border-accent/40 hover:border-accent" @click="openShareTransfer"><Link2 class="w-4 h-4" aria-hidden="true" />{{ provider === 'quark' ? '转存并发送到 115' : '转存 115 分享' }}</button>
        <button type="button" :disabled="busy || !currentAccountId || !!accountsError" class="file-button" @click="fetchFiles"><RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" aria-hidden="true" />刷新目录</button>
      </div>
      <p v-if="accountSuccess" role="status" class="text-sm text-accent break-words">{{ accountSuccess }}</p>
      <p v-if="accountActionError" role="alert" class="text-sm text-danger break-words">移除账号失败：{{ accountActionError }} 请重试移除操作。</p>
    </header>

    <section v-if="provider === '115' && (Object.keys(cidMap).length || mapError)" aria-label="分类目录" class="p-4 rounded-2xl border border-border/70 bg-surface shadow-xs space-y-3">
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
      <p v-if="currentAccountId && !accountsError" class="text-xs font-mono text-text-faint break-words">{{ accountName }} · 目录 ID: {{ currentCid }}</p>
      <p v-if="success" role="status" class="text-sm text-accent break-words">{{ success }}</p>
    </section>

    <section v-if="importStatus" aria-label="夸克发送到 115 进度" class="overflow-hidden rounded-2xl border border-border/80 bg-surface shadow-xs">
      <div class="flex flex-col gap-4 p-5 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0 space-y-2">
          <div class="flex flex-wrap items-center gap-2">
            <span class="rounded-full bg-accent-soft px-2.5 py-1 text-xs font-semibold text-accent">夸克 → 115</span>
            <h2 class="font-semibold text-text">{{ phaseLabel(importStatus.detail?.import.phase) }}</h2>
          </div>
          <p class="text-sm text-text-muted">固定目标：<span class="font-mono text-text">/emby/_待整理</span></p>
          <p v-if="importStatus.detail?.import.current_file" class="break-all text-sm text-text-muted">当前文件：{{ importStatus.detail.import.current_file }}</p>
        </div>
        <span class="font-mono text-sm font-semibold text-text">{{ Math.round(importStatus.task.progress) }}%</span>
      </div>
      <div class="h-1.5 bg-bg-muted" aria-hidden="true"><div class="h-full bg-accent transition-[width] duration-300" :style="{ width: `${Math.max(0, Math.min(100, importStatus.task.progress))}%` }"></div></div>
      <div class="grid gap-3 border-t border-border/60 p-5 text-sm sm:grid-cols-2">
        <p>文件：<strong>{{ importStatus.detail?.import.completed_files || 0 }} / {{ importStatus.detail?.import.total_files || 0 }}</strong></p>
        <p>已核对：<strong>{{ formatBytes(importStatus.detail?.import.completed_bytes || 0) }} / {{ formatBytes(importStatus.detail?.import.total_bytes || 0) }}</strong></p>
      </div>
      <div v-if="importStatus.task.error || importPollError" class="border-t border-border/60 px-5 py-3">
        <p v-if="importStatus.task.error" role="alert" class="text-sm text-danger break-words">{{ importStatus.task.error }}</p>
        <p v-if="importPollError" role="status" class="text-sm text-text-muted break-words">进度读取暂时中断：{{ importPollError }} 已保留最后一次状态，不会重复提交。</p>
      </div>
      <div class="flex flex-wrap items-center gap-2 border-t border-border/60 p-4">
        <button v-if="importActive" type="button" :disabled="importActionBusy" class="file-button text-danger border-danger/30" @click="cancelImport"><CircleStop class="w-4 h-4" aria-hidden="true" />取消导入</button>
        <button v-if="importTerminal && importStatus.task.status !== 'completed'" type="button" :disabled="importActionBusy" class="file-button" @click="retryImport"><RotateCcw class="w-4 h-4" aria-hidden="true" />核对后重试</button>
        <button type="button" :disabled="importActionBusy" class="file-button" @click="pollImport"><RefreshCw class="w-4 h-4" aria-hidden="true" />读取状态</button>
        <RouterLink to="/tasks" class="file-button">任务中心<ArrowRight class="w-4 h-4" aria-hidden="true" /></RouterLink>
        <button v-if="importStatus.task.status === 'completed'" type="button" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong" @click="showCompletedDestination">切换到 115 查看<ArrowRight class="w-4 h-4" aria-hidden="true" /></button>
      </div>
    </section>

    <section aria-label="文件列表" :aria-busy="loading || accountsLoading" class="rounded-2xl border border-border/70 bg-surface overflow-hidden shadow-xs">
      <div v-if="accountsLoading" role="status" class="p-8 text-center text-sm text-text-muted">正在读取账号…</div>
      <div v-else-if="accountsError" class="p-6 space-y-3">
        <h2 class="text-lg font-semibold">无法确认账号状态</h2>
        <p role="alert" class="text-sm text-danger break-words">账号列表读取失败：{{ accountsError }}</p>
        <button type="button" :disabled="busy" class="file-button" @click="reloadAccounts">重试读取账号</button>
      </div>
      <div v-else-if="!currentAccountId" class="p-6 space-y-3">
        <h2 class="text-lg font-semibold">尚未添加{{ providerLabel }}账号</h2>
        <p class="text-sm text-text-muted">添加账号后即可浏览{{ providerLabel }}网盘目录。</p>
        <button type="button" :disabled="busy" class="file-button" @click="openAccountEditor()">添加账号</button>
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
        <p class="text-sm text-text-muted">{{ provider === 'quark' ? '可在这里新建文件夹，或将夸克分享转存到当前目录后发送到 115。' : '可将 115 分享转存到这里、新建文件夹，或切换到其他目录。' }}</p>
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
        <div class="min-w-0 flex-1"><label for="files-move-target" class="block text-xs font-mono uppercase tracking-wider text-text-muted mb-1.5 font-medium">移动到当前账号的目录</label><select id="files-move-target" v-model="moveTargetCid" :disabled="busy" class="w-full rounded-xl border border-border/80 bg-bg px-3 text-sm focus:border-accent focus:outline-none"><option v-for="target in moveTargets" :key="target.cid" :value="target.cid">{{ target.name }}（ID: {{ target.cid }}）</option></select></div>
        <button type="button" :disabled="busy || !moveTargetCid || moveTargetCid === currentCid" class="file-button text-accent border-accent/40 hover:border-accent" @click="openOperation('move')"><FolderInput class="w-4 h-4" aria-hidden="true" />移动所选</button>
        <button type="button" :disabled="busy" class="file-button text-danger border-danger/30 hover:border-danger" @click="openOperation('delete')"><Trash2 class="w-4 h-4" aria-hidden="true" />删除所选</button>
      </div>
      <p v-if="moveTargetCid === currentCid" class="text-xs text-text-muted">请选择与当前目录不同的移动目标。</p>
    </section>

    <UiDialog v-if="showShareModal" :title="provider === 'quark' ? '转存夸克分享并发送到 115' : '转存 115 分享'" :busy="shareBusy" @close="showShareModal = false">
      <form class="space-y-4" @submit.prevent="saveSharedContent">
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="rounded-xl border border-border/70 bg-bg-muted/50 p-4">
            <p class="text-xs font-mono uppercase tracking-wider text-text-muted">保存到{{ providerLabel }}</p>
            <p class="mt-1 text-sm font-semibold break-words text-text">{{ accountName }} / {{ directoryName }}</p>
            <p class="mt-1 text-xs font-mono text-text-faint break-all">目录 ID：{{ currentCid }}</p>
          </div>
          <div v-if="provider === 'quark'" class="rounded-xl border border-accent/30 bg-accent-soft/40 p-4">
            <p class="text-xs font-mono uppercase tracking-wider text-text-muted">发送到 115</p>
            <p class="mt-1 text-sm font-semibold text-text">默认 115 账号</p>
            <p class="mt-1 text-xs font-mono text-text-faint">/emby/_待整理</p>
          </div>
        </div>
        <div>
          <label for="share-transfer-url" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">{{ providerLabel }}分享链接</label>
          <input id="share-transfer-url" v-model="shareForm.url" :disabled="shareBusy" type="text" inputmode="url" autocomplete="off" required autofocus :placeholder="provider === 'quark' ? 'https://pan.quark.cn/s/...' : 'https://115.com/s/...'" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" :aria-invalid="!!shareError" :aria-describedby="shareError ? 'share-transfer-error' : 'share-transfer-help'" />
          <p id="share-transfer-help" class="mt-2 text-xs text-text-muted">{{ provider === 'quark' ? '分享会先保存到当前夸克目录，再由服务器逐文件下载、上传并核对 115 目标。' : '支持 115.com 与 115cdn.com 分享链接。链接已包含提取码时，下方可以留空。' }}</p>
        </div>
        <div>
          <label for="share-transfer-password" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">提取码（可选）</label>
          <input id="share-transfer-password" v-model="shareForm.password" :disabled="shareBusy" type="text" autocomplete="off" maxlength="128" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm font-mono focus:border-accent focus:outline-none" />
        </div>
        <p v-if="shareError" id="share-transfer-error" role="alert" class="text-sm text-danger break-words">转存失败：{{ shareError }}</p>
        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <button type="button" :disabled="shareBusy" class="file-button" @click="showShareModal = false">取消</button>
          <button type="submit" :disabled="shareBusy || !shareForm.url.trim()" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong"><Loader2 v-if="shareBusy" class="w-4 h-4 animate-spin" aria-hidden="true" />{{ shareBusy ? '正在提交…' : provider === 'quark' ? '转存并发送到 115' : '转存到当前目录' }}</button>
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

    <UiDialog v-if="showAccountModal" :title="`${editingAccountId ? '更新' : '添加'}${providerLabel}账号`" :busy="accountBusy" @close="showAccountModal = false">
      <form class="space-y-4" @submit.prevent="saveAccount">
        <div><label for="account-name" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">账号名称</label><input id="account-name" v-model="newAccount.name" :disabled="accountBusy" required autofocus class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
        <div><label for="account-cookie" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">浏览器 Cookie{{ editingAccountId ? '（留空则保留）' : '' }}</label><input id="account-cookie" v-model="newAccount.cookie" :disabled="accountBusy" type="password" autocomplete="new-password" :required="!editingAccountId" :placeholder="editingAccountId ? '已保存；仅在需要更换时填写' : ''" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm font-mono focus:border-accent focus:outline-none" /></div>
        <div v-if="provider === '115'"><label for="account-token" class="block mb-2 text-xs font-mono uppercase tracking-wider text-text-muted font-medium">115 开放平台 Access Token（可选）</label><input id="account-token" v-model="newAccount.token" :disabled="accountBusy" type="password" autocomplete="new-password" :placeholder="editingAccountId ? '已保存；留空则保留' : '配置后优先使用原生秒传与分片上传'" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 text-sm font-mono focus:border-accent focus:outline-none" /></div>
        <label class="inline-flex min-h-11 items-center gap-3 text-sm text-text cursor-pointer"><input v-model="newAccount.is_default" :disabled="accountBusy" type="checkbox" class="accent-accent" />设为{{ providerLabel }}默认账号</label>
        <p v-if="editingAccountId" class="text-xs leading-5 text-text-muted">Cookie 和 Access Token 不会回显；留空会保留已保存值。</p>
        <p v-if="accountError" role="alert" class="text-sm text-danger break-words">{{ editingAccountId ? '更新' : '添加' }}账号失败：{{ accountError }}</p>
        <div class="flex justify-end gap-2 pt-2"><button type="button" :disabled="accountBusy" class="file-button" @click="showAccountModal = false">取消</button><button type="submit" :disabled="accountBusy" class="file-button bg-accent text-accent-contrast hover:bg-accent-strong">{{ accountBusy ? '正在保存…' : editingAccountId ? '保存账号修改' : '保存账号' }}</button></div>
      </form>
    </UiDialog>

    <UiDialog v-if="operation" :title="operationTitle" :busy="operationBusy" @close="operation = null">
      <form class="space-y-4" @submit.prevent="submitOperation">
        <p class="text-xs font-mono text-text-muted break-words">账号：{{ accountName }}<br />当前目录：{{ directoryName }}（CID: {{ currentCid }}）</p>
        <p class="text-sm font-medium text-text">{{ operation.name ? `项目：${operation.name}` : `已选择 ${operation.ids.length} 个项目` }}</p>
        <p v-if="operation.kind === 'move'" class="text-sm text-text-muted break-words">将 {{ operation.ids.length }} 个项目移动到「{{ accountName }} / {{ moveTargetName }}」（CID: {{ moveTargetCid }}）。</p>
        <p v-if="operation.kind === 'delete'" class="rounded-xl border border-danger/30 bg-danger/5 p-3.5 text-sm text-danger">删除后，{{ operation.ids.length }} 个项目将从当前目录移入{{ providerLabel }}回收站。请确认所选内容；恢复需前往{{ providerLabel }}网盘。</p>
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
.provider-tabs { width: fit-content; }
@media (max-width: 767px) {
  .desktop-files { display: none; }
  .mobile-files { display: block; }
  .action-dock > div:last-child { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .action-dock > div:last-child > div { grid-column: 1 / -1; }
  .provider-tabs { display: grid; width: 100%; }
  .provider-tabs button { min-height: 44px; }
  .files-view button,
  .files-view select,
  .files-view input:not([type='checkbox']) { min-height: 44px; }
}
</style>

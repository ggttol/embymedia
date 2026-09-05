import { computed, ref } from 'vue'

const STORAGE_KEY = 'embymedia_default_target_cid'
const cidMap = ref<Record<string, string>>({})
const selectedCid = ref(readPreference())
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const storageError = ref('')
let pending: Promise<void> | null = null

function readPreference(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) || ''
  } catch {
    return ''
  }
}

export interface TransferDestination {
  cid: string
  label: string
}

const options = computed(() => Object.entries(cidMap.value).map(([name, cid]) => ({ cid, name })))
const available = computed(() => selectedCid.value === '0' || options.value.some(option => option.cid === selectedCid.value))
const ready = computed(() => loaded.value && !loading.value && !error.value && available.value)
const targetLabel = computed(() => {
  if (selectedCid.value === '0') return '根目录（CID: 0）'
  const option = options.value.find(option => option.cid === selectedCid.value)
  return option ? `${option.name}（CID: ${option.cid}）` : selectedCid.value ? `不可用目录（CID: ${selectedCid.value}）` : '请选择目标目录'
})
const targetCid = computed({
  get: () => selectedCid.value,
  set: (cid: string) => {
    selectedCid.value = cid
    storageError.value = ''
    try {
      localStorage.setItem(STORAGE_KEY, cid)
    } catch {
      storageError.value = '此浏览器无法保存目录偏好；本次选择仍然有效。'
    }
  },
})

async function loadTargets(): Promise<void> {
  if (pending) return pending
  loading.value = true
  error.value = ''
  pending = (async () => {
    try {
      const response = await fetch('/api/v1/cid-map')
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      if (!data.map || typeof data.map !== 'object' || Array.isArray(data.map) || Object.values(data.map).some(cid => typeof cid !== 'string')) {
        throw new Error('目录配置响应格式不正确')
      }
      cidMap.value = data.map
      loaded.value = true
      if (!selectedCid.value) targetCid.value = Object.values(cidMap.value)[0] || '0'
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : String(cause)
    } finally {
      loading.value = false
      pending = null
    }
  })()
  return pending
}

/** Captures the destination before a request so later preference changes cannot relabel its result. */
function destination(): TransferDestination | null {
  return ready.value ? { cid: selectedCid.value, label: targetLabel.value } : null
}

export async function saveResource(id: number, target: TransferDestination): Promise<string> {
  const response = await fetch(`/api/v1/links/${id}/save`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target_cid: target.cid }),
  })
  const data = await response.json()
  if (!response.ok || data.success !== true) throw new Error(data.error || `HTTP ${response.status}`)
  if (data.method === 'share_save') return `已转存 ${data.count} 项至 ${target.label}`
  if (data.method === 'offline') return `已提交 115 离线下载队列，目标：${target.label}；完成状态请在 115 查看。`
  throw new Error('转存响应缺少可识别的执行方式，请先检查 115 目录，避免重复提交。')
}

export function useTransferTarget() {
  return { targetCid, targetLabel, options, available, ready, loading, error, storageError, loadTargets, destination }
}

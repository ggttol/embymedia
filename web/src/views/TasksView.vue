<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { Activity, CalendarClock, FileText, ListTodo, Loader2, Play, Plus, RotateCcw, X } from 'lucide-vue-next'
import UiDialog from '../components/UiDialog.vue'

type TaskType = 'emby_refresh' | 'emby_match' | 'emby_missing_posters' | 'c115_save_share' | 'c115_offline_download' | 'strm_sync' | 'strm_verify'

interface AsyncTask {
  id: string
  type: string
  schedule_id?: string
  payload: Record<string, unknown>
  status: string
  progress: number
  error?: string
  result?: string
  attempts: number
  max_attempts: number
  created_at: string
  updated_at: string
}

interface ScheduledTask {
  id: string
  name: string
  type: string
  cron_expr: string
  params?: string
  status: string
  result?: string
  error?: string
  last_run_at?: string
  next_run_at?: string
}

interface TaskRun {
  id: number
  attempt: number
  status: string
  progress: number
  logs?: string[]
  error?: string
  started_at: string
  completed_at?: string
}
interface TaskDefinition {
  label: string
  description: string
  defaultName: string
}

const taskTypeOrder: TaskType[] = ['emby_refresh', 'emby_missing_posters', 'emby_match', 'strm_sync', 'strm_verify', 'c115_save_share', 'c115_offline_download']
const taskDefinitions: Record<TaskType, TaskDefinition> = {
  emby_refresh: { label: '刷新 Emby 媒体库', description: '全库刷新会跟踪 Emby 的真实扫描进度直到结束；指定媒体库刷新会记录 Emby 已接受请求。', defaultName: '每日刷新媒体库' },
  emby_missing_posters: { label: '检查缺失海报', description: '找出没有主海报的 Emby 条目，结果会保存在执行记录中。', defaultName: '每日检查缺失海报' },
  emby_match: { label: '修正媒体匹配', description: '把一个 Emby 条目明确匹配到指定 TMDB 条目。', defaultName: '修正媒体匹配' },
  strm_sync: { label: '同步 STRM 文件', description: '根据媒体源目录创建或更新 STRM 文件。', defaultName: '每日同步 STRM' },
  strm_verify: { label: '检查 STRM 链接', description: '检查 STRM 是否仍指向媒体源目录中的有效文件。', defaultName: '每日检查 STRM' },
  c115_save_share: { label: '转存 115 分享', description: '按计划把一个 115 分享链接转存到指定目录。', defaultName: '定时转存 115 分享' },
  c115_offline_download: { label: '提交 115 离线下载', description: '批量提交下载地址，并保存到指定 115 目录。', defaultName: '定时提交离线下载' },
}
const frequencyOptions = [
  { value: 'daily', label: '每天凌晨 3 点', cron: '0 0 3 * * *' },
  { value: 'sixHours', label: '每 6 小时', cron: '0 0 */6 * * *' },
  { value: 'hourly', label: '每小时', cron: '0 0 * * * *' },
  { value: 'weekly', label: '每周一凌晨 3 点', cron: '0 0 3 * * 1' },
  { value: 'custom', label: '自定义时间', cron: '' },
] as const

function newScheduleForm() {
  return {
    name: taskDefinitions.emby_refresh.defaultName,
    type: 'emby_refresh' as TaskType,
    frequency: 'daily',
    cron_expr: '0 0 3 * * *',
    enabled: true,
    library_id: '',
    item_id: '',
    tmdb_id: '',
    share_url: '',
    share_password: '',
    urls: '',
    target_cid: '',
    account_id: '',
    library: '',
  }
}

const tasks = ref<ScheduledTask[]>([])
const asyncTasks = ref<AsyncTask[]>([])
const loading = ref(false)
const schedulesLoaded = ref(false)
const executionsLoaded = ref(false)
const schedulesError = ref('')
const executionsError = ref('')
const executingId = ref<string | null>(null)
const actionMessage = ref('')
const scheduleErrors = ref<Record<string, string>>({})
const taskErrors = ref<Record<string, string>>({})
const actionBusyId = ref<string | null>(null)
const retryTarget = ref<AsyncTask | null>(null)
const retryError = ref('')
const taskRuns = ref<Record<string, TaskRun[]>>({})
const runsLoading = ref<Record<string, boolean>>({})
const runsErrors = ref<Record<string, string>>({})
const showScheduleForm = ref(false)
const scheduleForm = ref(newScheduleForm())
const savingSchedule = ref(false)
const scheduleError = ref('')
const highlightedTaskId = ref('')
const selectedDefinition = computed(() => taskDefinitions[scheduleForm.value.type])
const activeTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'pending' || task.status === 'running').length)
const completedTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'completed').length)
const attentionTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'failed' || task.status === 'cancelled' || taskHasFindings(task)).length)
let pollTimer: number | undefined

let schedulesGeneration = 0
let executionsGeneration = 0
function taskDefinition(type: string): TaskDefinition {
  return taskDefinitions[type as TaskType] ?? { label: '未知任务', description: '该任务类型不受当前页面支持。', defaultName: '未知任务' }
}

function statusLabel(status: string) {
  return ({ pending: '等待执行', running: '正在执行', completed: '已完成', failed: '执行失败', cancelled: '已取消', idle: '等待下次执行', paused: '已停用' } as Record<string, string>)[status] ?? status
}

function frequencyLabel(cron: string) {
  return frequencyOptions.find((option) => option.cron === cron)?.label ?? `自定义时间：${cron}`
}

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '尚未执行'
}

function formatDuration(start?: string, end?: string) {
  if (!start) return '尚未开始'
  if (!end) return '进行中'
  const milliseconds = new Date(end).getTime() - new Date(start).getTime()
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return '时间无效'
  if (milliseconds < 1000) return `${milliseconds} 毫秒`
  const seconds = milliseconds / 1000
  if (seconds < 60) return `${seconds.toFixed(1)} 秒`
  const minutes = Math.floor(seconds / 60)
  return `${minutes} 分 ${Math.round(seconds % 60)} 秒`
}

function parsedResult(task: AsyncTask): unknown {
  if (!task.result) return null
  try {
    return JSON.parse(task.result)
  } catch {
    return task.result
  }
}

function resultRecord(task: AsyncTask): Record<string, unknown> | null {
  const result = parsedResult(task)
  return typeof result === 'object' && result !== null && !Array.isArray(result) ? result as Record<string, unknown> : null
}

function taskHasFindings(task: AsyncTask) {
  if (task.status === 'failed' || task.status === 'cancelled') return true
  if (task.status !== 'completed') return false
  const result = resultRecord(task)
  if (!result) return false
  if (task.type === 'emby_missing_posters') return Number(result.total_missing ?? result.returned ?? 0) > 0
  if (task.type === 'strm_sync' || task.type === 'strm_verify') {
    const strm = typeof result.strm === 'object' && result.strm !== null ? result.strm as Record<string, unknown> : null
    return Number(strm?.missing || 0) > 0 || Number(strm?.invalid || 0) > 0
  }
  return false
}

function taskStatusLabel(task: AsyncTask) {
  return task.status === 'completed' && taskHasFindings(task) ? '已完成 · 有发现' : statusLabel(task.status)
}

function resultSummary(task: AsyncTask) {
  const result = resultRecord(task)
  if (!result) return task.result ? '任务返回了文本结果。' : ''
  switch (task.type) {
    case 'emby_refresh':
      return result.completion_tracked === true
        ? `Emby 全库扫描已完成 · ${formatDuration(String(result.started_at || ''), String(result.completed_at || ''))}`
        : 'Emby 已接受刷新请求；该范围不提供后台完成状态。'
    case 'emby_missing_posters': {
      const total = Number(result.total_missing ?? result.returned ?? 0)
      const returned = Number(result.returned || 0)
      return result.truncated === true ? `发现 ${total} 个缺少海报的条目，显示前 ${returned} 个。` : `发现 ${total} 个缺少海报的条目。`
    }
    case 'emby_match': return `已向 Emby 提交 TMDB ${String(result.tmdb_id || '')} 的匹配结果。`
    case 'c115_save_share': return `已转存 ${Number(result.count || 0)} 个项目${result.title ? ` · ${String(result.title)}` : ''}。`
    case 'c115_offline_download': return `已提交 ${Array.isArray(result.task_ids) ? result.task_ids.length : 0} 个离线任务。`
    case 'strm_sync':
    case 'strm_verify': {
      const strm = typeof result.strm === 'object' && result.strm !== null ? result.strm as Record<string, unknown> : null
      return strm ? `有效 ${Number(strm.valid || 0)} · 缺失 ${Number(strm.missing || 0)} · 无效 ${Number(strm.invalid || 0)}${Number(strm.removed || 0) > 0 ? ` · 已清理 ${Number(strm.removed)} 个旧 STRM` : ''}` : 'STRM 操作已完成。'
    }
    default: return '任务已保存执行结果。'
  }
}

function resultDetails(task: AsyncTask) {
  const result = parsedResult(task)
  return typeof result === 'string' ? result : JSON.stringify(result, null, 2)
}

function scheduleName(task: AsyncTask) {
  return task.schedule_id ? tasks.value.find((schedule) => schedule.id === task.schedule_id)?.name : undefined
}

function activeExecution(schedule: ScheduledTask) {
  return asyncTasks.value.find((task) => (task.schedule_id === schedule.id || task.id === schedule.result) && (task.status === 'pending' || task.status === 'running'))
}

function userError(error?: string) {
  if (!error) return ''
  const known: Record<string, string> = {
    'media_root and strm_root must be configured': '请先在系统设置中填写媒体源根目录和 STRM 输出根目录。',
    'Emby API request failed': '无法连接 Emby，请检查服务地址和接口密钥。',
  }
  if (known[error]) return known[error]
  if (error.includes('is required')) return '任务缺少必填信息，请检查自动任务的执行参数。'
  return error
}

function taskSummary(type: string, payload: Record<string, unknown> = {}) {
  switch (type) {
    case 'emby_refresh': return payload.library_id ? `媒体库 ID：${payload.library_id}` : '范围：全部 Emby 媒体库'
    case 'emby_missing_posters': return '范围：全部 Emby 媒体条目'
    case 'emby_match': return `Emby 条目 ${payload.item_id || '未填写'} → TMDB ${payload.tmdb_id || '未填写'}`
    case 'strm_sync': return payload.library ? `媒体目录：${payload.library}` : '范围：全部已配置媒体目录'
    case 'strm_verify': return payload.library ? `检查目录：${payload.library}` : '范围：全部 STRM 文件'
    case 'c115_save_share': return `分享链接：${payload.url || '未填写'}${payload.target_cid ? ` · 保存到 CID ${payload.target_cid}` : ''}`
    case 'c115_offline_download': return `${Array.isArray(payload.urls) ? payload.urls.length : 0} 个下载地址${payload.target_cid ? ` · 保存到 CID ${payload.target_cid}` : ''}`
    default: return '没有可显示的任务信息'
  }
}

function scheduledPayload(task: ScheduledTask): Record<string, unknown> {
  try {
    return task.params ? JSON.parse(task.params) : {}
  } catch {
    return {}
  }
}

function buildPayload(): Record<string, unknown> {
  const form = scheduleForm.value
  switch (form.type) {
    case 'emby_refresh': return form.library_id.trim() ? { library_id: form.library_id.trim() } : {}
    case 'emby_missing_posters': return {}
    case 'emby_match':
      if (!form.item_id.trim() || !form.tmdb_id.trim()) throw new Error('请填写 Emby 条目 ID 和 TMDB ID。')
      return { item_id: form.item_id.trim(), tmdb_id: form.tmdb_id.trim() }
    case 'strm_sync':
    case 'strm_verify': return form.library.trim() ? { library: form.library.trim() } : {}
    case 'c115_save_share':
      if (!form.share_url.trim()) throw new Error('请填写 115 分享链接。')
      return {
        url: form.share_url.trim(),
        ...(form.share_password.trim() ? { password: form.share_password.trim() } : {}),
        ...(form.target_cid.trim() ? { target_cid: form.target_cid.trim() } : {}),
        ...(form.account_id.trim() ? { account_id: form.account_id.trim() } : {}),
      }
    case 'c115_offline_download': {
      const urls = form.urls.split(/\r?\n/).map((value) => value.trim()).filter(Boolean)
      if (urls.length === 0) throw new Error('请至少填写一个下载地址。')
      return {
        urls,
        ...(form.target_cid.trim() ? { target_cid: form.target_cid.trim() } : {}),
        ...(form.account_id.trim() ? { account_id: form.account_id.trim() } : {}),
      }
    }
  }
}

function selectTaskType() {
  scheduleForm.value.name = selectedDefinition.value.defaultName
}

async function fetchSchedules() {
  const generation = ++schedulesGeneration
  try {
    const response = await fetch('/api/v1/tasks')
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取自动任务失败')
    if (generation !== schedulesGeneration) return
    tasks.value = data.tasks ?? []
    schedulesLoaded.value = true
    schedulesError.value = ''
  } catch (error) {
    if (generation === schedulesGeneration) schedulesError.value = error instanceof Error ? error.message : '读取自动任务失败'
  }
}

async function fetchExecutions() {
  const generation = ++executionsGeneration
  try {
    const response = await fetch('/api/v1/async-tasks')
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取执行记录失败')
    if (generation !== executionsGeneration) return
    asyncTasks.value = data.tasks ?? []
    executionsLoaded.value = true
    executionsError.value = ''
    await Promise.all(Object.keys(taskRuns.value).map((id) => fetchTaskRuns(id, true)))
  } catch (error) {
    if (generation === executionsGeneration) executionsError.value = error instanceof Error ? error.message : '读取执行记录失败'
  }
}

async function fetchTasks() {
  if (loading.value) return
  loading.value = true
  try {
    await Promise.all([fetchSchedules(), fetchExecutions()])
  } finally {
    loading.value = false
  }
}

async function runTask(task: ScheduledTask) {
  if (executingId.value || activeExecution(task)) return
  executingId.value = task.id
  actionMessage.value = ''
  scheduleErrors.value[task.id] = ''
  try {
    const response = await fetch(`/api/v1/tasks/${encodeURIComponent(task.id)}/run`, { method: 'POST' })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '启动任务失败')
    if (!data.task?.id || data.task.schedule_id !== task.id || !data.schedule?.id) throw new Error('服务未返回可跟踪的执行记录')
    const queued = data.task as AsyncTask
    schedulesGeneration++
    executionsGeneration++
    asyncTasks.value = [queued, ...asyncTasks.value.filter((item) => item.id !== queued.id)]
    tasks.value = tasks.value.map((item) => item.id === data.schedule.id ? data.schedule as ScheduledTask : item)
    executionsLoaded.value = true
    taskRuns.value = { ...taskRuns.value, [queued.id]: [] }
    highlightedTaskId.value = queued.id
    actionMessage.value = `“${task.name}”已创建真实执行记录，正在跟踪后台状态。`
    schedulePoll(250)
    await nextTick()
    document.getElementById(`execution-${queued.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  } catch (error) {
    scheduleErrors.value[task.id] = error instanceof Error ? error.message : '启动任务失败'
  } finally {
    executingId.value = null
  }
}

async function cancelTask(task: AsyncTask) {
  if (actionBusyId.value || (task.status !== 'pending' && task.status !== 'running')) return
  actionBusyId.value = task.id
  taskErrors.value[task.id] = ''
  actionMessage.value = ''
  try {
    const response = await fetch(`/api/v1/async-tasks/${encodeURIComponent(task.id)}/cancel`, { method: 'POST' })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '停止任务失败')
    actionMessage.value = `已请求停止“${taskDefinition(task.type).label}”。`
    await fetchTasks()
  } catch (error) {
    taskErrors.value[task.id] = error instanceof Error ? error.message : '停止任务失败'
  } finally {
    actionBusyId.value = null
  }
}

function openRetry(task: AsyncTask) {
  if (actionBusyId.value) return
  retryError.value = ''
  retryTarget.value = task
}

function closeRetry() {
  if (!actionBusyId.value) retryTarget.value = null
}

async function retryTask() {
  const task = retryTarget.value
  if (!task || actionBusyId.value) return
  actionBusyId.value = task.id
  actionMessage.value = ''
  retryError.value = ''
  try {
    const response = await fetch(`/api/v1/async-tasks/${encodeURIComponent(task.id)}/retry`, { method: 'POST' })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '重新执行失败')
    if (!data.id) throw new Error('服务未返回可跟踪的执行记录')
    const queued = data as AsyncTask
    schedulesGeneration++
    executionsGeneration++
    asyncTasks.value = [queued, ...asyncTasks.value.filter((item) => item.id !== queued.id)]
    executionsLoaded.value = true
    taskRuns.value = { ...taskRuns.value, [queued.id]: [] }
    highlightedTaskId.value = queued.id
    actionMessage.value = `“${taskDefinition(task.type).label}”已创建新的执行记录。`
    retryTarget.value = null
    await fetchSchedules()
    schedulePoll(250)
    await nextTick()
    document.getElementById(`execution-${queued.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  } catch (error) {
    retryError.value = error instanceof Error ? error.message : '重新执行失败'
  } finally {
    actionBusyId.value = null
  }
}

async function fetchTaskRuns(id: string, background = false) {
  if (runsLoading.value[id] && !background) return
  if (!background) runsLoading.value[id] = true
  runsErrors.value[id] = ''
  try {
    const response = await fetch(`/api/v1/async-tasks/${encodeURIComponent(id)}/runs`)
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取执行记录失败')
    taskRuns.value = { ...taskRuns.value, [id]: data.runs ?? [] }
  } catch (error) {
    runsErrors.value[id] = error instanceof Error ? error.message : '读取执行记录失败'
  } finally {
    if (!background) runsLoading.value[id] = false
  }
}

async function toggleTaskRuns(id: string) {
  if (runsLoading.value[id]) return
  if (taskRuns.value[id]) {
    const next = { ...taskRuns.value }
    delete next[id]
    taskRuns.value = next
    return
  }
  await fetchTaskRuns(id)
}

function formatLog(line: string) {
  const [verb, type] = line.split(' ', 2)
  if (verb === 'started') return `开始执行：${taskDefinition(type).label}`
  if (verb === 'completed') return `执行记录完成：${taskDefinition(type).label}`
  if (verb === 'cancelled') return `已停止：${taskDefinition(type).label}`
  if (line === 'Emby accepted the full-library scan') return 'Emby 已接受全库扫描任务。'
  if (line === 'Emby full-library scan started') return 'Emby 已开始扫描媒体库。'
  if (line === 'Emby full-library scan reached its recorded terminal state') return 'Emby 已记录扫描完成。'
  if (line === 'Emby full-library scan was already running; tracking the existing run') return 'Emby 全库扫描已在运行，当前执行会继续跟踪它。'
  if (line === 'Emby accepted the item refresh; this endpoint does not expose completion state') return 'Emby 已接受指定媒体库刷新；该接口不提供后台完成状态。'
  const progress = /^Emby scan progress (\d+)%$/.exec(line)
  if (progress) return `Emby 扫描进度：${progress[1]}%`
  const posterFindings = /^Emby missing-poster findings total=(\d+) returned=(\d+) truncated=(true|false)$/.exec(line)
  if (posterFindings) return `Emby 缺失海报：共 ${posterFindings[1]} 个，本次返回 ${posterFindings[2]} 个${posterFindings[3] === 'true' ? '，结果已截断' : ''}。`
  const sync = /^STRM sync media=(\d+) created=(\d+) updated=(\d+) removed=(\d+) prune=(\S+)$/.exec(line)
  if (sync) return `STRM 同步：媒体 ${sync[1]} 个，新建 ${sync[2]} 个，更新 ${sync[3]} 个，清理旧文件 ${sync[4]} 个；清理状态 ${sync[5]}。`
  const findings = /^STRM findings valid=(\d+) missing=(\d+) invalid=(\d+)$/.exec(line)
  if (findings) return `STRM 校验：有效 ${findings[1]} 个，缺失 ${findings[2]} 个，无效 ${findings[3]} 个。`
  if (line === 'STRM source scan started') return '开始扫描媒体源文件。'
  const sourceProgress = /^STRM source scan processed (\d+) media files$/.exec(line)
  if (sourceProgress) return `已扫描 ${sourceProgress[1]} 个媒体文件。`
  const sourceComplete = /^STRM source scan completed with (\d+) media files$/.exec(line)
  if (sourceComplete) return `媒体源扫描完成，共 ${sourceComplete[1]} 个视频文件。`
  const reconciliation = /^STRM stale reconciliation removed (\d+) files with status (\S+)$/.exec(line)
  if (reconciliation) return `旧 STRM 协调完成：清理 ${reconciliation[1]} 个；状态 ${reconciliation[2]}。`
  if (line === 'STRM verification started') return '开始逐项验证 STRM 目标。'
  const verified = /^STRM verification checked (\d+) files$/.exec(line)
  if (verified) return `已验证 ${verified[1]} 个 STRM 文件。`
  const verificationComplete = /^STRM verification completed after (\d+) files$/.exec(line)
  if (verificationComplete) return `STRM 验证完成，共检查 ${verificationComplete[1]} 个文件。`
  if (line === 'STRM synchronization and verification completed') return 'STRM 同步和验证全部完成。'
  if (line.startsWith('failed: ')) return `执行失败：${userError(line.slice(8))}`
  return line
}

async function createSchedule() {
  if (savingSchedule.value) return
  savingSchedule.value = true
  scheduleError.value = ''
  actionMessage.value = ''
  try {
    const payload = buildPayload()
    const preset = frequencyOptions.find((option) => option.value === scheduleForm.value.frequency)
    const cronExpr = scheduleForm.value.frequency === 'custom' ? scheduleForm.value.cron_expr.trim() : preset?.cron
    if (!cronExpr) throw new Error('请填写自定义执行时间。')
    const response = await fetch('/api/v1/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: scheduleForm.value.name.trim(),
        type: scheduleForm.value.type,
        cron_expr: cronExpr,
        params: JSON.stringify(payload),
        enabled: scheduleForm.value.enabled,
      }),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '创建自动任务失败')
    actionMessage.value = `已创建自动任务“${data.name}”。`
    scheduleForm.value = newScheduleForm()
    showScheduleForm.value = false
    await fetchTasks()
  } catch (error) {
    scheduleError.value = error instanceof Error ? error.message : '创建自动任务失败'
  } finally {
    savingSchedule.value = false
  }
}

function asyncTone(task: AsyncTask) {
  if (task.status === 'completed' && taskHasFindings(task)) return 'bg-warn'
  if (task.status === 'completed') return 'bg-ok'
  if (task.status === 'running') return 'bg-warn animate-pulse'
  if (task.status === 'failed' || task.status === 'cancelled') return 'bg-danger'
  return 'bg-text-faint'
}

function schedulePoll(delay: number) {
  if (pollTimer) window.clearTimeout(pollTimer)
  pollTimer = window.setTimeout(pollTasks, delay)
}

async function pollTasks() {
  await fetchTasks()
  schedulePoll(activeTaskCount.value > 0 ? 1000 : 5000)
}

onMounted(() => { void pollTasks() })
onUnmounted(() => { if (pollTimer) window.clearTimeout(pollTimer) })
</script>

<template>
  <div class="task-center-page space-y-7 max-w-6xl">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-5 pb-7 border-b border-border">
      <div class="max-w-2xl">
        <p class="text-[10px] font-mono font-bold tracking-[0.18em] text-annotation mb-2">AUTOMATION / EXECUTION HISTORY</p>
        <h1 class="font-serif text-3xl font-bold text-text">任务中心</h1>
        <p class="text-sm text-text-muted mt-2">创建自动任务，查看每次执行结果，并处理失败或仍在运行的工作。</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" @click="showScheduleForm = !showScheduleForm" :disabled="savingSchedule" class="flex min-h-11 items-center gap-2 px-4 border border-accent bg-accent text-xs font-medium text-accent-contrast disabled:opacity-50">
          <X v-if="showScheduleForm" class="w-4 h-4" /><Plus v-else class="w-4 h-4" />{{ showScheduleForm ? '收起创建表单' : '新建自动任务' }}
        </button>
        <button type="button" @click="fetchTasks" :disabled="loading" class="flex min-h-11 items-center gap-2 px-4 border border-border bg-surface text-xs font-medium text-text disabled:opacity-50">
          <RotateCcw class="w-4 h-4" :class="{ 'animate-spin': loading }" />刷新
        </button>
      </div>
    </header>

    <div class="grid grid-cols-3 border border-border bg-surface">
      <div class="p-4 sm:p-5 border-r border-border"><strong class="block font-serif text-2xl text-text">{{ executionsLoaded ? activeTaskCount : '—' }}</strong><span class="text-xs text-text-muted">正在处理</span></div>
      <div class="p-4 sm:p-5 border-r border-border"><strong class="block font-serif text-2xl text-ok">{{ executionsLoaded ? completedTaskCount : '—' }}</strong><span class="text-xs text-text-muted">执行完成</span></div>
      <div class="p-4 sm:p-5"><strong class="block font-serif text-2xl" :class="attentionTaskCount ? 'text-warn' : 'text-text'">{{ executionsLoaded ? attentionTaskCount : '—' }}</strong><span class="text-xs text-text-muted">历史异常 / 发现</span></div>
    </div>
    <p v-if="!executionsLoaded" class="text-xs text-text-muted">{{ executionsError ? '执行记录尚未读取成功，暂时无法统计任务。' : '正在读取执行记录与任务统计…' }}</p>

    <p v-if="actionMessage" role="status" class="p-3 border-l-2 border-ok bg-ok/5 text-sm text-ok">{{ actionMessage }}</p>

    <form v-if="showScheduleForm" class="border border-border bg-surface shadow-sm" @submit.prevent="createSchedule">
      <fieldset :disabled="savingSchedule">
      <div class="p-5 sm:p-7 border-b border-border">
        <div class="flex items-center gap-2 mb-1"><CalendarClock class="w-4 h-4 text-annotation" /><h2 class="font-serif text-xl font-semibold text-text">创建自动任务</h2></div>
        <p class="text-sm text-text-muted">先选择要完成的工作，再填写这项工作需要的信息和执行时间。</p>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7 border-b border-border">
        <div><h3 class="font-serif font-semibold text-text">一、选择工作</h3><p class="mt-1 text-xs leading-5 text-text-faint">这里显示业务名称，不需要记住内部工具名。</p></div>
        <div>
          <label for="schedule-type" class="block mb-2 text-xs font-medium text-text-muted">要执行什么</label>
          <select id="schedule-type" v-model="scheduleForm.type" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" @change="selectTaskType">
            <option v-for="type in taskTypeOrder" :key="type" :value="type">{{ taskDefinitions[type].label }}</option>
          </select>
          <p class="mt-3 border-l-2 border-accent pl-3 text-sm leading-6 text-text-muted">{{ selectedDefinition.description }}</p>
        </div>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7 border-b border-border">
        <div><h3 class="font-serif font-semibold text-text">二、填写范围</h3><p class="mt-1 text-xs leading-5 text-text-faint">留空的可选项会使用系统设置中的默认值。</p></div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div v-if="scheduleForm.type === 'emby_refresh'" class="md:col-span-2"><label for="library-id" class="block mb-2 text-xs font-medium text-text-muted">媒体库 ID（可选）</label><input id="library-id" v-model="scheduleForm.library_id" placeholder="留空时刷新全部媒体库" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          <template v-else-if="scheduleForm.type === 'emby_match'">
            <div><label for="item-id" class="block mb-2 text-xs font-medium text-text-muted">Emby 条目 ID</label><input id="item-id" v-model="scheduleForm.item_id" required class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
            <div><label for="tmdb-id" class="block mb-2 text-xs font-medium text-text-muted">TMDB ID</label><input id="tmdb-id" v-model="scheduleForm.tmdb_id" required class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          </template>
          <div v-else-if="scheduleForm.type === 'strm_sync' || scheduleForm.type === 'strm_verify'" class="md:col-span-2"><label for="library-path" class="block mb-2 text-xs font-medium text-text-muted">媒体目录（可选）</label><input id="library-path" v-model="scheduleForm.library" placeholder="例如：Movies；留空时处理全部目录" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          <template v-else-if="scheduleForm.type === 'c115_save_share'">
            <div class="md:col-span-2"><label for="share-url" class="block mb-2 text-xs font-medium text-text-muted">115 分享链接</label><input id="share-url" v-model="scheduleForm.share_url" type="url" required placeholder="https://115.com/s/..." class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
            <div><label for="share-password" class="block mb-2 text-xs font-medium text-text-muted">提取码（可选）</label><input id="share-password" v-model="scheduleForm.share_password" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          </template>
          <div v-else-if="scheduleForm.type === 'c115_offline_download'" class="md:col-span-2"><label for="download-urls" class="block mb-2 text-xs font-medium text-text-muted">下载地址</label><textarea id="download-urls" v-model="scheduleForm.urls" required rows="5" placeholder="每行填写一个 HTTP、HTTPS、磁力或电驴地址" class="w-full border border-border bg-bg px-3 py-3 text-sm"></textarea></div>
          <template v-if="scheduleForm.type === 'c115_save_share' || scheduleForm.type === 'c115_offline_download'">
            <div><label for="target-cid" class="block mb-2 text-xs font-medium text-text-muted">保存目录 CID（可选）</label><input id="target-cid" v-model="scheduleForm.target_cid" placeholder="留空时使用根目录" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
            <div><label for="account-id" class="block mb-2 text-xs font-medium text-text-muted">115 账号 ID（可选）</label><input id="account-id" v-model="scheduleForm.account_id" placeholder="留空时使用默认账号" class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          </template>
          <p v-if="scheduleForm.type === 'emby_missing_posters'" class="md:col-span-2 p-4 border border-border bg-bg text-sm text-text-muted">无需额外参数。系统会检查全部 Emby 媒体条目。</p>
        </div>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7">
        <div><h3 class="font-serif font-semibold text-text">三、设置时间</h3><p class="mt-1 text-xs leading-5 text-text-faint">任务创建后也可以随时手动执行。</p></div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="schedule-name" class="block mb-2 text-xs font-medium text-text-muted">任务名称</label><input id="schedule-name" v-model="scheduleForm.name" required class="w-full min-h-11 border border-border bg-bg px-3 text-sm" /></div>
          <div><label for="schedule-frequency" class="block mb-2 text-xs font-medium text-text-muted">执行频率</label><select id="schedule-frequency" v-model="scheduleForm.frequency" class="w-full min-h-11 border border-border bg-bg px-3 text-sm"><option v-for="option in frequencyOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></div>
          <div v-if="scheduleForm.frequency === 'custom'" class="md:col-span-2"><label for="schedule-cron" class="block mb-2 text-xs font-medium text-text-muted">自定义 Cron 表达式（秒 分 时 日 月 周）</label><input id="schedule-cron" v-model="scheduleForm.cron_expr" required placeholder="0 0 3 * * *" class="w-full min-h-11 border border-border bg-bg px-3 font-mono text-sm" /></div>
          <label class="inline-flex min-h-11 items-center gap-2 text-sm"><input v-model="scheduleForm.enabled" type="checkbox" />创建后自动按计划执行</label>
        </div>
      </div>
      </fieldset>
      <p v-if="scheduleError" role="alert" class="mx-5 mb-5 border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger sm:mx-7">{{ scheduleError }}</p>

      <footer class="flex flex-col-reverse sm:flex-row sm:items-center sm:justify-end gap-2 p-5 sm:px-7 border-t border-border bg-bg-muted/60">
        <button type="button" :disabled="savingSchedule" class="min-h-11 px-5 border border-border bg-surface text-sm disabled:opacity-50" @click="showScheduleForm = false">取消</button>
        <button type="submit" :disabled="savingSchedule" class="inline-flex min-h-11 items-center justify-center gap-2 px-5 bg-accent text-accent-contrast text-sm font-medium disabled:opacity-50"><Loader2 v-if="savingSchedule" class="w-4 h-4 animate-spin" />{{ savingSchedule ? '正在保存' : '保存自动任务' }}</button>
      </footer>
    </form>

    <section class="space-y-3" aria-labelledby="execution-heading">
      <div class="flex items-center justify-between gap-3 px-1">
        <div><div class="flex items-center gap-2"><Activity class="w-4 h-4 text-annotation" /><h2 id="execution-heading" class="font-serif font-semibold text-xl text-text">最近执行</h2></div><p class="mt-1 text-xs text-text-faint">执行中每秒更新，空闲时每 5 秒更新；开始、结束、耗时、进度、日志与结果均保留。</p></div>
        <span class="text-xs font-mono text-text-faint">{{ executionsLoaded ? `${asyncTasks.length} 条` : '—' }}</span>
      </div>

      <div v-if="executionsError" role="alert" class="border-l-2 border-danger bg-danger/5 p-4 text-sm text-danger"><p>{{ executionsError }}</p><p v-if="executionsLoaded" class="mt-1">以下为上次读取的执行记录。</p><button type="button" :disabled="loading" class="mt-2 min-h-11 border border-danger/40 px-3 disabled:opacity-50" @click="fetchTasks">{{ loading ? '正在重试' : '重新读取' }}</button></div>
      <div v-else-if="!executionsLoaded" role="status" class="flex items-center justify-center gap-2 p-8 border border-border bg-surface text-sm text-text-muted"><Loader2 class="w-4 h-4 animate-spin" />正在读取执行记录</div>
      <div v-else-if="asyncTasks.length === 0" class="p-8 border border-dashed border-border bg-surface text-center text-text-faint text-sm">还没有执行记录。创建自动任务并选择“立即执行”后，进度会显示在这里。</div>

      <article v-for="task in asyncTasks" :id="`execution-${task.id}`" :key="task.id" class="p-5 border bg-surface shadow-sm space-y-4 transition-colors" :class="highlightedTaskId === task.id ? 'border-accent' : 'border-border'">
        <div class="flex flex-col sm:flex-row sm:items-start justify-between gap-3">
          <div class="flex items-start gap-3 min-w-0">
            <span class="w-2.5 h-2.5 mt-1.5 rounded-full shrink-0" :class="asyncTone(task)"></span>
            <div><h3 class="font-serif text-lg font-semibold text-text">{{ taskDefinition(task.type).label }}</h3><p class="mt-1 text-sm text-text-muted">{{ taskDefinition(task.type).description }}</p></div>
          </div>
          <span class="self-start px-2.5 py-1 text-xs font-semibold" :class="{ 'bg-ok/10 text-ok': task.status === 'completed' && !taskHasFindings(task), 'bg-warn/10 text-warn': task.status === 'running' || (task.status === 'completed' && taskHasFindings(task)), 'bg-danger/10 text-danger': task.status === 'failed' || task.status === 'cancelled', 'bg-bg-muted text-text-faint': task.status === 'pending' }">{{ taskStatusLabel(task) }}</span>
        </div>

        <div class="grid sm:grid-cols-[1fr_auto] gap-3 p-3 border border-border/70 bg-bg text-sm">
          <div><span class="text-text-muted break-words">{{ taskSummary(task.type, task.payload) }}</span><span v-if="scheduleName(task)" class="block mt-1 text-xs text-accent">来源：{{ scheduleName(task) }}</span><span class="block mt-1 font-mono text-[10px] text-text-faint break-all">执行 ID：{{ task.id }}</span></div>
          <div class="text-xs font-mono text-text-faint sm:text-right"><span class="block">入队：{{ formatTime(task.created_at) }}</span><span class="block mt-1">更新：{{ formatTime(task.updated_at) }}</span></div>
        </div>

        <div v-if="task.status === 'running' || task.progress > 0" class="flex items-center gap-3">
          <div class="flex-1 h-1.5 rounded-full bg-bg-muted overflow-hidden"><div class="h-full rounded-full transition-all" :class="task.status === 'failed' ? 'bg-danger' : taskHasFindings(task) ? 'bg-warn' : 'bg-accent'" :style="{ width: Math.min(100, task.progress || 0) + '%' }"></div></div>
          <span class="text-xs font-mono text-text-muted w-12 text-right">{{ Math.round(task.progress || 0) }}%</span>
        </div>

        <div v-if="task.error" class="p-3 border-l-2 border-danger bg-danger/5 text-sm text-danger">{{ userError(task.error) }}</div>

        <div v-if="task.result" class="p-3 border-l-2 text-sm" :class="taskHasFindings(task) ? 'border-warn bg-warn/5' : 'border-ok bg-ok/5'">
          <p class="font-medium" :class="taskHasFindings(task) ? 'text-warn' : 'text-ok'">{{ resultSummary(task) }}</p>
          <details class="mt-2"><summary class="min-h-11 cursor-pointer py-3 text-xs text-text-muted">查看完整执行结果</summary><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words border border-border bg-bg p-3 text-xs">{{ resultDetails(task) }}</pre></details>
        </div>

        <div class="flex flex-wrap gap-2">
          <button type="button" :disabled="runsLoading[task.id]" :aria-expanded="Boolean(taskRuns[task.id])" class="inline-flex min-h-11 items-center gap-1.5 border border-border px-3 text-xs font-medium disabled:opacity-50" @click="toggleTaskRuns(task.id)"><Loader2 v-if="runsLoading[task.id]" class="w-3.5 h-3.5 animate-spin" /><FileText v-else class="w-3.5 h-3.5" />{{ runsLoading[task.id] ? '正在读取记录' : taskRuns[task.id] ? '收起执行记录' : runsErrors[task.id] ? '重试读取记录' : '查看执行记录' }}</button>
          <button v-if="task.status === 'pending' || task.status === 'running'" type="button" :disabled="Boolean(actionBusyId)" class="min-h-11 border border-danger/40 px-3 text-xs font-medium text-danger disabled:opacity-50" @click="cancelTask(task)">{{ actionBusyId === task.id ? '正在提交' : '停止任务' }}</button>
          <button v-if="task.status === 'failed' || task.status === 'cancelled'" type="button" :disabled="Boolean(actionBusyId)" class="min-h-11 border border-accent px-3 text-xs font-medium text-accent disabled:opacity-50" @click="openRetry(task)">重新执行</button>
        </div>
        <p v-if="taskErrors[task.id]" role="alert" class="border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger">{{ taskErrors[task.id] }}</p>
        <p v-if="runsErrors[task.id]" role="alert" class="border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger">{{ runsErrors[task.id] }}</p>

        <div v-if="taskRuns[task.id]" class="border-t border-border pt-4 space-y-3">
          <p v-if="taskRuns[task.id].length === 0" class="text-sm text-text-faint">任务正在等待 worker 创建执行记录。</p>
          <div v-for="run in taskRuns[task.id]" :key="run.id" class="border border-border/70 bg-bg p-4 text-sm space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-3"><span class="font-medium">第 {{ run.attempt }} 次执行 · {{ statusLabel(run.status) }}</span><span class="font-mono text-xs text-text-muted">{{ Math.round(run.progress) }}%</span></div>
            <dl class="grid gap-2 text-xs sm:grid-cols-3"><div><dt class="text-text-faint">开始时间</dt><dd class="mt-1 font-mono break-all">{{ formatTime(run.started_at) }}</dd></div><div><dt class="text-text-faint">结束时间</dt><dd class="mt-1 font-mono break-all">{{ run.completed_at ? formatTime(run.completed_at) : '尚未结束' }}</dd></div><div><dt class="text-text-faint">执行耗时</dt><dd class="mt-1 font-mono">{{ formatDuration(run.started_at, run.completed_at) }}</dd></div></dl>
            <p v-if="run.error" class="border-l-2 border-danger bg-danger/5 p-3 text-xs text-danger">{{ userError(run.error) }}</p>
            <ol v-if="run.logs?.length" class="space-y-2 break-words text-xs text-text-muted"><li v-for="(line, index) in run.logs" :key="index" class="grid grid-cols-[1.25rem_minmax(0,1fr)] gap-2"><span class="font-mono text-text-faint">{{ index + 1 }}.</span><span>{{ formatLog(line) }}</span></li></ol>
            <p v-else class="text-xs text-text-faint">暂时没有日志；执行中会自动更新。</p>
          </div>
        </div>
      </article>
    </section>

    <section class="space-y-3" aria-labelledby="schedule-heading">
      <div class="flex items-center justify-between gap-3 px-1">
        <div><div class="flex items-center gap-2"><ListTodo class="w-4 h-4 text-accent" /><h2 id="schedule-heading" class="font-serif font-semibold text-xl text-text">自动任务</h2></div><p class="mt-1 text-xs text-text-faint">系统会按计划执行；也可以随时手动启动一次。</p></div>
        <span class="text-xs font-mono text-text-faint">{{ schedulesLoaded ? `${tasks.length} 个` : '—' }}</span>
      </div>
      <div v-if="schedulesError" role="alert" class="border-l-2 border-danger bg-danger/5 p-4 text-sm text-danger"><p>{{ schedulesError }}</p><p v-if="schedulesLoaded" class="mt-1">以下为上次读取的自动任务。</p><button type="button" :disabled="loading" class="mt-2 min-h-11 border border-danger/40 px-3 disabled:opacity-50" @click="fetchTasks">{{ loading ? '正在重试' : '重新读取' }}</button></div>
      <div v-else-if="!schedulesLoaded" role="status" class="flex items-center justify-center gap-2 p-8 border border-border bg-surface text-sm text-text-muted"><Loader2 class="w-4 h-4 animate-spin" />正在读取自动任务</div>
      <div v-else-if="tasks.length === 0" class="p-8 border border-dashed border-border bg-surface text-center text-sm text-text-faint">还没有自动任务。点击页面右上角“新建自动任务”开始配置。</div>
      <article v-for="task in tasks" :key="task.id" class="p-5 border border-border bg-surface shadow-sm flex flex-col lg:flex-row lg:items-center justify-between gap-5">
        <div class="space-y-3 min-w-0">
          <div class="flex items-center gap-2.5 flex-wrap"><span class="w-2.5 h-2.5 rounded-full" :class="activeExecution(task) ? 'bg-warn animate-pulse' : { 'bg-ok': task.status === 'idle' || task.status === 'completed', 'bg-danger': task.status === 'failed', 'bg-text-faint': task.status === 'paused' }"></span><h3 class="font-serif text-lg font-semibold text-text">{{ task.name }}</h3><span class="px-2 py-0.5 bg-accent-soft text-xs font-medium text-accent">{{ frequencyLabel(task.cron_expr) }}</span></div>
          <div><strong class="text-sm text-text">{{ taskDefinition(task.type).label }}</strong><p class="mt-1 text-sm text-text-muted">{{ taskSummary(task.type, scheduledPayload(task)) }}</p></div>
          <div class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-text-faint"><span>最近启动：{{ formatTime(task.last_run_at) }}</span><span v-if="task.next_run_at">下次：{{ formatTime(task.next_run_at) }}</span><span v-if="activeExecution(task)" class="font-semibold text-warn">本次：{{ statusLabel(activeExecution(task)?.status || '') }} · {{ Math.round(activeExecution(task)?.progress || 0) }}%</span><span v-if="task.error" class="text-danger">{{ userError(task.error) }}</span></div>
          <p v-if="scheduleErrors[task.id]" role="alert" class="border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger">{{ scheduleErrors[task.id] }}</p>
        </div>
        <button type="button" @click="runTask(task)" :disabled="Boolean(executingId) || Boolean(activeExecution(task))" class="flex min-h-11 shrink-0 items-center justify-center gap-2 px-4 border border-accent bg-bg text-sm font-medium text-accent disabled:opacity-50">
          <Loader2 v-if="executingId === task.id || activeExecution(task)" class="w-4 h-4 animate-spin" /><Play v-else class="w-4 h-4" /><span>{{ executingId === task.id ? '正在创建执行' : activeExecution(task)?.status === 'pending' ? '等待执行' : activeExecution(task) ? `执行中 ${Math.round(activeExecution(task)?.progress || 0)}%` : '立即执行' }}</span>
        </button>
      </article>
    </section>

    <footer class="task-list-end" aria-label="任务列表结束"><span>END OF TASK HISTORY</span><strong>已显示全部执行记录与自动任务</strong></footer>

    <UiDialog v-if="retryTarget" title="重新执行任务" :busy="Boolean(actionBusyId)" @close="closeRetry">
      <form class="space-y-5" @submit.prevent="retryTask">
        <p class="text-sm leading-6 text-text-muted">确认重新执行“{{ taskDefinition(retryTarget.type).label }}”？系统会创建一条新的执行记录。</p>
        <p v-if="retryTarget.error" class="border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger">{{ userError(retryTarget.error) }}</p>
        <p v-if="retryError" role="alert" class="border-l-2 border-danger bg-danger/5 p-3 text-sm text-danger">{{ retryError }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" :disabled="Boolean(actionBusyId)" class="min-h-11 border border-border bg-surface px-5 text-sm disabled:opacity-50" @click="closeRetry">取消</button>
          <button type="submit" :disabled="Boolean(actionBusyId)" class="inline-flex min-h-11 items-center justify-center gap-2 bg-accent px-5 text-sm text-accent-contrast disabled:opacity-50"><Loader2 v-if="actionBusyId" class="w-4 h-4 animate-spin" />{{ actionBusyId ? '正在加入队列' : '确认重新执行' }}</button>
        </div>
      </form>
    </UiDialog>
  </div>
</template>

<style scoped>
.task-center-page { padding-bottom: max(10rem, 18vh); }
.task-list-end { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding-top: 20px; border-top: 1px solid var(--border); color: var(--text-faint); }
.task-list-end span { font: 700 10px/1.4 "SFMono-Regular", Consolas, monospace; letter-spacing: .14em; }
.task-list-end strong { font-size: 12px; font-weight: 500; }
@media (max-width: 639px) { .task-center-page { padding-bottom: 6rem; } .task-list-end { align-items: flex-start; flex-direction: column; gap: 6px; } }
</style>

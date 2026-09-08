<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { Activity, CalendarClock, FileText, FolderDown, ListTodo, Loader2, Play, Plus, RefreshCcw, RotateCcw, ScanSearch, ShieldCheck, WandSparkles, X } from 'lucide-vue-next'
import UiDialog from '../components/UiDialog.vue'

type TaskType = 'series_auto_fill' | 'emby_refresh' | 'emby_match' | 'emby_missing_posters' | 'emby_metadata_repair' | 'c115_save_share' | 'c115_offline_download' | 'strm_sync' | 'strm_verify'

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

const taskTypeOrder: TaskType[] = ['series_auto_fill', 'emby_metadata_repair', 'emby_missing_posters', 'emby_refresh', 'emby_match', 'strm_sync', 'strm_verify', 'c115_save_share', 'c115_offline_download']
const taskDefinitions: Record<TaskType, TaskDefinition> = {
  series_auto_fill: { label: '自动补集', description: '只检查“电视剧追更”和“综艺追更”的已播缺集；验证资源内的准确集号后，精确转存到原剧集目录并刷新 Emby。', defaultName: '每日自动补集' },
  emby_refresh: { label: '同步媒体并刷新 Emby', description: '先把网盘视频同步为 STRM，再跟踪 Emby 全库扫描直到结束；指定媒体库 ID 时只提交该库刷新。', defaultName: '每日同步媒体并刷新 Emby' },
  emby_missing_posters: { label: '检查并修复海报', description: '检查没有主海报的电影和剧集，向 Emby 请求完整图片刷新，并复查实际修复结果。', defaultName: '每周检查并修复海报' },
  emby_metadata_repair: { label: '检查并修复元数据', description: '检查缺少 TMDB 身份的电影和剧集；仅自动应用标题、年份、类型唯一一致且不会产生重复条目的候选。', defaultName: '每周检查并修复元数据' },
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
    auto_fill_libraries: ['电视剧追更', '综艺追更'] as string[],
    auto_fill_mode: 'transfer',
    candidate_limit: '10',
    max_series: '20',
    replace_completed_pack: true,
    metadata_limit: '100',
    metadata_auto_apply: true,
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
const executionFilter = ref<'all' | 'running' | 'completed' | 'attention'>('all')
const executionPageSize = 5
const visibleExecutionLimit = ref(executionPageSize)
const selectedDefinition = computed(() => taskDefinitions[scheduleForm.value.type])
const orderedTasks = computed(() => [...tasks.value].sort((left, right) => {
  const leftMinute = hourlyMinute(left.cron_expr)
  const rightMinute = hourlyMinute(right.cron_expr)
  if (leftMinute !== null && rightMinute !== null) return leftMinute - rightMinute
  return new Date(left.next_run_at || 0).getTime() - new Date(right.next_run_at || 0).getTime()
}))
const activeTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'pending' || task.status === 'running').length)
const completedTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'completed' && !taskHasFindings(task)).length)
const attentionTaskCount = computed(() => asyncTasks.value.filter((task) => task.status === 'failed' || task.status === 'cancelled' || taskHasFindings(task)).length)
const filteredAsyncTasks = computed(() => {
  if (executionFilter.value === 'running') return asyncTasks.value.filter((t) => t.status === 'pending' || t.status === 'running')
  if (executionFilter.value === 'completed') return asyncTasks.value.filter((t) => t.status === 'completed' && !taskHasFindings(t))
  if (executionFilter.value === 'attention') return asyncTasks.value.filter((t) => t.status === 'failed' || t.status === 'cancelled' || taskHasFindings(t))
  return asyncTasks.value
})
const visibleAsyncTasks = computed(() => filteredAsyncTasks.value.slice(0, visibleExecutionLimit.value))
const hiddenExecutionCount = computed(() => Math.max(0, filteredAsyncTasks.value.length - visibleAsyncTasks.value.length))
watch(executionFilter, () => { visibleExecutionLimit.value = executionPageSize })

function showMoreExecutions() {
  visibleExecutionLimit.value += executionPageSize
}

function collapseExecutions() {
  visibleExecutionLimit.value = executionPageSize
  document.getElementById('execution-heading')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
let pollTimer: number | undefined

let schedulesGeneration = 0
let executionsGeneration = 0
function taskDefinition(type: string): TaskDefinition {
  return taskDefinitions[type as TaskType] ?? { label: '未知任务', description: '该任务类型不受当前页面支持。', defaultName: '未知任务' }
}

function statusLabel(status: string) {
  return ({ pending: '等待执行', running: '正在执行', completed: '已完成', failed: '执行失败', cancelled: '已取消', idle: '等待下次执行', paused: '已停用' } as Record<string, string>)[status] ?? status
}

function hourlyMinute(cron: string): number | null {
  const fields = cron.trim().split(/\s+/)
  if (fields.length !== 6 || fields[0] !== '0' || fields[2] !== '*' || fields.slice(3).some((field) => field !== '*')) return null
  const minute = Number(fields[1])
  return Number.isInteger(minute) && minute >= 0 && minute <= 59 ? minute : null
}

function frequencyLabel(cron: string) {
  const minute = hourlyMinute(cron)
  if (minute !== null) return minute === 0 ? '每小时整点' : `每小时第 ${minute} 分钟`
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
  if (task.type === 'emby_missing_posters') return Number(result.remaining || 0) > 0 || (Array.isArray(result.failed) && result.failed.length > 0)
  if (task.type === 'emby_metadata_repair') return Number(result.needs_review || 0) > 0 || Number(result.no_match || 0) > 0
  if (task.type === 'series_auto_fill') return Number(result.remaining || 0) > 0 || (Array.isArray(result.libraries) && result.libraries.some((library) => typeof library === 'object' && library !== null && Array.isArray((library as Record<string, unknown>).issues) && ((library as Record<string, unknown>).issues as unknown[]).length > 0))
  if (task.type === 'strm_sync' || task.type === 'strm_verify' || task.type === 'emby_refresh') {
    const strm = typeof result.strm === 'object' && result.strm !== null ? result.strm as Record<string, unknown> : null
    if (Number(strm?.missing || 0) > 0 || Number(strm?.invalid || 0) > 0) return true
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
    case 'series_auto_fill': {
      const mode = result.mode === 'preview' ? '预检' : '自动转存'
      return `${mode}：发现 ${Number(result.missing || 0)} 集，匹配 ${Number(result.matched || 0)} 集，转存 ${Number(result.transferred || 0)} 集，仍缺 ${Number(result.remaining || 0)} 集。`
    }
    case 'emby_refresh': {
      const strm = typeof result.strm === 'object' && result.strm !== null ? result.strm as Record<string, unknown> : null
      const synchronized = strm ? `STRM 新建 ${Number(strm.created || 0)}、更新 ${Number(strm.updated || 0)}、清理 ${Number(strm.removed || 0)} 个旧文件和 ${Number(strm.removed_directories || 0)} 个空目录；` : ''
      return result.completion_tracked === true
        ? `${synchronized}Emby 全库扫描已完成 · ${formatDuration(String(result.started_at || ''), String(result.completed_at || ''))}`
        : 'Emby 已接受指定媒体库刷新；该范围不提供后台完成状态。'
    }
    case 'emby_missing_posters': {
      const suffix = result.timed_out === true ? '，验证等待已超时' : ''
      return `发现 ${Number(result.found || 0)} 个缺失海报，已提交 ${Number(result.repair_requested || 0)} 个刷新，下载候选 ${Number(result.candidate_downloaded || 0)} 个，确认修复 ${Number(result.repaired || 0)} 个，仍缺 ${Number(result.remaining || 0)} 个${suffix}。`
    }
    case 'emby_metadata_repair': return `扫描 ${Number(result.scanned || 0)} 个条目，缺少 TMDB ${Number(result.missing_identity || 0)} 个；本次处理 ${Number(result.processed || 0)} 个，自动修复 ${Number(result.auto_matched || 0)} 个，待确认 ${Number(result.needs_review || 0)} 个，无候选 ${Number(result.no_match || 0)} 个。`
    case 'emby_match': return `已向 Emby 提交 TMDB ${String(result.tmdb_id || '')} 的匹配结果。`
    case 'c115_save_share': return `已转存 ${Number(result.count || 0)} 个项目${result.title ? ` · ${String(result.title)}` : ''}。`
    case 'c115_offline_download': return `已提交 ${Array.isArray(result.task_ids) ? result.task_ids.length : 0} 个离线任务。`
    case 'strm_sync':
    case 'strm_verify': {
      const strm = typeof result.strm === 'object' && result.strm !== null ? result.strm as Record<string, unknown> : null
      return strm ? `有效 ${Number(strm.valid || 0)} · 缺失 ${Number(strm.missing || 0)} · 无效 ${Number(strm.invalid || 0)}${Number(strm.removed || 0) > 0 || Number(strm.removed_directories || 0) > 0 ? ` · 已清理 ${Number(strm.removed || 0)} 个旧 STRM 和 ${Number(strm.removed_directories || 0)} 个空目录` : ''}` : 'STRM 操作已完成。'
    }
    default: return '任务已保存执行结果。'
  }
}

function resultDetails(task: AsyncTask) {
  const result = parsedResult(task)
  return typeof result === 'string' ? result : JSON.stringify(result, null, 2)
}
function autoFillLibraryResults(task: AsyncTask): Record<string, unknown>[] {
  const result = resultRecord(task)
  return Array.isArray(result?.libraries) ? result.libraries.filter((value): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value)) : []
}

function recordList(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.filter((item): item is Record<string, unknown> => typeof item === 'object' && item !== null && !Array.isArray(item)) : []
}

function textList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
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
  if (error.includes('canonical TMDB identity is missing or duplicated')) return '剧集缺少唯一 TMDB 身份；为避免写错目录，本次没有转存。'
  if (error.includes('expected one 115 folder named')) return '没有找到唯一同名的 115 剧集目录；本次没有转存。'
  if (error.includes('did not appear in CloudDrive')) return '115 已接受转存，但文件未在等待时间内通过 CloudDrive 出现。'
  if (error.includes('canonical Series path or unique TMDB identity changed')) return 'Emby 扫描后剧集路径或 TMDB 唯一身份发生变化，请人工检查。'
  if (error.includes('is absent from its eligible library')) return '缺集所属剧集不在目标追更库中；本次没有转存。'
  return error
}

function taskSummary(type: string, payload: Record<string, unknown> = {}) {
  switch (type) {
    case 'series_auto_fill': {
      const libraries = Array.isArray(payload.libraries) ? payload.libraries.join('、') : '未选择'
      return `${payload.transfer === false ? '仅预检' : payload.replace_completed_pack === true ? '自动转存 · 完结整包替换' : '自动转存'} · ${libraries}`
    }
    case 'emby_refresh': return payload.library_id ? `媒体库 ID：${payload.library_id}` : '范围：全部媒体源与 Emby 媒体库'
    case 'emby_missing_posters': return '范围：全部 Emby 媒体条目'
    case 'emby_match': return `Emby 条目 ${payload.item_id || '未填写'} → TMDB ${payload.tmdb_id || '未填写'}`
    case 'emby_metadata_repair': return `${payload.auto_apply === false ? '仅生成候选' : '安全自动匹配'} · 本次最多 ${Number(payload.limit || 100)} 个条目`
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
    case 'series_auto_fill':
      if (form.auto_fill_libraries.length === 0) throw new Error('请至少选择一个追更媒体库。')
      return {
        libraries: [...form.auto_fill_libraries],
        transfer: form.auto_fill_mode === 'transfer',
        replace_completed_pack: form.auto_fill_mode === 'transfer' && form.replace_completed_pack,
        candidate_limit: Number(form.candidate_limit),
        max_series: Number(form.max_series),
      }
    case 'emby_refresh': return form.library_id.trim() ? { library_id: form.library_id.trim() } : {}
    case 'emby_missing_posters': return {}
    case 'emby_metadata_repair': return { limit: Number(form.metadata_limit), auto_apply: form.metadata_auto_apply }
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
  const posterRepair = /^Emby poster repair found=(\d+) requested=(\d+) downloaded=(\d+) repaired=(\d+) remaining=(\d+) failed=(\d+)$/.exec(line)
  if (posterRepair) return `Emby 海报修复：发现 ${posterRepair[1]} 个，提交刷新 ${posterRepair[2]} 个，下载候选 ${posterRepair[3]} 个，确认修复 ${posterRepair[4]} 个，仍缺 ${posterRepair[5]} 个，请求失败 ${posterRepair[6]} 个。`
  const metadataRepair = /^Emby metadata repair scanned=(\d+) missing=(\d+) processed=(\d+) matched=(\d+) review=(\d+) no_match=(\d+)$/.exec(line)
  if (metadataRepair) return `Emby 元数据修复：扫描 ${metadataRepair[1]} 个，缺少 TMDB ${metadataRepair[2]} 个，处理 ${metadataRepair[3]} 个，自动修复 ${metadataRepair[4]} 个，待确认 ${metadataRepair[5]} 个，无候选 ${metadataRepair[6]} 个。`
  const sync = /^STRM sync media=(\d+) created=(\d+) updated=(\d+) removed=(\d+) removed_directories=(\d+) prune=(\S+)$/.exec(line)
  if (sync) return `STRM 同步：媒体 ${sync[1]} 个，新建 ${sync[2]} 个，更新 ${sync[3]} 个，清理旧文件 ${sync[4]} 个、空目录 ${sync[5]} 个；清理状态 ${sync[6]}。`
  const findings = /^STRM findings valid=(\d+) missing=(\d+) invalid=(\d+)$/.exec(line)
  if (findings) return `STRM 校验：有效 ${findings[1]} 个，缺失 ${findings[2]} 个，无效 ${findings[3]} 个。`
  if (line === 'STRM source scan started') return '开始扫描媒体源文件。'
  const sourceProgress = /^STRM source scan processed (\d+) media files$/.exec(line)
  if (sourceProgress) return `已扫描 ${sourceProgress[1]} 个媒体文件。`
  const sourceComplete = /^STRM source scan completed with (\d+) media files$/.exec(line)
  if (sourceComplete) return `媒体源扫描完成，共 ${sourceComplete[1]} 个视频文件。`
  const reconciliation = /^STRM stale reconciliation removed (\d+) files and (\d+) empty directories with status (\S+)$/.exec(line)
  if (reconciliation) return `旧 STRM 协调完成：清理 ${reconciliation[1]} 个文件、${reconciliation[2]} 个空目录；状态 ${reconciliation[3]}。`
  if (line === 'STRM verification started') return '开始逐项验证 STRM 目标。'
  const verified = /^STRM verification checked (\d+) files$/.exec(line)
  if (verified) return `已验证 ${verified[1]} 个 STRM 文件。`
  const verificationComplete = /^STRM verification completed after (\d+) files$/.exec(line)
  if (verificationComplete) return `STRM 验证完成，共检查 ${verificationComplete[1]} 个文件。`
  if (line === 'STRM synchronization and verification completed') return 'STRM 同步和验证全部完成。'
  const autoFillScan = /^Auto-fill scanning library (.+)$/.exec(line)
  if (autoFillScan) return `开始检查 ${autoFillScan[1]} 的已播缺集。`
  const autoFillSync = /^Auto-fill STRM sync library=(.+) created=(\d+) updated=(\d+)$/.exec(line)
  if (autoFillSync) return `${autoFillSync[1]} STRM 同步：新建 ${autoFillSync[2]} 个，更新 ${autoFillSync[3]} 个。`
  const autoFillComplete = /^Auto-fill completed mode=(\S+) missing=(\d+) matched=(\d+) transferred=(\d+) remaining=(\d+)$/.exec(line)
  if (autoFillComplete) return `补集${autoFillComplete[1] === 'preview' ? '预检' : '执行'}完成：发现 ${autoFillComplete[2]} 集，匹配 ${autoFillComplete[3]} 集，转存 ${autoFillComplete[4]} 集，仍缺 ${autoFillComplete[5]} 集。`
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

    <div class="flex flex-wrap items-center gap-2 p-1.5 rounded-2xl border border-border/80 bg-surface/80 backdrop-blur-xs shadow-xs">
      <button
        type="button"
        @click="executionFilter = 'all'"
        class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 border"
        :class="executionFilter === 'all' ? 'bg-text text-bg border-text shadow-xs' : 'bg-transparent text-text-secondary border-transparent hover:bg-bg-muted hover:text-text'"
      >
        <span>全部记录</span>
        <span class="font-mono text-[11px] px-1.5 py-0.2 rounded-full" :class="executionFilter === 'all' ? 'bg-bg/20 text-bg' : 'bg-bg-muted text-text-faint'">{{ executionsLoaded ? asyncTasks.length : '—' }}</span>
      </button>

      <button
        type="button"
        @click="executionFilter = 'running'"
        class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 border"
        :class="executionFilter === 'running' ? 'bg-warn text-white border-warn shadow-xs' : 'bg-transparent text-text-secondary border-transparent hover:bg-bg-muted hover:text-text'"
      >
        <span class="w-1.5 h-1.5 rounded-full bg-warn" :class="{ 'bg-white animate-pulse': executionFilter === 'running' }"></span>
        <span>正在处理</span>
        <span class="font-mono text-[11px] px-1.5 py-0.2 rounded-full" :class="executionFilter === 'running' ? 'bg-white/20 text-white' : 'bg-bg-muted text-text-faint'">{{ executionsLoaded ? activeTaskCount : '—' }}</span>
      </button>

      <button
        type="button"
        @click="executionFilter = 'completed'"
        class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 border"
        :class="executionFilter === 'completed' ? 'bg-ok text-white border-ok shadow-xs' : 'bg-transparent text-text-secondary border-transparent hover:bg-bg-muted hover:text-text'"
      >
        <span class="w-1.5 h-1.5 rounded-full bg-ok" :class="{ 'bg-white': executionFilter === 'completed' }"></span>
        <span>执行完成</span>
        <span class="font-mono text-[11px] px-1.5 py-0.2 rounded-full" :class="executionFilter === 'completed' ? 'bg-white/20 text-white' : 'bg-bg-muted text-text-faint'">{{ executionsLoaded ? completedTaskCount : '—' }}</span>
      </button>

      <button
        type="button"
        @click="executionFilter = 'attention'"
        class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 border"
        :class="executionFilter === 'attention' ? 'bg-danger text-white border-danger shadow-xs' : 'bg-transparent text-text-secondary border-transparent hover:bg-bg-muted hover:text-text'"
      >
        <span class="w-1.5 h-1.5 rounded-full bg-danger" :class="{ 'bg-white': executionFilter === 'attention' }"></span>
        <span>异常 / 发现</span>
        <span class="font-mono text-[11px] px-1.5 py-0.2 rounded-full" :class="executionFilter === 'attention' ? 'bg-white/20 text-white' : 'bg-bg-muted text-text-faint'">{{ executionsLoaded ? attentionTaskCount : '—' }}</span>
      </button>
    </div>
    <p v-if="!executionsLoaded" class="text-xs text-text-muted">{{ executionsError ? '执行记录尚未读取成功，暂时无法统计任务。' : '正在读取执行记录与任务统计…' }}</p>

    <p v-if="actionMessage" role="status" class="p-3.5 rounded-xl border border-ok/30 bg-ok/5 text-sm text-ok flex items-center gap-2">{{ actionMessage }}</p>

    <form v-if="showScheduleForm" class="rounded-2xl border border-border/80 bg-surface shadow-card overflow-hidden" @submit.prevent="createSchedule">
      <fieldset :disabled="savingSchedule">
      <div class="p-5 sm:p-7 border-b border-border/70 bg-bg-muted/30">
        <div class="flex items-center gap-2 mb-1"><CalendarClock class="w-4 h-4 text-annotation" /><h2 class="font-serif text-xl font-semibold text-text">创建自动任务</h2></div>
        <p class="text-sm text-text-muted">先选择要完成的工作，再填写这项工作需要的信息和执行时间。</p>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7 border-b border-border/70">
        <div><h3 class="font-serif font-semibold text-text">一、选择工作</h3><p class="mt-1 text-xs leading-5 text-text-faint">这里显示业务名称，不需要记住内部工具名。</p></div>
        <div>
          <label for="schedule-type" class="block mb-2 text-xs font-medium text-text-muted">要执行什么</label>
          <select id="schedule-type" v-model="scheduleForm.type" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" @change="selectTaskType">
            <option v-for="type in taskTypeOrder" :key="type" :value="type">{{ taskDefinitions[type].label }}</option>
          </select>
          <p class="mt-3 border-l-2 border-accent pl-3 text-sm leading-6 text-text-muted">{{ selectedDefinition.description }}</p>
        </div>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7 border-b border-border/70">
        <div><h3 class="font-serif font-semibold text-text">二、填写范围</h3><p class="mt-1 text-xs leading-5 text-text-faint">留空的可选项会使用系统设置中的默认值。</p></div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div v-if="scheduleForm.type === 'series_auto_fill'" class="md:col-span-2 space-y-5">
            <div class="auto-fill-lock">
              <div class="flex items-start gap-3"><ShieldCheck class="mt-0.5 h-5 w-5 shrink-0 text-accent" /><div><strong class="block font-serif text-base text-text">范围锁定：只处理两个追更库</strong><p class="mt-1 text-xs leading-5 text-text-muted">不会读取或改动“电视剧”“综艺”及其他媒体库；未来集、特别篇和无法确认集号的资源会保留为未解决。</p></div></div>
              <div class="mt-4 grid gap-2 sm:grid-cols-2">
                <label v-for="library in ['电视剧追更', '综艺追更']" :key="library" class="flex min-h-12 cursor-pointer items-center gap-3 border border-border/80 bg-surface px-4 py-2.5 focus-within:border-accent">
                  <input v-model="scheduleForm.auto_fill_libraries" type="checkbox" :value="library" class="accent-accent" />
                  <span><strong class="block text-sm font-medium text-text">{{ library }}</strong><small class="text-xs text-text-faint">Emby 媒体库与同名 115 目录</small></span>
                </label>
              </div>
            </div>

            <fieldset>
              <legend class="mb-2 text-xs font-medium text-text-muted">执行方式</legend>
              <div class="grid gap-2 sm:grid-cols-2">
                <label class="auto-fill-mode" :class="{ 'auto-fill-mode-active': scheduleForm.auto_fill_mode === 'transfer' }"><input v-model="scheduleForm.auto_fill_mode" class="sr-only" type="radio" value="transfer" /><FolderDown class="h-5 w-5" /><span><strong>自动转存并验证</strong><small>精确匹配集号，写入原剧集目录，生成 STRM 后复查 Emby。</small></span></label>
                <label class="auto-fill-mode" :class="{ 'auto-fill-mode-active': scheduleForm.auto_fill_mode === 'preview' }"><input v-model="scheduleForm.auto_fill_mode" class="sr-only" type="radio" value="preview" /><ScanSearch class="h-5 w-5" /><span><strong>仅预检</strong><small>只盘点缺集并验证候选，不向 115 写入文件。</small></span></label>
              </div>
            </fieldset>
            <label v-if="scheduleForm.auto_fill_mode === 'transfer'" class="flex cursor-pointer items-start gap-3 border-2 border-danger/40 bg-danger/5 p-4 text-sm text-text">
              <input v-model="scheduleForm.replace_completed_pack" type="checkbox" class="mt-0.5 accent-danger" />
              <span><strong class="block text-danger">完结整包自动替换旧版本</strong><small class="mt-1 block leading-5 text-text-muted">仅当整包覆盖全部已播集、新 Series 已绑定同一 TMDB 且扫描后无缺集时执行；随后自动删除旧 Emby 条目，并把旧 115 目录移入回收站。需要在系统设置中开启危险操作。</small></span>
            </label>

            <div class="grid gap-4 sm:grid-cols-2">
              <div><label for="candidate-limit" class="mb-2 block text-xs font-medium text-text-muted">每部剧检查候选数</label><select id="candidate-limit" v-model="scheduleForm.candidate_limit" class="min-h-11 w-full border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none"><option value="5">5 个 · 更快</option><option value="10">10 个 · 推荐</option><option value="20">20 个 · 更全面</option><option value="30">30 个 · 最大范围</option></select></div>
              <div><label for="max-series" class="mb-2 block text-xs font-medium text-text-muted">每库最多处理剧集</label><input id="max-series" v-model="scheduleForm.max_series" type="number" min="1" max="100" required class="min-h-11 w-full border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
            </div>

            <ol class="auto-fill-flow" aria-label="自动补集执行流程">
              <li><ScanSearch /><span><b>01</b> 读取已播缺集</span></li><li><ShieldCheck /><span><b>02</b> 核验剧名与集号</span></li><li><FolderDown /><span><b>03</b> 精确转存原目录</span></li><li><RefreshCcw /><span><b>04</b> 刷新并复查 Emby</span></li>
            </ol>
          </div>
          <div v-else-if="scheduleForm.type === 'emby_refresh'" class="md:col-span-2"><label for="library-id" class="block mb-2 text-xs font-medium text-text-muted">媒体库 ID（可选）</label><input id="library-id" v-model="scheduleForm.library_id" placeholder="留空时刷新全部媒体库" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          <template v-else-if="scheduleForm.type === 'emby_match'">
            <div><label for="item-id" class="block mb-2 text-xs font-medium text-text-muted">Emby 条目 ID</label><input id="item-id" v-model="scheduleForm.item_id" required class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
            <div><label for="tmdb-id" class="block mb-2 text-xs font-medium text-text-muted">TMDB ID</label><input id="tmdb-id" v-model="scheduleForm.tmdb_id" required class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          </template>
          <div v-else-if="scheduleForm.type === 'strm_sync' || scheduleForm.type === 'strm_verify'" class="md:col-span-2"><label for="library-path" class="block mb-2 text-xs font-medium text-text-muted">媒体目录（可选）</label><input id="library-path" v-model="scheduleForm.library" placeholder="例如：Movies；留空时处理全部目录" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          <template v-else-if="scheduleForm.type === 'c115_save_share'">
            <div class="md:col-span-2"><label for="share-url" class="block mb-2 text-xs font-medium text-text-muted">115 分享链接</label><input id="share-url" v-model="scheduleForm.share_url" type="url" required placeholder="https://115.com/s/..." class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
            <div><label for="share-password" class="block mb-2 text-xs font-medium text-text-muted">提取码（可选）</label><input id="share-password" v-model="scheduleForm.share_password" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          </template>
          <div v-else-if="scheduleForm.type === 'c115_offline_download'" class="md:col-span-2"><label for="download-urls" class="block mb-2 text-xs font-medium text-text-muted">下载地址</label><textarea id="download-urls" v-model="scheduleForm.urls" required rows="5" placeholder="每行填写一个 HTTP、HTTPS、磁力或电驴地址" class="w-full rounded-xl border border-border/80 bg-bg px-3.5 py-3 text-sm focus:border-accent focus:outline-none"></textarea></div>
          <template v-if="scheduleForm.type === 'c115_save_share' || scheduleForm.type === 'c115_offline_download'">
            <div><label for="target-cid" class="block mb-2 text-xs font-medium text-text-muted">保存目录 CID（可选）</label><input id="target-cid" v-model="scheduleForm.target_cid" placeholder="留空时使用根目录" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
            <div><label for="account-id" class="block mb-2 text-xs font-medium text-text-muted">115 账号 ID（可选）</label><input id="account-id" v-model="scheduleForm.account_id" placeholder="留空时使用默认账号" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          </template>
          <div v-if="scheduleForm.type === 'emby_metadata_repair'" class="md:col-span-2 space-y-4">
            <div class="flex items-start gap-3 border-l-4 border-accent bg-accent/5 p-4"><WandSparkles class="mt-0.5 h-5 w-5 shrink-0 text-accent" /><div><strong class="font-serif text-text">唯一匹配才自动应用</strong><p class="mt-1 text-xs leading-5 text-text-muted">标题、首播年份和媒体类型必须同时一致；同一 TMDB 对应多个目录、候选接近或类型可疑时只列入待确认，不会自动改写。</p></div></div>
            <div class="grid gap-4 sm:grid-cols-2"><div><label for="metadata-limit" class="mb-2 block text-xs font-medium text-text-muted">本次最多处理</label><select id="metadata-limit" v-model="scheduleForm.metadata_limit" class="min-h-11 w-full border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none"><option value="50">50 个</option><option value="100">100 个 · 推荐</option><option value="200">200 个</option><option value="500">500 个 · 最大范围</option></select></div><label class="flex min-h-11 items-center gap-3 self-end border border-border/80 bg-surface px-4 py-2.5 text-sm"><input v-model="scheduleForm.metadata_auto_apply" type="checkbox" class="accent-accent" /><span><strong class="block text-text">应用安全唯一候选</strong><small class="text-text-faint">关闭后只生成候选报告</small></span></label></div>
          </div>
          <p v-if="scheduleForm.type === 'emby_missing_posters'" class="md:col-span-2 p-4 rounded-xl border border-border/70 bg-bg text-sm text-text-muted">无需额外参数。任务会检查全部 Emby 电影和剧集，为缺少主海报的条目请求完整图片刷新，并在结果稳定后复查。</p>
        </div>
      </div>

      <div class="grid lg:grid-cols-[220px_minmax(0,1fr)] gap-5 p-5 sm:p-7">
        <div><h3 class="font-serif font-semibold text-text">三、设置时间</h3><p class="mt-1 text-xs leading-5 text-text-faint">任务创建后也可以随时手动执行。</p></div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div><label for="schedule-name" class="block mb-2 text-xs font-medium text-text-muted">任务名称</label><input id="schedule-name" v-model="scheduleForm.name" required class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none" /></div>
          <div><label for="schedule-frequency" class="block mb-2 text-xs font-medium text-text-muted">执行频率</label><select id="schedule-frequency" v-model="scheduleForm.frequency" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 text-sm focus:border-accent focus:outline-none"><option v-for="option in frequencyOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></div>
          <div v-if="scheduleForm.frequency === 'custom'" class="md:col-span-2"><label for="schedule-cron" class="block mb-2 text-xs font-medium text-text-muted">自定义 Cron 表达式（秒 分 时 日 月 周）</label><input id="schedule-cron" v-model="scheduleForm.cron_expr" required placeholder="0 0 3 * * *" class="w-full min-h-11 rounded-xl border border-border/80 bg-bg px-3.5 font-mono text-sm focus:border-accent focus:outline-none" /></div>
          <label class="inline-flex min-h-11 items-center gap-2.5 text-sm cursor-pointer"><input v-model="scheduleForm.enabled" type="checkbox" class="rounded border-border accent-accent" />创建后自动按计划执行</label>
        </div>
      </div>
      </fieldset>
      <p v-if="scheduleError" role="alert" class="mx-5 mb-5 rounded-xl border border-danger/30 bg-danger/5 p-3.5 text-sm text-danger sm:mx-7">{{ scheduleError }}</p>

      <footer class="flex flex-col-reverse sm:flex-row sm:items-center sm:justify-end gap-2.5 p-5 sm:px-7 border-t border-border/70 bg-bg-muted/40">
        <button type="button" :disabled="savingSchedule" class="min-h-11 px-5 rounded-xl border border-border/80 bg-surface hover:bg-bg-muted text-sm font-medium disabled:opacity-50 transition-colors" @click="showScheduleForm = false">取消</button>
        <button type="submit" :disabled="savingSchedule" class="inline-flex min-h-11 items-center justify-center gap-2 px-5 rounded-xl bg-accent text-accent-contrast text-sm font-medium hover:bg-accent-strong disabled:opacity-50 shadow-xs transition-colors"><Loader2 v-if="savingSchedule" class="w-4 h-4 animate-spin" />{{ savingSchedule ? '正在保存' : '保存自动任务' }}</button>
      </footer>
    </form>

    <section class="space-y-4" aria-labelledby="schedule-heading">
      <div class="flex items-center justify-between gap-3 px-1">
        <div><div class="flex items-center gap-2"><ListTodo class="w-4 h-4 text-accent" /><h2 id="schedule-heading" class="font-serif font-semibold text-xl text-text">自动任务</h2></div><p class="mt-1 text-xs text-text-faint">系统会按计划执行；也可以随时手动启动一次。</p></div>
        <span class="text-xs font-mono text-text-faint">{{ schedulesLoaded ? `${tasks.length} 个` : '—' }}</span>
      </div>
      <div v-if="schedulesError" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-4 text-sm text-danger"><p>{{ schedulesError }}</p><p v-if="schedulesLoaded" class="mt-1">以下为上次读取的自动任务。</p><button type="button" :disabled="loading" class="mt-2 min-h-11 rounded-lg border border-danger/40 px-3 disabled:opacity-50" @click="fetchTasks">{{ loading ? '正在重试' : '重新读取' }}</button></div>
      <div v-else-if="!schedulesLoaded" role="status" class="flex items-center justify-center gap-2 p-8 rounded-2xl border border-border/80 bg-surface text-sm text-text-muted"><Loader2 class="w-4 h-4 animate-spin" />正在读取自动任务</div>
      <div v-else-if="tasks.length === 0" class="p-8 rounded-2xl border border-dashed border-border bg-surface text-center text-sm text-text-faint">还没有自动任务。点击页面右上角“新建自动任务”开始配置。</div>
      <article v-for="task in orderedTasks" :key="task.id" class="p-5 sm:p-6 rounded-2xl border border-border/80 bg-surface shadow-xs hover:shadow-card hover:border-border-strong transition-all duration-200 flex flex-col lg:flex-row lg:items-center justify-between gap-5">
        <div class="space-y-3 min-w-0">
          <div class="flex items-center gap-2.5 flex-wrap"><span class="w-2.5 h-2.5 rounded-full" :class="activeExecution(task) ? 'bg-warn animate-pulse' : { 'bg-ok': task.status === 'idle' || task.status === 'completed', 'bg-danger': task.status === 'failed', 'bg-text-faint': task.status === 'paused' }"></span><h3 class="font-serif text-lg font-semibold text-text">{{ task.name }}</h3><span class="px-2.5 py-0.5 rounded-full bg-accent-soft text-xs font-medium text-accent border border-accent/20">{{ frequencyLabel(task.cron_expr) }}</span></div>
          <div><strong class="text-sm text-text font-medium">{{ taskDefinition(task.type).label }}</strong><p class="mt-1 text-sm text-text-muted">{{ taskSummary(task.type, scheduledPayload(task)) }}</p></div>
          <div class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-text-faint"><span>最近启动：{{ formatTime(task.last_run_at) }}</span><span v-if="task.next_run_at">下次：{{ formatTime(task.next_run_at) }}</span><span v-if="activeExecution(task)" class="font-semibold text-warn">本次：{{ statusLabel(activeExecution(task)?.status || '') }} · {{ Math.round(activeExecution(task)?.progress || 0) }}%</span><span v-if="task.error" class="text-danger">{{ userError(task.error) }}</span></div>
          <p v-if="scheduleErrors[task.id]" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{{ scheduleErrors[task.id] }}</p>
        </div>
        <button type="button" @click="runTask(task)" :disabled="Boolean(executingId) || Boolean(activeExecution(task))" class="flex min-h-11 shrink-0 items-center justify-center gap-2 px-4 rounded-xl border border-accent/80 bg-surface hover:bg-accent hover:text-accent-contrast text-sm font-medium text-accent shadow-xs disabled:opacity-50 transition-all duration-200">
          <Loader2 v-if="executingId === task.id || activeExecution(task)" class="w-4 h-4 animate-spin" /><Play v-else class="w-4 h-4" /><span>{{ executingId === task.id ? '正在创建执行' : activeExecution(task)?.status === 'pending' ? '等待执行' : activeExecution(task) ? `执行中 ${Math.round(activeExecution(task)?.progress || 0)}%` : '立即执行' }}</span>
        </button>
      </article>
    </section>

    <section class="space-y-4" aria-labelledby="execution-heading">
      <div class="flex items-center justify-between gap-3 px-1">
        <div><div class="flex items-center gap-2"><Activity class="w-4 h-4 text-annotation" /><h2 id="execution-heading" class="font-serif font-semibold text-xl text-text">最近执行</h2></div><p class="mt-1 text-xs text-text-faint">执行中每秒更新，空闲时每 5 秒更新；开始、结束、耗时、进度、日志与结果均保留。</p></div>
        <span class="text-xs font-mono text-text-faint">{{ executionsLoaded ? `显示 ${visibleAsyncTasks.length} / 筛选 ${filteredAsyncTasks.length} / 共 ${asyncTasks.length} 条` : '—' }}</span>
      </div>

      <div v-if="executionsError" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-4 text-sm text-danger"><p>{{ executionsError }}</p><p v-if="executionsLoaded" class="mt-1">以下为上次读取的执行记录。</p><button type="button" :disabled="loading" class="mt-2 min-h-11 rounded-lg border border-danger/40 px-3 disabled:opacity-50" @click="fetchTasks">{{ loading ? '正在重试' : '重新读取' }}</button></div>
      <div v-else-if="!executionsLoaded" role="status" class="flex items-center justify-center gap-2 p-8 rounded-2xl border border-border/80 bg-surface text-sm text-text-muted"><Loader2 class="w-4 h-4 animate-spin" />正在读取执行记录</div>
      <div v-else-if="filteredAsyncTasks.length === 0" class="p-8 rounded-2xl border border-dashed border-border bg-surface text-center text-text-faint text-sm">{{ asyncTasks.length === 0 ? '还没有执行记录。创建自动任务并选择“立即执行”后，进度会显示在这里。' : '当前筛选分类下无执行记录。' }}</div>

      <article v-for="task in visibleAsyncTasks" :id="`execution-${task.id}`" :key="task.id" class="p-5 sm:p-6 rounded-2xl border bg-surface shadow-xs space-y-4 transition-all duration-200 hover:shadow-card" :class="highlightedTaskId === task.id ? 'border-accent ring-2 ring-accent/20' : 'border-border/80 hover:border-border-strong'">
        <div class="flex flex-col sm:flex-row sm:items-start justify-between gap-3">
          <div class="flex items-start gap-3.5 min-w-0">
            <span class="w-2.5 h-2.5 mt-2 rounded-full shrink-0" :class="asyncTone(task)"></span>
            <div><h3 class="font-serif text-lg font-semibold text-text tracking-tight">{{ taskDefinition(task.type).label }}</h3><p class="mt-1 text-sm text-text-muted">{{ taskDefinition(task.type).description }}</p></div>
          </div>
          <span
            class="self-start px-3 py-1 rounded-full text-xs font-medium border"
            :class="{
              'bg-[#edf4ee] text-[#2e5e43] border-[#d3e3d7]': task.status === 'completed' && !taskHasFindings(task),
              'bg-[#f8f3ec] text-[#9b6228] border-[#ebd8bc]': task.status === 'running',
              'bg-[#faf3e8] text-[#9b6826] border-[#ebd8bc]': task.status === 'completed' && taskHasFindings(task),
              'bg-[#faeeee] text-[#9e3939] border-[#f3d3d3]': task.status === 'failed' || task.status === 'cancelled',
              'bg-[#f4f2ee] text-[#78726b] border-[#e2ded6]': task.status === 'pending'
            }"
          >
            {{ taskStatusLabel(task) }}
          </span>
        </div>

        <div class="grid sm:grid-cols-[1fr_auto] gap-3 p-3.5 rounded-xl border border-border/60 bg-bg-muted/40 text-sm">
          <div><span class="text-text-muted break-words">{{ taskSummary(task.type, task.payload) }}</span><span v-if="scheduleName(task)" class="block mt-1 text-xs text-accent">来源：{{ scheduleName(task) }}</span><span class="block mt-1 font-mono text-[10px] text-text-faint break-all">执行 ID：{{ task.id }}</span></div>
          <div class="text-xs font-mono text-text-faint sm:text-right"><span class="block">入队：{{ formatTime(task.created_at) }}</span><span class="block mt-1">更新：{{ formatTime(task.updated_at) }}</span></div>
        </div>

        <div v-if="task.status === 'running' || task.progress > 0" class="flex items-center gap-3">
          <div class="flex-1 h-2 rounded-full bg-bg-muted/80 p-0.5 border border-border/40 overflow-hidden">
            <div
              class="h-full rounded-full transition-all duration-300"
              :class="{
                'progress-streamer-danger bg-danger': task.status === 'failed',
                'progress-streamer-warn bg-warn': taskHasFindings(task),
                'progress-streamer bg-accent': task.status !== 'failed' && !taskHasFindings(task)
              }"
              :style="{ width: Math.min(100, task.progress || 0) + '%' }"
            ></div>
          </div>
          <span class="text-xs font-mono font-medium text-text-muted w-12 text-right">{{ Math.round(task.progress || 0) }}%</span>
        </div>

        <div v-if="task.error" class="p-3.5 rounded-xl border border-danger/30 bg-danger/5 text-sm text-danger">{{ userError(task.error) }}</div>

        <div v-if="task.result" class="p-3.5 rounded-xl border text-sm" :class="taskHasFindings(task) ? 'border-warn/30 bg-warn/5' : 'border-ok/30 bg-ok/5'">
          <p class="font-medium" :class="taskHasFindings(task) ? 'text-warn' : 'text-ok'">{{ resultSummary(task) }}</p>
          <div v-if="task.type === 'series_auto_fill'" class="mt-4 space-y-3">
            <section v-for="library in autoFillLibraryResults(task)" :key="String(library.library_id)" class="border border-border/70 bg-surface">
              <header class="flex flex-wrap items-center justify-between gap-2 border-b border-border/60 px-3.5 py-3"><strong class="font-serif text-text">{{ library.library_name }}</strong><span class="font-mono text-[10px] text-text-faint">LIBRARY {{ library.library_id }}</span></header>
              <dl class="grid grid-cols-2 sm:grid-cols-4">
                <div class="auto-fill-result-stat"><dt>发现缺集</dt><dd>{{ Number(library.missing_count || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>候选匹配</dt><dd>{{ Number(library.matched_count || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>完成转存</dt><dd>{{ Number(library.transferred_count || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>仍需处理</dt><dd :class="Number(library.remaining_count || 0) > 0 ? 'text-warn' : 'text-ok'">{{ Number(library.remaining_count || 0) }}</dd></div>
              </dl>
              <details v-if="recordList(library.series).length" class="border-t border-border/60 px-3.5"><summary class="min-h-11 cursor-pointer py-3 text-xs font-medium text-text-muted hover:text-text">查看 {{ recordList(library.series).length }} 部剧集明细</summary>
                <ul class="space-y-2 pb-3.5">
                  <li v-for="series in recordList(library.series)" :key="String(series.series_id)" class="border-l-2 px-3 py-2" :class="series.issue || textList(series.remaining_episodes).length ? 'border-warn bg-warn/5' : 'border-ok bg-ok/5'">
                    <div class="flex flex-wrap items-center justify-between gap-2"><strong class="text-sm text-text">{{ series.series_name }}</strong><span class="font-mono text-[10px] text-text-faint">{{ series.folder || series.series_id }}</span></div>
                    <p class="mt-1 text-xs text-text-muted">缺集 {{ textList(series.missing_episodes).join('、') || '—' }}<span v-if="textList(series.matched_episodes).length"> · 候选匹配 {{ textList(series.matched_episodes).join('、') }}</span><span v-if="textList(series.transferred_episodes).length"> · 已转存 {{ textList(series.transferred_episodes).join('、') }}</span><span v-if="textList(series.remaining_episodes).length"> · 仍缺 {{ textList(series.remaining_episodes).join('、') }}</span></p>
                    <p v-if="series.replacement_status" class="mt-1 text-xs font-medium" :class="series.replacement_status === 'replaced' ? 'text-ok' : 'text-warn'">{{ series.replacement_status === 'replaced' ? `整包替换完成：旧版本已删除，新目录 ${series.replacement_folder}` : series.replacement_status === 'staged' ? '新整包已转存，正在验证后续删除。' : '新整包未通过完整验证，旧版本已保留。' }}</p>
                    <p v-if="series.issue" class="mt-1 text-xs text-warn">{{ userError(String(series.issue)) }}</p>
                  </li>
                </ul>
              </details>
            </section>
          </div>
          <div v-if="task.type === 'emby_metadata_repair'" class="mt-4 border border-border/70 bg-surface">
            <header class="flex items-center gap-2 border-b border-border/60 px-3.5 py-3"><WandSparkles class="h-4 w-4 text-accent" /><strong class="font-serif text-text">元数据处理明细</strong></header>
            <dl class="grid grid-cols-2 sm:grid-cols-4"><div class="auto-fill-result-stat"><dt>缺少身份</dt><dd>{{ Number(resultRecord(task)?.missing_identity || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>自动修复</dt><dd class="text-ok">{{ Number(resultRecord(task)?.auto_matched || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>待确认</dt><dd class="text-warn">{{ Number(resultRecord(task)?.needs_review || 0) }}</dd></div><div class="auto-fill-result-stat"><dt>无候选</dt><dd>{{ Number(resultRecord(task)?.no_match || 0) }}</dd></div></dl>
            <details v-if="recordList(resultRecord(task)?.items).length" class="border-t border-border/60 px-3.5"><summary class="min-h-11 cursor-pointer py-3 text-xs font-medium text-text-muted">查看 {{ recordList(resultRecord(task)?.items).length }} 个条目</summary><ul class="space-y-2 pb-3.5"><li v-for="item in recordList(resultRecord(task)?.items)" :key="String(item.item_id)" class="border-l-2 px-3 py-2" :class="item.status === 'matched' ? 'border-ok bg-ok/5' : 'border-warn bg-warn/5'"><div class="flex flex-wrap justify-between gap-2"><strong class="text-sm text-text">{{ item.name }}</strong><span class="font-mono text-[10px] text-text-faint">{{ item.type }} · {{ item.production_year || '年份未知' }}</span></div><p class="mt-1 text-xs text-text-muted">检索名：{{ item.search_name }}<span v-if="item.applied_tmdb_id"> · 已匹配 TMDB {{ item.applied_tmdb_id }}</span></p><p v-if="item.reason" class="mt-1 text-xs text-warn">{{ item.reason }}</p><p v-if="recordList(item.candidates).length" class="mt-1 text-xs text-text-faint">候选：{{ recordList(item.candidates).map(candidate => `${candidate.name} (${candidate.production_year || '年份未知'}) · TMDB ${recordList([candidate])[0].provider_ids && typeof recordList([candidate])[0].provider_ids === 'object' ? (recordList([candidate])[0].provider_ids as Record<string, unknown>).Tmdb || '—' : '—'}`).join('；') }}</p></li></ul></details>
          </div>
          <details class="mt-2"><summary class="min-h-11 cursor-pointer py-2 text-xs text-text-muted hover:text-text">查看完整执行结果</summary><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-bg p-3 font-mono text-xs">{{ resultDetails(task) }}</pre></details>
        </div>

        <div class="flex flex-wrap gap-2 pt-1">
          <button type="button" :disabled="runsLoading[task.id]" :aria-expanded="Boolean(taskRuns[task.id])" class="inline-flex min-h-10 items-center gap-1.5 rounded-xl border border-border/80 bg-surface px-3.5 text-xs font-medium hover:bg-bg-muted disabled:opacity-50 transition-colors" @click="toggleTaskRuns(task.id)"><Loader2 v-if="runsLoading[task.id]" class="w-3.5 h-3.5 animate-spin" /><FileText v-else class="w-3.5 h-3.5" />{{ runsLoading[task.id] ? '正在读取记录' : taskRuns[task.id] ? '收起执行记录' : runsErrors[task.id] ? '重试读取记录' : '查看执行记录' }}</button>
          <button v-if="task.status === 'pending' || task.status === 'running'" type="button" :disabled="Boolean(actionBusyId)" class="min-h-10 rounded-xl border border-danger/40 px-3.5 text-xs font-medium text-danger hover:bg-danger/5 disabled:opacity-50 transition-colors" @click="cancelTask(task)">{{ actionBusyId === task.id ? '正在提交' : '停止任务' }}</button>
          <button v-if="task.status === 'failed' || task.status === 'cancelled'" type="button" :disabled="Boolean(actionBusyId)" class="min-h-10 rounded-xl border border-accent px-3.5 text-xs font-medium text-accent hover:bg-accent/5 disabled:opacity-50 transition-colors" @click="openRetry(task)">重新执行</button>
        </div>
        <p v-if="taskErrors[task.id]" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{{ taskErrors[task.id] }}</p>
        <p v-if="runsErrors[task.id]" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{{ runsErrors[task.id] }}</p>

        <div v-if="taskRuns[task.id]" class="border-t border-border/70 pt-4 space-y-3">
          <p v-if="taskRuns[task.id].length === 0" class="text-sm text-text-faint">任务正在等待 worker 创建执行记录。</p>
          <div v-for="run in taskRuns[task.id]" :key="run.id" class="rounded-xl border border-border/60 bg-bg-muted/30 p-4 text-sm space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-3"><span class="font-medium text-text">第 {{ run.attempt }} 次执行 · {{ statusLabel(run.status) }}</span><span class="font-mono text-xs text-text-muted">{{ Math.round(run.progress) }}%</span></div>
            <dl class="grid gap-2 text-xs sm:grid-cols-3"><div><dt class="text-text-faint">开始时间</dt><dd class="mt-1 font-mono break-all">{{ formatTime(run.started_at) }}</dd></div><div><dt class="text-text-faint">结束时间</dt><dd class="mt-1 font-mono break-all">{{ run.completed_at ? formatTime(run.completed_at) : '尚未结束' }}</dd></div><div><dt class="text-text-faint">执行耗时</dt><dd class="mt-1 font-mono">{{ formatDuration(run.started_at, run.completed_at) }}</dd></div></dl>
            <p v-if="run.error" class="rounded-lg border border-danger/30 bg-danger/5 p-3 text-xs text-danger">{{ userError(run.error) }}</p>
            <ol v-if="run.logs?.length" class="space-y-2 break-words text-xs text-text-muted"><li v-for="(line, index) in run.logs" :key="index" class="grid grid-cols-[1.25rem_minmax(0,1fr)] gap-2"><span class="font-mono text-text-faint">{{ index + 1 }}.</span><span>{{ formatLog(line) }}</span></li></ol>
            <p v-else class="text-xs text-text-faint">暂时没有日志；执行中会自动更新。</p>
          </div>
        </div>
      </article>
      <div v-if="executionsLoaded && filteredAsyncTasks.length > executionPageSize" class="flex flex-col items-center gap-2 border-t border-border/70 pt-5 sm:flex-row sm:justify-center">
        <button v-if="hiddenExecutionCount > 0" type="button" class="min-h-11 border border-accent bg-surface px-5 text-sm font-medium text-accent transition-colors hover:bg-accent hover:text-accent-contrast" @click="showMoreExecutions">查看更多 · 还有 {{ hiddenExecutionCount }} 条</button>
        <button v-if="visibleExecutionLimit > executionPageSize" type="button" class="min-h-11 border border-border bg-surface px-5 text-sm font-medium text-text-muted transition-colors hover:bg-bg-muted hover:text-text" @click="collapseExecutions">收起到最近 {{ executionPageSize }} 条</button>
      </div>
    </section>


    <footer class="task-list-end" aria-label="任务列表结束"><span>END OF TASK CENTER</span><strong>自动任务优先展示，执行记录按需展开</strong></footer>

    <UiDialog v-if="retryTarget" title="重新执行任务" :busy="Boolean(actionBusyId)" @close="closeRetry">
      <form class="space-y-5" @submit.prevent="retryTask">
        <p class="text-sm leading-6 text-text-muted">确认重新执行“{{ taskDefinition(retryTarget.type).label }}”？系统会创建一条新的执行记录。</p>
        <p v-if="retryTarget.error" class="rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{{ userError(retryTarget.error) }}</p>
        <p v-if="retryError" role="alert" class="rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{{ retryError }}</p>
        <div class="flex flex-wrap justify-end gap-2.5">
          <button type="button" :disabled="Boolean(actionBusyId)" class="min-h-11 rounded-xl border border-border/80 bg-surface hover:bg-bg-muted px-5 text-sm font-medium disabled:opacity-50 transition-colors" @click="closeRetry">取消</button>
          <button type="submit" :disabled="Boolean(actionBusyId)" class="inline-flex min-h-11 items-center justify-center gap-2 rounded-xl bg-accent hover:bg-accent-strong px-5 text-sm text-accent-contrast font-medium disabled:opacity-50 shadow-xs transition-colors"><Loader2 v-if="actionBusyId" class="w-4 h-4 animate-spin" />{{ actionBusyId ? '正在加入队列' : '确认重新执行' }}</button>
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
@keyframes progress-shimmer {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}
.progress-streamer {
  background: linear-gradient(90deg, var(--accent) 0%, #e08b6e 50%, var(--accent) 100%);
  background-size: 200% 100%;
  animation: progress-shimmer 2.5s ease-in-out infinite;
}
.progress-streamer-warn {
  background: linear-gradient(90deg, var(--warn) 0%, #d49e52 50%, var(--warn) 100%);
  background-size: 200% 100%;
  animation: progress-shimmer 2.5s ease-in-out infinite;
}
.progress-streamer-danger {
  background: linear-gradient(90deg, var(--danger) 0%, #d96e6e 50%, var(--danger) 100%);
  background-size: 200% 100%;
  animation: progress-shimmer 2.5s ease-in-out infinite;
}
.auto-fill-result-stat details:not([open]) > :not(summary),
section > details:not([open]) > :not(summary) { display: none; }
.auto-fill-result-stat { padding: 12px 14px; border-right: 1px solid var(--border); }
.auto-fill-result-stat:last-child { border-right: 0; }
.auto-fill-result-stat dt { color: var(--text-faint); font-size: 10px; }
.auto-fill-result-stat dd { margin-top: 3px; color: var(--text); font: 600 18px/1.2 "SFMono-Regular", Consolas, monospace; }
@media (max-width: 639px) { .auto-fill-result-stat:nth-child(2) { border-right: 0; } .auto-fill-result-stat:nth-child(n+3) { border-top: 1px solid var(--border); } }
.auto-fill-lock { border-left: 3px solid var(--accent); background: color-mix(in srgb, var(--accent) 6%, var(--bg)); padding: 16px; }
.auto-fill-mode { display: grid; grid-template-columns: 20px minmax(0, 1fr); gap: 12px; min-height: 84px; padding: 14px; border: 1px solid var(--border); background: var(--surface); color: var(--text-muted); cursor: pointer; transition: border-color 180ms ease, background-color 180ms ease, color 180ms ease; }
.auto-fill-mode strong, .auto-fill-mode small { display: block; }
.auto-fill-mode strong { color: var(--text); font-size: 14px; }
.auto-fill-mode small { margin-top: 4px; font-size: 12px; line-height: 1.55; }
.auto-fill-mode-active { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 7%, var(--surface)); color: var(--accent); box-shadow: inset 3px 0 0 var(--accent); }
.auto-fill-flow { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); border: 1px solid var(--border); background: var(--surface); }
.auto-fill-flow li { position: relative; display: flex; min-height: 76px; align-items: center; gap: 9px; padding: 12px; color: var(--text-muted); font-size: 11px; line-height: 1.45; }
.auto-fill-flow li + li { border-left: 1px solid var(--border); }
.auto-fill-flow svg { width: 17px; height: 17px; flex: none; color: var(--accent); }
.auto-fill-flow b { display: block; margin-bottom: 2px; color: var(--annotation); font: 700 10px/1.2 "SFMono-Regular", Consolas, monospace; letter-spacing: .1em; }
@media (max-width: 639px) { .auto-fill-flow { grid-template-columns: 1fr 1fr; } .auto-fill-flow li:nth-child(3) { border-left: 0; } .auto-fill-flow li:nth-child(n+3) { border-top: 1px solid var(--border); } }
@media (prefers-reduced-motion: reduce) { .auto-fill-mode { transition: none; } }
@media (max-width: 639px) { .task-center-page { padding-bottom: 6rem; } .task-list-end { align-items: flex-start; flex-direction: column; gap: 6px; } }
</style>

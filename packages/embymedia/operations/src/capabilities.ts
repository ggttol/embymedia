export const PRODUCT_AREAS = [
  'system-dashboard',
  'libraries-scan',
  'c115',
  'resource-search',
  'series-followup',
  'episode-gaps',
  'posters',
  'deduplication',
  'media-delete-move',
  'cleanup',
  'smart-actions',
  'schedules',
  'tasks',
  'logs-undo',
  'emby-users',
  'settings',
] as const

export const WIRE_TOOLS = [
  'embymedia_health',
  'embymedia_library',
  'embymedia_resource',
  'embymedia_series',
  'embymedia_analyze',
  'embymedia_task',
  'embymedia_audit',
  'embymedia_user',
  'embymedia_schedule',
  'embymedia_config',
  'embymedia_plan',
  'embymedia_execute',
  'embymedia_verify',
] as const

export const OPERATION_KINDS = [
  'library.create',
  'library.scan',
  'resource.save_share',
  'resource.offline',
  'resource.add_new',
  'series.update',
  'series.archive',
  'poster.apply',
  'poster.fix_batch',
  'metadata.refresh',
  'media.delete',
  'media.move',
  'dedup.delete',
  'dedup.replace',
  'cleanup.empty_strm',
  'cleanup.empty_cloud',
  'cleanup.execute',
  'user.create',
  'user.policy_update',
  'user.delete',
  'schedule.upsert',
  'schedule.delete',
  'schedule.run',
  'config.update',
  'config.credential_rotate',
  'smart_action.policy_update',
  'smart_action.dismiss',
  'undo.execute',
] as const

export const SCHEDULE_KINDS = [
  'scan_all',
  'zhuigeng_scan_airing',
  'fix_posters_all',
  'refresh_no_rating_all',
  'monitor_incremental',
  'smart_actions_refresh',
  'smart_actions_autopilot',
  'doctor',
] as const

export type ProductArea = (typeof PRODUCT_AREAS)[number]
export type WireTool = (typeof WIRE_TOOLS)[number]
export type OperationKind = (typeof OPERATION_KINDS)[number]
export type ScheduleKind = (typeof SCHEDULE_KINDS)[number]
export type CapabilityCategory = 'read' | 'write' | 'control'
export type Risk = 'low' | 'medium' | 'high' | 'critical'
export type ParityOwner =
  | { readonly kind: 'tool'; readonly tool: WireTool; readonly action: string }
  | { readonly kind: 'host-route'; readonly route: '/health' | '/api/v2/openapi.json' | '/hooks/clouddrive2' }
  | { readonly kind: 'platform-replacement'; readonly replacement: 'Authelia + DSH browser auth' }

export interface Capability {
  readonly capabilityId: string
  readonly area: ProductArea
  readonly category: CapabilityCategory
  readonly risk: Risk
  readonly owner: ParityOwner
  readonly actionKind?: OperationKind | 'task.cancel' | 'dynamic-operation-plan'
}

const query = (
  capabilityId: string,
  area: ProductArea,
  tool: Exclude<WireTool, 'embymedia_plan' | 'embymedia_execute' | 'embymedia_verify'>,
  action: string,
): Capability => ({ capabilityId, area, category: 'read', risk: 'low', owner: { kind: 'tool', tool, action } })

const operation = (kind: OperationKind, area: ProductArea, risk: Risk): Capability => ({
  capabilityId: `operation.${kind}`,
  area,
  category: 'write',
  risk,
  owner: { kind: 'tool', tool: 'embymedia_plan', action: kind },
  actionKind: kind,
})

export const CAPABILITIES: readonly Capability[] = [
  query('health.check', 'system-dashboard', 'embymedia_health', 'checks'),
  query('library.list', 'libraries-scan', 'embymedia_library', 'list_libraries'),
  query('library.summary', 'libraries-scan', 'embymedia_library', 'summary'),
  query('library.items', 'libraries-scan', 'embymedia_library', 'list_items'),
  query('library.count-items', 'libraries-scan', 'embymedia_library', 'count_items'),
  query('library.strm', 'libraries-scan', 'embymedia_library', 'list_strm'),
  query('library.gaps-series', 'episode-gaps', 'embymedia_library', 'gaps_series'),
  query('library.gaps-library', 'episode-gaps', 'embymedia_library', 'gaps_library'),
  ...['test_115', 'parse_share', 'snapshot_share', 'inspect_candidate', 'list_entries', 'auto_cid', 'stats', 'search', 'library_context', 'duplicates', 'transfer_preview'].map(action =>
    query(`resource.${action}`, action === 'test_115' || action === 'parse_share' || action === 'snapshot_share' || action === 'inspect_candidate' || action === 'list_entries' || action === 'auto_cid' ? 'c115' : 'resource-search', 'embymedia_resource', action),
  ),
  ...['status', 'workbench', 'resource_plan', 'gaps_summary', 'scan_airing'].map(action =>
    query(`series.${action}`, 'series-followup', 'embymedia_series', action),
  ),
  ...['dashboard', 'autostrm_status', 'smart_summary', 'smart_list', 'smart_get', 'smart_policies', 'smart_inspect', 'smart_refresh', 'smart_workbench', 'smart_verify', 'from_task', 'from_next_action', 'dedup', 'poster_mismatch', 'poster_search', 'cleanup', 'cleanup_verify'].map(action =>
    query(`analyze.${action}`, action.startsWith('poster_') ? 'posters' : action === 'dedup' ? 'deduplication' : action.startsWith('smart_') || action.startsWith('from_') ? 'smart-actions' : action.startsWith('cleanup') ? 'cleanup' : 'system-dashboard', 'embymedia_analyze', action),
  ),
  query('task.list', 'tasks', 'embymedia_task', 'list'),
  query('task.get', 'tasks', 'embymedia_task', 'get'),
  { capabilityId: 'task.cancel', area: 'tasks', category: 'control', risk: 'low', owner: { kind: 'tool', tool: 'embymedia_task', action: 'cancel' }, actionKind: 'task.cancel' },
  ...['logs', 'audit', 'undo'].map(action => query(`audit.${action}`, 'logs-undo', 'embymedia_audit', action)),
  query('user.list', 'emby-users', 'embymedia_user', 'list'),
  query('user.get-policy', 'emby-users', 'embymedia_user', 'get_policy'),
  query('schedule.list', 'schedules', 'embymedia_schedule', 'list'),
  query('schedule.get', 'schedules', 'embymedia_schedule', 'get'),
  ...['get', 'export', 'diagnostics', 'credential_status'].map(action => query(`config.${action}`, 'settings', 'embymedia_config', action)),
  operation('library.create', 'libraries-scan', 'medium'),
  operation('library.scan', 'libraries-scan', 'medium'),
  operation('resource.save_share', 'c115', 'high'),
  operation('resource.offline', 'c115', 'high'),
  operation('resource.add_new', 'resource-search', 'high'),
  operation('series.update', 'series-followup', 'high'),
  operation('series.archive', 'series-followup', 'high'),
  operation('poster.apply', 'posters', 'medium'),
  operation('poster.fix_batch', 'posters', 'medium'),
  operation('metadata.refresh', 'posters', 'medium'),
  operation('media.delete', 'media-delete-move', 'critical'),
  operation('media.move', 'media-delete-move', 'high'),
  operation('dedup.delete', 'deduplication', 'critical'),
  operation('dedup.replace', 'deduplication', 'critical'),
  operation('cleanup.empty_strm', 'cleanup', 'critical'),
  operation('cleanup.empty_cloud', 'cleanup', 'critical'),
  operation('cleanup.execute', 'cleanup', 'critical'),
  operation('user.create', 'emby-users', 'high'),
  operation('user.policy_update', 'emby-users', 'high'),
  operation('user.delete', 'emby-users', 'high'),
  operation('schedule.upsert', 'schedules', 'medium'),
  operation('schedule.delete', 'schedules', 'medium'),
  operation('schedule.run', 'schedules', 'medium'),
  operation('config.update', 'settings', 'medium'),
  operation('config.credential_rotate', 'settings', 'high'),
  operation('smart_action.policy_update', 'smart-actions', 'high'),
  operation('smart_action.dismiss', 'smart-actions', 'low'),
  operation('undo.execute', 'logs-undo', 'high'),
] as const

const operationByKind = Object.fromEntries(
  OPERATION_KINDS.map(kind => [kind, CAPABILITIES.find(capability => capability.actionKind === kind)!]),
) as Readonly<Record<OperationKind, Capability>>
const capabilityById: Readonly<Record<string, Capability>> = Object.fromEntries(
  CAPABILITIES.map(capability => [capability.capabilityId, capability]),
)

const LEGACY_MUTATION_ACTIONS: Readonly<Record<string, OperationKind | 'task.cancel' | 'dynamic-operation-plan'>> = {
  auto_all: 'dedup.delete',
  cancel_task: 'task.cancel',
  catalog_transfer_execute: 'resource.add_new',
  cleanup_empty_dirs: 'cleanup.empty_strm',
  cleanup_empty_folders: 'cleanup.empty_cloud',
  cleanup_workbench_execute: 'cleanup.execute',
  create_library: 'library.create',
  create_schedule: 'schedule.upsert',
  create_user: 'user.create',
  delete_schedule: 'schedule.delete',
  delete_user: 'user.delete',
  dismiss_smart_action: 'smart_action.dismiss',
  exec_undo: 'undo.execute',
  execute_dedup: 'dedup.delete',
  execute_dedup_batch: 'dedup.delete',
  execute_delete: 'media.delete',
  execute_delete_batch: 'media.delete',
  execute_move: 'media.move',
  execute_move_batch: 'media.move',
  execute_smart_action: 'dynamic-operation-plan',
  execute_smart_actions_batch: 'dynamic-operation-plan',
  fix_batch: 'poster.fix_batch',
  import_config: 'config.update',
  offline: 'resource.offline',
  offline_batch: 'resource.offline',
  put_config: 'config.update',
  refresh_no_rating: 'metadata.refresh',
  refresh_series: 'metadata.refresh',
  replace_batch: 'dedup.replace',
  replace_execute: 'dedup.replace',
  run_schedule: 'schedule.run',
  save: 'resource.save_share',
  save_batch: 'resource.save_share',
  scan_all_libraries: 'library.scan',
  scan_library: 'library.scan',
  smart_actions_workbench_execute: 'dynamic-operation-plan',
  update_schedule: 'schedule.upsert',
  update_smart_action_policy: 'smart_action.policy_update',
  update_user_policy: 'user.policy_update',
  add_new: 'resource.add_new',
  apply: 'poster.apply',
  archive_execute: 'series.archive',
  update_execute: 'series.update',
}

const READ_CAPABILITY_BY_TAG: Readonly<Record<string, string>> = {
  autostrm: 'analyze.autostrm_status',
  c115: 'resource.test_115',
  catalog: 'resource.search',
  config: 'config.get',
  dashboard: 'analyze.dashboard',
  dedup: 'analyze.dedup',
  gaps: 'library.gaps-series',
  insights: 'analyze.cleanup',
  logs: 'audit.logs',
  media: 'library.list',
  posters: 'analyze.poster_mismatch',
  schedules: 'schedule.list',
  smart_actions: 'analyze.smart_list',
  system: 'health.check',
  tasks: 'task.list',
  undo: 'audit.undo',
  users: 'user.list',
  wizard: 'resource.transfer_preview',
  zhuigeng: 'series.status',
}

const READ_CAPABILITY_BY_OPERATION: Readonly<Record<string, string>> = {
  auto_cid: 'resource.auto_cid',
  auto_cid_task: 'resource.auto_cid',
  autostrm_status: 'analyze.autostrm_status',
  catalog_duplicates: 'resource.duplicates',
  catalog_library_context: 'resource.library_context',
  catalog_remote_search: 'resource.search',
  catalog_search: 'resource.search',
  catalog_stats: 'resource.stats',
  catalog_transfer_plan: 'resource.transfer_preview',
  cleanup_summary: 'analyze.cleanup',
  cleanup_workbench_analyze: 'analyze.cleanup',
  cleanup_workbench_verify: 'analyze.cleanup_verify',
  config_diagnostics: 'config.diagnostics',
  detect_mismatch: 'analyze.poster_mismatch',
  duplicates: 'analyze.dedup',
  export_config: 'config.export',
  from_next_action: 'analyze.from_next_action',
  gaps_summary: 'series.gaps_summary',
  get_config: 'config.get',
  get_smart_action: 'analyze.smart_get',
  get_task: 'task.get',
  get_user_policy: 'user.get-policy',
  image_proxy: 'analyze.poster_search',
  inspect_smart_actions: 'analyze.smart_inspect',
  libraries: 'library.list',
  library_items: 'library.items',
  list_audit_logs: 'audit.audit',
  list_logs: 'audit.logs',
  list_schedules: 'schedule.list',
  list_smart_action_policies: 'analyze.smart_policies',
  list_strm: 'library.strm',
  list_tasks: 'task.list',
  list_undo: 'audit.undo',
  list_users: 'user.list',
  parse_url: 'resource.parse_share',
  preview_delete: 'analyze.cleanup',
  preview_move: 'analyze.cleanup',
  resource_plan: 'series.resource_plan',
  scan_airing: 'series.scan_airing',
  scan_library_gaps: 'library.gaps-library',
  search: 'analyze.poster_search',
  search_query: 'analyze.poster_search',
  series_gaps_detail: 'library.gaps-series',
  smart_action_from_next_action: 'analyze.from_next_action',
  smart_action_from_task: 'analyze.from_task',
  smart_actions_summary: 'analyze.smart_summary',
  smart_actions_workbench_analyze: 'analyze.smart_workbench',
  smart_actions_workbench_get: 'analyze.smart_workbench',
  smart_actions_workbench_verify: 'analyze.smart_verify',
  snap: 'resource.snapshot_share',
  status: 'series.status',
  system_summary: 'health.check',
  test_candidate_cookie: 'resource.test_115',
  test_cookie: 'resource.test_115',
  verify_smart_action: 'analyze.smart_verify',
  workbench: 'series.workbench',
}

export interface LegacyOperation {
  readonly method: string
  readonly path: string
  readonly operationId: string
  readonly tag: string
}

/** Resolve every legacy method/path to an explicit tool, host route, or platform replacement. */
export function resolveLegacyOperation(operation: LegacyOperation): Capability | ParityOwner {
  if (operation.path.startsWith('/api/v2/auth/')) {
    return { kind: 'platform-replacement', replacement: 'Authelia + DSH browser auth' }
  }
  if (operation.path === '/health') return { kind: 'host-route', route: '/health' }
  if (operation.path === '/api/v2/openapi.json') return { kind: 'host-route', route: '/api/v2/openapi.json' }
  if (operation.path === '/api/v2/autostrm/webhook') return { kind: 'host-route', route: '/hooks/clouddrive2' }

  const mutation = LEGACY_MUTATION_ACTIONS[operation.operationId]
  if (mutation === 'task.cancel') return capabilityById['task.cancel']!
  if (mutation === 'dynamic-operation-plan') {
    return {
      capabilityId: `legacy.${operation.tag}.${operation.operationId}`,
      area: 'smart-actions',
      category: 'write',
      risk: 'critical',
      owner: { kind: 'tool', tool: 'embymedia_plan', action: 'dynamic-operation-plan' },
      actionKind: 'dynamic-operation-plan',
    }
  }
  if (mutation !== undefined) return operationByKind[mutation]

  const capabilityId = READ_CAPABILITY_BY_OPERATION[operation.operationId] ?? READ_CAPABILITY_BY_TAG[operation.tag]
  const capability = capabilityId === undefined ? undefined : capabilityById[capabilityId]
  if (capability === undefined) {
    throw new Error(`unmapped legacy operation ${operation.method} ${operation.path} (${operation.operationId})`)
  }
  return capability
}

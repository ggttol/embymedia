import { EmbymediaError } from './errors.ts'
import { SCHEDULE_KINDS, type OperationKind, type Risk, type ScheduleKind } from './capabilities.ts'
import type { TargetProjection } from './schemas.ts'

export const RISK_ORDER: Readonly<Record<Risk, number>> = {
  low: 0,
  medium: 1,
  high: 2,
  critical: 3,
}

export const SCHEDULE_RISKS: Readonly<Record<ScheduleKind, Risk>> = {
  scan_all: 'medium',
  zhuigeng_scan_airing: 'medium',
  fix_posters_all: 'medium',
  refresh_no_rating_all: 'medium',
  monitor_incremental: 'medium',
  smart_actions_refresh: 'low',
  smart_actions_autopilot: 'medium',
  doctor: 'low',
}

export const AUTOPILOT_EXECUTOR_TYPES = ['poster_fix', 'metadata_refresh', 'library_scan'] as const

const STANDING_FORBIDDEN: Readonly<Record<OperationKind, boolean>> = {
  'library.create': false,
  'library.scan': false,
  'resource.save_share': true,
  'resource.offline': true,
  'resource.add_new': true,
  'series.update': true,
  'series.archive': true,
  'poster.apply': false,
  'poster.fix_batch': false,
  'metadata.refresh': false,
  'media.delete': true,
  'media.move': true,
  'dedup.delete': true,
  'dedup.replace': true,
  'cleanup.empty_strm': true,
  'cleanup.empty_cloud': true,
  'cleanup.execute': true,
  'user.create': true,
  'user.policy_update': true,
  'user.delete': true,
  'schedule.upsert': true,
  'schedule.delete': true,
  'schedule.run': false,
  'config.update': true,
  'config.credential_rotate': true,
  'smart_action.policy_update': true,
  'smart_action.dismiss': true,
  'undo.execute': true,
}

export function riskAtMost(actual: Risk, maximum: Risk): boolean {
  return RISK_ORDER[actual] <= RISK_ORDER[maximum]
}

export function assertStandingAuthorization(kind: ScheduleKind, operation: OperationKind, approvedRisk: Risk): void {
  if (!(SCHEDULE_KINDS as readonly string[]).includes(kind)) throw new EmbymediaError('INVALID_INPUT', 'unsupported schedule kind')
  if (STANDING_FORBIDDEN[operation]) throw new EmbymediaError('POLICY_DENIED', `${operation} cannot receive standing authorization`)
  const generatedRisk = SCHEDULE_RISKS[kind]
  if (!riskAtMost(generatedRisk, 'medium') || !riskAtMost(generatedRisk, approvedRisk)) {
    throw new EmbymediaError('POLICY_DENIED', 'schedule risk exceeds standing authorization')
  }
}

export function assertExplicitTargets(targets: readonly TargetProjection[], destructive: boolean): void {
  if (destructive && targets.length === 0) throw new EmbymediaError('POLICY_DENIED', 'destructive plan must disclose explicit targets')
  if (targets.length > 100) throw new EmbymediaError('POLICY_DENIED', 'plan may contain at most 100 explicit targets')
  const ids: Record<string, true> = {}
  for (const target of targets) {
    if (target.id.trim().length === 0 || target.label.trim().length === 0) throw new EmbymediaError('INVALID_INPUT', 'target id and label are required')
    if (/其余\s*\d+|remaining\s+\d+|and\s+\d+\s+more/i.test(target.label)) {
      throw new EmbymediaError('POLICY_DENIED', 'target summaries may not hide destructive objects')
    }
    if (ids[target.id]) throw new EmbymediaError('CONFLICT', `duplicate target ${target.id}`)
    ids[target.id] = true
  }
}

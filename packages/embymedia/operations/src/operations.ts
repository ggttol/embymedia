import { createHash } from 'node:crypto'
import type { ResolvedConfig } from './config.ts'
import { parseC115Share } from './clients/c115.ts'
import { EmbymediaError } from './errors.ts'
import type { OperationPlanStore } from './plans.ts'
import {
  assertExactKeys,
  canonicalJson,
  type JsonValue,
  type OperationProjection,
  type TargetProjection,
} from './schemas.ts'
import { OPERATION_KINDS, type OperationKind, type Risk } from './capabilities.ts'
import { assertExplicitTargets } from './risk.ts'

export interface OperationContext {
  readonly principal: string
  readonly sessionId: string
  readonly idempotencyKey: string
  readonly expiresInMs?: number
}

export interface PreviewResult {
  readonly targets: readonly TargetProjection[]
  readonly steps: readonly string[]
  readonly verification: JsonValue
  readonly risk?: Risk
  readonly destructive?: boolean
  readonly reversible?: boolean
}

export type OperationPreviewer = (
  input: Readonly<Record<string, unknown>>,
  signal: AbortSignal,
  context?: OperationContext,
) => Promise<PreviewResult>

interface OperationSpec {
  readonly risk: Risk
  readonly destructive: boolean
  readonly reversible: boolean
  readonly allowed: readonly string[]
  readonly required: readonly string[]
}

const SPECS: Readonly<Record<OperationKind, OperationSpec>> = {
  'library.create': { risk: 'medium', destructive: false, reversible: false, allowed: ['name', 'collectionType', 'libraryOptions'], required: ['name', 'collectionType'] },
  'library.scan': { risk: 'medium', destructive: false, reversible: false, allowed: ['libraryId', 'libraryName', 'mediaFolder', 'top', 'tops', 'outputFolder', 'itemId', 'mode'], required: ['libraryId', 'libraryName', 'mediaFolder'] },
  'resource.save_share': { risk: 'high', destructive: false, reversible: false, allowed: ['url', 'fileIds', 'targetCid', 'libraryId', 'libraryName'], required: ['url', 'targetCid'] },
  'resource.offline': { risk: 'high', destructive: false, reversible: false, allowed: ['url', 'targetCid', 'libraryId', 'libraryName'], required: ['url', 'targetCid'] },
  'resource.add_new': { risk: 'high', destructive: false, reversible: false, allowed: ['candidate', 'candidates', 'scan', 'oldVersionPolicy'], required: ['scan'] },
  'series.update': { risk: 'high', destructive: false, reversible: false, allowed: ['libraryId', 'seriesId', 'numberingMode', 'candidate', 'candidates', 'requestedEpisodes'], required: ['libraryId', 'seriesId', 'requestedEpisodes'] },
  'series.archive': { risk: 'high', destructive: false, reversible: true, allowed: ['seriesId', 'fromLibraryId', 'toLibraryId', 'fromLibrary', 'toLibrary', 'folder'], required: ['seriesId', 'fromLibraryId', 'toLibraryId', 'folder'] },
  'poster.apply': { risk: 'medium', destructive: false, reversible: false, allowed: ['itemId', 'tmdbId'], required: ['itemId', 'tmdbId'] },
  'poster.fix_batch': { risk: 'medium', destructive: false, reversible: false, allowed: ['items'], required: ['items'] },
  'metadata.refresh': { risk: 'medium', destructive: false, reversible: false, allowed: ['itemId', 'recursive'], required: ['itemId'] },
  'media.delete': { risk: 'critical', destructive: true, reversible: false, allowed: ['libraryId', 'itemIds', 'retryPlanId'], required: [] },
  'media.move': { risk: 'high', destructive: false, reversible: true, allowed: ['targets'], required: ['targets'] },
  'dedup.delete': { risk: 'critical', destructive: true, reversible: false, allowed: ['libraryId', 'tmdbId', 'keepItemId', 'removeItemIds', 'cloudRootIds', 'retryPlanId'], required: [] },
  'dedup.replace': { risk: 'critical', destructive: true, reversible: true, allowed: ['keep', 'remove', 'tmdbId'], required: ['keep', 'remove'] },
  'cleanup.empty_strm': { risk: 'critical', destructive: true, reversible: false, allowed: ['targets'], required: ['targets'] },
  'cleanup.empty_cloud': { risk: 'critical', destructive: true, reversible: false, allowed: ['targets'], required: ['targets'] },
  'cleanup.execute': { risk: 'critical', destructive: true, reversible: false, allowed: ['targets', 'dimensions'], required: ['targets'] },
  'user.create': { risk: 'high', destructive: false, reversible: true, allowed: ['name', 'passwordStaged', 'policy'], required: ['name'] },
  'user.policy_update': { risk: 'high', destructive: false, reversible: true, allowed: ['userId', 'policy'], required: ['userId', 'policy'] },
  'user.delete': { risk: 'high', destructive: true, reversible: false, allowed: ['userId'], required: ['userId'] },
  'schedule.upsert': { risk: 'medium', destructive: false, reversible: true, allowed: ['scheduleId', 'name', 'kind', 'params', 'cadence', 'enabled'], required: ['name', 'kind', 'params', 'cadence', 'enabled'] },
  'schedule.delete': { risk: 'medium', destructive: true, reversible: true, allowed: ['scheduleId'], required: ['scheduleId'] },
  'schedule.run': { risk: 'medium', destructive: false, reversible: false, allowed: ['scheduleId'], required: ['scheduleId'] },
  'config.update': { risk: 'medium', destructive: false, reversible: true, allowed: ['settings'], required: ['settings'] },
  'config.credential_rotate': { risk: 'high', destructive: false, reversible: true, allowed: ['credential'], required: ['credential'] },
  'smart_action.policy_update': { risk: 'high', destructive: false, reversible: true, allowed: ['key', 'enabled', 'mode', 'maxRisk', 'params'], required: ['key', 'enabled', 'mode', 'maxRisk', 'params'] },
  'smart_action.dismiss': { risk: 'low', destructive: false, reversible: false, allowed: ['actionId', 'reason'], required: ['actionId'] },
  'undo.execute': { risk: 'high', destructive: false, reversible: false, allowed: ['undoId'], required: ['undoId'] },
}

/** Return model-facing input fields from the same specification used by validation. */
export function operationInputGuide(): string {
  return OPERATION_KINDS.map((kind) => {
    if (kind === 'media.delete') return 'media.delete input={"libraryId":"<library id>","itemIds":["<Emby item id>"]}; retry={"retryPlanId":"<partial plan id>"}'
    if (kind === 'dedup.delete') return 'dedup.delete input={"libraryId":"<library id>","tmdbId":"<id>","keepItemId":"<Series id>","removeItemIds":["<Series id>"]}; retry={"retryPlanId":"<partial plan id>"}'
    if (kind === 'resource.add_new') return 'resource.add_new input={"candidates":[{"candidateId":"<opaque id>"}],"scan":{"libraryId":"<id>","libraryName":"<name>","mediaFolder":"<name>","outputFolder":"<Series folder>"}}; Host resolves the canonical target CID; candidate={...} remains valid for one share'
    const spec = SPECS[kind]
    const optional = spec.allowed.filter(field => !spec.required.includes(field))
    return `${kind} required=[${spec.required.join(',')}]${optional.length === 0 ? '' : ` optional=[${optional.join(',')}]`}`
  }).join('; ')
}

function requireFields(input: Readonly<Record<string, unknown>>, required: readonly string[], allowed: readonly string[]): void {
  const missing = required.filter(key => input[key] === undefined)
  if (missing.length > 0) throw new EmbymediaError('INVALID_INPUT', `missing operation fields: ${missing.join(', ')}; accepted fields: ${allowed.join(', ')}`)
}

function validateFixedInputs(kind: OperationKind, input: Readonly<Record<string, unknown>>): void {
  if (kind === 'schedule.run') assertExactKeys(input, ['scheduleId'], 'schedule.run input')
  if (kind === 'smart_action.policy_update') assertExactKeys(input, ['key', 'enabled', 'mode', 'maxRisk', 'params'], 'smart_action.policy_update input')
  if (kind === 'smart_action.dismiss') assertExactKeys(input, ['actionId', 'reason'], 'smart_action.dismiss input')
  if (kind === 'undo.execute') assertExactKeys(input, ['undoId'], 'undo.execute input')
  if (kind === 'config.credential_rotate') {
    assertExactKeys(input, ['credential'], 'config.credential_rotate input')
    const allowed = ['emby-api-key', 'c115-cookie', 'tmdb-api-key', 'resource-api-token', 'clouddrive-webhook-secret', 'outbound-proxy-url']
    if (typeof input.credential !== 'string' || !allowed.includes(input.credential)) throw new EmbymediaError('INVALID_INPUT', 'unsupported credential id')
  }
  if (kind === 'resource.add_new') {
    const singular = input.candidate !== undefined
    const batch = input.candidates !== undefined
    if (singular === batch) throw new EmbymediaError('INVALID_INPUT', 'resource.add_new requires exactly one of candidate or candidates')
    if (batch && (!Array.isArray(input.candidates) || input.candidates.length === 0 || input.candidates.length > 100)) {
      throw new EmbymediaError('INVALID_INPUT', 'resource.add_new candidates must contain 1 to 100 items')
    }
  }
  if (kind === 'series.update') {
    const singular = input.candidate !== undefined
    const batch = input.candidates !== undefined
    if (singular === batch) throw new EmbymediaError('INVALID_INPUT', 'series.update requires exactly one of candidate or candidates')
    if (batch && (!Array.isArray(input.candidates) || input.candidates.length === 0 || input.candidates.length > 100)) throw new EmbymediaError('INVALID_INPUT', 'series.update candidates must contain 1 to 100 items')
  }
  if (kind === 'media.delete') {
    if (input.retryPlanId !== undefined) assertExactKeys(input, ['retryPlanId'], 'media.delete retry input')
    else requireFields(input, ['libraryId', 'itemIds'], SPECS[kind].allowed)
  }
  if (kind === 'dedup.delete') {
    if (input.retryPlanId !== undefined) assertExactKeys(input, ['retryPlanId'], 'dedup.delete retry input')
    else requireFields(input, ['libraryId', 'tmdbId', 'keepItemId', 'removeItemIds'], SPECS[kind].allowed)
  }
}

export function assertWritePolicy(config: ResolvedConfig, targets: readonly TargetProjection[]): void {
  if (config.writeMode === 'disabled') throw new EmbymediaError('POLICY_DENIED', 'writes are disabled')
  if (config.writeMode === 'enabled') return
  if (targets.length === 0) throw new EmbymediaError('POLICY_DENIED', 'staging requires canonical target ids')
  for (const target of targets) {
    const libraryAllowed = target.canonicalLibraryId !== undefined && config.stagingLibraryIds.has(target.canonicalLibraryId)
    const cidAllowed = target.canonicalCid !== undefined && config.stagingCids.has(target.canonicalCid)
    if (!libraryAllowed && !cidAllowed) throw new EmbymediaError('POLICY_DENIED', `target ${target.id} is outside staging allowlists`)
  }
}

export interface PreparedOperationInput {
  readonly previewValue: unknown
  readonly persistedValue: unknown
  readonly secret?: string
  readonly secrets?: readonly (string | undefined)[]
}

function sanitizeCandidate(value: unknown): { readonly preview: unknown; readonly persisted: unknown; readonly secret?: string } {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return { preview: value, persisted: value }
  const candidate = value as Readonly<Record<string, unknown>>
  if (typeof candidate.url !== 'string') return { preview: value, persisted: value }
  const explicit = typeof candidate.password === 'string' ? candidate.password : undefined
  const share = parseC115Share(candidate.url, explicit)
  if (share.receiveCode === undefined) return { preview: value, persisted: value }
  const persisted: Record<string, unknown> = { ...candidate, url: `https://115.com/s/${share.shareCode}`, passwordStaged: true }
  delete persisted.password
  return { preview: value, persisted, secret: share.receiveCode }
}

/** Remove 115 access codes from persisted plan material while retaining them for this process lifetime. */
export function prepareOperationInput(kind: OperationKind, value: unknown): PreparedOperationInput {
  if (!['resource.add_new', 'series.update'].includes(kind) || typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { previewValue: value, persistedValue: value }
  }
  const input = value as Readonly<Record<string, unknown>>
  if (Array.isArray(input.candidates)) {
    const prepared = input.candidates.map(sanitizeCandidate)
    return {
      previewValue: value,
      persistedValue: { ...input, candidates: prepared.map(item => item.persisted) },
      secrets: prepared.map(item => item.secret),
    }
  }
  const prepared = sanitizeCandidate(input.candidate)
  return {
    previewValue: value,
    persistedValue: { ...input, candidate: prepared.persisted },
    ...(prepared.secret === undefined ? {} : { secret: prepared.secret }),
  }
}

export class OperationPlanner {
  constructor(
    private readonly store: OperationPlanStore,
    private readonly config: ResolvedConfig,
    private readonly previewers: Readonly<Partial<Record<OperationKind, OperationPreviewer>>>,
  ) {}

  async create(kind: OperationKind, value: unknown, context: OperationContext, signal: AbortSignal, persistedValue: unknown = value): Promise<OperationProjection> {
    if (!(OPERATION_KINDS as readonly string[]).includes(kind)) throw new EmbymediaError('INVALID_INPUT', 'unknown operation kind')
    assertExactKeys(value, SPECS[kind].allowed, `${kind} input`)
    const input = value as Readonly<Record<string, unknown>>
    requireFields(input, SPECS[kind].required, SPECS[kind].allowed)
    validateFixedInputs(kind, input)
    const previewer = this.previewers[kind]
    if (previewer === undefined) throw new EmbymediaError('POLICY_DENIED', `operation ${kind} has no deterministic previewer`)
    const preview = await previewer(input, signal, context)
    assertExplicitTargets(preview.targets, preview.destructive ?? SPECS[kind].destructive)
    assertWritePolicy(this.config, preview.targets)
    const spec = SPECS[kind]
    const risk = preview.risk ?? spec.risk
    const destructive = preview.destructive ?? spec.destructive
    const reversible = preview.reversible ?? spec.reversible
    if (destructive && preview.targets.length > 100) throw new EmbymediaError('POLICY_DENIED', 'destructive plan exceeds 100 explicit targets')
    const confirmation = {
      kind,
      input: persistedValue as JsonValue,
      targets: preview.targets,
      steps: preview.steps,
      verification: preview.verification,
      risk,
      destructive,
      reversible,
    } as unknown as JsonValue
    const expiresAt = new Date(Date.now() + Math.min(Math.max(context.expiresInMs ?? 15 * 60_000, 30_000), 60 * 60_000))
    const material = canonicalJson({
      sessionId: context.sessionId,
      principal: context.principal,
      idempotencyKey: context.idempotencyKey,
      confirmation,
    })
    return this.store.create({
      kind,
      requestedBy: context.principal,
      sessionId: context.sessionId,
      risk,
      destructive,
      reversible,
      confirmation,
      targets: preview.targets,
      steps: preview.steps,
      verification: preview.verification,
      expiresAt,
      idempotencyKey: createHash('sha256').update(material).digest('hex'),
    })
  }
}

export { SPECS as OPERATION_SPECS }

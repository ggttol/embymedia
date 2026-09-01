import { createHash } from 'node:crypto'
import { lstat } from 'node:fs/promises'
import { posix } from 'node:path'
import type { Database } from './database/index.ts'
import type { ResolvedConfig } from './config.ts'
import { EmbyClient, type EmbyItem, type EmbyUserPolicy } from './clients/emby.ts'
import { C115Client, parseC115Share, type C115Snapshot } from './clients/c115.ts'
import { ResourceApiClient } from './clients/resource-api.ts'
import { TmdbClient } from './clients/tmdb.ts'
import { ProxyHttpTransport } from './clients/http.ts'
import { isVideoFileName, LibraryDomainService, type ScanRequest } from './domain/library.ts'
import { ResourceDomainService, type AddNewRequest, type OnboardingState, type ResourceCandidate } from './domain/resource.ts'
import { SettingsDomainService } from './domain/admin.ts'
import { SMART_ACTION_TYPES, SmartActionEngine, type SmartActionType } from './domain/smart-actions.ts'
import { MediaCleanupDomainService, type DedupExpectation } from './domain/cleanup.ts'
import { MediaMutationService, type DeleteTarget } from './domain/mutations.ts'
import {
  absoluteEpisodeKeysFromText,
  declaredTmdbId,
  episodeKeysFromText,
  normalizedSeriesName,
  SeriesDomainService,
  type EpisodeGap,
  type EpisodeNumberingMode,
  type SeriesStatus,
} from './domain/series.ts'
import type { EmbymediaCredentialRecords } from './credentials.ts'
import { assertWritePolicy, prepareOperationInput, type OperationContext, type OperationPreviewer } from './operations.ts'
import type { OperationExecutionHandler } from './execution.ts'
import type { OperationVerifier, VerificationResult } from './verification.ts'
import type { OperationKind, Risk } from './capabilities.ts'
import { canonicalJson, type CredentialId, type JsonValue, type OperationProjection, type TargetProjection } from './schemas.ts'
import { abortableSleep } from './cancellation.ts'
import { capturePath, embyLibraryPath, safeUnder, type PathSnapshot } from './media/paths.ts'
import { EmbymediaError, PartialOperationError } from './errors.ts'

interface RuntimeDependencies {
  readonly database: Database
  readonly config: ResolvedConfig
  readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>
  readonly credentials: EmbymediaCredentialRecords
  readonly c115Client: (signal: AbortSignal) => Promise<C115Client>
  readonly resourceClient: (signal: AbortSignal) => Promise<ResourceApiClient>
  readonly planSecret?: (planId: string) => string | readonly (string | undefined)[] | undefined
}

interface PreparedAddNew {
  readonly request: AddNewRequest
  readonly snapshot: C115Snapshot
  readonly snapshots: readonly C115Snapshot[]
  readonly expectedNames: readonly string[]
}

interface CachedAddNew {
  readonly prepared: PreparedAddNew
  readonly expiresAt: number
}


interface CachedSeriesUpdate {
  readonly prepared: PreparedSeriesUpdate
  readonly expiresAt: number
}
type CanonicalSeries = SeriesStatus & { readonly folder: string; readonly path: string }

type RequestedEpisode = Pick<EpisodeGap, 'season' | 'episode' | 'absolute'>

interface PreparedSeriesUpdate {
  readonly request: AddNewRequest
  readonly snapshot: C115Snapshot
  readonly snapshots: readonly C115Snapshot[]
  readonly series: CanonicalSeries
  readonly requestedEpisodes: readonly RequestedEpisode[]
  readonly libraryRootCid: string
  readonly seriesCid: string
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new EmbymediaError('INVALID_INPUT', `${label} must be an object`)
  }
  return value as Record<string, unknown>
}

function requiredString(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.trim().length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return value.trim()
}
function requiredNumber(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new EmbymediaError('INVALID_INPUT', `${label} must be a finite number`)
  return value
}

function pathSnapshot(value: unknown, label: string): PathSnapshot {
  const row = object(value, label)
  const type = requiredString(row.type, `${label} type`)
  if (type !== 'file' && type !== 'directory') throw new EmbymediaError('INVALID_INPUT', `${label} has an invalid path type`)
  return {
    root: requiredString(row.root, `${label} root`),
    rootRealpath: requiredString(row.rootRealpath, `${label} root realpath`),
    relativePath: requiredString(row.relativePath, `${label} relative path`),
    absolutePath: requiredString(row.absolutePath, `${label} absolute path`),
    realpath: requiredString(row.realpath, `${label} realpath`),
    inode: requiredString(row.inode, `${label} inode`),
    mtimeMs: requiredNumber(row.mtimeMs, `${label} mtime`),
    size: requiredNumber(row.size, `${label} size`),
    type,
  }
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 ? value.trim() : undefined
}

function inputOf(plan: OperationProjection): Readonly<Record<string, unknown>> {
  return object(object(plan.confirmation, 'operation confirmation').input, 'operation input')
}

function resultObject(plan: OperationProjection): Readonly<Record<string, JsonValue>> | undefined {
  return typeof plan.result === 'object' && plan.result !== null && !Array.isArray(plan.result)
    ? plan.result as Readonly<Record<string, JsonValue>>
    : undefined
}

function snapshotHash(snapshot: C115Snapshot): string {
  const evidence = [...(snapshot.evidence ?? [])]
    .map(file => ({ id: file.id ?? null, name: file.name, path: file.path ?? null, size: file.size ?? null, directory: file.directory === true }))
    .sort((left, right) => (left.path ?? left.name).localeCompare(right.path ?? right.name) || String(left.id).localeCompare(String(right.id)))
  return createHash('sha256').update(canonicalJson({
    title: snapshot.title ?? null,
    files: snapshot.files.map(file => ({ id: file.id ?? null, name: file.name, size: file.size ?? null, directory: file.directory === true })),
    evidence,
  })).digest('hex')
}

function snapshotFileIds(snapshot: C115Snapshot): readonly string[] {
  const ids: string[] = []
  for (const file of snapshot.files) {
    if (file.id === undefined) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 snapshot item has no stable id')
    ids.push(file.id)
  }
  return ids
}

function hasOk(value: JsonValue | undefined): boolean {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false
  return (value as Readonly<Record<string, JsonValue>>).ok === true
}

function hasPrimaryImage(item: EmbyItem | undefined): boolean {
  if (item === undefined || typeof item.ImageTags !== 'object' || item.ImageTags === null || Array.isArray(item.ImageTags)) return false
  const primary = (item.ImageTags as Readonly<Record<string, unknown>>).Primary
  return typeof primary === 'string' && primary.length > 0
}
function requestedEpisodes(value: unknown, mode: EpisodeNumberingMode): readonly RequestedEpisode[] {
  if (!Array.isArray(value) || value.length === 0) throw new EmbymediaError('INVALID_INPUT', 'series update requires requested episodes')
  if (value.length > 100) throw new EmbymediaError('POLICY_DENIED', 'series update may request at most 100 episodes')
  const seen = new Set<string>()
  return value.map((entry) => {
    const row = object(entry, 'requested episode')
    const season = row.season
    const episode = row.episode
    const absolute = row.absolute
    if (typeof season !== 'number' || !Number.isInteger(season) || season <= 0 || typeof episode !== 'number' || !Number.isInteger(episode) || episode <= 0) {
      throw new EmbymediaError('INVALID_INPUT', 'requested episode season and episode must be positive integers')
    }
    if (mode === 'absolute' && (typeof absolute !== 'number' || !Number.isInteger(absolute) || absolute <= 0)) {
      throw new EmbymediaError('INVALID_INPUT', 'absolute series updates require a positive absolute episode number')
    }
    const requested = { season, episode, ...(typeof absolute === 'number' ? { absolute } : {}) }
    const key = episodeKey(requested, mode)
    if (seen.has(key)) throw new EmbymediaError('INVALID_INPUT', `duplicate requested episode ${key}`)
    seen.add(key)
    return requested
  })
}

function episodeKey(episode: RequestedEpisode, mode: EpisodeNumberingMode): string {
  if (mode === 'absolute') return `absolute:${String(episode.absolute)}`
  return `${String(episode.season)}:${String(episode.episode)}`
}

export class EmbymediaOperationRuntime {
  readonly libraries: LibraryDomainService
  readonly resources: ResourceDomainService
  readonly settings: SettingsDomainService
  readonly smartActions: SmartActionEngine
  readonly series: SeriesDomainService
  readonly mutations: MediaMutationService
  readonly cleanup: MediaCleanupDomainService
  readonly previewers: Readonly<Partial<Record<OperationKind, OperationPreviewer>>>
  readonly handlers: Readonly<Partial<Record<OperationKind, OperationExecutionHandler>>>
  readonly verifiers: Readonly<Partial<Record<OperationKind, OperationVerifier>>>
  private readonly seriesUpdateCache = new Map<string, CachedSeriesUpdate>()
  private readonly addNewCache = new Map<string, CachedAddNew>()

  constructor(private readonly deps: RuntimeDependencies) {
    this.libraries = new LibraryDomainService(
      deps.database,
      deps.config.mediaRoot,
      deps.config.strmRoot,
      deps.embyClient,
    )
    this.settings = new SettingsDomainService(deps.database)
    this.smartActions = new SmartActionEngine(deps.database, {})
    this.mutations = new MediaMutationService(deps.database, deps.embyClient, {
      resolveIds: async (parentCid, targets, signal) => {
        const c115 = await deps.c115Client(signal)
        const entries = await c115.listEntries(parentCid, signal)
        const ids: string[] = []
        for (const target of targets) {
          const entry = entries.find(item => item.id === target.id)
          if (entry === undefined) continue
          if (entry.name !== target.name || entry.directory !== target.directory) {
            throw new EmbymediaError('CONFLICT', `115 cleanup target ${target.id} changed after preview`)
          }
          if (target.directory && target.treeHash !== undefined && await c115.treeHash(entry.id, signal) !== target.treeHash) {
            throw new EmbymediaError('CONFLICT', `115 cleanup directory ${target.id} changed after preview`)
          }
          ids.push(entry.id)
        }
        return ids
      },
      deleteIds: async (parentCid, ids, signal) => {
        await (await deps.c115Client(signal)).deleteIds(parentCid, ids, signal)
      },
    })
    this.cleanup = new MediaCleanupDomainService(
      deps.config,
      this.libraries,
      deps.embyClient,
      deps.c115Client,
      libraryName => this.libraryRootCid(libraryName),
    )
    this.resources = new ResourceDomainService(
      deps.c115Client,
      deps.resourceClient,
      this.libraries,
      {
        waitVisible: (request, signal) => this.waitVisible(request, signal),
        inspectPoster: (request, signal) => this.inspectAddedItems(request, signal),
        analyzeDedup: (request, signal) => this.analyzeAddedDuplicates(request, signal),
        handleOldVersion: () => Promise.resolve({ action: 'not-requested' }),
        verify: (state, signal) => this.verifyOnboarding(state, signal),
      },
    )
    const unavailableArchive = (): Promise<JsonValue> => Promise.reject(new EmbymediaError('POLICY_DENIED', 'series archive executor is not wired'))
    this.series = new SeriesDomainService(
      deps.embyClient,
      () => this.tmdbClient(),
      deps.resourceClient,
      { moveCloud: unavailableArchive, moveStrm: unavailableArchive, notifyEmby: unavailableArchive, verify: unavailableArchive },
    )
    this.previewers = {
      'media.delete': (input, signal, context) => this.previewMediaDelete(input, signal, context),
      'dedup.delete': (input, signal, context) => this.previewDedupDelete(input, signal, context),
      'library.create': (input, signal) => this.previewLibraryCreate(input, signal),
      'library.scan': (input, signal) => this.previewLibraryScan(input, signal),
      'resource.add_new': (input, signal) => this.previewAddNew(input, signal),
      'series.update': (input, signal) => this.previewSeriesUpdate(input, signal),
      'poster.apply': (input, signal) => this.previewEmbyItem(input, signal, 'apply selected TMDB metadata and poster'),
      'poster.fix_batch': (input, signal) => this.previewPosterBatch(input, signal),
      'metadata.refresh': (input, signal) => this.previewEmbyItem(input, signal, 'refresh item metadata and images'),
      'user.create': (input, signal) => this.previewUserCreate(input, signal),
      'user.policy_update': (input, signal) => this.previewUserExisting(input, signal, 'update Emby user policy'),
      'user.delete': (input, signal) => this.previewUserExisting(input, signal, 'delete Emby user'),
      'config.update': input => this.previewConfigUpdate(input),
      'config.credential_rotate': input => this.previewCredentialRotate(input),
      'smart_action.policy_update': input => this.previewSmartPolicy(input),
      'smart_action.dismiss': (input, signal) => this.previewSmartDismiss(input, signal),
      'undo.execute': input => this.previewUndo(input),
    }
    this.handlers = {
      'media.delete': { execute: (plan, signal) => this.executeMediaDelete(plan, signal) },
      'dedup.delete': { execute: (plan, signal) => this.executeMediaDelete(plan, signal) },
      'library.create': { execute: (plan, signal) => this.executeLibraryCreate(plan, signal) },
      'library.scan': { execute: (plan, signal) => this.executeLibraryScan(plan, signal) },
      'resource.add_new': { execute: (plan, signal) => this.executeAddNew(plan, signal) },
      'series.update': { execute: (plan, signal) => this.executeSeriesUpdate(plan, signal) },
      'poster.apply': { execute: (plan, signal) => this.executePosterApply(plan, signal) },
      'poster.fix_batch': { execute: (plan, signal) => this.executePosterBatch(plan, signal) },
      'metadata.refresh': { execute: (plan, signal) => this.executeMetadataRefresh(plan, signal) },
      'user.create': { execute: (plan, signal) => this.executeUserCreate(plan, signal) },
      'user.policy_update': { execute: (plan, signal) => this.executeUserPolicy(plan, signal) },
      'user.delete': { execute: (plan, signal) => this.executeUserDelete(plan, signal) },
      'config.update': { execute: (plan, signal) => this.executeConfigUpdate(plan, signal) },
      'config.credential_rotate': { execute: (plan, signal) => this.executeCredentialRotate(plan, signal) },
      'smart_action.policy_update': { execute: (plan, signal) => this.executeSmartPolicy(plan, signal) },
      'smart_action.dismiss': { execute: (plan, signal) => this.executeSmartDismiss(plan, signal) },
      'undo.execute': { execute: plan => this.executeUndo(plan) },
    }
    this.verifiers = {
      'media.delete': { verify: (plan, signal) => this.verifyMediaDelete(plan, signal) },
      'dedup.delete': { verify: (plan, signal) => this.verifyMediaDelete(plan, signal) },
      'library.create': { verify: (plan, signal) => this.verifyLibraryCreate(plan, signal) },
      'library.scan': { verify: (plan, signal) => this.verifyLibraryScan(plan, signal) },
      'resource.add_new': { verify: (plan, signal) => this.verifyAddNew(plan, signal) },
      'series.update': { verify: (plan, signal) => this.verifySeriesUpdate(plan, signal) },
      'poster.apply': { verify: (plan, signal) => this.verifyPosterApply(plan, signal) },
      'poster.fix_batch': { verify: (plan, signal) => this.verifyPosterBatch(plan, signal) },
      'metadata.refresh': { verify: (plan, signal) => this.verifyMetadataRefresh(plan, signal) },
      'user.create': { verify: (plan, signal) => this.verifyUserCreate(plan, signal) },
      'user.policy_update': { verify: (plan, signal) => this.verifyUserPolicy(plan, signal) },
      'user.delete': { verify: (plan, signal) => this.verifyUserDelete(plan, signal) },
      'config.update': { verify: plan => this.verifyConfigUpdate(plan) },
      'config.credential_rotate': { verify: plan => this.verifyCredentialRotate(plan) },
      'smart_action.policy_update': { verify: plan => this.verifySmartPolicy(plan) },
      'smart_action.dismiss': { verify: plan => this.verifySmartDismiss(plan) },
      'undo.execute': { verify: plan => this.verifyUndo(plan) },
    }
  }

  async previewResourceAddNew(input: Readonly<Record<string, unknown>>, signal: AbortSignal): Promise<JsonValue> {
    const preview = await this.previewAddNew(input, signal)
    return {
      kind: 'resource.add_new',
      input: prepareOperationInput('resource.add_new', input).persistedValue as JsonValue,
      targets: preview.targets,
      steps: preview.steps,
      verification: preview.verification,
    }
  }

  async revalidate(plan: OperationProjection, signal: AbortSignal, context?: OperationContext): Promise<void> {
    assertWritePolicy(this.deps.config, plan.targets)
    signal.throwIfAborted()
    if (plan.kind === 'library.create') {
      const name = requiredString(inputOf(plan).name, 'library name')
      if ((await this.libraries.libraries(signal)).some(library => library.name === name)) {
        throw new EmbymediaError('CONFLICT', `Emby library ${name} was created after preview`)
      }
      return
    }
    if (plan.kind === 'library.scan') {
      const request = await this.scanRequest(inputOf(plan), signal)
      const snapshot = await capturePath(this.deps.config.mediaRoot, posix.join(request.mediaFolder, request.top ?? ''))
      const expected = plan.targets[0]?.snapshot
      if (expected === undefined || expected.inode !== snapshot.inode || expected.mtimeMs !== snapshot.mtimeMs || expected.size !== snapshot.size) {
        throw new EmbymediaError('CONFLICT', 'library scan target changed after preview')
      }
      return
    }
    if (plan.kind === 'resource.add_new') {
      const prepared = await this.prepareAddNew(inputOf(plan), signal, this.deps.planSecret?.(plan.id))
      const expected = object(plan.verification, 'add-new verification')
      const hashes = Array.isArray(expected.snapshotHashes) ? expected.snapshotHashes : [expected.snapshotHash]
      if (canonicalJson(hashes) !== canonicalJson(prepared.snapshots.map(snapshotHash))) throw new EmbymediaError('CONFLICT', '115 shares changed after preview')
      return
    }
    if (plan.kind === 'series.update') {
      const prepared = await this.prepareSeriesUpdate(inputOf(plan), signal, this.deps.planSecret?.(plan.id))
      const verification = object(plan.verification, 'series update verification')
      const hashes = Array.isArray(verification.snapshotHashes) ? verification.snapshotHashes : [verification.snapshotHash]
      if (canonicalJson(hashes) !== canonicalJson(prepared.snapshots.map(snapshotHash))) throw new EmbymediaError('CONFLICT', '115 shares changed after preview')
      const bindingChanged = verification.seriesId !== prepared.series.id
        || verification.tmdbId !== prepared.series.tmdbId
        || verification.seriesPath !== prepared.series.path
        || verification.seriesCid !== prepared.seriesCid
      if (bindingChanged) throw new EmbymediaError('CONFLICT', 'series update target changed after preview')
      return
    }
    if (plan.kind === 'media.delete' || plan.kind === 'dedup.delete') {
      const cleanupPreviewer = this.previewers[plan.kind]
      if (cleanupPreviewer === undefined) throw new EmbymediaError('POLICY_DENIED', `operation ${plan.kind} has no cleanup previewer`)
      const refreshed = await cleanupPreviewer(inputOf(plan), signal, context)
      const targetsChanged = canonicalJson(refreshed.targets as unknown as JsonValue) !== canonicalJson(plan.targets as unknown as JsonValue)
      const bindingsChanged = canonicalJson(refreshed.verification) !== canonicalJson(plan.verification)
      if (targetsChanged || bindingsChanged) throw new EmbymediaError('CONFLICT', `${plan.kind} targets changed after preview`)
      return
    }
    const previewer = this.previewers[plan.kind]
    if (previewer !== undefined) {
      const refreshed = await previewer(inputOf(plan), signal)
      if (canonicalJson(refreshed.targets as unknown as JsonValue) !== canonicalJson(plan.targets as unknown as JsonValue)) {
        throw new EmbymediaError('CONFLICT', `${plan.kind} targets changed after preview`)
      }
      return
    }
    throw new EmbymediaError('POLICY_DENIED', `operation ${plan.kind} is not wired for execution`)
  }

  private async previewLibraryCreate(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    const name = requiredString(input.name, 'library name')
    const collectionType = requiredString(input.collectionType, 'collection type')
    object(input.libraryOptions ?? {}, 'library options')
    if ((await this.libraries.libraries(signal)).some(library => library.name === name)) throw new EmbymediaError('CONFLICT', `Emby library ${name} already exists`)
    return {
      targets: [{ id: name, label: name }],
      steps: ['create Emby virtual folder', 'refresh Emby library registry', 'verify library is visible'],
      verification: { name, collectionType, observable: 'Emby virtual folder list' },
    }
  }

  private async previewLibraryScan(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    const request = await this.scanRequest(input, signal)
    const relativePath = posix.join(request.mediaFolder, request.top ?? '')
    const snapshot = await capturePath(this.deps.config.mediaRoot, relativePath)
    return {
      targets: [{
        id: request.libraryId,
        label: `${request.libraryName}/${request.top ?? '*'}`,
        canonicalLibraryId: request.libraryId,
        path: relativePath,
        snapshot: { inode: snapshot.inode, mtimeMs: snapshot.mtimeMs, size: snapshot.size },
      }],
      steps: ['enumerate media files', 'generate missing STRM files', 'refresh Emby', 'verify Emby paths'],
      verification: { libraryId: request.libraryId, mediaFolder: request.mediaFolder, top: request.top ?? null },
    }
  }

  private async previewAddNew(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    const prepared = await this.prepareAddNew(input, signal)
    return {
      targets: [{
        id: `${prepared.request.scan.libraryId}:${prepared.request.scan.top ?? prepared.expectedNames.join('|')}`,
        label: prepared.request.candidates === undefined ? prepared.request.candidate.title : `${String(prepared.request.candidates.length)} shares: ${prepared.request.candidate.title}`,
        canonicalLibraryId: prepared.request.scan.libraryId,
        canonicalCid: prepared.request.candidate.targetCid,
        ...(prepared.request.scan.outputFolder === undefined ? prepared.request.scan.top === undefined ? {} : { path: posix.join(prepared.request.scan.mediaFolder, prepared.request.scan.top) } : { path: posix.join(prepared.request.scan.mediaFolder, prepared.request.scan.outputFolder) }),
      }],
      steps: [
        'revalidate every 115 share snapshot',
        'save every share to the canonical 115 CID',
        'wait for every CloudDrive media leaf',
        'generate STRM and refresh Emby once',
        'inspect poster and duplicate facts',
        'independently verify Emby visibility',
      ],
      verification: {
        snapshotHash: snapshotHash(prepared.snapshot),
        snapshotHashes: prepared.snapshots.map(snapshotHash),
        expectedNames: prepared.expectedNames,
        libraryId: prepared.request.scan.libraryId,
        libraryName: prepared.request.scan.libraryName,
        mediaFolder: prepared.request.scan.mediaFolder,
        top: prepared.request.scan.top ?? null,
        outputFolder: prepared.request.scan.outputFolder ?? null,
      },
    }
  }

  private async previewSeriesUpdate(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    const prepared = await this.prepareSeriesUpdate(input, signal)
    return {
      targets: [{
        id: prepared.series.id,
        label: `${prepared.series.name} / ${prepared.requestedEpisodes.map(episode => `S${String(episode.season).padStart(2, '0')}E${String(episode.episode).padStart(2, '0')}`).join(', ')}`,
        canonicalLibraryId: prepared.series.libraryId,
        canonicalCid: prepared.seriesCid,
        path: posix.join(prepared.series.libraryName, prepared.series.folder),
      }],
      steps: [
        'revalidate the target Series id, TMDB id, folder, and missing episodes',
        'save the verified 115 resource inside the existing series directory',
        'wait for CloudDrive visibility under the existing series root',
        'generate STRM files only under the existing series root and refresh Emby',
        'verify requested episodes belong to the target Series and no duplicate TMDB Series exists',
      ],
      verification: {
        snapshotHash: snapshotHash(prepared.snapshot),
        snapshotHashes: prepared.snapshots.map(snapshotHash),
        expectedNames: prepared.snapshots.map(snapshot => snapshot.files[0]!.name),
        libraryId: prepared.series.libraryId,
        libraryName: prepared.series.libraryName,
        mediaFolder: prepared.series.libraryName,
        top: prepared.series.folder,
        seriesId: prepared.series.id,
        seriesName: prepared.series.name,
        seriesPath: prepared.series.path,
        folder: prepared.series.folder,
        tmdbId: prepared.series.tmdbId,
        requestedEpisodes: prepared.requestedEpisodes,
        localCountBefore: prepared.series.localCount,
        libraryRootCid: prepared.libraryRootCid,
        seriesCid: prepared.seriesCid,
      },
    }
  }

  private async previewCleanupRetry(retryPlanId: string, kind: 'media.delete' | 'dedup.delete', context: OperationContext) {
    const row = (await this.deps.database.query<{
      kind: string
      status: string
      requested_by: string
      session_id: string
      targets: JsonValue
      confirmation: JsonValue
    }>(
      'SELECT kind,status,requested_by,session_id,targets,confirmation FROM operation_plans WHERE id=$1',
      [retryPlanId],
    )).rows[0]
    if (row === undefined) throw new EmbymediaError('NOT_FOUND', `cleanup retry plan ${retryPlanId} was not found`)
    if (row.requested_by !== context.principal || row.session_id !== context.sessionId) {
      throw new EmbymediaError('POLICY_DENIED', 'cleanup retry source belongs to another owner or session')
    }
    if (row.kind !== kind || row.status !== 'partial') throw new EmbymediaError('CONFLICT', 'cleanup retry requires a partial plan of the same kind')
    if (!Array.isArray(row.targets) || row.targets.length === 0) throw new EmbymediaError('POLICY_DENIED', 'partial cleanup plan has no canonical targets')
    const sourceVerification = object(object(row.confirmation, 'partial cleanup confirmation').verification, 'partial cleanup verification')
    const targets = row.targets.map((value): TargetProjection => {
      const target = object(value, 'cleanup retry target')
      const snapshot = target.snapshot
      return {
        id: requiredString(target.id, 'cleanup target id'),
        label: requiredString(target.label, 'cleanup target label'),
        ...(typeof target.canonicalLibraryId === 'string' ? { canonicalLibraryId: target.canonicalLibraryId } : {}),
        ...(typeof target.canonicalCid === 'string' ? { canonicalCid: target.canonicalCid } : {}),
        ...(typeof target.path === 'string' ? { path: target.path } : {}),
        ...(typeof snapshot === 'object' && snapshot !== null ? { snapshot: snapshot as NonNullable<TargetProjection['snapshot']> } : {}),
      }
    })
    const verification = { ...sourceVerification, retryOf: retryPlanId }
    return {
      targets,
      steps: ['resume the exact partial cleanup targets', 'treat already absent components as complete', 'verify Emby, STRM, and 115 absence'],
      verification: verification as unknown as JsonValue,
    }
  }

  private async previewMediaDelete(input: Readonly<Record<string, unknown>>, signal: AbortSignal, context?: OperationContext) {
    const retryPlanId = optionalString(input.retryPlanId)
    if (retryPlanId !== undefined && Object.keys(input).some(key => key !== 'retryPlanId')) throw new EmbymediaError('INVALID_INPUT', 'cleanup retry accepts only retryPlanId')
    if (retryPlanId !== undefined) {
      if (context === undefined) throw new EmbymediaError('POLICY_DENIED', 'cleanup retry requires authenticated operation context')
      return this.previewCleanupRetry(retryPlanId, 'media.delete', context)
    }
    const libraryId = requiredString(input.libraryId, 'library id')
    if (!Array.isArray(input.itemIds)) throw new EmbymediaError('INVALID_INPUT', 'media.delete requires item ids')
    const prepared = await this.cleanup.prepareMediaDelete(libraryId, input.itemIds.map(value => requiredString(value, 'Emby item id')), signal)
    return {
      targets: prepared.targets,
      steps: ['delete each exact Emby item', 'remove its unchanged STRM root', 'delete its exact 115 root entry', 'verify all three facts are absent'],
      verification: { deleteTargets: prepared.deleteTargets, cloudNames: prepared.cloudNames } as unknown as JsonValue,
    }
  }

  private async previewDedupDelete(input: Readonly<Record<string, unknown>>, signal: AbortSignal, context?: OperationContext) {
    const retryPlanId = optionalString(input.retryPlanId)
    if (retryPlanId !== undefined && Object.keys(input).some(key => key !== 'retryPlanId')) throw new EmbymediaError('INVALID_INPUT', 'cleanup retry accepts only retryPlanId')
    if (retryPlanId !== undefined) {
      if (context === undefined) throw new EmbymediaError('POLICY_DENIED', 'cleanup retry requires authenticated operation context')
      return this.previewCleanupRetry(retryPlanId, 'dedup.delete', context)
    }
    const libraryId = requiredString(input.libraryId, 'library id')
    const tmdbId = requiredString(input.tmdbId, 'TMDB id')
    const keepItemId = requiredString(input.keepItemId, 'keep item id')
    if (!Array.isArray(input.removeItemIds)) throw new EmbymediaError('INVALID_INPUT', 'dedup.delete requires remove item ids')
    const removeItemIds = input.removeItemIds.map(value => requiredString(value, 'remove item id'))
    const rawCloudRootIds = input.cloudRootIds === undefined ? {} : object(input.cloudRootIds, 'dedup.delete cloud root ids')
    const cloudRootIds = Object.fromEntries(Object.entries(rawCloudRootIds).map(([itemId, cloudId]) => [
      requiredString(itemId, 'cloud root item id'), requiredString(cloudId, 'cloud root id'),
    ]))
    const prepared = await this.cleanup.prepareDedupDelete(
      libraryId, tmdbId, keepItemId, removeItemIds, signal, cloudRootIds,
    )
    return {
      targets: prepared.targets,
      steps: ['keep the explicitly selected TMDB Series', 'delete each exact duplicate Emby Series', 'remove unchanged duplicate STRM and 115 roots', 'verify one TMDB Series remains'],
      verification: {
        deleteTargets: prepared.deleteTargets,
        cloudNames: prepared.cloudNames,
        dedupExpectation: prepared.dedupExpectation,
      } as unknown as JsonValue,
    }
  }
  private async previewEmbyItem(input: Readonly<Record<string, unknown>>, signal: AbortSignal, action: string) {
    const itemId = requiredString(input.itemId, 'Emby item id')
    const item = await (await this.deps.embyClient(signal)).item(itemId, 'ProviderIds,Path', signal)
    if (item === undefined) throw new EmbymediaError('NOT_FOUND', `Emby item ${itemId} was not found`)
    if (input.tmdbId !== undefined) requiredString(input.tmdbId, 'TMDB id')
    return {
      targets: [{ id: item.Id, label: item.Name, ...(item.Path === undefined ? {} : { path: item.Path }) }],
      steps: [action, 're-read Emby item and provider ids'],
      verification: { itemId, previousTmdbId: item.ProviderIds?.Tmdb ?? null },
    }
  }

  private async previewPosterBatch(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    if (!Array.isArray(input.items) || input.items.length === 0) throw new EmbymediaError('INVALID_INPUT', 'poster batch requires at least one item')
    if (input.items.length > 100) throw new EmbymediaError('POLICY_DENIED', 'poster batch may contain at most 100 items')
    const emby = await this.deps.embyClient(signal)
    const targets = []
    for (const raw of input.items) {
      const row = object(raw, 'poster batch item')
      const itemId = requiredString(row.itemId, 'Emby item id')
      requiredString(row.tmdbId, 'TMDB id')
      const item = await emby.item(itemId, 'ProviderIds,Path', signal)
      if (item === undefined) throw new EmbymediaError('NOT_FOUND', `Emby item ${itemId} was not found`)
      targets.push({ id: item.Id, label: item.Name, ...(item.Path === undefined ? {} : { path: item.Path }) })
    }
    return {
      targets,
      steps: ['apply selected TMDB metadata to each item', 'refresh metadata and images', 'verify provider ids'],
      verification: { itemCount: targets.length },
    }
  }

  private async previewUserCreate(input: Readonly<Record<string, unknown>>, signal: AbortSignal) {
    const name = requiredString(input.name, 'Emby user name')
    if ((await (await this.deps.embyClient(signal)).users(signal)).some(user => user.Name === name)) {
      throw new EmbymediaError('CONFLICT', `Emby user ${name} already exists`)
    }
    return {
      targets: [{ id: name, label: name }],
      steps: ['create Emby user', 'apply optional policy', 'verify user visibility'],
      verification: { name },
    }
  }

  private async previewUserExisting(input: Readonly<Record<string, unknown>>, signal: AbortSignal, action: string) {
    const userId = requiredString(input.userId, 'Emby user id')
    const user = (await (await this.deps.embyClient(signal)).users(signal)).find(item => item.Id === userId)
    if (user === undefined) throw new EmbymediaError('NOT_FOUND', `Emby user ${userId} was not found`)
    return {
      targets: [{ id: user.Id, label: user.Name }],
      steps: [action, 're-read Emby user list and policy'],
      verification: { userId, name: user.Name },
    }
  }

  private async previewConfigUpdate(input: Readonly<Record<string, unknown>>) {
    const settings = object(input.settings, 'settings')
    const keys = Object.keys(settings).sort()
    if (keys.length === 0) throw new EmbymediaError('INVALID_INPUT', 'config update requires at least one setting')
    return {
      targets: keys.map(key => ({ id: key, label: key })),
      steps: ['validate non-secret settings', 'write settings transactionally', 're-read persisted values'],
      verification: { keys },
    }
  }

  private async previewCredentialRotate(input: Readonly<Record<string, unknown>>) {
    const credential = requiredString(input.credential, 'credential id')
    const supported = ['emby-api-key', 'c115-cookie', 'tmdb-api-key', 'resource-api-token', 'outbound-proxy-url']
    if (!supported.includes(credential)) {
      throw new EmbymediaError('POLICY_DENIED', credential === 'clouddrive-webhook-secret'
        ? 'CloudDrive webhook rotation requires the root helper canary workflow'
        : `unsupported credential ${credential}`)
    }
    return {
      targets: [{ id: credential, label: credential }],
      steps: ['stage write-only value', 'validate upstream with pending credential', 'promote pending credential', 'verify configured status'],
      verification: { credential, secretValueExposed: false },
    }
  }

  private async previewSmartPolicy(input: Readonly<Record<string, unknown>>) {
    const key = requiredString(input.key, 'Smart Action policy key')
    if (!(SMART_ACTION_TYPES as readonly string[]).includes(key)) throw new EmbymediaError('INVALID_INPUT', `unsupported Smart Action type ${key}`)
    if (typeof input.enabled !== 'boolean') throw new EmbymediaError('INVALID_INPUT', 'policy enabled must be boolean')
    const mode = requiredString(input.mode, 'policy mode')
    if (!['confirm', 'suggest', 'auto'].includes(mode)) throw new EmbymediaError('INVALID_INPUT', 'invalid Smart Action policy mode')
    const maxRisk = requiredString(input.maxRisk, 'policy max risk')
    if (!['low', 'medium', 'high', 'critical'].includes(maxRisk)) throw new EmbymediaError('INVALID_INPUT', 'invalid Smart Action max risk')
    return {
      targets: [{ id: key, label: key }],
      steps: ['update Smart Action policy', 're-read effective policy'],
      verification: { key, enabled: input.enabled, mode, maxRisk },
    }
  }

  private async previewSmartDismiss(input: Readonly<Record<string, unknown>>, _signal: AbortSignal) {
    const actionId = requiredString(input.actionId, 'Smart Action id')
    const action = await this.smartActions.get(actionId)
    if (!['suggested', 'ready'].includes(action.status)) throw new EmbymediaError('CONFLICT', 'Smart Action cannot be dismissed in its current state')
    return {
      targets: [{ id: action.id, label: action.title }],
      steps: ['dismiss Smart Action', 'verify dismissed status'],
      verification: { actionId, previousStatus: action.status },
    }
  }

  private async previewUndo(input: Readonly<Record<string, unknown>>) {
    const undoId = requiredString(input.undoId, 'undo id')
    const preview = await this.settings.previewUndo(undoId)
    return {
      targets: preview.keys.map(key => ({ id: key, label: key })),
      steps: ['restore previous non-secret settings transactionally', 'mark undo entry applied', 're-read restored settings'],
      verification: { undoId, keys: preview.keys },
    }
  }

  private async executeLibraryCreate(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    const name = requiredString(input.name, 'library name')
    const collectionType = requiredString(input.collectionType, 'collection type')
    const options = object(input.libraryOptions ?? {}, 'library options')
    const emby = await this.deps.embyClient(signal)
    await emby.createVirtualFolder(name, collectionType, options, signal)
    await emby.refreshLibrary(signal)
    for (let attempt = 0; attempt < 20; attempt++) {
      const found = (await emby.libraries(signal)).find(library => library.name === name)
      if (found !== undefined) return found as unknown as JsonValue
      if (attempt < 19) await abortableSleep(500, signal)
    }
    throw new EmbymediaError('VERIFICATION_FAILED', `created Emby library ${name} is not visible`)
  }

  private async executeLibraryScan(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    return await this.libraries.scan(await this.scanRequest(inputOf(plan), signal), signal) as unknown as JsonValue
  }

  private async executeAddNew(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const prepared = await this.prepareAddNew(inputOf(plan), signal, this.deps.planSecret?.(plan.id))
    const expected = object(plan.verification, 'add-new verification')
    const hashes = Array.isArray(expected.snapshotHashes) ? expected.snapshotHashes : [expected.snapshotHash]
    if (canonicalJson(hashes) !== canonicalJson(prepared.snapshots.map(snapshotHash))) throw new EmbymediaError('CONFLICT', '115 shares changed after approval')
    const state = await this.resources.executeAddNew(prepared.request, signal)
    return this.publicOnboarding(state)
  }
  private async executeSeriesUpdate(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const prepared = await this.prepareSeriesUpdate(inputOf(plan), signal, this.deps.planSecret?.(plan.id))
    const expected = object(plan.verification, 'series update verification')
    const hashes = Array.isArray(expected.snapshotHashes) ? expected.snapshotHashes : [expected.snapshotHash]
    const changed = canonicalJson(hashes) !== canonicalJson(prepared.snapshots.map(snapshotHash))
      || expected.seriesId !== prepared.series.id
      || expected.tmdbId !== prepared.series.tmdbId
      || expected.seriesPath !== prepared.series.path
      || expected.seriesCid !== prepared.seriesCid
    if (changed) throw new EmbymediaError('CONFLICT', 'series update target changed after approval')
    const state = await this.resources.executeAddNew(prepared.request, signal)
    return {
      ...this.publicOnboarding(state) as Readonly<Record<string, JsonValue>>,
      series: {
        id: prepared.series.id,
        tmdbId: prepared.series.tmdbId,
        folder: prepared.series.folder,
        requestedEpisodes: prepared.requestedEpisodes,
      },
    }
  }

  private deleteTargets(plan: OperationProjection): readonly DeleteTarget[] {
    const verification = object(plan.verification, 'media delete verification')
    if (!Array.isArray(verification.deleteTargets) || verification.deleteTargets.length === 0) {
      throw new EmbymediaError('POLICY_DENIED', 'media delete plan has no canonical targets')
    }
    return verification.deleteTargets.map((value) => {
      const row = object(value, 'media delete target')
      const strm = row.strm === undefined ? undefined : pathSnapshot(row.strm, 'STRM snapshot')
      const strmTreeHash = optionalString(row.strmTreeHash)
      if (strm?.type === 'directory' && strmTreeHash === undefined) throw new EmbymediaError('INVALID_INPUT', 'directory cleanup target has no recursive manifest')
      const cloudIds = row.cloudIds === undefined
        ? undefined
        : Array.isArray(row.cloudIds) ? row.cloudIds.map(value => requiredString(value, '115 item id')) : undefined
      if (row.cloudIds !== undefined && (cloudIds === undefined || cloudIds.length !== 1 || typeof row.cloudDirectory !== 'boolean')) {
        throw new EmbymediaError('INVALID_INPUT', 'cleanup target has an incomplete 115 binding')
      }
      const cloudTreeHash = optionalString(row.cloudTreeHash)
      return {
        id: requiredString(row.id, 'media delete target id'),
        embyItemId: requiredString(row.embyItemId, 'Emby item id'),
        embyPath: requiredString(row.embyPath, 'Emby item path'),
        embyType: requiredString(row.embyType, 'Emby item type'),
        ...(typeof row.embyTmdbId === 'string' ? { embyTmdbId: requiredString(row.embyTmdbId, 'Emby TMDB id') } : {}),
        ...(strm === undefined ? {} : { strm }),
        ...(strmTreeHash === undefined ? {} : { strmTreeHash }),
        ...(cloudIds === undefined ? {} : {
          cloudParentCid: requiredString(row.cloudParentCid, '115 parent CID'), cloudIds,
          cloudName: requiredString(row.cloudName, '115 item name'), cloudDirectory: row.cloudDirectory as boolean,
        }),
        ...(cloudTreeHash === undefined ? {} : { cloudTreeHash }),
      }
    })
  }

  private cleanupDedupExpectation(plan: OperationProjection): DedupExpectation | undefined {
    if (plan.kind !== 'dedup.delete') return undefined
    const raw = object(object(plan.verification, 'dedup verification').dedupExpectation, 'dedup expectation')
    return {
      libraryId: requiredString(raw.libraryId, 'library id'),
      tmdbId: requiredString(raw.tmdbId, 'TMDB id'),
      keepItemId: requiredString(raw.keepItemId, 'keep item id'),
    }
  }

  private async executeMediaDelete(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    return await this.mutations.deleteTargets(
      plan.id,
      this.deleteTargets(plan),
      signal,
      this.cleanupDedupExpectation(plan),
    ) as unknown as JsonValue
  }

  private async applyPoster(itemId: string, tmdbId: string, signal: AbortSignal): Promise<EmbyItem> {
    const emby = await this.deps.embyClient(signal)
    let effectStarted = false
    try {
      await emby.applyRemoteSearch(itemId, tmdbId, signal)
      effectStarted = true
      await emby.refreshItem(itemId, true, signal)
      let current: EmbyItem | undefined
      for (let attempt = 0; attempt < 60; attempt++) {
        signal.throwIfAborted()
        current = await emby.item(itemId, 'ProviderIds,Path,ImageTags', signal)
        if (current?.ProviderIds?.Tmdb === tmdbId && hasPrimaryImage(current)) return current
        if (attempt < 59) await abortableSleep(2_000, signal)
      }
      throw new EmbymediaError('VERIFICATION_FAILED', 'Emby did not retain the selected TMDB id and primary poster', {
        itemId, expectedTmdbId: tmdbId, actualTmdbId: current?.ProviderIds?.Tmdb ?? null, primaryImage: hasPrimaryImage(current),
      })
    } catch (error) {
      if (!effectStarted) throw error
      throw new PartialOperationError('PARTIAL_FAILURE', 'poster mutation interrupted after Emby accepted the metadata update', {
        itemId, tmdbId, cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executePosterApply(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    return await this.applyPoster(requiredString(input.itemId, 'Emby item id'), requiredString(input.tmdbId, 'TMDB id'), signal) as unknown as JsonValue
  }

  private async executePosterBatch(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    if (!Array.isArray(input.items)) throw new EmbymediaError('INVALID_INPUT', 'poster batch items are required')
    const rows = input.items.map((raw) => {
      const row = object(raw, 'poster batch item')
      return { itemId: requiredString(row.itemId, 'Emby item id'), tmdbId: requiredString(row.tmdbId, 'TMDB id') }
    })
    const output: EmbyItem[] = []
    try {
      for (const row of rows) output.push(await this.applyPoster(row.itemId, row.tmdbId, signal))
      return output as unknown as JsonValue
    } catch (error) {
      if (output.length === 0) throw error
      throw new PartialOperationError('PARTIAL_FAILURE', 'poster batch interrupted after earlier items were updated', {
        completedItemIds: output.map(item => item.Id), cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executeMetadataRefresh(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const itemId = requiredString(inputOf(plan).itemId, 'Emby item id')
    const emby = await this.deps.embyClient(signal)
    await emby.refreshItem(itemId, true, signal)
    try {
      const item = await emby.item(itemId, 'ProviderIds,Path', signal)
      if (item === undefined) throw new EmbymediaError('VERIFICATION_FAILED', 'refreshed Emby item is not visible')
      return item as unknown as JsonValue
    } catch (error) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'metadata refresh verification failed after Emby accepted the refresh', {
        itemId, cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executeUserCreate(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    if (input.passwordStaged === true) throw new EmbymediaError('POLICY_DENIED', 'staged Emby user passwords are not wired; create the user without a password or configure it in Emby')
    const emby = await this.deps.embyClient(signal)
    const created = await emby.createUser(requiredString(input.name, 'Emby user name'), undefined, signal)
    try {
      if (input.policy !== undefined) await emby.updateUserPolicy(created.Id, object(input.policy, 'Emby user policy'), signal)
      const verified = (await emby.users(signal)).find(user => user.Id === created.Id)
      if (verified === undefined) throw new EmbymediaError('VERIFICATION_FAILED', 'created Emby user is not visible')
      return verified as unknown as JsonValue
    } catch (error) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'user creation verification failed after Emby created the account', {
        userId: created.Id, cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executeUserPolicy(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    const userId = requiredString(input.userId, 'Emby user id')
    const policy = object(input.policy, 'Emby user policy') as EmbyUserPolicy
    const emby = await this.deps.embyClient(signal)
    await emby.updateUserPolicy(userId, policy, signal)
    try {
      const user = (await emby.users(signal)).find(item => item.Id === userId)
      if (user === undefined) throw new EmbymediaError('VERIFICATION_FAILED', 'updated Emby user is not visible')
      return user as unknown as JsonValue
    } catch (error) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'user policy verification failed after Emby accepted the update', {
        userId, cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executeUserDelete(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const userId = requiredString(inputOf(plan).userId, 'Emby user id')
    const emby = await this.deps.embyClient(signal)
    await emby.deleteUser(userId, signal)
    try {
      if ((await emby.users(signal)).some(user => user.Id === userId)) throw new EmbymediaError('VERIFICATION_FAILED', 'deleted Emby user remains visible')
      return { userId, deleted: true }
    } catch (error) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'user deletion verification failed after Emby accepted the deletion', {
        userId, cause: error instanceof Error ? error.message : String(error),
      })
    }
  }

  private async executeConfigUpdate(plan: OperationProjection, _signal: AbortSignal): Promise<JsonValue> {
    const settings = object(inputOf(plan).settings, 'settings') as Readonly<Record<string, JsonValue>>
    return await this.settings.update(plan.id, settings) as unknown as JsonValue
  }

  private async executeUndo(plan: OperationProjection): Promise<JsonValue> {
    return this.settings.executeUndo(requiredString(inputOf(plan).undoId, 'undo id'))
  }

  private async executeCredentialRotate(plan: OperationProjection, signal: AbortSignal): Promise<JsonValue> {
    const credential = requiredString(inputOf(plan).credential, 'credential id') as CredentialId
    if (credential === 'clouddrive-webhook-secret') throw new EmbymediaError('POLICY_DENIED', 'CloudDrive webhook rotation requires the root helper canary workflow')
    const pending = await this.deps.credentials.readPending(plan.id, credential)
    await this.validateCredential(credential, pending, signal)
    await this.deps.credentials.promotePending(plan.id, credential)
    return { credential, configured: true }
  }

  async validateCredential(credential: Exclude<CredentialId, 'clouddrive-webhook-secret'>, value: string, signal: AbortSignal): Promise<void> {
    if (credential === 'emby-api-key') {
      await new EmbyClient({ baseUrl: this.deps.config.embyBaseUrl, token: value }).libraries(signal)
      return
    }
    if (credential === 'c115-cookie') {
      await new C115Client({ cookie: value }).test(signal)
      return
    }
    let proxyUrl: string | undefined
    try { proxyUrl = await this.deps.credentials.read('outbound-proxy-url') } catch {}
    if (credential === 'tmdb-api-key') {
      await new TmdbClient({
        apiKey: value,
        baseUrl: await this.setting('tmdb_base_url', 'https://api.themoviedb.org'),
        ...(proxyUrl === undefined ? {} : { proxyUrl }),
      }).configuration(signal)
      return
    }
    if (credential === 'resource-api-token') {
      await new ResourceApiClient({
        baseUrl: await this.setting('resource_api_base_url', 'http://gaotao.cc:8100'),
        token: value,
        allowInsecureHttp: this.deps.config.allowInsecureResourceApi,
        ...(proxyUrl === undefined ? {} : { proxyUrl }),
      }).search('credential-check', { limit: 1 }, signal)
      return
    }
    if (credential === 'outbound-proxy-url') {
      const transport = new ProxyHttpTransport({ proxyUrl: value })
      const tmdbBaseUrl = await this.setting('tmdb_base_url', 'https://api.themoviedb.org')
      const resourceBaseUrl = await this.setting('resource_api_base_url', 'http://gaotao.cc:8100')
      const responses = await Promise.all([
        transport.request(new URL('/3/configuration', tmdbBaseUrl), signal, { method: 'GET' }),
        transport.request(new URL(resourceBaseUrl), signal, { method: 'GET' }),
      ])
      if (responses.some(response => response.status === 407 || response.status >= 500)) {
        throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '代理无法访问 TMDB 或资源 API')
      }
      return
    }
    throw new EmbymediaError('INVALID_INPUT', `unsupported credential ${credential}`)
  }

  private async executeSmartPolicy(plan: OperationProjection, _signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    const key = requiredString(input.key, 'Smart Action policy key') as SmartActionType
    const mode = requiredString(input.mode, 'policy mode') as 'confirm' | 'suggest' | 'auto'
    const maxRisk = requiredString(input.maxRisk, 'policy max risk') as Risk
    const params = input.params as JsonValue
    await this.smartActions.updatePolicy(key, input.enabled === true, mode, maxRisk, params)
    return { key, updated: true }
  }

  private async executeSmartDismiss(plan: OperationProjection, _signal: AbortSignal): Promise<JsonValue> {
    const input = inputOf(plan)
    return await this.smartActions.dismiss(requiredString(input.actionId, 'Smart Action id'), optionalString(input.reason)) as unknown as JsonValue
  }

  private async verifyLibraryCreate(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const name = requiredString(inputOf(plan).name, 'library name')
    const library = (await this.libraries.libraries(signal)).find(item => item.name === name)
    return { ok: library !== undefined, facts: { name, visible: library !== undefined, libraryId: library?.id ?? null } }
  }

  private async verifyLibraryScan(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const result = resultObject(plan)
    const status = typeof result?.status === 'string' ? result.status : 'unknown'
    const request = await this.scanRequest(inputOf(plan), signal)
    const items = await this.libraries.items(request.libraryId, 1, signal)
    return {
      ok: status === 'done',
      ...(status === 'partial' ? { partial: true } : {}),
      facts: { taskStatus: status, libraryId: request.libraryId, libraryReadable: items.length >= 0 },
    }
  }

  private async verifyAddNew(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const request = this.addNewVerificationRequest(plan)
    const items = await this.matchingItems(request, signal)
    const result = resultObject(plan)
    const verification = result?.verification
    const scanTask = typeof result?.scanTask === 'object' && result.scanTask !== null && !Array.isArray(result.scanTask)
      ? result.scanTask as Readonly<Record<string, JsonValue>>
      : undefined
    const ok = hasOk(verification) && scanTask?.status === 'done' && items.length > 0
    return {
      ok,
      facts: {
        libraryId: request.scan.libraryId,
        top: request.scan.top ?? null,
        embyVisible: items.length,
        pipelineVerified: hasOk(verification),
        scanTaskStatus: scanTask?.status ?? null,
      },
    }
  }

  private async verifySeriesUpdate(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const expected = object(plan.verification, 'series update verification')
    const libraryId = requiredString(expected.libraryId, 'library id')
    const seriesId = requiredString(expected.seriesId, 'series id')
    const tmdbId = requiredString(expected.tmdbId, 'TMDB id')
    const seriesPath = requiredString(expected.seriesPath, 'series path')
    const mode: EpisodeNumberingMode = inputOf(plan).numberingMode === 'absolute' ? 'absolute' : 'season'
    const requested = requestedEpisodes(expected.requestedEpisodes, mode)
    const current = await this.series.detail(libraryId, seriesId, mode, signal)
    const missingKeys = new Set(current.gaps.map(episode => episodeKey(episode, mode)))
    const missingAfter = requested.filter(episode => missingKeys.has(episodeKey(episode, mode)))
    const emby = await this.deps.embyClient(signal)
    const sameTmdb = (await emby.items(libraryId, 'Series', 'ProviderIds,Path', signal)).filter(item => item.ProviderIds?.Tmdb === tmdbId)
    const result = resultObject(plan)
    const pipelineVerified = hasOk(result?.verification)
    const scanTask = typeof result?.scanTask === 'object' && result.scanTask !== null && !Array.isArray(result.scanTask)
      ? result.scanTask as Readonly<Record<string, JsonValue>>
      : undefined
    const targetReadable = !['metadata_error', 'target_error', 'unknown'].includes(current.lane)
    const sameBinding = current.tmdbId === tmdbId && current.path === seriesPath
    const uniqueSeries = sameTmdb.length === 1 && sameTmdb[0]?.Id === seriesId
    return {
      ok: pipelineVerified && scanTask?.status === 'done' && targetReadable && sameBinding && missingAfter.length === 0 && uniqueSeries,
      facts: {
        libraryId,
        seriesId,
        tmdbId,
        pipelineVerified,
        scanTaskStatus: scanTask?.status ?? null,
        targetReadable,
        sameBinding,
        uniqueSeries,
        duplicateSeriesIds: sameTmdb.filter(item => item.Id !== seriesId).map(item => item.Id),
        requestedEpisodes: requested,
        missingAfter,
        localCountBefore: expected.localCountBefore ?? null,
        localCountAfter: current.localCount,
      } as unknown as JsonValue,
    }
  }

  private async verifyMediaDelete(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const resultStatus = resultObject(plan)?.status
    try {
      const expectation = this.cleanupDedupExpectation(plan)
      const verified = await this.cleanup.verify(this.deleteTargets(plan), expectation, signal)
      const partial = resultStatus === 'partial' || (resultStatus === 'done' && !verified.ok)
      return {
        ok: verified.ok && resultStatus === 'done',
        ...(partial ? { partial: true } : {}),
        facts: {
          ...object(verified.facts, 'cleanup verification facts'),
          resultStatus: resultStatus ?? null,
        } as unknown as JsonValue,
      }
    } catch (error) {
      if (signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED')) throw error
      return {
        ok: false,
        partial: true,
        facts: {
          resultStatus: resultStatus ?? null,
          diagnostics: [error instanceof Error ? error.message : String(error)],
        },
      }
    }
  }

  private async verifyPosterApply(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const input = inputOf(plan)
    const itemId = requiredString(input.itemId, 'Emby item id')
    const tmdbId = requiredString(input.tmdbId, 'TMDB id')
    const item = await (await this.deps.embyClient(signal)).item(itemId, 'ProviderIds,Path,ImageTags', signal)
    const primaryImage = hasPrimaryImage(item)
    return { ok: item?.ProviderIds?.Tmdb === tmdbId && primaryImage, facts: { itemId, expectedTmdbId: tmdbId, actualTmdbId: item?.ProviderIds?.Tmdb ?? null, primaryImage } }
  }

  private async verifyPosterBatch(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const input = inputOf(plan)
    if (!Array.isArray(input.items)) return { ok: false, facts: { reason: 'missing batch items' } }
    const emby = await this.deps.embyClient(signal)
    const facts: Array<{ itemId: string; expectedTmdbId: string; actualTmdbId: string | null; primaryImage: boolean }> = []
    for (const raw of input.items) {
      const row = object(raw, 'poster batch item')
      const itemId = requiredString(row.itemId, 'Emby item id')
      const expectedTmdbId = requiredString(row.tmdbId, 'TMDB id')
      const item = await emby.item(itemId, 'ProviderIds,ImageTags', signal)
      facts.push({ itemId, expectedTmdbId, actualTmdbId: item?.ProviderIds?.Tmdb ?? null, primaryImage: hasPrimaryImage(item) })
    }
    return { ok: facts.every(item => item.actualTmdbId === item.expectedTmdbId && item.primaryImage), facts: { items: facts } as unknown as JsonValue }
  }

  private async verifyMetadataRefresh(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const itemId = requiredString(inputOf(plan).itemId, 'Emby item id')
    const item = await (await this.deps.embyClient(signal)).item(itemId, 'ProviderIds,Path', signal)
    return { ok: item !== undefined, facts: { itemId, visible: item !== undefined, path: item?.Path ?? null } }
  }

  private async verifyUserCreate(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const name = requiredString(inputOf(plan).name, 'Emby user name')
    const user = (await (await this.deps.embyClient(signal)).users(signal)).find(item => item.Name === name)
    return { ok: user !== undefined, facts: { name, visible: user !== undefined, userId: user?.Id ?? null } }
  }

  private async verifyUserPolicy(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const input = inputOf(plan)
    const userId = requiredString(input.userId, 'Emby user id')
    const expected = object(input.policy, 'Emby user policy')
    const user = (await (await this.deps.embyClient(signal)).users(signal)).find(item => item.Id === userId)
    const actual = user?.Policy
    const matches = actual !== undefined && Object.entries(expected).every(([key, value]) => canonicalJson(actual[key] as JsonValue) === canonicalJson(value as JsonValue))
    return { ok: matches, facts: { userId, visible: user !== undefined, matches } }
  }

  private async verifyUserDelete(plan: OperationProjection, signal: AbortSignal): Promise<VerificationResult> {
    const userId = requiredString(inputOf(plan).userId, 'Emby user id')
    const visible = (await (await this.deps.embyClient(signal)).users(signal)).some(item => item.Id === userId)
    return { ok: !visible, facts: { userId, visible } }
  }

  private async verifyConfigUpdate(plan: OperationProjection): Promise<VerificationResult> {
    const settings = object(inputOf(plan).settings, 'settings') as Readonly<Record<string, JsonValue>>
    const rows = await this.deps.database.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings WHERE key = ANY($1)', [Object.keys(settings)])
    const actual = Object.fromEntries(rows.rows.map(row => [row.key, row.value]))
    const matches = Object.entries(settings).every(([key, value]) => canonicalJson(actual[key] ?? null) === canonicalJson(value))
    return { ok: matches, facts: { keys: Object.keys(settings), matches } }
  }

  private async verifyUndo(plan: OperationProjection): Promise<VerificationResult> {
    const undoId = requiredString(inputOf(plan).undoId, 'undo id')
    const restored = await this.settings.verifyUndo(undoId)
    return { ok: restored, facts: { undoId, restored } }
  }
  private addNewVerificationRequest(plan: OperationProjection): AddNewRequest {
    const input = inputOf(plan)
    const rawValues = Array.isArray(input.candidates) ? input.candidates : [input.candidate]
    if (rawValues.length === 0) throw new EmbymediaError('POLICY_DENIED', 'add-new verification has no persisted candidates')
    const expected = object(plan.verification, 'add-new verification')
    if (!Array.isArray(expected.expectedNames) || expected.expectedNames.length === 0) throw new EmbymediaError('POLICY_DENIED', 'add-new verification has no persisted expected names')
    const expectedNames = expected.expectedNames.map(value => requiredString(value, 'expected media name'))
    const canonicalCid = plan.targets[0]?.canonicalCid
    if (canonicalCid === undefined) throw new EmbymediaError('POLICY_DENIED', 'add-new verification has no canonical target CID')
    const candidates = rawValues.map((value, index): ResourceCandidate => {
      const raw = object(value, `resource candidate ${String(index + 1)}`)
      return {
        mode: 'share', url: 'verification-only', title: requiredString(raw.title, 'resource title'),
        targetCid: canonicalCid,
      }
    })
    const top = expected.top === null ? undefined : requiredString(expected.top, 'scan top')
    return {
      candidate: candidates[0]!,
      ...(candidates.length === 1 ? {} : { candidates }),
      scan: {
        libraryId: requiredString(expected.libraryId, 'library id'),
        libraryName: requiredString(expected.libraryName, 'library name'),
        mediaFolder: requiredString(expected.mediaFolder, 'media folder'),
        ...(expected.outputFolder === null || expected.outputFolder === undefined ? {} : { outputFolder: requiredString(expected.outputFolder, 'output folder') }),
        ...(top === undefined ? candidates.length === 1 ? {} : { tops: expectedNames } : { top }),
      },
      expectedNames,
      expectedPaths: expectedNames,
    }
  }


  private async verifyCredentialRotate(plan: OperationProjection): Promise<VerificationResult> {
    const credential = requiredString(inputOf(plan).credential, 'credential id')
    const status = (await this.deps.credentials.status()).find(item => item.id === credential)
    return { ok: status?.configured === true, facts: { credential, configured: status?.configured ?? false, writable: status?.writable ?? false } }
  }

  private async verifySmartPolicy(plan: OperationProjection): Promise<VerificationResult> {
    const input = inputOf(plan)
    const key = requiredString(input.key, 'Smart Action policy key')
    const policy = (await this.smartActions.policies()).find(item => item.key === key)
    const matches = policy !== undefined && policy.enabled === input.enabled && policy.mode === input.mode && policy.maxRisk === input.maxRisk
      && canonicalJson(policy.params) === canonicalJson(input.params as JsonValue)
    return { ok: matches, facts: { key, visible: policy !== undefined, matches } }
  }

  private async verifySmartDismiss(plan: OperationProjection): Promise<VerificationResult> {
    const actionId = requiredString(inputOf(plan).actionId, 'Smart Action id')
    const action = await this.smartActions.get(actionId)
    return { ok: action.status === 'dismissed', facts: { actionId, status: action.status } }
  }

  private async scanRequest(input: Readonly<Record<string, unknown>>, signal: AbortSignal): Promise<ScanRequest> {

    const libraryId = requiredString(input.libraryId, 'library id')
    const requestedName = requiredString(input.libraryName, 'library name')
    const mediaFolder = requiredString(input.mediaFolder, 'media folder')
    const library = (await this.libraries.libraries(signal)).find(item => item.id === libraryId)
    if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
    if (library.name !== requestedName) throw new EmbymediaError('CONFLICT', `library name changed from ${requestedName} to ${library.name}`)
    const top = optionalString(input.top)
    const tops = input.tops === undefined ? undefined : Array.isArray(input.tops) ? input.tops.map(value => requiredString(value, 'scan top')) : undefined
    if (input.tops !== undefined && tops === undefined) throw new EmbymediaError('INVALID_INPUT', 'scan tops must be an array')
    if (top !== undefined && tops !== undefined) throw new EmbymediaError('INVALID_INPUT', 'scan accepts top or tops, not both')
    if (tops !== undefined && (tops.length === 0 || tops.length > 100)) throw new EmbymediaError('INVALID_INPUT', 'scan tops must contain 1 to 100 paths')
    const selected = tops ?? (top === undefined ? [] : [top])
    if (selected.length === 0) await lstat(await this.mediaPath(mediaFolder, undefined))
    else for (const value of selected) await lstat(await this.mediaPath(mediaFolder, value))
    const outputFolder = optionalString(input.outputFolder)
    return { libraryId, libraryName: library.name, mediaFolder, ...(top === undefined ? {} : { top }), ...(tops === undefined ? {} : { tops }), ...(outputFolder === undefined ? {} : { outputFolder }) }
  }

  private addNewCacheKey(input: Readonly<Record<string, unknown>>): string {
    const rawValues = Array.isArray(input.candidates) ? input.candidates : [input.candidate]
    const candidates = rawValues.map((value) => {
      const raw = object(value, 'resource candidate')
      const url = requiredString(raw.url, 'resource URL')
      let shareCode = url
      try { shareCode = parseC115Share(url, typeof raw.password === 'string' ? raw.password : undefined).shareCode } catch {}
      return {
        shareCode,
        title: raw.title ?? null,
        targetCid: raw.targetCid ?? raw.cid ?? null,
        fileIds: raw.fileIds ?? null,
      }
    })
    const material = { candidates, scan: input.scan ?? null, oldVersionPolicy: input.oldVersionPolicy ?? null } as unknown as JsonValue
    return createHash('sha256').update(canonicalJson(material)).digest('hex')
  }

  private async prepareAddNew(
    input: Readonly<Record<string, unknown>>,
    signal: AbortSignal,
    stagedPassword?: string | readonly (string | undefined)[],
  ): Promise<PreparedAddNew> {
    const batchInput = Array.isArray(input.candidates)
    const cacheKey = this.addNewCacheKey(input)
    const cached = batchInput ? this.addNewCache.get(cacheKey) : undefined
    if (cached !== undefined && cached.expiresAt > Date.now()) return cached.prepared
    if (cached !== undefined) this.addNewCache.delete(cacheKey)
    const rawValues = Array.isArray(input.candidates) ? input.candidates : [input.candidate]
    if (rawValues.length === 0 || rawValues.length > 100) throw new EmbymediaError('INVALID_INPUT', 'resource.add_new requires 1 to 100 candidates')
    let batchScan: ScanRequest | undefined
    let batchTargetCid: string | undefined
    if (Array.isArray(input.candidates)) {
      const rawScan = object(input.scan, 'resource.add_new scan')
      const libraryId = requiredString(rawScan.libraryId, 'library id')
      const requestedName = requiredString(rawScan.libraryName, 'library name')
      const mediaFolder = requiredString(rawScan.mediaFolder, 'media folder')
      if (rawScan.top !== undefined) throw new EmbymediaError('INVALID_INPUT', 'batch resource.add_new scan does not accept top')
      const library = (await this.libraries.libraries(signal)).find(item => item.id === libraryId)
      if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
      if (library.name !== requestedName || mediaFolder !== library.name) throw new EmbymediaError('CONFLICT', `scan target differs from canonical library ${library.name}`)
      const outputFolder = optionalString(rawScan.outputFolder)
      if (library.collectionType === 'tvshows' && outputFolder === undefined) throw new EmbymediaError('INVALID_INPUT', 'batch TV onboarding requires scan.outputFolder')
      batchTargetCid = await this.libraryRootCid(library.name)
      batchScan = { libraryId, libraryName: library.name, mediaFolder: library.name, ...(outputFolder === undefined ? {} : { outputFolder }) }
    }
    const stagedPasswords = Array.isArray(stagedPassword) ? stagedPassword : [stagedPassword]
    const c115 = await this.deps.c115Client(signal)
    const candidates: ResourceCandidate[] = []
    const snapshots: C115Snapshot[] = []
    for (let index = 0; index < rawValues.length; index++) {
      const raw = object(rawValues[index], `resource candidate ${String(index + 1)}`)
      const url = requiredString(raw.url, 'resource URL')
      const title = requiredString(raw.title, 'resource title')
      const targetCid = optionalString(raw.targetCid ?? raw.cid) ?? batchTargetCid
      if (targetCid === undefined) throw new EmbymediaError('INVALID_INPUT', 'resource candidate target CID is required')
      const explicitMode = optionalString(raw.mode)
      const mode: ResourceCandidate['mode'] = explicitMode === 'offline' ? 'offline' : 'share'
      if (mode !== 'share') throw new EmbymediaError('POLICY_DENIED', 'resource.add_new requires verifiable 115 shares')
      const fileIds = Array.isArray(raw.fileIds) ? raw.fileIds.map(value => requiredString(value, 'file id')) : undefined
      const password = optionalString(raw.password) ?? stagedPasswords[index]
      if (raw.passwordStaged === true && password === undefined) throw new EmbymediaError('CONFLICT', 'staged 115 share password expired; create a new plan')
      const candidate: ResourceCandidate = { mode, url, title, targetCid, ...(password === undefined ? {} : { password }), ...(fileIds === undefined ? {} : { fileIds }) }
      const snapshot = await c115.snapshot(url, candidate.password, fileIds, signal)
      if (snapshot.files.length !== 1) throw new EmbymediaError('POLICY_DENIED', 'resource.add_new requires one wrapped top-level share root per candidate')
      const root = snapshot.files[0]!
      const evidenceLeaves = (snapshot.evidence ?? []).flatMap(file =>
        file.directory === false && file.path !== undefined && isVideoFileName(file.name) ? [file.path] : [],
      )
      if (root.directory === true && evidenceLeaves.length === 0) throw new EmbymediaError('POLICY_DENIED', 'resource share directory contains no supported video leaf evidence')
      if (root.directory !== true && (root.directory !== false || !isVideoFileName(root.name))) throw new EmbymediaError('POLICY_DENIED', 'resource share root is not a supported video file or directory')
      candidates.push({ ...candidate, fileIds: snapshotFileIds(snapshot) })
      snapshots.push(snapshot)
    }
    const targetCids = new Set(candidates.map(candidate => candidate.targetCid))
    if (targetCids.size !== 1) throw new EmbymediaError('CONFLICT', 'batch candidates must use one canonical target CID')
    const expectedNames = snapshots.map(snapshot => snapshot.files[0]!.name)
    if (new Set(expectedNames).size !== expectedNames.length) throw new EmbymediaError('CONFLICT', 'batch candidates contain duplicate top-level names')
    const expectedPaths = snapshots.flatMap((snapshot) => {
      const root = snapshot.files[0]!
      if (root.directory !== true) return [root.name]
      return (snapshot.evidence ?? []).flatMap(file => file.directory === false && file.path !== undefined && isVideoFileName(file.name) ? [file.path] : [])
    })
    let scan: ScanRequest
    if (candidates.length === 1) {
      scan = await this.addNewScan(input.scan, candidates[0]!, snapshots[0]!, signal)
    } else {
      if (batchScan === undefined || batchTargetCid === undefined) throw new EmbymediaError('INVALID_INPUT', 'batch resource.add_new requires a canonical scan object')
      if ([...targetCids][0] !== batchTargetCid) throw new EmbymediaError('CONFLICT', 'batch target CID differs from the configured library root')
      scan = { ...batchScan, tops: expectedNames }
    }
    for (const name of expectedNames) {
      try {
        await lstat(await this.mediaPath(scan.mediaFolder, name))
        throw new EmbymediaError('CONFLICT', `resource.add_new target already exists: ${posix.join(scan.mediaFolder, name)}`)
      } catch (error) {
        if (error instanceof EmbymediaError) throw error
        if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
      }
    }
    const policy = optionalString(input.oldVersionPolicy)
    if (policy !== undefined && policy !== 'keep') throw new EmbymediaError('POLICY_DENIED', `old version policy ${policy} is not safely implemented`)
    const prepared: PreparedAddNew = {
      request: {
        candidate: candidates[0]!,
        ...(candidates.length === 1 ? {} : { candidates }),
        scan,
        expectedNames,
        expectedPaths,
      },
      snapshot: snapshots[0]!,
      snapshots,
      expectedNames,
    }
    if (batchInput) this.addNewCache.set(cacheKey, { prepared, expiresAt: Date.now() + 5 * 60_000 })
    return prepared
  }

  private seriesUpdateCacheKey(input: Readonly<Record<string, unknown>>): string {
    const candidateKey = this.addNewCacheKey({ candidates: input.candidates ?? [input.candidate], scan: null })
    const material = { candidateKey, libraryId: input.libraryId ?? null, seriesId: input.seriesId ?? null, numberingMode: input.numberingMode ?? null, requestedEpisodes: input.requestedEpisodes ?? null } as unknown as JsonValue
    return createHash('sha256').update(canonicalJson(material)).digest('hex')
  }

  private async prepareSeriesUpdate(
    input: Readonly<Record<string, unknown>>,
    signal: AbortSignal,
    stagedPassword?: string | readonly (string | undefined)[],
  ): Promise<PreparedSeriesUpdate> {
    const batchInput = Array.isArray(input.candidates)
    const cacheKey = this.seriesUpdateCacheKey(input)
    const cached = batchInput ? this.seriesUpdateCache.get(cacheKey) : undefined
    if (cached !== undefined && cached.expiresAt > Date.now()) return cached.prepared
    if (cached !== undefined) this.seriesUpdateCache.delete(cacheKey)
    const libraryId = requiredString(input.libraryId, 'library id')
    const seriesId = requiredString(input.seriesId, 'series id')
    const numberingMode: EpisodeNumberingMode = input.numberingMode === 'absolute' ? 'absolute' : 'season'
    const requested = requestedEpisodes(input.requestedEpisodes, numberingMode)
    const series = await this.series.detail(libraryId, seriesId, numberingMode, signal)
    if (series.folder === undefined || series.path === undefined) throw new EmbymediaError('POLICY_DENIED', `Emby series ${seriesId} has no canonical folder`)
    const currentGaps = new Set(series.gaps.map(episode => episodeKey(episode, numberingMode)))
    const stale = requested.filter(episode => !currentGaps.has(episodeKey(episode, numberingMode)))
    if (stale.length > 0) throw new EmbymediaError('CONFLICT', 'requested episodes are no longer missing from the target Series', { stale })
    const folderTmdbId = declaredTmdbId(series.folder)
    if (folderTmdbId !== undefined && folderTmdbId !== series.tmdbId) throw new EmbymediaError('CONFLICT', `series folder declares TMDB ${folderTmdbId} but Emby has ${series.tmdbId}`)
    const libraryRootCid = await this.libraryRootCid(series.libraryName)
    const c115 = await this.deps.c115Client(signal)
    const directories = await c115.listDirectories(libraryRootCid, signal)
    const exact = directories.filter(directory => directory.name === series.folder)
    const byTmdb = directories.filter(directory => declaredTmdbId(directory.name) === series.tmdbId)
    const normalizedNames = new Set([normalizedSeriesName(series.folder), normalizedSeriesName(series.name)])
    const byName = directories.filter(directory => normalizedNames.has(normalizedSeriesName(directory.name)))
    const matched = new Map([...exact, ...byTmdb, ...byName].map(directory => [directory.cid, directory]))
    if (matched.size > 1) throw new EmbymediaError('CONFLICT', 'series identity resolved multiple 115 directories', { names: [...matched.values()].map(item => item.name) })
    const directory = [...matched.values()][0]
    if (directory === undefined) throw new EmbymediaError('POLICY_DENIED', `115 library ${series.libraryName} has no unique directory for TMDB ${series.tmdbId}`)

    const rawValues = Array.isArray(input.candidates) ? input.candidates : [input.candidate]
    if (rawValues.length === 0 || rawValues.length > 100) throw new EmbymediaError('INVALID_INPUT', 'series.update requires 1 to 100 candidates')
    const stagedPasswords = Array.isArray(stagedPassword) ? stagedPassword : [stagedPassword]
    const candidates: ResourceCandidate[] = []
    const snapshots: C115Snapshot[] = []
    const markerEvidence: string[] = []
    const coverageEvidence: string[] = []
    const requestedKeys = new Set(requested.map(episode => episodeKey(episode, numberingMode)))
    const expectedPaths: string[] = []
    for (let index = 0; index < rawValues.length; index++) {
      const raw = object(rawValues[index], `resource candidate ${String(index + 1)}`)
      const url = requiredString(raw.url, 'resource URL')
      const title = requiredString(raw.title, 'resource title')
      const explicitMode = optionalString(raw.mode)
      if (explicitMode !== undefined && explicitMode !== 'share') throw new EmbymediaError('POLICY_DENIED', 'series.update requires verifiable 115 shares')
      const fileIds = Array.isArray(raw.fileIds) ? raw.fileIds.map(value => requiredString(value, 'file id')) : undefined
      const password = optionalString(raw.password) ?? stagedPasswords[index]
      if (raw.passwordStaged === true && password === undefined) throw new EmbymediaError('CONFLICT', 'staged 115 share password expired; create a new plan')
      const snapshot = await c115.snapshot(url, password, fileIds, signal)
      if (snapshot.files.length !== 1) throw new EmbymediaError('POLICY_DENIED', 'each series.update candidate must expose one top-level root')
      const root = snapshot.files[0]!
      const recursiveEvidence = (snapshot.evidence ?? []).flatMap(file => [file.name, file.path ?? ''])
      const videoLeaves = (snapshot.evidence ?? []).filter(file => file.directory === false && isVideoFileName(file.name))
      markerEvidence.push(title, snapshot.title ?? '', root.name, ...recursiveEvidence)
      if (videoLeaves.length > 0) {
        coverageEvidence.push(...videoLeaves.map(file => file.name))
        for (const file of videoLeaves) {
          if (file.path === undefined) continue
          const keys = numberingMode === 'absolute' ? absoluteEpisodeKeysFromText(file.name) : episodeKeysFromText(file.name)
          if ([...keys].some(key => requestedKeys.has(key))) expectedPaths.push(file.path)
        }
      } else if (root.directory === false && isVideoFileName(root.name)) {
        coverageEvidence.push(root.name)
        const keys = numberingMode === 'absolute' ? absoluteEpisodeKeysFromText(root.name) : episodeKeysFromText(root.name)
        if ([...keys].some(key => requestedKeys.has(key))) expectedPaths.push(root.name)
      }
      candidates.push({ mode: 'share', url, title, targetCid: directory.cid, ...(password === undefined ? {} : { password }), fileIds: snapshotFileIds(snapshot) })
      snapshots.push(snapshot)
    }
    const declaredIds = new Set(markerEvidence.flatMap(value => declaredTmdbId(value) ?? []))
    if ([...declaredIds].some(id => id !== series.tmdbId)) throw new EmbymediaError('CONFLICT', 'resource TMDB marker differs from the target Series', { expectedTmdbId: series.tmdbId, declaredTmdbIds: [...declaredIds] })
    const covered = new Set(coverageEvidence.flatMap(value => [...(numberingMode === 'absolute' ? absoluteEpisodeKeysFromText(value) : episodeKeysFromText(value))]))
    const uncovered = requested.filter(episode => !covered.has(episodeKey(episode, numberingMode)))
    if (uncovered.length > 0) throw new EmbymediaError('POLICY_DENIED', '115 snapshot does not prove coverage of every requested episode', { uncovered })
    if (new Set(expectedPaths).size !== expectedPaths.length) throw new EmbymediaError('CONFLICT', 'series.update candidates contain duplicate media paths')
    const request: AddNewRequest = {
      candidate: candidates[0]!,
      ...(candidates.length === 1 ? {} : { candidates }),
      scan: { libraryId: series.libraryId, libraryName: series.libraryName, mediaFolder: series.libraryName, top: series.folder },
      expectedNames: snapshots.map(snapshot => snapshot.files[0]!.name),
      expectedPaths,
      seriesPlacement: { folder: series.folder, libraryId: series.libraryId, seriesId: series.id, tmdbId: series.tmdbId, numberingMode, requestedEpisodes: requested },
    }
    const prepared: PreparedSeriesUpdate = {
      request,
      snapshot: snapshots[0]!,
      snapshots,
      series: { ...series, folder: series.folder, path: series.path },
      requestedEpisodes: requested,
      libraryRootCid,
      seriesCid: directory.cid,
    }
    if (batchInput) this.seriesUpdateCache.set(cacheKey, { prepared, expiresAt: Date.now() + 5 * 60_000 })
    return prepared

  }
  private async libraryRootCid(libraryName: string): Promise<string> {
    const rows = await this.deps.database.query<{ value: JsonValue }>("SELECT value FROM app_settings WHERE key='c115_cid_map'")
    const cidMap = rows.rows[0]?.value
    if (typeof cidMap !== 'object' || cidMap === null || Array.isArray(cidMap)) throw new EmbymediaError('POLICY_DENIED', 'c115 CID map is not configured')
    return requiredString((cidMap as Readonly<Record<string, JsonValue>>)[libraryName], `115 root CID for ${libraryName}`)
  }

  private async addNewScan(value: unknown, candidate: ResourceCandidate, snapshot: C115Snapshot, signal: AbortSignal): Promise<ScanRequest> {
    const shareRoot = snapshot.files[0]
    if (shareRoot === undefined) throw new EmbymediaError('NOT_FOUND', '115 share contains no transferable root')
    const expectedTop = shareRoot.name
    if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
      const raw = value as Readonly<Record<string, unknown>>
      const libraryId = requiredString(raw.libraryId, 'library id')
      const requestedName = requiredString(raw.libraryName, 'library name')
      const requestedMediaFolder = requiredString(raw.mediaFolder, 'media folder')
      const library = (await this.libraries.libraries(signal)).find(item => item.id === libraryId)
      if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
      if (library.name !== requestedName || requestedMediaFolder !== library.name) {
        throw new EmbymediaError('CONFLICT', `scan target differs from canonical library ${library.name}`)
      }
      const configuredCid = await this.libraryRootCid(library.name)
      if (candidate.targetCid !== configuredCid) throw new EmbymediaError('CONFLICT', 'resource target CID differs from the configured library root')
      const declaredTop = optionalString(raw.top)
      if (declaredTop !== undefined && declaredTop !== expectedTop) throw new EmbymediaError('CONFLICT', 'scan top differs from the verified share root')
      return { libraryId, libraryName: library.name, mediaFolder: library.name, top: expectedTop }
    }
    if (value !== true) throw new EmbymediaError('INVALID_INPUT', 'resource.add_new scan must be true or a canonical scan object')
    const rows = await this.deps.database.query<{ value: JsonValue }>("SELECT value FROM app_settings WHERE key='c115_cid_map'")
    const cidMap = rows.rows[0]?.value
    if (typeof cidMap !== 'object' || cidMap === null || Array.isArray(cidMap)) throw new EmbymediaError('POLICY_DENIED', 'c115 CID map is not configured')
    const libraryName = Object.entries(cidMap).find(([, cid]) => cid === candidate.targetCid)?.[0]
    if (libraryName === undefined) throw new EmbymediaError('POLICY_DENIED', `target CID ${candidate.targetCid} is not mapped to a canonical library`)
    const library = (await this.libraries.libraries(signal)).find(item => item.name === libraryName)
    if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryName} is not configured`)
    const top = snapshot.files[0]?.name
    return { libraryId: library.id, libraryName, mediaFolder: libraryName, ...(top === undefined ? {} : { top }) }
  }

  private async setting(key: string, fallback: string): Promise<string> {
    const row = (await this.deps.database.query<{ value: JsonValue }>('SELECT value FROM app_settings WHERE key=$1', [key])).rows[0]
    return typeof row?.value === 'string' && row.value.trim().length > 0 ? row.value : fallback
  }

  private async tmdbClient(): Promise<TmdbClient> {
    let proxyUrl: string | undefined
    try { proxyUrl = await this.deps.credentials.read('outbound-proxy-url') } catch {}
    return new TmdbClient({
      apiKey: await this.deps.credentials.read('tmdb-api-key'),
      baseUrl: await this.setting('tmdb_base_url', 'https://api.themoviedb.org'),
      ...(proxyUrl === undefined ? {} : { proxyUrl }),
    })
  }

  private async mediaPath(mediaFolder: string, top: string | undefined): Promise<string> {
    return safeUnder(this.deps.config.mediaRoot, posix.join(mediaFolder, top ?? ''))
  }

  private expectedMediaNames(request: AddNewRequest): readonly string[] {
    if (request.expectedNames !== undefined && request.expectedNames.length > 0) return request.expectedNames
    if (request.scan.top !== undefined) return [request.scan.top]
    throw new EmbymediaError('POLICY_DENIED', 'add-new verification has no canonical expected media names')
  }

  private expectedMediaRelativePaths(request: AddNewRequest): readonly string[] {
    const prefix = request.seriesPlacement?.folder
    const expected = request.expectedPaths !== undefined && request.expectedPaths.length > 0
      ? request.expectedPaths
      : this.expectedMediaNames(request)
    return expected.map(path => posix.join(request.scan.mediaFolder, prefix ?? '', path))
  }

  private expectedEmbyRootOptions(request: AddNewRequest): readonly (readonly string[])[] {
    const libraryRoot = embyLibraryPath(request.scan.libraryName)
    const prefix = request.seriesPlacement?.folder ?? request.scan.outputFolder
    return this.expectedMediaNames(request).map((name) => {
      const raw = posix.join(libraryRoot, prefix ?? '', name)
      const extension = posix.extname(name)
      if (extension.length === 0) return [raw]
      return [raw, posix.join(posix.dirname(raw), `${posix.basename(name, extension)}.strm`)]
    })
  }

  private async waitVisible(request: AddNewRequest, signal: AbortSignal): Promise<JsonValue> {
    const relativePaths = this.expectedMediaRelativePaths(request)
    const expected = await Promise.all(relativePaths.map(async relativePath => ({
      relativePath,
      absolutePath: await safeUnder(this.deps.config.mediaRoot, relativePath),
    })))
    const unresolved = new Map(expected.map(entry => [entry.relativePath, entry.absolutePath]))
    for (let attempt = 0; attempt < 60; attempt++) {
      signal.throwIfAborted()
      for (const [relativePath, absolutePath] of unresolved) {
        try {
          const metadata = await lstat(absolutePath)
          if (metadata.isFile()) unresolved.delete(relativePath)
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
        }
      }
      if (unresolved.size === 0) return { visible: true, expectedCount: expected.length, visibleCount: expected.length, attempts: attempt + 1 }
      if (attempt < 59) await abortableSleep(2_000, signal)
    }
    throw new EmbymediaError('VERIFICATION_FAILED', 'CloudDrive did not expose all expected media leaves within 120 seconds', {
      expectedCount: expected.length,
      visibleCount: expected.length - unresolved.size,
      missingCount: unresolved.size,
      missingSample: [...unresolved.keys()].slice(0, 20),
    })
  }
  private async matchingItems(request: AddNewRequest, signal: AbortSignal): Promise<readonly EmbyItem[]> {
    const emby = await this.deps.embyClient(signal)
    const rootOptions = this.expectedEmbyRootOptions(request)
    const items = new Map<string, EmbyItem>()
    for (const path of new Set(rootOptions.flat())) {
      for (const item of await emby.itemsByPath(request.scan.libraryId, path, 'Movie,Series,Episode', 'Path,ProviderIds,ImageTags', signal)) items.set(item.Id, item)
    }
    const prefixes = rootOptions.flat()
    return [...items.values()].filter((item) => {
      const path = item.Path
      return typeof path === 'string' && prefixes.some(prefix => path === prefix || path.startsWith(`${prefix}/`))
    })
  }

  private async inspectAddedItems(request: AddNewRequest, signal: AbortSignal): Promise<JsonValue> {
    const rootOptions = this.expectedEmbyRootOptions(request)
    const items = (await this.matchingItems(request, signal)).filter((item) => {
      const path = item.Path
      return typeof path === 'string' && rootOptions.some(options => options.includes(path))
    })
    return {
      checked: items.length,
      missingTmdb: items.filter(item => item.ProviderIds?.Tmdb === undefined).map(item => item.Id),
      missingPrimaryImage: items.filter(item => !hasPrimaryImage(item)).map(item => item.Id),
    }
  }

  private async analyzeAddedDuplicates(request: AddNewRequest, signal: AbortSignal): Promise<JsonValue> {
    const items = await this.matchingItems(request, signal)
    const counts: Record<string, number> = {}
    for (const item of items) {
      const tmdb = item.ProviderIds?.Tmdb
      if (tmdb !== undefined) counts[tmdb] = (counts[tmdb] ?? 0) + 1
    }
    return { duplicateTmdbIds: Object.entries(counts).filter(([, count]) => count > 1).map(([id]) => id) }
  }

  private async verifyOnboarding(state: OnboardingState, signal: AbortSignal): Promise<JsonValue> {
    const placement = state.request.seriesPlacement
    if (placement !== undefined) {
      const emby = await this.deps.embyClient(signal)
      let episodes: readonly EmbyItem[] = []
      let missingAfter = placement.requestedEpisodes
      for (let attempt = 0; attempt < 60; attempt++) {
        signal.throwIfAborted()
        episodes = await emby.episodes(placement.seriesId, signal)
        missingAfter = placement.requestedEpisodes.filter(requested => !episodes.some((episode) => {
          const canonical = episode.ParentIndexNumber === requested.season && episode.IndexNumber === requested.episode
          if (placement.numberingMode === 'season') return canonical
          return canonical || (requested.absolute !== undefined && episode.IndexNumber === requested.absolute)
        }))
        if (missingAfter.length === 0) break
        if (attempt < 59) await abortableSleep(2_000, signal)
      }
      return {
        ok: state.scanTask?.status === 'done' && missingAfter.length === 0,
        scanTaskId: state.scanTask?.id ?? null,
        scanTaskStatus: state.scanTask?.status ?? null,
        seriesId: placement.seriesId,
        tmdbId: placement.tmdbId,
        episodeCount: episodes.length,
        requestedEpisodes: placement.requestedEpisodes,
        missingAfter,
      }
    }
    const items = await this.matchingItems(state.request, signal)
    const rootOptions = this.expectedEmbyRootOptions(state.request)
    const rootItems = items.filter((item) => {
      const path = item.Path
      return typeof path === 'string' && rootOptions.some(options => options.includes(path))
    })
    const visibleRoots = rootOptions.filter(options => rootItems.some((item) => {
      const path = item.Path
      return typeof path === 'string' && options.includes(path)
    })).length
    const metadataReady = rootItems.length === rootOptions.length && rootItems.every(item => item.ProviderIds?.Tmdb !== undefined && hasPrimaryImage(item))
    return {
      ok: state.scanTask?.status === 'done' && visibleRoots === rootOptions.length,
      scanTaskId: state.scanTask?.id ?? null,
      scanTaskStatus: state.scanTask?.status ?? null,
      embyVisible: items.length,
      visibleRoots,
      expectedRoots: rootOptions.length,
      metadataReady,
      metadataStatus: metadataReady ? 'ready' : 'follow-up-required',
      missingMetadataItemIds: rootItems.filter(item => item.ProviderIds?.Tmdb === undefined || !hasPrimaryImage(item)).map(item => item.Id),
    }
  }

  private publicOnboarding(state: OnboardingState): JsonValue {
    return {
      candidate: { mode: state.request.candidate.mode, title: state.request.candidate.title, targetCid: state.request.candidate.targetCid },
      candidates: (state.request.candidates ?? [state.request.candidate]).map(candidate => ({ mode: candidate.mode, title: candidate.title, targetCid: candidate.targetCid })),
      scan: state.request.scan as unknown as JsonValue,
      accepted: state.accepted ?? null,
      visible: state.visible ?? null,
      scanTask: state.scanTask === undefined ? null : state.scanTask as unknown as JsonValue,
      poster: state.poster ?? null,
      dedup: state.dedup ?? null,
      oldVersion: state.oldVersion ?? null,
      verification: state.verification ?? null,
      stages: state.stages,
    }
  }
}

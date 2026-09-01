import type { C115Client, C115Snapshot } from '../clients/c115.ts'
import type { ResourceApiClient, ResourceSearchResult } from '../clients/resource-api.ts'
import type { LibraryDomainService, ScanRequest } from './library.ts'
import { runPipeline, type PipelineStage } from '../cancellation.ts'
import { asEmbymediaError, EmbymediaError, PartialOperationError } from '../errors.ts'
import type { JsonValue, TaskRunProjection } from '../schemas.ts'

export interface ResourceCandidate {
  readonly mode: 'share' | 'offline'
  readonly url: string
  readonly password?: string
  readonly fileIds?: readonly string[]
  readonly title: string
  readonly targetCid: string
}

/** Existing Series root and requested episodes bound to one onboarding run. */
export interface SeriesPlacement {
  readonly folder: string
  readonly libraryId: string
  readonly seriesId: string
  readonly tmdbId: string
  readonly numberingMode: 'season' | 'absolute'
  readonly requestedEpisodes: readonly { readonly season: number; readonly episode: number; readonly absolute?: number }[]
}

export interface AddNewRequest {
  readonly candidate: ResourceCandidate
  readonly candidates?: readonly ResourceCandidate[]
  readonly scan: ScanRequest
  readonly expectedNames?: readonly string[]
  readonly expectedPaths?: readonly string[]
  readonly seriesPlacement?: SeriesPlacement
}

export interface OnboardingState {
  readonly request: AddNewRequest
  readonly snapshot?: C115Snapshot
  readonly snapshots?: readonly C115Snapshot[]
  readonly accepted?: { readonly mode: 'share' | 'offline'; readonly detail: JsonValue }
  readonly visible?: JsonValue
  readonly scanTask?: TaskRunProjection
  readonly poster?: JsonValue
  readonly dedup?: JsonValue
  readonly oldVersion?: JsonValue
  readonly verification?: JsonValue
  readonly stages: readonly string[]
}

export interface OnboardingHooks {
  readonly waitVisible: (request: AddNewRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly inspectPoster: (request: AddNewRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly analyzeDedup: (request: AddNewRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly handleOldVersion: (request: AddNewRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly verify: (state: OnboardingState, signal: AbortSignal) => Promise<JsonValue>
}

export class ResourceDomainService {
  constructor(
    private readonly c115Client: (signal: AbortSignal) => Promise<C115Client>,
    private readonly resourceClient: (signal: AbortSignal) => Promise<ResourceApiClient>,
    private readonly libraries: LibraryDomainService,
    private readonly hooks: OnboardingHooks,
  ) {}

  async search(query: string, signal: AbortSignal): Promise<ResourceSearchResult> {
    return (await this.resourceClient(signal)).search(query, {}, signal)
  }

  async snapshot(candidate: ResourceCandidate, signal: AbortSignal): Promise<C115Snapshot> {
    if (candidate.mode !== 'share') throw new EmbymediaError('INVALID_INPUT', 'snapshot requires a share candidate')
    return (await this.c115Client(signal)).snapshot(candidate.url, candidate.password, candidate.fileIds, signal)
  }

  async executeAddNew(
    request: AddNewRequest,
    signal: AbortSignal,
    progress?: (stage: string, index: number, total: number) => Promise<void> | void,
  ): Promise<OnboardingState> {
    const candidates = request.candidates ?? [request.candidate]
    const initial: OnboardingState = { request, stages: [] }
    const stages: readonly PipelineStage<OnboardingState>[] = [
      {
        name: 'validate-candidate',
        run: async (state) => {
          const c115 = await this.c115Client(signal)
          const snapshots: C115Snapshot[] = []
          for (const candidate of candidates) {
            if (candidate.mode !== 'share') throw new EmbymediaError('POLICY_DENIED', 'batch onboarding requires verifiable 115 shares')
            const snapshot = await c115.snapshot(candidate.url, candidate.password, candidate.fileIds, signal)
            if (snapshot.files.length === 0) throw new EmbymediaError('NOT_FOUND', 'resource share contains no transferable files')
            snapshots.push(snapshot)
          }
          return { ...state, snapshot: snapshots[0]!, snapshots, stages: [...state.stages, 'validate-candidate'] }
        },
      },
      {
        name: 'accept-115',
        run: async (state) => {
          const c115 = await this.c115Client(signal)
          const details: JsonValue[] = []
          for (const candidate of candidates) {
            details.push(await c115.saveShare(candidate.url, candidate.password, candidate.fileIds, candidate.targetCid, signal) as unknown as JsonValue)
          }
          return { ...state, accepted: { mode: 'share', detail: { total: details.length, items: details } }, stages: [...state.stages, 'accept-115'] }
        },
      },
      {
        name: 'wait-resource-visible',
        run: async state => ({ ...state, visible: await this.hooks.waitVisible(request, signal), stages: [...state.stages, 'wait-resource-visible'] }),
      },
      {
        name: 'generate-strm-and-scan',
        run: async (state) => {
          const scanTask = await this.libraries.scan(request.scan, signal)
          if (scanTask.status !== 'done') throw new EmbymediaError('VERIFICATION_FAILED', 'library scan did not complete successfully', { taskId: scanTask.id, status: scanTask.status })
          return { ...state, scanTask, stages: [...state.stages, 'generate-strm-and-scan'] }
        },
      },
      {
        name: 'poster-check',
        run: async state => ({ ...state, poster: await this.hooks.inspectPoster(request, signal), stages: [...state.stages, 'poster-check'] }),
      },
      {
        name: 'deduplicate',
        run: async state => ({ ...state, dedup: await this.hooks.analyzeDedup(request, signal), stages: [...state.stages, 'deduplicate'] }),
      },
      {
        name: 'old-version-policy',
        run: async state => ({ ...state, oldVersion: await this.hooks.handleOldVersion(request, signal), stages: [...state.stages, 'old-version-policy'] }),
      },
      {
        name: 'business-verification',
        run: async (state) => {
          const verification = await this.hooks.verify(state, signal)
          const record = typeof verification === 'object' && verification !== null && !Array.isArray(verification)
            ? verification as Record<string, JsonValue>
            : undefined
          if (record?.ok !== true) throw new EmbymediaError('VERIFICATION_FAILED', 'resource onboarding verification failed', verification)
          return { ...state, verification, stages: [...state.stages, 'business-verification'] }
        },
      },
    ]
    let activeStage: string | undefined
    try {
      return await runPipeline(initial, stages, signal, async (stage, index, total) => {
        activeStage = stage
        await progress?.(stage, index, total)
      })
    } catch (error) {
      if (activeStage === undefined || activeStage === 'validate-candidate') throw error
      const cause = asEmbymediaError(error)
      throw new PartialOperationError(cause.code, cause.message, {
        stage: activeStage,
        externalEffectsMayHaveStarted: true,
        cause: cause.toJSON(),
      }, cause.correlationId)
    }
  }
}

import { randomUUID } from 'node:crypto'
import type { Database } from '../database/index.ts'
import type { EmbyClient } from '../clients/emby.ts'
import { lstat } from 'node:fs/promises'
import { assertPathUnchanged, assertTreeUnchanged, capturePath, moveSnapshotted, removeSnapshotted, type PathSnapshot } from '../media/paths.ts'
import { EmbymediaError, PartialOperationError } from '../errors.ts'
import { abortableSleep } from '../cancellation.ts'
import type { JsonValue } from '../schemas.ts'

export interface DedupCandidate {
  readonly id: string
  readonly itemId: string
  readonly libraryId: string
  readonly library: string
  readonly folder: string
  readonly tmdbId: string
  readonly resolutionHeight: number
  readonly size: number
  readonly updatedAt: string
}

export interface DedupGroup {
  readonly tmdbId: string
  readonly keep: DedupCandidate
  readonly remove: readonly DedupCandidate[]
  readonly confidence: 'high' | 'medium' | 'review'
  readonly reason: string
}

export interface DeleteTarget {
  readonly id: string
  readonly embyItemId: string
  readonly embyPath: string
  readonly embyType: string
  readonly embyTmdbId?: string
  readonly strm?: PathSnapshot
  readonly cloud?: PathSnapshot
  readonly cloudParentCid?: string
  readonly cloudIds?: readonly string[]
  readonly strmTreeHash?: string
  readonly cloudName?: string
  readonly cloudDirectory?: boolean
  readonly cloudTreeHash?: string
}

export interface TargetMutationResult {
  readonly id: string
  readonly emby: 'deleted' | 'failed' | 'missing' | 'skipped'
  readonly strm: 'deleted' | 'failed' | 'skipped' | 'not-requested'
  readonly cloud: 'deleted' | 'failed' | 'skipped' | 'not-requested'
  readonly errors: readonly string[]
}

export interface MoveTarget {
  readonly id: string
  readonly strm: PathSnapshot
  readonly cloud: PathSnapshot
  readonly destinationStrmRoot: string
  readonly destinationStrmRelative: string
  readonly destinationCloudRoot: string
  readonly destinationCloudRelative: string
}

export interface MutationDedupExpectation {
  readonly libraryId: string
  readonly tmdbId: string
  readonly keepItemId: string
}

export interface CloudTargetBinding {
  readonly id: string
  readonly name: string
  readonly directory: boolean
  readonly treeHash?: string
}

export interface CloudDelete {
  readonly resolveIds: (parentCid: string, targets: readonly CloudTargetBinding[], signal: AbortSignal) => Promise<readonly string[]>
  readonly deleteIds: (parentCid: string, ids: readonly string[], signal: AbortSignal) => Promise<void>
}

function undoPath(value: JsonValue | undefined, label: string): string {
  if (typeof value !== 'string' || value.length === 0) throw new EmbymediaError('POLICY_DENIED', `undo entry has invalid ${label}`)
  return value
}

function candidateScore(candidate: DedupCandidate): number {
  const timestamp = Date.parse(candidate.updatedAt)
  const age = Number.isFinite(timestamp) ? Math.floor(timestamp / 86_400_000) : 0
  return candidate.resolutionHeight * 1_000_000_000_000 + candidate.size * 1_000 + age
}

export function analyzeDuplicates(items: readonly DedupCandidate[]): readonly DedupGroup[] {
  const grouped: Record<string, DedupCandidate[]> = {}
  for (const item of items) (grouped[item.tmdbId] ??= []).push(item)
  return Object.entries(grouped).flatMap(([tmdbId, candidates]) => {
    if (candidates.length < 2) return []
    const ranked = [...candidates].sort((left, right) => candidateScore(right) - candidateScore(left) || left.id.localeCompare(right.id))
    const [keep, ...remove] = ranked
    const equalTop = remove[0] !== undefined && candidateScore(remove[0]) === candidateScore(keep!)
    const confidence = equalTop ? 'review' : keep!.resolutionHeight > remove[0]!.resolutionHeight ? 'high' : 'medium'
    return [{
      tmdbId,
      keep: keep!,
      remove,
      confidence,
      reason: `keep ${keep!.resolutionHeight}p/${String(keep!.size)} bytes; remove ${String(remove.length)} older or lower-ranked version(s)`,
    }]
  })
}

export class MediaMutationService {
  constructor(
    private readonly database: Database,
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly cloudDelete: CloudDelete,
  ) {}

  private async preflightTargets(
    targets: readonly DeleteTarget[],
    dedupExpectation: MutationDedupExpectation | undefined,
    signal: AbortSignal,
  ): Promise<void> {
    const emby = await this.embyClient(signal)
    for (const target of targets) {
      signal.throwIfAborted()
      const item = await emby.item(target.embyItemId, 'Path,ProviderIds,Type', signal)
      if (item !== undefined && (item.Path !== target.embyPath || item.Type !== target.embyType || item.ProviderIds?.Tmdb !== target.embyTmdbId)) {
        throw new EmbymediaError('CONFLICT', `Emby cleanup target ${target.embyItemId} changed after preview`)
      }
      if (target.strm !== undefined) {
        try {
          await assertPathUnchanged(target.strm)
          if (target.strm.type === 'directory') {
            if (target.strmTreeHash === undefined) throw new EmbymediaError('POLICY_DENIED', 'directory cleanup target has no recursive manifest')
            await assertTreeUnchanged(target.strm, target.strmTreeHash)
          }
        } catch (error) {
          if (!(error instanceof EmbymediaError && error.code === 'CONFLICT' && error.message.includes('disappeared'))) throw error
        }
      }
      const cloudId = target.cloudIds?.[0]
      const ownsCloud = target.cloudParentCid !== undefined || cloudId !== undefined || target.cloudIds !== undefined
      if (ownsCloud) {
        if (target.cloudParentCid === undefined || cloudId === undefined || target.cloudIds?.length !== 1 || target.cloudName === undefined || target.cloudDirectory === undefined) {
          throw new EmbymediaError('POLICY_DENIED', 'cleanup target has an incomplete 115 binding')
        }
        await this.cloudDelete.resolveIds(target.cloudParentCid, [{ id: cloudId, name: target.cloudName, directory: target.cloudDirectory, ...(target.cloudTreeHash === undefined ? {} : { treeHash: target.cloudTreeHash }) }], signal)
      } else if (target.strm !== undefined) {
        throw new EmbymediaError('POLICY_DENIED', 'cleanup target has STRM state without a 115 binding')
      }
    }
    if (dedupExpectation !== undefined) {
      const matching = (await emby.items(dedupExpectation.libraryId, 'Series', 'ProviderIds,Path', signal))
        .filter(item => item.ProviderIds?.Tmdb === dedupExpectation.tmdbId)
      const expectedIds = new Set([dedupExpectation.keepItemId, ...targets.map(target => target.embyItemId)])
      const currentIds = matching.map(item => item.Id)
      if (!currentIds.includes(dedupExpectation.keepItemId) || currentIds.some(id => !expectedIds.has(id))) {
        throw new EmbymediaError('CONFLICT', 'dedup cleanup group changed after preview')
      }
    }
  }

  async deleteTargets(
    planId: string,
    targets: readonly DeleteTarget[],
    signal: AbortSignal,
    dedupExpectation?: MutationDedupExpectation,
  ): Promise<{ status: 'done' | 'partial'; targets: readonly TargetMutationResult[] }> {
    const emby = await this.embyClient(signal)
    const results: TargetMutationResult[] = []
    let effectsMayHaveStarted = false
    await this.preflightTargets(targets, dedupExpectation, signal)
    try {
      for (const target of targets) {
        signal.throwIfAborted()
        const errors: string[] = []
        let embyStatus: TargetMutationResult['emby'] = 'skipped'
        let strmStatus: TargetMutationResult['strm'] = target.strm === undefined ? 'not-requested' : 'deleted'
        let cloudStatus: TargetMutationResult['cloud'] = target.cloudIds === undefined ? 'not-requested' : 'deleted'
        try {
          const existing = await emby.item(target.embyItemId, 'Path,ProviderIds,Type', signal)
          if (existing === undefined) embyStatus = 'missing'
          else if (existing.Path !== target.embyPath || existing.Type !== target.embyType || existing.ProviderIds?.Tmdb !== target.embyTmdbId) {
            throw new EmbymediaError('CONFLICT', `Emby cleanup target ${target.embyItemId} changed after preflight`)
          } else {
            effectsMayHaveStarted = true
            await emby.deleteItem(target.embyItemId, signal)
            embyStatus = 'deleted'
          }
        } catch (error) {
          embyStatus = 'failed'
          errors.push(`Emby: ${error instanceof Error ? error.message : String(error)}`)
        }
        if (target.strm !== undefined) {
          try {
            let strmExists = true
            try { await lstat(target.strm.absolutePath) } catch (error) {
              if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
              strmExists = false
            }
            if (strmExists) {
              if (target.strm.type === 'directory') {
                if (target.strmTreeHash === undefined) throw new EmbymediaError('POLICY_DENIED', 'directory cleanup target has no recursive manifest')
                await assertTreeUnchanged(target.strm, target.strmTreeHash)
              }
              effectsMayHaveStarted = true
              await removeSnapshotted(target.strm)
            }
          } catch (error) {
            strmStatus = 'failed'
            errors.push(`STRM: ${error instanceof Error ? error.message : String(error)}`)
          }
        }
        if (strmStatus !== 'failed' && target.cloudIds !== undefined) {
          try {
            const cloudId = target.cloudIds[0]
            if (target.cloudParentCid === undefined || cloudId === undefined || target.cloudIds.length !== 1 || target.cloudName === undefined || target.cloudDirectory === undefined) {
              throw new EmbymediaError('POLICY_DENIED', 'cleanup target has an incomplete 115 binding')
            }
            const existingIds = await this.cloudDelete.resolveIds(target.cloudParentCid, [{
              id: cloudId, name: target.cloudName, directory: target.cloudDirectory,
              ...(target.cloudTreeHash === undefined ? {} : { treeHash: target.cloudTreeHash }),
            }], signal)
            if (existingIds.length > 0) {
              effectsMayHaveStarted = true
              await this.cloudDelete.deleteIds(target.cloudParentCid, existingIds, signal)
            }
          } catch (error) {
            cloudStatus = 'failed'
            errors.push(`Cloud: ${error instanceof Error ? error.message : String(error)}`)
          }
        } else if (target.cloudIds !== undefined) {
          cloudStatus = 'skipped'
        }
        results.push({ id: target.id, emby: embyStatus, strm: strmStatus, cloud: cloudStatus, errors })
      }
      if (results.some(result => result.emby === 'failed')) {
        await emby.refreshLibrary(signal)
        for (let attempt = 0; attempt < 30; attempt++) {
          signal.throwIfAborted()
          let pending = 0
          for (let index = 0; index < results.length; index++) {
            const result = results[index]!
            if (result.emby !== 'failed') continue
            const target = targets[index]!
            if (await emby.item(target.embyItemId, 'Path', signal) === undefined) {
              results[index] = { ...result, emby: 'missing', errors: result.errors.filter(error => !error.startsWith('Emby:')) }
            } else pending++
          }
          if (pending === 0) break
          if (attempt < 29) await abortableSleep(2_000, signal)
        }
      }
    } catch (error) {
      if (!effectsMayHaveStarted) throw error
      throw new PartialOperationError('PARTIAL_FAILURE', 'cleanup interrupted after destructive effects may have started', {
        planId,
        targets: results,
        cause: error instanceof Error ? error.message : String(error),
      } as unknown as JsonValue)
    }
    const status = results.every(result => result.errors.length === 0) ? 'done' : 'partial'
    if (signal.aborted && effectsMayHaveStarted) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'cleanup cancelled after destructive effects may have started', {
        planId,
        targets: results,
      } as unknown as JsonValue)
    }
    try {
      await this.database.query(
        `INSERT INTO audit_logs(actor,action,detail,correlation_id,destructive)
         VALUES ('gaotao','media.delete',$1,$2,true)`,
        [JSON.stringify({ planId, status, targets: results }), randomUUID()],
      )
    } catch (error) {
      throw new PartialOperationError('PARTIAL_FAILURE', 'cleanup audit failed after destructive effects may have started', {
        planId,
        status,
        targets: results,
        cause: error instanceof Error ? error.message : String(error),
      } as unknown as JsonValue)
    }
    return { status, targets: results }
  }

  async moveTarget(planId: string, target: MoveTarget, signal: AbortSignal): Promise<{ undoId: string; strm: string; cloud: string }> {
    signal.throwIfAborted()
    const cloud = await moveSnapshotted(target.cloud, target.destinationCloudRoot, target.destinationCloudRelative)
    let strm: string
    try {
      strm = await moveSnapshotted(target.strm, target.destinationStrmRoot, target.destinationStrmRelative)
    } catch (error) {
      const movedCloud = await capturePath(target.destinationCloudRoot, target.destinationCloudRelative)
      await moveSnapshotted(movedCloud, target.cloud.root, target.cloud.relativePath)
      throw error
    }
    const undoId = randomUUID()
    const payload = {
      kind: 'move',
      strm: { fromRoot: target.strm.root, from: target.strm.relativePath, toRoot: target.destinationStrmRoot, to: target.destinationStrmRelative },
      cloud: { fromRoot: target.cloud.root, from: target.cloud.relativePath, toRoot: target.destinationCloudRoot, to: target.destinationCloudRelative },
    }
    await this.database.query(
      'INSERT INTO undo_entries(id,plan_id,op,payload) VALUES ($1,$2,\'move\',$3)',
      [undoId, planId, JSON.stringify(payload)],
    )
    return { undoId, strm, cloud }
  }

  async executeUndo(undoId: string, signal: AbortSignal): Promise<JsonValue> {
    signal.throwIfAborted()
    return this.database.transaction(async (client) => {
      const result = await client.query<{ id: string; op: string; payload: JsonValue; undone: boolean }>(
        'SELECT id,op,payload,undone FROM undo_entries WHERE id=$1 FOR UPDATE',
        [undoId],
      )
      const entry = result.rows[0]
      if (entry === undefined) throw new EmbymediaError('NOT_FOUND', 'undo entry not found')
      if (entry.undone) return { undoId, status: 'already-undone' }
      if (entry.op !== 'move' || typeof entry.payload !== 'object' || entry.payload === null || Array.isArray(entry.payload)) {
        throw new EmbymediaError('POLICY_DENIED', 'undo entry is not automatically reversible')
      }
      const payload = entry.payload as Record<string, JsonValue>
      const strm = payload.strm as Record<string, JsonValue>
      const cloud = payload.cloud as Record<string, JsonValue>
      const cloudToRoot = undoPath(cloud.toRoot, 'cloud destination root')
      const cloudTo = undoPath(cloud.to, 'cloud destination path')
      const cloudFromRoot = undoPath(cloud.fromRoot, 'cloud source root')
      const cloudFrom = undoPath(cloud.from, 'cloud source path')
      const strmToRoot = undoPath(strm.toRoot, 'STRM destination root')
      const strmTo = undoPath(strm.to, 'STRM destination path')
      const strmFromRoot = undoPath(strm.fromRoot, 'STRM source root')
      const strmFrom = undoPath(strm.from, 'STRM source path')
      const cloudSnapshot = await capturePath(cloudToRoot, cloudTo)
      const strmSnapshot = await capturePath(strmToRoot, strmTo)
      const restoredCloud = await moveSnapshotted(cloudSnapshot, cloudFromRoot, cloudFrom)
      let restoredStrm: string
      try {
        restoredStrm = await moveSnapshotted(strmSnapshot, strmFromRoot, strmFrom)
      } catch (error) {
        const rollback = await capturePath(cloudFromRoot, cloudFrom)
        await moveSnapshotted(rollback, cloudToRoot, cloudTo)
        throw error
      }
      await client.query('UPDATE undo_entries SET undone=true WHERE id=$1', [undoId])
      return { undoId, status: 'done', cloud: restoredCloud, strm: restoredStrm }
    })
  }
}

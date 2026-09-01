import { lstat, readFile } from 'node:fs/promises'
import { posix, resolve } from 'node:path'
import type { C115Client, C115Entry } from '../clients/c115.ts'
import type { EmbyClient, EmbyItem, EmbyLibrary } from '../clients/emby.ts'
import type { ResolvedConfig } from '../config.ts'
import { EmbymediaError } from '../errors.ts'
import { capturePath, captureTreeHash, embyLibraryPath, safeUnder, type PathSnapshot } from '../media/paths.ts'
import type { JsonValue, TargetProjection } from '../schemas.ts'
import type { VerificationResult } from '../verification.ts'
import type { DeleteTarget } from './mutations.ts'
import { declaredTmdbId, normalizedSeriesName } from './series.ts'

interface LibraryLookup {
  readonly libraries: (signal: AbortSignal) => Promise<readonly EmbyLibrary[]>
}

interface CloudName {
  readonly itemId: string
  readonly name: string
  readonly id: string
}

interface DedupVerificationFacts extends DedupExpectation {
  readonly currentItemIds: readonly string[]
  readonly keeperRetained: boolean
}

/** Canonical mutation targets and their public plan projections. */
export interface PreparedMediaDelete {
  readonly deleteTargets: readonly DeleteTarget[]
  readonly targets: readonly TargetProjection[]
  readonly cloudNames: readonly CloudName[]
}

/** The complete duplicate group that must retain exactly one selected Series. */
export interface DedupExpectation {
  readonly libraryId: string
  readonly tmdbId: string
  readonly keepItemId: string
}

/** Canonical duplicate mutation targets plus their retained-Series expectation. */
export interface PreparedDedupDelete extends PreparedMediaDelete {
  readonly dedupExpectation: DedupExpectation
}


function directMediaRoot(root: string, path: string, itemId: string, itemType: string): { readonly relativePath: string; readonly nested: boolean } {
  if (!path.startsWith(`${root}/`)) throw new EmbymediaError('POLICY_DENIED', `Emby item ${itemId} is outside its library root`)
  const child = path.slice(root.length + 1)
  if (child.length === 0) throw new EmbymediaError('POLICY_DENIED', `Emby item ${itemId} has no library-root entry`)
  if (!child.includes('/')) return { relativePath: child, nested: false }
  if (itemType === 'Episode') throw new EmbymediaError('POLICY_DENIED', `media.delete does not delete a Series folder through one nested Episode: ${child}`)
  return { relativePath: child.split('/')[0]!, nested: true }
}

function directSeriesRoot(root: string, path: string, itemId: string): string {
  if (!path.startsWith(`${root}/`)) {
    throw new EmbymediaError('POLICY_DENIED', `Emby item ${itemId} is outside its library root`)
  }
  const top = path.slice(root.length + 1).split('/')[0]
  if (top === undefined || top.length === 0) throw new EmbymediaError('POLICY_DENIED', `Series ${itemId} has no library-root directory`)
  return top
}

async function absent(path: string): Promise<boolean> {
  try {
    await lstat(path)
    return false
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return true
    throw error
  }
}

/** Prepares and verifies exact Emby, STRM, and 115 deletion roots. */
export class MediaCleanupDomainService {
  constructor(
    private readonly config: Pick<ResolvedConfig, 'mediaRoot' | 'strmRoot'>,
    private readonly libraryLookup: LibraryLookup,
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly c115Client: (signal: AbortSignal) => Promise<C115Client>,
    private readonly libraryRootCid: (libraryName: string) => Promise<string>,
  ) {}

  /**
   * Resolves immutable deletion targets for direct children of one Emby library.
   * @param libraryId Emby library identifier.
   * @param itemIds Exact Emby item identifiers whose isolated roots are removed.
   * @param signal Cancellation signal for remote lookups.
   * @returns Mutation targets, public plan targets, and exact 115 names.
   */
  async prepareMediaDelete(libraryId: string, itemIds: readonly string[], signal: AbortSignal): Promise<PreparedMediaDelete> {
    return this.prepareTargets(libraryId, itemIds, false, signal, {})
  }

  private async prepareTargets(
    libraryId: string,
    itemIds: readonly string[],
    allowSeries: boolean,
    signal: AbortSignal,
    cloudRootIds: Readonly<Record<string, string>>,
    seriesInventory?: readonly EmbyItem[],
  ): Promise<PreparedMediaDelete> {
    if (itemIds.length === 0) throw new EmbymediaError('INVALID_INPUT', 'media.delete requires item ids')
    if (itemIds.length > 100) throw new EmbymediaError('POLICY_DENIED', 'media.delete may contain at most 100 items')
    if (new Set(itemIds).size !== itemIds.length) throw new EmbymediaError('INVALID_INPUT', 'media.delete item ids must be unique')

    const library = (await this.libraryLookup.libraries(signal)).find(item => item.id === libraryId)
    if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
    const emby = await this.embyClient(signal)
    const rootCid = await this.libraryRootCid(library.name)
    const c115 = await this.c115Client(signal)
    const cloudEntries = await c115.listEntries(rootCid, signal)
    const libraryPath = embyLibraryPath(library.name)
    const deleteTargets: DeleteTarget[] = []
    const targets: TargetProjection[] = []
    const cloudNames: CloudName[] = []
    const resolvedCloudIds = new Set<string>()

    const requested: EmbyItem[] = []
    const seriesById = new Map((seriesInventory ?? []).map(item => [item.Id, item]))
    for (const itemId of itemIds) {
      const item = seriesById.get(itemId) ?? await emby.item(itemId, 'Path,ProviderIds,Type', signal)
      if (item === undefined) throw new EmbymediaError('NOT_FOUND', `Emby item ${itemId} was not found`)
      requested.push(item)
    }
    const selectedIds = new Set(itemIds)
    const containsSeries = requested.some(item => item.Type === 'Series')
    const allSeries = containsSeries ? seriesInventory ?? await emby.items(libraryId, 'Series', 'Path,ProviderIds,Type', signal) : []
    const rootOwners = new Map<string, string>()
    for (const item of requested) {
      if (item.Path === undefined || item.Type === undefined) continue
      if (item.Type === 'Series') {
        rootOwners.set(directSeriesRoot(libraryPath, item.Path, item.Id), item.Id)
        continue
      }
      const root = directMediaRoot(libraryPath, item.Path, item.Id, item.Type)
      if (root.nested) rootOwners.set(root.relativePath, item.Id)
    }
    for (const item of requested) {
      signal.throwIfAborted()
      const itemId = item.Id
      const itemType = item.Type
      if (itemType === undefined) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `Emby item ${itemId} has no type`)
      if (allowSeries && itemType !== 'Series') throw new EmbymediaError('CONFLICT', `dedup.delete target ${itemId} is not a Series`)
      if (!allowSeries && !['Episode', 'Movie', 'Video', 'Series'].includes(itemType)) {
        throw new EmbymediaError('POLICY_DENIED', `media.delete does not support Emby item type ${itemType}`)
      }
      const itemPath = item.Path?.trim()
      if (itemPath === undefined || itemPath.length === 0) throw new EmbymediaError('INVALID_INPUT', `Emby item ${itemId} has no path`)
      const isSeries = itemType === 'Series'
      const mediaRoot = isSeries
        ? { relativePath: directSeriesRoot(libraryPath, itemPath, itemId), nested: itemPath.slice(libraryPath.length + 1).includes('/') }
        : directMediaRoot(libraryPath, itemPath, itemId, itemType)
      const relativeStrm = mediaRoot.relativePath
      const rootPath = `${libraryPath}/${relativeStrm}`
      if (isSeries) {
        const outsideSelection = allSeries.filter(other =>
          !selectedIds.has(other.Id) && (other.Path === rootPath || other.Path?.startsWith(`${rootPath}/`) === true),
        )
        if (outsideSelection.length > 0) throw new EmbymediaError('CONFLICT', `Series cleanup root ${relativeStrm} contains an unselected Series`, { itemIds: outsideSelection.map(other => other.Id) })
      } else if (mediaRoot.nested) {
        const neighbors = (await emby.itemsByPath(libraryId, rootPath, 'Movie,Episode,Video,Series', 'Path,Type', signal))
          .filter(other => !selectedIds.has(other.Id))
        if (neighbors.length > 0) throw new EmbymediaError('CONFLICT', `cleanup root ${relativeStrm} contains an unselected Emby media item`, { itemIds: neighbors.map(other => other.Id) })
      }

      const ownsRoot = rootOwners.get(relativeStrm) === undefined || rootOwners.get(relativeStrm) === itemId
      if (!ownsRoot) {
        deleteTargets.push({
          id: `${libraryId}:${itemId}`, embyItemId: itemId, embyPath: itemPath, embyType: itemType,
          ...(item.ProviderIds?.Tmdb === undefined ? {} : { embyTmdbId: item.ProviderIds.Tmdb }),
        })
        targets.push({ id: itemId, label: item.Name, canonicalLibraryId: libraryId, path: posix.join(library.name, relativeStrm) })
        continue
      }

      const strm = await capturePath(this.config.strmRoot, posix.join(library.name, relativeStrm))
      let cloudName = await this.cloudName(library.name, itemId, relativeStrm, strm)
      const nestedSeries = isSeries && mediaRoot.nested
      const explicitCloudId = cloudRootIds[itemId]
      if (explicitCloudId !== undefined && !nestedSeries) throw new EmbymediaError('POLICY_DENIED', `explicit 115 root id is allowed only for a nested Series cleanup target ${itemId}`)
      let matches = explicitCloudId === undefined
        ? cloudEntries.filter(entry => entry.name === cloudName && entry.directory === (strm.type === 'directory'))
        : cloudEntries.filter(entry => entry.id === explicitCloudId && entry.directory === (strm.type === 'directory'))
      if (matches.length === 0 && explicitCloudId === undefined && nestedSeries && strm.type === 'directory') {
        const normalizedName = normalizedSeriesName(item.Name)
        const tmdbId = item.ProviderIds?.Tmdb
        matches = cloudEntries.filter(entry => entry.directory && (normalizedSeriesName(entry.name) === normalizedName || (tmdbId !== undefined && declaredTmdbId(entry.name) === tmdbId)))
      }
      if (matches.length === 1) cloudName = matches[0]!.name
      const cloud = matches.length === 1 ? matches[0] : undefined
      if (cloud === undefined) throw new EmbymediaError('CONFLICT', `115 root ${library.name} resolved ${String(matches.length)} entries for ${cloudName}`)
      if (resolvedCloudIds.has(cloud.id)) throw new EmbymediaError('CONFLICT', `media.delete resolves multiple roots to 115 entry ${cloudName}`)
      resolvedCloudIds.add(cloud.id)
      const strmTreeHash = strm.type === 'directory' ? await captureTreeHash(strm) : undefined
      const cloudTreeHash = cloud.directory ? await c115.treeHash(cloud.id, signal) : undefined
      deleteTargets.push({
        id: `${libraryId}:${itemId}`, embyItemId: itemId, embyPath: itemPath, embyType: itemType,
        ...(item.ProviderIds?.Tmdb === undefined ? {} : { embyTmdbId: item.ProviderIds.Tmdb }),
        strm, ...(strmTreeHash === undefined ? {} : { strmTreeHash }), cloudParentCid: rootCid,
        cloudIds: [cloud.id], cloudName, ...(cloudTreeHash === undefined ? {} : { cloudTreeHash }), cloudDirectory: cloud.directory,
      })
      cloudNames.push({ itemId, name: cloudName, id: cloud.id })
      targets.push({
        id: itemId, label: `${item.Name} / ${cloudName}`, canonicalLibraryId: libraryId, canonicalCid: rootCid,
        path: posix.join(library.name, relativeStrm), snapshot: { inode: strm.inode, mtimeMs: strm.mtimeMs, size: strm.size },
      })
    }
    return { deleteTargets, targets, cloudNames }
  }

  /**
   * Validates a complete same-TMDB Series group before preparing duplicate deletion.
   * @param libraryId Emby library containing the duplicate group.
   * @param tmdbId Shared TMDB identifier for the complete group.
   * @param keepItemId Explicit Series identifier to retain.
   * @param removeItemIds Every other Series identifier in the current group.
   * @param signal Cancellation signal for remote lookups.
   * @param cloudRootIds Optional exact 115 root IDs for nested removal targets.
   * @returns Canonical deletion targets and the retained-Series expectation.
   */
  async prepareDedupDelete(
    libraryId: string,
    tmdbId: string,
    keepItemId: string,
    removeItemIds: readonly string[],
    signal: AbortSignal,
    cloudRootIds: Readonly<Record<string, string>> = {},
  ): Promise<PreparedDedupDelete> {
    if (removeItemIds.length === 0) throw new EmbymediaError('INVALID_INPUT', 'dedup.delete requires remove item ids')
    if (Object.keys(cloudRootIds).some(itemId => !removeItemIds.includes(itemId))) {
      throw new EmbymediaError('INVALID_INPUT', 'dedup.delete cloud root ids must name only removal targets')
    }
    if (removeItemIds.includes(keepItemId)) throw new EmbymediaError('INVALID_INPUT', 'dedup.delete cannot remove the keep item')
    if (new Set(removeItemIds).size !== removeItemIds.length) throw new EmbymediaError('INVALID_INPUT', 'dedup.delete remove item ids must be unique')
    if (removeItemIds.length > 100) throw new EmbymediaError('POLICY_DENIED', 'dedup.delete may remove at most 100 items')

    const emby = await this.embyClient(signal)
    const seriesInventory = await emby.items(libraryId, 'Series', 'ProviderIds,Path', signal)
    const matching = seriesInventory.filter(item => item.ProviderIds?.Tmdb === tmdbId)
    if (!matching.some(item => item.Id === keepItemId)) {
      throw new EmbymediaError('CONFLICT', 'dedup.delete keep item is not in the TMDB duplicate group', {
        tmdbId,
        keepItemId,
        currentItemIds: matching.map(item => item.Id),
      })
    }
    const expectedIds = new Set([keepItemId, ...removeItemIds])
    if (matching.length < 2 || expectedIds.size !== matching.length || matching.some(item => !expectedIds.has(item.Id))) {
      throw new EmbymediaError('CONFLICT', 'dedup.delete must name the complete current TMDB duplicate group', {
        tmdbId,
        currentItemIds: matching.map(item => item.Id),
      })
    }
    const prepared = await this.prepareTargets(libraryId, removeItemIds, true, signal, cloudRootIds, seriesInventory)
    return { ...prepared, dedupExpectation: { libraryId, tmdbId, keepItemId } }
  }

  /**
   * Proves every prepared target is absent from Emby, STRM, and its exact 115 root.
   * @param planTargets Immutable targets captured during preparation.
   * @param dedupExpectation Optional retained-Series requirement for duplicate deletion.
   * @param signal Cancellation signal for remote lookups.
   * @returns Verification facts and their aggregate result.
   */
  async verify(
    planTargets: readonly DeleteTarget[],
    dedupExpectation: DedupExpectation | undefined,
    signal: AbortSignal,
  ): Promise<VerificationResult> {
    const emby = await this.embyClient(signal)
    const c115 = await this.c115Client(signal)
    const cloudByParent = new Map<string, readonly C115Entry[]>()
    const targetFacts: Array<{ id: string; embyAbsent: boolean; strmAbsent: boolean; cloudAbsent: boolean }> = []

    for (const target of planTargets) {
      signal.throwIfAborted()
      let strmAbsent = true
      if (target.strm !== undefined) {
        if (resolve(target.strm.root) !== resolve(this.config.strmRoot)) throw new EmbymediaError('POLICY_DENIED', `cleanup target ${target.id} has an invalid STRM root`)
        const strmPath = await safeUnder(this.config.strmRoot, target.strm.relativePath)
        if (strmPath !== target.strm.absolutePath) throw new EmbymediaError('POLICY_DENIED', `cleanup target ${target.id} STRM path changed roots`)
        strmAbsent = await absent(strmPath)
      }
      let cloudAbsent = true
      if (target.cloudIds !== undefined) {
        const cloudId = target.cloudIds[0]
        if (target.cloudParentCid === undefined || cloudId === undefined || target.cloudIds.length !== 1 || target.cloudName === undefined || target.cloudDirectory === undefined) {
          throw new EmbymediaError('POLICY_DENIED', `cleanup target ${target.id} has an incomplete 115 binding`)
        }
        let cloudEntries = cloudByParent.get(target.cloudParentCid)
        if (cloudEntries === undefined) {
          cloudEntries = await c115.listEntries(target.cloudParentCid, signal)
          cloudByParent.set(target.cloudParentCid, cloudEntries)
        }
        cloudAbsent = !cloudEntries.some(entry => entry.id === cloudId || (entry.name === target.cloudName && entry.directory === target.cloudDirectory))
      }
      targetFacts.push({
        id: target.id,
        embyAbsent: await emby.item(target.embyItemId, 'Path', signal) === undefined,
        strmAbsent,
        cloudAbsent,
      })
    }
    let dedupFacts: DedupVerificationFacts | undefined
    if (dedupExpectation !== undefined) {
      const matching = (await emby.items(dedupExpectation.libraryId, 'Series', 'ProviderIds,Path', signal))
        .filter(item => item.ProviderIds?.Tmdb === dedupExpectation.tmdbId)
      const currentItemIds = matching.map(item => item.Id)
      dedupFacts = {
        ...dedupExpectation,
        currentItemIds,
        keeperRetained: currentItemIds.length === 1 && currentItemIds[0] === dedupExpectation.keepItemId,
      }
    }
    const targetsAbsent = targetFacts.every(fact => fact.embyAbsent && fact.strmAbsent && fact.cloudAbsent)
    return {
      ok: targetsAbsent && (dedupFacts?.keeperRetained ?? true),
      facts: {
        targets: targetFacts,
        ...(dedupFacts === undefined ? {} : { dedup: dedupFacts }),
      } as unknown as JsonValue,
    }
  }

  private async cloudName(libraryName: string, itemId: string, relativeStrm: string, strm: PathSnapshot): Promise<string> {
    let cloudName = relativeStrm
    if (strm.type === 'file') {
      const target = (await readFile(strm.absolutePath, 'utf8')).trim()
      const prefix = `/media/${libraryName}/`
      if (!target.startsWith(prefix)) {
        throw new EmbymediaError('POLICY_DENIED', `STRM target for ${itemId} is outside media library ${libraryName}`)
      }
      cloudName = target.slice(prefix.length)
      if (cloudName.length === 0 || cloudName.includes('/')) {
        throw new EmbymediaError('POLICY_DENIED', `media.delete only accepts direct library-root media files: ${cloudName}`)
      }
    }
    await safeUnder(this.config.mediaRoot, posix.join(libraryName, cloudName))
    return cloudName
  }
}

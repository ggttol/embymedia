import type { EmbyClient, EmbyItem } from '../clients/emby.ts'
import type { TmdbClient, TmdbEpisode, TmdbTv } from '../clients/tmdb.ts'
import type { ResourceApiClient, ResourceSearchResult } from '../clients/resource-api.ts'
import { EmbymediaError } from '../errors.ts'
import { embyLibraryPath } from '../media/paths.ts'
import type { JsonValue } from '../schemas.ts'

export type EpisodeNumberingMode = 'season' | 'absolute'

export interface LocalEpisode {
  readonly season?: number
  readonly episode?: number
  readonly absolute?: number
}

export interface EpisodeGap {
  readonly season: number
  readonly episode: number
  readonly absolute?: number
  readonly airDate?: string
  readonly name?: string
}

export interface SeriesStatus {
  readonly id: string
  readonly libraryId: string
  readonly libraryName: string
  readonly name: string
  readonly path?: string
  readonly folder?: string
  readonly tmdbId: string
  readonly tmdbStatus: string
  readonly localCount: number
  readonly missingCount: number
  readonly lane: 'healthy_airing' | 'update_needed' | 'archive_ready' | 'complete_after_update' | 'metadata_error' | 'target_error' | 'unknown'
  readonly gaps: readonly EpisodeGap[]
  readonly blocker?: { readonly code: string; readonly message: string }
}

export interface SeriesResourcePlan {
  readonly series: SeriesStatus
  readonly numberingMode: EpisodeNumberingMode
  readonly query: string
  readonly requestedEpisodes: readonly EpisodeGap[]
  readonly gapTotal: number
  readonly deferredGapCount: number
  readonly searchHasMore: boolean
  readonly search: ResourceSearchResult
}

export interface SeriesStatusPage {
  readonly rows: readonly SeriesStatus[]
  readonly total: number
  readonly offset: number
  readonly limit: number
  readonly hasMore: boolean
}

export interface ArchiveHooks {
  readonly moveCloud: (request: ArchiveRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly moveStrm: (request: ArchiveRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly notifyEmby: (request: ArchiveRequest, signal: AbortSignal) => Promise<JsonValue>
  readonly verify: (request: ArchiveRequest, signal: AbortSignal) => Promise<JsonValue>
}

export interface ArchiveRequest {
  readonly seriesId: string
  readonly fromLibraryId: string
  readonly toLibraryId: string
  readonly fromLibrary: string
  readonly toLibrary: string
  readonly folder: string
}
type ExpectedTmdbEpisode = TmdbEpisode & { readonly absolute_number?: number }

interface ArchiveResult {
  readonly cloud: JsonValue
  readonly strm: JsonValue
  readonly emby: JsonValue
  readonly verification: JsonValue
}


const TMDB_MARKER = /\b(?:tmdbid[-_]|tmdb-)(\d+)\b/i
const TMDB_MARKER_GLOBAL = /\b(?:tmdbid[-_]|tmdb-)\d+\b/gi
const EPISODE_RANGE = /\bS(\d{1,2})E(\d{1,3})(?:\s*(?:-|~|至|到)\s*(?:S(\d{1,2}))?E?(\d{1,3}))?\b/gi
const ABSOLUTE_EPISODE_RANGE = /(?:^|[^\p{L}\p{N}])E(\d{1,3})(?:\s*(?:-|~|至|到)\s*E?(\d{1,3}))?(?=$|[^\p{L}\p{N}])/giu

/** Return the first TMDB id declared by a supported media-folder marker. */
export function declaredTmdbId(value: string): string | undefined {
  return TMDB_MARKER.exec(value)?.[1]
}

/** Normalize a series folder or title for a conservative unique-name match. */
export function normalizedSeriesName(value: string): string {
  return value.normalize('NFC')
    .replace(TMDB_MARKER_GLOBAL, ' ')
    .replace(/(?:19|20)\d{2}/g, ' ')
    .toLocaleLowerCase('zh-CN')
    .replace(/[\s._\-—–:：·,，()（）[\]【】{}]+/g, '')
}

/** Parse season/episode keys such as S01E03 and S01E03-E05 from resource text. */
export function episodeKeysFromText(value: string): ReadonlySet<string> {
  const keys = new Set<string>()
  for (const match of value.matchAll(EPISODE_RANGE)) {
    const season = Number(match[1])
    const start = Number(match[2])
    const endSeason = match[3] === undefined ? season : Number(match[3])
    const end = match[4] === undefined ? start : Number(match[4])
    const invalid = !Number.isInteger(season) || season <= 0 || endSeason !== season
      || !Number.isInteger(start) || start <= 0 || end < start || end - start > 100
    if (invalid) continue
    for (let episode = start; episode <= end; episode++) keys.add(`${String(season)}:${String(episode)}`)
  }
  return keys
}

/** Parse standalone absolute episode keys such as E13 and E13-E15 from resource text. */
export function absoluteEpisodeKeysFromText(value: string): ReadonlySet<string> {
  const keys = new Set<string>()
  for (const match of value.matchAll(ABSOLUTE_EPISODE_RANGE)) {
    const start = Number(match[1])
    const end = match[2] === undefined ? start : Number(match[2])
    if (!Number.isInteger(start) || start <= 0 || end < start || end - start > 100) continue
    for (let episode = start; episode <= end; episode++) keys.add(`absolute:${String(episode)}`)
  }
  return keys
}

/** Resolve the direct series-folder segment under one Emby library root. */
export function seriesFolderFromPath(path: string | undefined, libraryName: string): string | undefined {
  if (path === undefined) return undefined
  const root = embyLibraryPath(libraryName)
  if (!path.startsWith(`${root}/`)) return undefined
  const relative = path.slice(root.length + 1)
  const [folder, ...rest] = relative.split('/')
  return folder !== undefined && folder.length > 0 && rest.length === 0 ? folder : undefined
}

function localEpisodes(
  items: readonly EmbyItem[],
  expected: readonly ExpectedTmdbEpisode[],
  mode: EpisodeNumberingMode,
): readonly LocalEpisode[] {
  const absoluteByEpisode = new Map(expected.map(episode => [
    `${String(episode.season_number)}:${String(episode.episode_number)}`,
    episode.absolute_number,
  ]))
  return items.map((episode) => {
    const season = typeof episode.ParentIndexNumber === 'number' ? episode.ParentIndexNumber : undefined
    const number = typeof episode.IndexNumber === 'number' ? episode.IndexNumber : undefined
    const mappedAbsolute = season === undefined || number === undefined ? undefined : absoluteByEpisode.get(`${String(season)}:${String(number)}`)
    const absolute = mappedAbsolute ?? (mode === 'absolute' ? number : undefined)
    return {
      ...(season === undefined ? {} : { season }),
      ...(number === undefined ? {} : { episode: number }),
      ...(absolute === undefined ? {} : { absolute }),
    }
  })
}
function aired(episode: TmdbEpisode, now: Date): boolean {
  if (episode.air_date === undefined) return false
  const timestamp = Date.parse(`${episode.air_date}T23:59:59Z`)
  return Number.isFinite(timestamp) && timestamp <= now.getTime()
}

export function computeEpisodeGaps(
  local: readonly LocalEpisode[],
  expected: readonly ExpectedTmdbEpisode[],
  mode: EpisodeNumberingMode,
  now = new Date(),
): readonly EpisodeGap[] {
  const have: Record<string, true> = {}
  for (const episode of local) {
    if (mode === 'absolute') {
      if (episode.absolute !== undefined) have[String(episode.absolute)] = true
    } else if (episode.season !== undefined && episode.episode !== undefined) {
      have[`${String(episode.season)}:${String(episode.episode)}`] = true
    }
  }
  return expected.filter(episode => aired(episode, now)).flatMap((episode) => {
    const absolute = episode.absolute_number
    const key = mode === 'absolute' ? absolute === undefined ? undefined : String(absolute) : `${String(episode.season_number)}:${String(episode.episode_number)}`
    if (key === undefined || have[key]) return []
    return [{
      season: episode.season_number,
      episode: episode.episode_number,
      ...(absolute === undefined ? {} : { absolute }),
      ...(episode.air_date === undefined ? {} : { airDate: episode.air_date }),
      ...(episode.name === undefined ? {} : { name: episode.name }),
    }]
  })
}

export class SeriesDomainService {
  constructor(
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly tmdbClient: (signal: AbortSignal) => Promise<TmdbClient>,
    private readonly resourceClient: (signal: AbortSignal) => Promise<ResourceApiClient>,
    private readonly archiveHooks: ArchiveHooks,
  ) {}

  async statusPage(libraryId: string, mode: EpisodeNumberingMode, signal: AbortSignal, limit = 100, offset = 0): Promise<SeriesStatusPage> {
    const emby = await this.embyClient(signal)
    const library = (await emby.libraries(signal)).find(item => item.id === libraryId)
    if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
    const tmdb = await this.tmdbClient(signal)
    const safeLimit = Math.min(Math.max(Math.floor(limit), 1), 100)
    const safeOffset = Math.max(0, Math.floor(offset))
    const page = await emby.itemPage(libraryId, 'Series', 'ProviderIds,Path,Status', safeLimit, signal, undefined, safeOffset)
    const rows: SeriesStatus[] = []
    for (const item of page.items) {
      signal.throwIfAborted()
      rows.push(await this.statusForItem(item, library.id, library.name, mode, emby, tmdb, signal))
    }
    return { rows, total: page.total, offset: safeOffset, limit: safeLimit, hasMore: safeOffset + rows.length < page.total }
  }

  async status(libraryId: string, mode: EpisodeNumberingMode, signal: AbortSignal): Promise<readonly SeriesStatus[]> {
    return (await this.statusPage(libraryId, mode, signal)).rows
  }

  async workbench(libraryId: string, mode: EpisodeNumberingMode, signal: AbortSignal, limit = 100, offset = 0) {
    const page = await this.statusPage(libraryId, mode, signal, limit, offset)
    const counts: Record<string, number> = {}
    for (const row of page.rows) counts[row.lane] = (counts[row.lane] ?? 0) + 1
    return { ...page, counts, behindTotal: page.rows.reduce((sum, row) => sum + row.missingCount, 0) }
  }

  /**
   * Returns only incomplete Series without per-episode payloads for bounded library inventory.
   * @param libraryId Emby library to inspect.
   * @param mode Episode-numbering mode used to calculate gaps.
   * @param signal Cancellation signal for Emby and TMDB reads.
   * @returns Compact incomplete-Series rows, counts, and aggregate missing episodes.
   */
  async gapsSummary(libraryId: string, mode: EpisodeNumberingMode, signal: AbortSignal, limit = 100, offset = 0) {
    const page = await this.statusPage(libraryId, mode, signal, limit, offset)
    const incomplete = page.rows.filter(row => row.missingCount > 0 || ['unknown', 'metadata_error', 'target_error'].includes(row.lane))
    const laneCounts: Record<string, number> = {}
    const statusCounts: Record<string, number> = {}
    for (const row of incomplete) {
      laneCounts[row.lane] = (laneCounts[row.lane] ?? 0) + 1
      statusCounts[row.tmdbStatus] = (statusCounts[row.tmdbStatus] ?? 0) + 1
    }
    return {
      rows: incomplete.map(row => ({
        id: row.id, libraryId: row.libraryId, libraryName: row.libraryName, name: row.name,
        tmdbId: row.tmdbId, tmdbStatus: row.tmdbStatus, localCount: row.localCount,
        missingCount: row.missingCount, lane: row.lane,
        ...(row.blocker === undefined ? {} : { blocker: row.blocker }),
      })),
      laneCounts,
      statusCounts,
      behindTotal: incomplete.reduce((sum, row) => sum + row.missingCount, 0),
      totalSeries: page.total,
      offset: page.offset,
      limit: page.limit,
      hasMore: page.hasMore,
    }
  }

  async detail(libraryId: string, seriesId: string, mode: EpisodeNumberingMode, signal: AbortSignal): Promise<SeriesStatus> {
    const emby = await this.embyClient(signal)
    const library = (await emby.libraries(signal)).find(item => item.id === libraryId)
    if (library === undefined) throw new EmbymediaError('NOT_FOUND', `Emby library ${libraryId} was not found`)
    const item = await emby.item(seriesId, 'ProviderIds,Path,Status', signal)
    if (item === undefined) throw new EmbymediaError('NOT_FOUND', `Emby series ${seriesId} was not found in library ${libraryId}`)
    const result = await this.statusForItem(item, library.id, library.name, mode, emby, await this.tmdbClient(signal), signal)
    if (result.lane === 'target_error' || result.folder === undefined || result.path === undefined) {
      throw new EmbymediaError('POLICY_DENIED', `Emby series ${seriesId} does not have a canonical path and TMDB binding in ${library.name}`)
    }
    if (result.tmdbId.length === 0) throw new EmbymediaError('POLICY_DENIED', `Emby series ${seriesId} has no TMDB id`)
    return result
  }

  async resourcePlan(libraryId: string, seriesId: string, mode: EpisodeNumberingMode, signal: AbortSignal): Promise<SeriesResourcePlan> {
    const series = await this.detail(libraryId, seriesId, mode, signal)
    if (series.gaps.length === 0) throw new EmbymediaError('CONFLICT', `Emby series ${seriesId} has no aired episode gaps`)
    const gapTotal = series.gaps.length
    const requestedEpisodes = series.gaps.slice(0, 20)
    const missing = requestedEpisodes.map((gap) => {
      if (mode === 'absolute' && gap.absolute !== undefined) return `E${String(gap.absolute).padStart(2, '0')}`
      return `S${String(gap.season).padStart(2, '0')}E${String(gap.episode).padStart(2, '0')}`
    }).join(' ')
    const resource = await this.resourceClient(signal)
    const options = { limit: 80, exact: false, diskType: '115' } as const
    let query = `${series.name} ${missing}`.trim()
    let search = await resource.search(query, options, signal)
    if (search.items.length === 0) {
      query = series.name
      search = await resource.search(query, options, signal)
    }
    return {
      series,
      numberingMode: mode,
      query,
      requestedEpisodes,
      gapTotal,
      deferredGapCount: gapTotal - requestedEpisodes.length,
      searchHasMore: search.hasMore,
      search,
    }
  }

  private async statusForItem(
    item: EmbyItem,
    libraryId: string,
    libraryName: string,
    mode: EpisodeNumberingMode,
    emby: EmbyClient,
    tmdb: TmdbClient,
    signal: AbortSignal,
  ): Promise<SeriesStatus> {
    const path = item.Path
    const folder = seriesFolderFromPath(path, libraryName)
    const base = {
      id: item.Id,
      libraryId,
      libraryName,
      name: item.Name,
      ...(path === undefined ? {} : { path }),
      ...(folder === undefined ? {} : { folder }),
    }
    const tmdbId = item.ProviderIds?.Tmdb
    if (folder === undefined) return { ...base, tmdbId: tmdbId ?? '', tmdbStatus: '', localCount: 0, missingCount: 0, lane: 'target_error', gaps: [], blocker: { code: 'POLICY_DENIED', message: 'Series path is outside the canonical library root' } }
    if (tmdbId === undefined) return { ...base, tmdbId: '', tmdbStatus: '', localCount: 0, missingCount: 0, lane: 'metadata_error', gaps: [], blocker: { code: 'NOT_FOUND', message: 'Series has no TMDB provider id' } }
    const folderTmdbId = declaredTmdbId(folder)
    if (folderTmdbId !== undefined && folderTmdbId !== tmdbId) {
      return { ...base, tmdbId, tmdbStatus: '', localCount: 0, missingCount: 0, lane: 'target_error', gaps: [], blocker: { code: 'CONFLICT', message: 'Series folder TMDB marker differs from Emby metadata' } }
    }
    try {
      const [metadata, episodes] = await Promise.all([tmdb.tv(tmdbId, signal), emby.episodes(item.Id, signal)])
      const expected = await this.expectedEpisodes(tmdb, metadata, signal)
      const gaps = computeEpisodeGaps(localEpisodes(episodes, expected, mode), expected, mode)
      const ended = ['Ended', 'Canceled'].includes(metadata.status ?? '')
      const lane = gaps.length > 0 ? ended ? 'complete_after_update' : 'update_needed' : ended ? 'archive_ready' : 'healthy_airing'
      return { ...base, tmdbId, tmdbStatus: metadata.status ?? '', localCount: episodes.length, missingCount: gaps.length, lane, gaps }
    } catch (error) {
      if (signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED')) throw error
      const blocker = error instanceof EmbymediaError
        ? { code: error.code, message: error.message }
        : { code: 'UPSTREAM_UNAVAILABLE', message: error instanceof Error ? error.message : String(error) }
      return { ...base, tmdbId, tmdbStatus: '', localCount: 0, missingCount: 0, lane: 'unknown', gaps: [], blocker }
    }
  }
  async archive(request: ArchiveRequest, signal: AbortSignal): Promise<ArchiveResult> {
    if (request.fromLibraryId === request.toLibraryId) {
      throw new EmbymediaError('INVALID_INPUT', 'archive source and destination libraries must differ')
    }
    const cloud = await this.archiveHooks.moveCloud(request, signal)
    const strm = await this.archiveHooks.moveStrm(request, signal)
    const emby = await this.archiveHooks.notifyEmby(request, signal)
    const verification = await this.archiveHooks.verify(request, signal)
    const verified = typeof verification === 'object' && verification !== null && !Array.isArray(verification)
      ? verification as Readonly<Record<string, JsonValue>>
      : undefined
    if (verified?.ok !== true) {
      throw new EmbymediaError('VERIFICATION_FAILED', 'series archive verification failed', verification)
    }
    return { cloud, strm, emby, verification }
  }

  private async expectedEpisodes(tmdb: TmdbClient, tv: TmdbTv, signal: AbortSignal): Promise<readonly ExpectedTmdbEpisode[]> {
    const seasons = (tv.seasons ?? []).filter(season => season.season_number > 0)
    const output: ExpectedTmdbEpisode[] = []
    let absolute = 0
    for (const season of seasons) {
      for (const episode of await tmdb.season(tv.id, season.season_number, signal)) {
        absolute++
        output.push({ ...episode, absolute_number: absolute })
      }
    }
    return output
  }
}

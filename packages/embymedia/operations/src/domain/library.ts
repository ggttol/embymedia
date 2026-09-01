import { randomUUID } from 'node:crypto'
import { lstat, readFile, readdir, realpath } from 'node:fs/promises'
import { posix, relative } from 'node:path'
import type { Database } from '../database/index.ts'
import type { EmbyClient, EmbyItem, EmbyLibrary } from '../clients/emby.ts'
import { abortableSleep } from '../cancellation.ts'
import { EmbymediaError } from '../errors.ts'
import { createStrmBatchWriter, embyLibraryPath, safeUnder, strmRelativePath } from '../media/paths.ts'
import { SCHEMA_VERSION, type JsonValue, type TaskRunProjection } from '../schemas.ts'

const VIDEO_EXTENSIONS: Readonly<Record<string, true>> = {
  '.3gp': true, '.avi': true, '.flv': true, '.iso': true, '.m2ts': true, '.m4v': true,
  '.mkv': true, '.mov': true, '.mp4': true, '.mpeg': true, '.mpg': true, '.mts': true,
  '.rmvb': true, '.ts': true, '.vob': true, '.webm': true, '.wmv': true,
}

/** Returns whether a filename has an extension supported by STRM generation. */
export function isVideoFileName(name: string): boolean {
  return VIDEO_EXTENSIONS[posix.extname(name).toLowerCase()] === true
}

export interface ScanRequest {
  readonly libraryId: string
  readonly libraryName: string
  readonly mediaFolder: string
  readonly top?: string
  readonly tops?: readonly string[]
  readonly outputFolder?: string
}

export interface StrmEntry {
  readonly relativePath: string
  readonly target: string
  readonly orphan: boolean
}

export interface LibraryContextItem {
  readonly libraryId: string
  readonly libraryName: string
  readonly id: string
  readonly name: string
  readonly type: string
  readonly path?: string
  readonly tmdbId?: string
}

export interface LibraryContextResult {
  readonly query: string
  readonly totalMatches: number
  readonly truncated: boolean
  readonly items: readonly LibraryContextItem[]
  readonly duplicateGroups: readonly { readonly key: string; readonly items: readonly LibraryContextItem[] }[]
}

export interface LibrarySummary {
  readonly id: string
  readonly name: string
  readonly collectionType?: string
  readonly itemTypes: string
  readonly total: number
  readonly locations: readonly string[]
}

interface TaskRow {
  readonly id: string
  readonly kind: string
  readonly label: string
  readonly status: TaskRunProjection['status']
  readonly progress: string
  readonly total: string
  readonly status_text: string
  readonly cancel_requested: boolean
  readonly correlation_id: string
  readonly queued_at: Date
  readonly updated_at: Date
  readonly started_at: Date | null
  readonly ended_at: Date | null
  readonly result: JsonValue | null
  readonly error: JsonValue | null
}

function taskProjection(row: TaskRow): TaskRunProjection {
  return {
    schemaVersion: SCHEMA_VERSION,
    id: row.id,
    kind: row.kind,
    label: row.label,
    status: row.status,
    progress: Number(row.progress),
    total: Number(row.total),
    statusText: row.status_text,
    cancelRequested: row.cancel_requested,
    correlationId: row.correlation_id,
    queuedAt: row.queued_at.toISOString(),
    updatedAt: row.updated_at.toISOString(),
    ...(row.started_at === null ? {} : { startedAt: row.started_at.toISOString() }),
    ...(row.ended_at === null ? {} : { endedAt: row.ended_at.toISOString() }),
    ...(row.result === null ? {} : { result: row.result }),
    ...(row.error === null ? {} : { error: row.error }),
  }
}

export class LibraryDomainService {
  constructor(
    private readonly database: Database,
    private readonly mediaRoot: string,
    private readonly strmRoot: string,
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly verifyAttempts = 60,
    private readonly verifyDelayMs = 2_000,
  ) {}

  async libraries(signal: AbortSignal): Promise<readonly EmbyLibrary[]> {
    return (await this.embyClient(signal)).libraries(signal)
  }
  async summary(signal: AbortSignal): Promise<readonly LibrarySummary[]> {
    const emby = await this.embyClient(signal)
    const libraries = await emby.libraries(signal)
    return Promise.all(libraries.map(async (library) => {
      const itemTypes = library.collectionType === 'movies'
        ? 'Movie'
        : library.collectionType === 'tvshows' ? 'Series' : 'Movie,Series'
      const total = (await emby.itemPage(library.id, itemTypes, '', 1, signal)).total
      return {
        id: library.id,
        name: library.name,
        ...(library.collectionType === undefined ? {} : { collectionType: library.collectionType }),
        itemTypes,
        total,
        locations: library.locations,
      }
    }))
  }


  async itemPage(libraryId: string, limit: number, signal: AbortSignal, search?: string, itemTypes = 'Movie,Series,Episode', offset = 0) {
    return (await this.embyClient(signal)).itemPage(libraryId, itemTypes, 'Path,ProviderIds', limit, signal, search, offset)
  }

  async searchItems(libraryId: string, query: string, limit: number, offset: number, signal: AbortSignal, itemTypes = 'Movie,Series,Episode') {
    const normalize = (value: string) => value.normalize('NFKC').toLocaleLowerCase('zh-CN').replace(/[\p{P}\p{S}\s]+/gu, '')
    const needle = normalize(query)
    if (needle.length === 0) throw new EmbymediaError('INVALID_INPUT', 'library item search requires text')
    const matches = (await (await this.embyClient(signal)).items(libraryId, itemTypes, 'Path,ProviderIds', signal))
      .filter(item => normalize(item.Name).includes(needle))
    return { items: matches.slice(offset, offset + limit), total: matches.length }
  }

  async itemCount(libraryId: string, signal: AbortSignal, search?: string, itemTypes = 'Movie'): Promise<number> {
    return (await this.embyClient(signal)).itemPage(libraryId, itemTypes, '', 1, signal, search).then(page => page.total)
  }

  async items(libraryId: string, limit: number, signal: AbortSignal, search?: string, itemTypes = 'Movie,Series,Episode'): Promise<readonly EmbyItem[]> {
    const emby = await this.embyClient(signal)
    const items = search === undefined || search.trim().length === 0
      ? await emby.items(libraryId, itemTypes, 'Path,ProviderIds', signal)
      : await emby.search(search, itemTypes, signal, libraryId)
    return items.slice(0, Math.min(Math.max(limit, 1), 1000))
  }

  async context(queryValue: string, libraryId: string | undefined, limit: number, signal: AbortSignal): Promise<LibraryContextResult> {
    const query = queryValue.trim()
    if (query.length === 0) throw new EmbymediaError('INVALID_INPUT', 'library context query is required')
    const safeLimit = Math.min(Math.max(limit, 1), 100)
    const libraries = (await this.libraries(signal)).filter(library => libraryId === undefined || library.id === libraryId)
    if (libraries.length === 0) throw new EmbymediaError('NOT_FOUND', 'target Emby library was not found')
    const items: LibraryContextItem[] = []
    let truncated = false
    for (const library of libraries) {
      const matches = await this.items(library.id, safeLimit + 1, signal, query)
      if (matches.length > safeLimit - items.length) truncated = true
      for (const match of matches.slice(0, Math.max(0, safeLimit - items.length))) {
        items.push({
          libraryId: library.id,
          libraryName: library.name,
          id: match.Id,
          name: match.Name,
          type: match.Type ?? 'Unknown',
          ...(match.Path === undefined ? {} : { path: match.Path }),
          ...(match.ProviderIds?.Tmdb === undefined ? {} : { tmdbId: match.ProviderIds.Tmdb }),
        })
      }
      if (items.length >= safeLimit) break
    }
    const groups = new Map<string, LibraryContextItem[]>()
    for (const item of items) {
      const key = item.tmdbId === undefined ? `name:${item.name.normalize('NFC').toLocaleLowerCase()}` : `tmdb:${item.tmdbId}`
      groups.set(key, [...(groups.get(key) ?? []), item])
    }
    return {
      query,
      totalMatches: items.length,
      truncated,
      items,
      duplicateGroups: [...groups].filter(([, group]) => group.length > 1).map(([key, group]) => ({ key, items: group })),
    }
  }

  async listStrm(library: string | undefined, limit: number, signal: AbortSignal): Promise<readonly StrmEntry[]> {
    const canonicalRoot = await realpath(this.strmRoot)
    const base = library === undefined ? canonicalRoot : await safeUnder(this.strmRoot, library)
    const files = await this.walk(base, signal, '.strm', Math.min(Math.max(limit, 1), 5000))
    const entries: StrmEntry[] = []
    for (const file of files) {
      signal.throwIfAborted()
      const target = (await readFile(file, 'utf8')).trim()
      let orphan = true
      if (target.startsWith('/media/')) {
        const mediaRelative = target.slice('/media/'.length)
        try {
          await lstat(await safeUnder(this.mediaRoot, mediaRelative))
          orphan = false
        } catch {
          orphan = true
        }
      }
      entries.push({ relativePath: relative(canonicalRoot, file).split(posix.sep).join('/'), target, orphan })
    }
    return entries
  }

  async scan(request: ScanRequest, signal: AbortSignal): Promise<TaskRunProjection> {
    const id = randomUUID()
    const correlationId = randomUUID()
    const selectedTops = request.tops ?? (request.top === undefined ? undefined : [request.top])
    const selected = selectedTops?.length === 1 ? selectedTops[0] : undefined
    const label = `scan ${request.libraryName}${selected === undefined ? selectedTops === undefined ? '' : `/${String(selectedTops.length)} roots` : `/${selected}`}`
    await this.database.query(
      `INSERT INTO task_runs(id,kind,label,source,params,status,total,correlation_id)
       VALUES ($1,'library.scan',$2,'interactive',$3,'queued',0,$4)`,
      [id, label, JSON.stringify(request), correlationId],
    )
    try {
      await this.database.query("UPDATE task_runs SET status='running',started_at=now(),updated_at=now(),status_text='enumerating media' WHERE id=$1", [id])
      const libraryMediaRoot = await safeUnder(this.mediaRoot, request.mediaFolder)
      const mediaBases = selectedTops === undefined
        ? [libraryMediaRoot]
        : await Promise.all(selectedTops.map(top => safeUnder(this.mediaRoot, posix.join(request.mediaFolder, top))))
      const files: string[] = []
      for (const mediaBase of mediaBases) {
        const mediaBaseStat = await lstat(mediaBase)
        files.push(...(mediaBaseStat.isFile() ? [mediaBase] : await this.walk(mediaBase, signal, undefined, 100_000 - files.length)))
      }
      const videos = files.filter(isVideoFileName)
      await this.database.query('UPDATE task_runs SET total=$2,updated_at=now() WHERE id=$1', [id, videos.length])
      const writeStrm = await createStrmBatchWriter(this.strmRoot, request.libraryName)
      let generated = 0
      for (let index = 0; index < videos.length; index++) {
        signal.throwIfAborted()
        const file = videos[index]!
        const relativeMedia = relative(libraryMediaRoot, file).split(posix.sep).join('/')
        try {
          const outputPath = request.outputFolder === undefined ? undefined : posix.join(request.outputFolder, strmRelativePath(posix.basename(relativeMedia)))
          await writeStrm(relativeMedia, request.mediaFolder, outputPath)
          generated++
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
        }
        if ((index + 1) % 100 === 0 || index === videos.length - 1) {
          await this.database.query(
            "UPDATE task_runs SET progress=$2,updated_at=now(),status_text='generating STRM' WHERE id=$1",
            [id, index + 1],
          )
        }
      }
      const emby = await this.embyClient(signal)
      await emby.refreshLibrary(signal)
      await this.database.query("UPDATE task_runs SET status='verifying',updated_at=now(),status_text='verifying Emby visibility' WHERE id=$1", [id])
      const expected = new Set(videos.map((file) => {
        const relativeMedia = relative(libraryMediaRoot, file).split(posix.sep).join('/')
        const outputPath = request.outputFolder === undefined ? strmRelativePath(relativeMedia) : posix.join(request.outputFolder, strmRelativePath(posix.basename(relativeMedia)))
        return posix.join(embyLibraryPath(request.libraryName), outputPath)
      }))
      let visible = 0
      const targetedPaths = request.outputFolder !== undefined
        ? [posix.join(embyLibraryPath(request.libraryName), request.outputFolder)]
        : selectedTops === undefined ? undefined : selectedTops.flatMap(top => [
          posix.join(embyLibraryPath(request.libraryName), top),
          posix.join(embyLibraryPath(request.libraryName), strmRelativePath(top)),
        ])
      for (let attempt = 0; attempt < this.verifyAttempts; attempt++) {
        signal.throwIfAborted()
        let embyItems = targetedPaths === undefined
          ? await emby.items(request.libraryId, 'Movie,Series,Episode', 'Path', signal)
          : (await Promise.all([...new Set(targetedPaths)].map(path => emby.itemsByPath(request.libraryId, path, 'Movie,Series,Episode', 'Path', signal)))).flat()
        visible = new Set(embyItems.flatMap(item => typeof item.Path === 'string' && expected.has(item.Path) ? [item.Path] : [])).size
        if (targetedPaths !== undefined && visible < expected.size) {
          embyItems = await emby.items(request.libraryId, 'Movie,Series,Episode', 'Path', signal)
          visible = new Set(embyItems.flatMap(item => typeof item.Path === 'string' && expected.has(item.Path) ? [item.Path] : [])).size
        }
        if (visible >= expected.size) break
        if (attempt < this.verifyAttempts - 1) await abortableSleep(this.verifyDelayMs, signal)
      }
      const status = visible >= expected.size ? 'done' : 'partial'
      const result = { generated, expected: expected.size, visible }
      const completed = await this.database.query<TaskRow>(
        'UPDATE task_runs SET status=$2,ended_at=now(),updated_at=now(),status_text=$3,result=$4 WHERE id=$1 RETURNING *',
        [id, status, status === 'done' ? 'scan verified' : 'scan visibility incomplete', JSON.stringify(result)],
      )
      return taskProjection(completed.rows[0]!)
    } catch (error) {
      const cancelled = signal.aborted || (error instanceof EmbymediaError && error.code === 'CANCELLED')
      const failed = await this.database.query<TaskRow>(
        'UPDATE task_runs SET status=$2,ended_at=now(),updated_at=now(),status_text=$3,error=$4 WHERE id=$1 RETURNING *',
        [id, cancelled ? 'cancelled' : 'error', cancelled ? 'scan cancelled' : 'scan failed', JSON.stringify({ message: error instanceof Error ? error.message : String(error) })],
      )
      return taskProjection(failed.rows[0]!)
    }
  }

  private async walk(root: string, signal: AbortSignal, extension: string | undefined, limit: number): Promise<readonly string[]> {
    const output: string[] = []
    const stack = [root]
    while (stack.length > 0) {
      signal.throwIfAborted()
      const directory = stack.pop()!
      for (const entry of await readdir(directory, { withFileTypes: true })) {
        const path = posix.join(directory, entry.name)
        if (entry.isSymbolicLink()) throw new EmbymediaError('POLICY_DENIED', 'symlink found during media traversal')
        if (entry.isDirectory()) stack.push(path)
        else if (entry.isFile() && (extension === undefined || posix.extname(entry.name).toLowerCase() === extension)) output.push(path)
        if (output.length > limit) throw new EmbymediaError('POLICY_DENIED', 'filesystem traversal exceeded item limit')
      }
    }
    return output.sort()
  }
}

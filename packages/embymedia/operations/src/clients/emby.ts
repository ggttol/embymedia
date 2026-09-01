import { randomUUID } from 'node:crypto'
import { EmbymediaError } from '../errors.ts'

export interface EmbyClientOptions {
  readonly baseUrl: string
  readonly token: string
  readonly connectTimeoutMs?: number
  readonly totalTimeoutMs?: number
  readonly pageSize?: number
  readonly maxPages?: number
  readonly maxItems?: number
  readonly fetch?: typeof globalThis.fetch
}

export interface EmbyVirtualFolder {
  readonly Name?: string
  readonly ItemId?: string
  readonly CollectionType?: string
  readonly Locations?: readonly string[]
  readonly LibraryOptions?: Readonly<Record<string, unknown>>
}

export interface EmbyLibrary {
  readonly id: string
  readonly name: string
  readonly collectionType?: string
  readonly locations: readonly string[]
  readonly libraryOptions?: Readonly<Record<string, unknown>>
}

export interface EmbyItem {
  readonly Id: string
  readonly Name: string
  readonly Type?: string
  readonly Path?: string
  readonly ProductionYear?: number
  readonly ProviderIds?: Readonly<Record<string, string>>
  readonly ParentId?: string
  readonly SeriesId?: string
  readonly SeriesName?: string
  readonly IndexNumber?: number
  readonly ParentIndexNumber?: number
  readonly UserData?: Readonly<Record<string, unknown>>
  readonly [key: string]: unknown
}

export interface EmbyItemsPage {
  readonly Items: readonly EmbyItem[]
  readonly TotalRecordCount: number
}

export interface EmbyItemPageResult {
  readonly items: readonly EmbyItem[]
  readonly total: number
}

export interface EmbyRemoteSearchCandidate {
  readonly Name?: string
  readonly ProductionYear?: number
  readonly ProviderIds?: Readonly<Record<string, string>>
  readonly ImageUrl?: string
  readonly SearchProviderName?: string
}

export interface EmbyUserPolicy {
  readonly IsAdministrator?: boolean
  readonly IsDisabled?: boolean
  readonly RemoteClientBitrateLimit?: number
  readonly SimultaneousStreamLimit?: number
  readonly [key: string]: unknown
}

export interface EmbyUser {
  readonly Id: string
  readonly Name: string
  readonly LastActivityDate?: string
  readonly Policy?: EmbyUserPolicy
}

type HttpMethod = 'GET' | 'POST' | 'DELETE'

function nonEmpty(value: string, label: string): string {
  const trimmed = value.trim()
  if (trimmed.length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return trimmed
}

function statusError(status: number, correlationId: string): EmbymediaError {
  const detail = { upstream: 'emby', status, correlationId }
  if (status === 401 || status === 403) return new EmbymediaError('AUTH_REQUIRED', 'Emby authentication failed', detail, correlationId)
  if (status === 404) return new EmbymediaError('NOT_FOUND', 'Emby resource not found', detail, correlationId)
  if (status === 409) return new EmbymediaError('CONFLICT', 'Emby reported a conflict', detail, correlationId)
  if (status === 429) return new EmbymediaError('RATE_LIMITED', 'Emby rate limit reached', detail, correlationId)
  return new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby request failed', detail, correlationId)
}

function assertObject(value: unknown, label: string, correlationId: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `Emby returned malformed ${label}`, { upstream: 'emby', correlationId }, correlationId)
  }
  return value as Record<string, unknown>
}

function item(value: unknown, correlationId: string): EmbyItem {
  const object = assertObject(value, 'item', correlationId)
  if (typeof object.Id !== 'string' || typeof object.Name !== 'string') {
    throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed item', { upstream: 'emby', correlationId }, correlationId)
  }
  return object as EmbyItem
}

export class EmbyClient {
  private readonly baseUrl: string
  private readonly token: string
  private readonly connectTimeoutMs: number
  private readonly totalTimeoutMs: number
  private readonly pageSize: number
  private readonly maxPages: number
  private readonly maxItems: number
  private readonly fetchImpl: typeof globalThis.fetch

  constructor(options: EmbyClientOptions) {
    this.baseUrl = nonEmpty(options.baseUrl, 'Emby base URL').replace(/\/$/, '')
    const parsed = new URL(this.baseUrl)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') throw new EmbymediaError('INVALID_INPUT', 'Emby base URL must use HTTP or HTTPS')
    this.token = nonEmpty(options.token, 'Emby token')
    this.connectTimeoutMs = options.connectTimeoutMs ?? 10_000
    this.totalTimeoutMs = options.totalTimeoutMs ?? 45_000
    this.pageSize = options.pageSize ?? 200
    this.maxPages = options.maxPages ?? 100
    this.maxItems = options.maxItems ?? 100_000
    this.fetchImpl = options.fetch ?? globalThis.fetch
    if (this.connectTimeoutMs <= 0 || this.totalTimeoutMs < this.connectTimeoutMs) {
      throw new EmbymediaError('INVALID_INPUT', 'Emby timeouts are invalid')
    }
  }

  private async request(
    method: HttpMethod,
    path: string,
    signal: AbortSignal,
    options: { readonly query?: Readonly<Record<string, string | number | boolean | undefined>>; readonly body?: unknown } = {},
  ): Promise<{ readonly response: Response; readonly correlationId: string; readonly done: () => void }> {
    signal.throwIfAborted()
    const correlationId = randomUUID()
    const url = new URL(`${this.baseUrl}${path}`)
    for (const [key, value] of Object.entries(options.query ?? {})) {
      if (key.toLowerCase() === 'api_key') throw new EmbymediaError('POLICY_DENIED', 'Emby token query parameters are forbidden')
      if (value !== undefined) url.searchParams.set(key, String(value))
    }
    if (url.searchParams.has('api_key')) throw new EmbymediaError('POLICY_DENIED', 'Emby token query parameters are forbidden')
    const controller = new AbortController()
    const combined = AbortSignal.any([signal, controller.signal])
    const connectTimer = setTimeout(() =>{  controller.abort(new Error('Emby connect timeout')) }, this.connectTimeoutMs)
    const totalTimer = setTimeout(() =>{  controller.abort(new Error('Emby total timeout')) }, this.totalTimeoutMs)
    try {
      const response = await this.fetchImpl(url, {
        method,
        signal: combined,
        headers: {
          accept: 'application/json',
          'x-emby-token': this.token,
          ...(options.body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
      })
      clearTimeout(connectTimer)
      if (!response.ok) throw statusError(response.status, correlationId)
      return { response, correlationId, done: () => { clearTimeout(totalTimer) } }
    } catch (error) {
      clearTimeout(totalTimer)
      if (error instanceof EmbymediaError) throw error
      if (signal.aborted) throw new EmbymediaError('CANCELLED', 'Emby request cancelled', { upstream: 'emby', correlationId }, correlationId)
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby request timed out or failed', { upstream: 'emby', correlationId }, correlationId)
    } finally {
      clearTimeout(connectTimer)
      // The total timer remains active while a successful response body is consumed.
    }
  }

  private async json(method: HttpMethod, path: string, signal: AbortSignal, options?: Parameters<EmbyClient['request']>[3]): Promise<{ value: unknown; correlationId: string }> {
    const { response, correlationId, done } = await this.request(method, path, signal, options)
    try {
      const value: unknown = await response.json()
      return { value, correlationId }
    } catch {
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed JSON', { upstream: 'emby', correlationId }, correlationId)
    } finally {
      done()
    }
  }

  private async status(method: Exclude<HttpMethod, 'GET'>, path: string, signal: AbortSignal, options?: Parameters<EmbyClient['request']>[3]): Promise<void> {
    const { done } = await this.request(method, path, signal, options)
    done()
  }

  async virtualFolders(signal: AbortSignal): Promise<readonly EmbyVirtualFolder[]> {
    const { value, correlationId } = await this.json('GET', '/Library/VirtualFolders', signal)
    if (!Array.isArray(value) || value.some(folder => typeof folder !== 'object' || folder === null || Array.isArray(folder))) {
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed virtual folders', { upstream: 'emby', correlationId }, correlationId)
    }
    return value as EmbyVirtualFolder[]
  }

  async libraries(signal: AbortSignal): Promise<readonly EmbyLibrary[]> {
    return (await this.virtualFolders(signal)).map(folder => ({
      id: nonEmpty(folder.ItemId ?? '', 'Emby library id'),
      name: nonEmpty(folder.Name ?? '', 'Emby library name'),
      ...(folder.CollectionType === undefined ? {} : { collectionType: folder.CollectionType }),
      locations: folder.Locations ?? [],
      ...(folder.LibraryOptions === undefined ? {} : { libraryOptions: folder.LibraryOptions }),
    }))
  }

  async itemPage(
    parentId: string,
    itemTypes: string,
    fields: string,
    limit: number,
    signal: AbortSignal,
    search?: string,
    offset = 0,
  ): Promise<EmbyItemPageResult> {
    nonEmpty(parentId, 'parent id')
    if (!Number.isInteger(limit) || limit < 1 || limit > 500) throw new EmbymediaError('INVALID_INPUT', 'item page limit must be between 1 and 500')
    if (!Number.isInteger(offset) || offset < 0) throw new EmbymediaError('INVALID_INPUT', 'item page offset must be a non-negative integer')
    const { value, correlationId } = await this.json('GET', '/Items', signal, {
      query: {
        ParentId: parentId,
        IncludeItemTypes: itemTypes,
        Recursive: true,
        Fields: fields,
        StartIndex: offset,
        Limit: limit,
        ...(search === undefined || search.trim().length === 0 ? {} : { SearchTerm: search.trim() }),
      },
    })
    const object = assertObject(value, 'items page', correlationId)
    if (!Array.isArray(object.Items) || typeof object.TotalRecordCount !== 'number') {
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed items page', { upstream: 'emby', correlationId }, correlationId)
    }
    return { items: object.Items.map(entry => item(entry, correlationId)), total: object.TotalRecordCount }
  }

  async itemsByPath(parentId: string, path: string, itemTypes: string, fields: string, signal: AbortSignal): Promise<readonly EmbyItem[]> {
    nonEmpty(parentId, 'parent id')
    nonEmpty(path, 'item path')
    const output: EmbyItem[] = []
    for (let page = 0; page < this.maxPages; page++) {
      const { value, correlationId } = await this.json('GET', '/Items', signal, {
        query: { ParentId: parentId, Path: path, IncludeItemTypes: itemTypes, Recursive: true, Fields: fields, StartIndex: page * this.pageSize, Limit: this.pageSize },
      })
      const object = assertObject(value, 'items page', correlationId)
      if (!Array.isArray(object.Items) || typeof object.TotalRecordCount !== 'number') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed items page', { upstream: 'emby', correlationId }, correlationId)
      output.push(...object.Items.map(entry => item(entry, correlationId)))
      if (output.length >= object.TotalRecordCount || object.Items.length === 0) return output
    }
    throw new EmbymediaError('POLICY_DENIED', 'Emby path query exceeded pagination limit', { path })
  }

  async items(
    parentId: string,
    itemTypes: string,
    fields: string,
    signal: AbortSignal,
  ): Promise<readonly EmbyItem[]> {
    nonEmpty(parentId, 'parent id')
    const output: EmbyItem[] = []
    for (let page = 0; page < this.maxPages; page++) {
      const { value, correlationId } = await this.json('GET', '/Items', signal, {
        query: {
          ParentId: parentId,
          IncludeItemTypes: itemTypes,
          Recursive: true,
          Fields: fields,
          StartIndex: page * this.pageSize,
          Limit: this.pageSize,
        },
      })
      const object = assertObject(value, 'items page', correlationId)
      if (!Array.isArray(object.Items) || typeof object.TotalRecordCount !== 'number') {
        throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed items page', { upstream: 'emby', correlationId }, correlationId)
      }
      output.push(...object.Items.map(entry => item(entry, correlationId)))
      if (output.length > this.maxItems) throw new EmbymediaError('POLICY_DENIED', 'Emby pagination exceeded item limit')
      if (output.length >= object.TotalRecordCount || object.Items.length === 0) return output
    }
    throw new EmbymediaError('POLICY_DENIED', 'Emby pagination exceeded page limit')
  }

  async episodes(seriesId: string, signal: AbortSignal): Promise<readonly EmbyItem[]> {
    nonEmpty(seriesId, 'series id')
    const { value, correlationId } = await this.json('GET', `/Shows/${encodeURIComponent(seriesId)}/Episodes`, signal, {
      query: { Fields: 'Path,ProviderIds,ParentIndexNumber,IndexNumber', Limit: this.maxItems },
    })
    const object = assertObject(value, 'episodes page', correlationId)
    if (!Array.isArray(object.Items)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed episodes', { upstream: 'emby', correlationId }, correlationId)
    if (object.Items.length > this.maxItems) throw new EmbymediaError('POLICY_DENIED', 'Emby episodes exceeded item limit')
    return object.Items.map(entry => item(entry, correlationId))
  }

  async search(term: string, itemTypes: string, signal: AbortSignal, parentId?: string): Promise<readonly EmbyItem[]> {
    const query = nonEmpty(term, 'search term')
    const { value, correlationId } = await this.json('GET', '/Items', signal, {
      query: { SearchTerm: query, IncludeItemTypes: itemTypes, Recursive: true, Fields: 'Path,ProviderIds', Limit: this.pageSize, ...(parentId === undefined ? {} : { ParentId: nonEmpty(parentId, 'parent id') }) },
    })
    const object = assertObject(value, 'search page', correlationId)
    if (!Array.isArray(object.Items)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed search page', { upstream: 'emby', correlationId }, correlationId)
    return object.Items.map(entry => item(entry, correlationId))
  }

  async item(itemId: string, fields: string, signal: AbortSignal): Promise<EmbyItem | undefined> {
    const id = nonEmpty(itemId, 'item id')
    const { value, correlationId } = await this.json('GET', '/Items', signal, {
      query: { Ids: id, Fields: fields, Limit: 1 },
    })
    const object = assertObject(value, 'item lookup', correlationId)
    if (!Array.isArray(object.Items)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed item lookup', { upstream: 'emby', correlationId }, correlationId)
    const first = object.Items[0]
    return first === undefined ? undefined : item(first, correlationId)
  }

  async createVirtualFolder(name: string, collectionType: string, libraryOptions: Readonly<Record<string, unknown>>, signal: AbortSignal): Promise<void> {
    await this.status('POST', '/Library/VirtualFolders', signal, {
      query: { name: nonEmpty(name, 'library name'), collectionType: nonEmpty(collectionType, 'collection type'), refreshLibrary: false },
      body: { LibraryOptions: libraryOptions },
    })
  }

  async refreshLibrary(signal: AbortSignal): Promise<void> {
    await this.status('POST', '/Library/Refresh', signal)
  }

  async refreshItem(itemId: string, recursive: boolean, signal: AbortSignal): Promise<void> {
    await this.status('POST', `/Items/${encodeURIComponent(nonEmpty(itemId, 'item id'))}/Refresh`, signal, {
      query: { Recursive: recursive, MetadataRefreshMode: 'FullRefresh', ImageRefreshMode: 'FullRefresh', ReplaceAllMetadata: false, ReplaceAllImages: false },
    })
  }

  async deleteItem(itemId: string, signal: AbortSignal): Promise<void> {
    await this.status('DELETE', `/Items/${encodeURIComponent(nonEmpty(itemId, 'item id'))}`, signal)
  }

  async notifyMediaUpdated(updates: readonly { readonly Path: string; readonly UpdateType: 'Created' | 'Modified' | 'Deleted' }[], signal: AbortSignal): Promise<void> {
    if (updates.length === 0) throw new EmbymediaError('INVALID_INPUT', 'at least one media update is required')
    await this.status('POST', '/Library/Media/Updated', signal, { body: { Updates: updates } })
  }

  async remoteSearch(itemId: string, name: string, year: number | undefined, itemType: 'Movie' | 'Series', signal: AbortSignal): Promise<readonly EmbyRemoteSearchCandidate[]> {
    const { value, correlationId } = await this.json('POST', `/Items/RemoteSearch/${itemType}`, signal, {
      body: { ItemId: nonEmpty(itemId, 'item id'), SearchInfo: { Name: nonEmpty(name, 'name'), ...(year === undefined ? {} : { Year: year }) } },
    })
    if (!Array.isArray(value)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed remote search', { upstream: 'emby', correlationId }, correlationId)
    return value.map(candidate => assertObject(candidate, 'remote search candidate', correlationId) as EmbyRemoteSearchCandidate)
  }

  async applyRemoteSearch(itemId: string, tmdbId: string, signal: AbortSignal): Promise<void> {
    await this.status('POST', `/Items/RemoteSearch/Apply/${encodeURIComponent(nonEmpty(itemId, 'item id'))}`, signal, {
      query: { ReplaceAllImages: true },
      body: { ProviderIds: { Tmdb: nonEmpty(tmdbId, 'TMDB id') } },
    })
  }

  async users(signal: AbortSignal): Promise<readonly EmbyUser[]> {
    const { value, correlationId } = await this.json('GET', '/Users', signal)
    if (!Array.isArray(value)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed users', { upstream: 'emby', correlationId }, correlationId)
    return value.map((user) => {
      const object = assertObject(user, 'user', correlationId)
      if (typeof object.Id !== 'string' || typeof object.Name !== 'string') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed user', { upstream: 'emby', correlationId }, correlationId)
      return object as unknown as EmbyUser
    })
  }

  async updateUserPolicy(userId: string, policy: EmbyUserPolicy, signal: AbortSignal): Promise<void> {
    await this.status('POST', `/Users/${encodeURIComponent(nonEmpty(userId, 'user id'))}/Policy`, signal, { body: policy })
  }

  async createUser(name: string, password: string | undefined, signal: AbortSignal): Promise<EmbyUser> {
    const { value, correlationId } = await this.json('POST', '/Users/New', signal, { body: { Name: nonEmpty(name, 'user name') } })
    const object = assertObject(value, 'created user', correlationId)
    if (typeof object.Id !== 'string' || typeof object.Name !== 'string') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'Emby returned malformed created user', { upstream: 'emby', correlationId }, correlationId)
    if (password !== undefined) await this.setUserPassword(object.Id, password, signal)
    return object as unknown as EmbyUser
  }

  async setUserPassword(userId: string, password: string, signal: AbortSignal): Promise<void> {
    await this.status('POST', `/Users/${encodeURIComponent(nonEmpty(userId, 'user id'))}/Password`, signal, {
      body: { Id: userId, NewPw: password, ResetPassword: false },
    })
  }

  async deleteUser(userId: string, signal: AbortSignal): Promise<void> {
    await this.status('DELETE', `/Users/${encodeURIComponent(nonEmpty(userId, 'user id'))}`, signal)
  }
}

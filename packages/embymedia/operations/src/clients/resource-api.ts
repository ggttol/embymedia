import { EmbymediaError } from '../errors.ts'
import { ProxyHttpTransport, type ProxyHttpTransportOptions } from './http.ts'

export interface ResourceApiOptions extends ProxyHttpTransportOptions {
  readonly baseUrl: string
  readonly token?: string
  readonly allowInsecureHttp?: boolean
}

export interface ResourceSearchItem {
  readonly title: string
  readonly type?: string
  readonly diskType?: string
  readonly url: string
  readonly password?: string
  readonly source?: string
  readonly sourceChannels: readonly string[]
}

export interface ResourceSearchResult {
  readonly items: readonly ResourceSearchItem[]
  readonly total: number
  readonly limit: number
  readonly offset: number
  readonly hasMore: boolean
  readonly query: string
  readonly exact: boolean
  readonly sort: string
  readonly diskTypes: readonly { readonly diskType: string; readonly count: number }[]
}

function required(value: string, label: string): string {
  const trimmed = value.trim()
  if (trimmed.length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return trimmed
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `resource API returned malformed ${label}`)
  return value as Record<string, unknown>
}

export class ResourceApiClient {
  private readonly baseUrl: string
  private readonly token: string | undefined
  private readonly transport: ProxyHttpTransport

  constructor(options: ResourceApiOptions) {
    this.baseUrl = required(options.baseUrl, 'resource API base URL').replace(/\/$/, '')
    const endpoint = new URL(this.baseUrl)
    if (endpoint.protocol !== 'http:' && endpoint.protocol !== 'https:') throw new EmbymediaError('INVALID_INPUT', 'resource API base URL must use HTTP or HTTPS')
    this.token = options.token?.trim() || undefined
    const loopback = endpoint.hostname === 'localhost' || endpoint.hostname === '::1' || /^127(?:\.\d{1,3}){3}$/.test(endpoint.hostname)
    if (this.token !== undefined && endpoint.protocol !== 'https:' && !loopback && options.allowInsecureHttp !== true) {
      throw new EmbymediaError('POLICY_DENIED', 'credentialed resource API requires HTTPS or an explicit insecure-HTTP deployment override')
    }
    this.transport = new ProxyHttpTransport(options)
  }

  get proxied(): boolean {
    return this.transport.proxied
  }

  async search(
    query: string,
    options: { readonly limit?: number; readonly offset?: number; readonly exact?: boolean; readonly sort?: string; readonly diskType?: string },
    signal: AbortSignal,
  ): Promise<ResourceSearchResult> {
    const limit = Math.min(Math.max(options.limit ?? 80, 1), 500)
    const offset = Math.max(options.offset ?? 0, 0)
    const url = new URL(`${this.baseUrl}/api/v1/search`)
    url.searchParams.set('kw', required(query, 'resource query'))
    url.searchParams.set('limit', String(limit))
    url.searchParams.set('offset', String(offset))
    url.searchParams.set('sort', options.sort?.trim() || 'relevance')
    if (options.exact === true) url.searchParams.set('exact', 'true')
    if (options.diskType?.trim()) url.searchParams.set('disk_type', options.diskType.trim())
    const response = await this.transport.request(url, signal, {
      method: 'GET',
      headers: { accept: 'application/json', ...(this.token === undefined ? {} : { authorization: `Bearer ${this.token}` }) },
    })
    if (response.status === 401 || response.status === 403) throw new EmbymediaError('AUTH_REQUIRED', 'resource API authentication failed', { status: response.status })
    if (response.status === 404) throw new EmbymediaError('NOT_FOUND', 'resource API endpoint not found', { status: response.status })
    if (response.status === 429) throw new EmbymediaError('RATE_LIMITED', 'resource API rate limit reached', { status: response.status })
    if (!response.ok) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'resource API request failed', { status: response.status })
    let envelope: Record<string, unknown>
    try {
      envelope = object(await response.json(), 'response')
    } catch (error) {
      if (error instanceof EmbymediaError) throw error
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'resource API returned malformed JSON')
    }
    if (envelope.code !== 0) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'resource API rejected search', { code: typeof envelope.code === 'number' ? envelope.code : -1 })
    const data = object(envelope.data ?? {}, 'data')
    const results = Array.isArray(data.results) ? data.results : []
    const items = results.flatMap((result) => {
      const row = object(result, 'result')
      const title = typeof row.title === 'string' ? row.title.trim() : ''
      const resourceUrl = typeof row.url === 'string' ? row.url.trim() : ''
      if (title.length === 0 || resourceUrl.length === 0) return []
      return [{
        title,
        ...(typeof row.type === 'string' ? { type: row.type } : {}),
        ...(typeof row.disk_type === 'string' ? { diskType: row.disk_type } : {}),
        url: resourceUrl,
        ...(typeof row.password === 'string' && row.password.length > 0 ? { password: row.password } : {}),
        ...(typeof row.source === 'string' ? { source: row.source } : {}),
        sourceChannels: Array.isArray(row.source_channels) ? row.source_channels.filter(value => typeof value === 'string') : [],
      }]
    })
    const diskTypes = (Array.isArray(data.disk_types) ? data.disk_types : []).flatMap((entry) => {
      const row = object(entry, 'disk type')
      return typeof row.disk_type === 'string' && typeof row.count === 'number'
        ? [{ diskType: row.disk_type, count: row.count }]
        : []
    })
    return {
      items,
      total: typeof data.total === 'number' ? data.total : items.length,
      limit: typeof data.limit === 'number' && data.limit > 0 ? data.limit : limit,
      offset: typeof data.offset === 'number' ? Math.max(data.offset, offset) : offset,
      hasMore: data.has_more === true,
      query: typeof data.query === 'string' && data.query.trim().length > 0 ? data.query : query.trim(),
      exact: data.exact === true,
      sort: typeof data.sort === 'string' && data.sort.trim().length > 0 ? data.sort : options.sort?.trim() || 'relevance',
      diskTypes,
    }
  }
}

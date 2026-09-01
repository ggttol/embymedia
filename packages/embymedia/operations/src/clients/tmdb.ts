import { EmbymediaError } from '../errors.ts'
import { ProxyHttpTransport, type ProxyHttpTransportOptions } from './http.ts'

export interface TmdbClientOptions extends ProxyHttpTransportOptions {
  readonly baseUrl?: string
  readonly apiKey: string
  readonly language?: string
}

export interface TmdbEpisode {
  readonly air_date?: string
  readonly episode_number: number
  readonly name?: string
  readonly season_number: number
  readonly id: number
}

export interface TmdbTv {
  readonly id: number
  readonly name: string
  readonly original_name?: string
  readonly status?: string
  readonly first_air_date?: string
  readonly last_air_date?: string
  readonly number_of_episodes?: number
  readonly number_of_seasons?: number
  readonly poster_path?: string
  readonly last_episode_to_air?: TmdbEpisode
  readonly next_episode_to_air?: TmdbEpisode
  readonly seasons?: readonly { readonly season_number: number; readonly episode_count: number; readonly air_date?: string; readonly name?: string }[]
}

export interface TmdbSearchResult {
  readonly id: number
  readonly media_type?: 'movie' | 'tv' | 'person'
  readonly title?: string
  readonly name?: string
  readonly original_title?: string
  readonly original_name?: string
  readonly release_date?: string
  readonly first_air_date?: string
  readonly poster_path?: string
  readonly popularity?: number
}

function required(value: string, label: string): string {
  const trimmed = value.trim()
  if (trimmed.length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return trimmed
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `TMDB returned malformed ${label}`)
  return value as Record<string, unknown>
}

export class TmdbClient {
  private readonly baseUrl: string
  private readonly apiKey: string
  private readonly language: string
  private readonly transport: ProxyHttpTransport

  constructor(options: TmdbClientOptions) {
    this.baseUrl = (options.baseUrl ?? 'https://api.themoviedb.org').replace(/\/$/, '')
    this.apiKey = required(options.apiKey, 'TMDB API key')
    this.language = options.language ?? 'zh-CN'
    const parsed = new URL(this.baseUrl)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') throw new EmbymediaError('INVALID_INPUT', 'TMDB base URL must use HTTP or HTTPS')
    this.transport = new ProxyHttpTransport(options)
  }

  get proxied(): boolean {
    return this.transport.proxied
  }

  private async get(path: string, signal: AbortSignal, query: Readonly<Record<string, string | number | undefined>> = {}): Promise<Record<string, unknown>> {
    const prefix = this.baseUrl.endsWith('/3') ? '' : '/3'
    const url = new URL(`${this.baseUrl}${prefix}${path}`)
    url.searchParams.set('api_key', this.apiKey)
    url.searchParams.set('language', this.language)
    for (const [key, value] of Object.entries(query)) if (value !== undefined) url.searchParams.set(key, String(value))
    const response = await this.transport.request(url, signal, { method: 'GET', headers: { accept: 'application/json' } })
    if (response.status === 401 || response.status === 403) throw new EmbymediaError('AUTH_REQUIRED', 'TMDB authentication failed', { status: response.status })
    if (response.status === 404) throw new EmbymediaError('NOT_FOUND', 'TMDB resource not found', { status: response.status })
    if (response.status === 429) throw new EmbymediaError('RATE_LIMITED', 'TMDB rate limit reached', { status: response.status })
    if (!response.ok) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB request failed', { status: response.status })
    try {
      return object(await response.json(), 'JSON')
    } catch (error) {
      if (error instanceof EmbymediaError) throw error
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed JSON')
    }
  }

  configuration(signal: AbortSignal): Promise<Record<string, unknown>> {
    return this.get('/configuration', signal)
  }

  async tv(id: string | number, signal: AbortSignal): Promise<TmdbTv> {
    const value = await this.get(`/tv/${encodeURIComponent(required(String(id), 'TMDB TV id'))}`, signal)
    if (typeof value.id !== 'number' || typeof value.name !== 'string') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed TV metadata')
    return value as unknown as TmdbTv
  }

  async season(tvId: string | number, seasonNumber: number, signal: AbortSignal): Promise<readonly TmdbEpisode[]> {
    if (!Number.isInteger(seasonNumber) || seasonNumber < 0) throw new EmbymediaError('INVALID_INPUT', 'season number must be a non-negative integer')
    const value = await this.get(`/tv/${encodeURIComponent(required(String(tvId), 'TMDB TV id'))}/season/${String(seasonNumber)}`, signal)
    if (!Array.isArray(value.episodes)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed season metadata')
    return value.episodes.map((episode) => {
      const row = object(episode, 'episode')
      if (typeof row.id !== 'number' || typeof row.episode_number !== 'number' || typeof row.season_number !== 'number') {
        throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed episode metadata')
      }
      return row as unknown as TmdbEpisode
    })
  }

  async search(
    type: 'movie' | 'tv' | 'multi',
    text: string,
    year: number | undefined,
    page: number,
    signal: AbortSignal,
  ): Promise<{ results: readonly TmdbSearchResult[]; totalPages: number; totalResults: number }> {
    if (!Number.isInteger(page) || page < 1 || page > 500) throw new EmbymediaError('INVALID_INPUT', 'TMDB page must be between 1 and 500')
    const value = await this.get(`/search/${type}`, signal, {
      query: required(text, 'TMDB search text'),
      page,
      ...(year === undefined ? {} : type === 'movie' ? { year } : { first_air_date_year: year }),
    })
    if (!Array.isArray(value.results) || typeof value.total_pages !== 'number' || typeof value.total_results !== 'number') {
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed search results')
    }
    return {
      results: value.results.map(result => object(result, 'search result') as unknown as TmdbSearchResult),
      totalPages: value.total_pages,
      totalResults: value.total_results,
    }
  }

  async images(type: 'movie' | 'tv', id: string | number, signal: AbortSignal): Promise<readonly { filePath: string; width?: number; height?: number; voteAverage?: number }[]> {
    const value = await this.get(`/${type}/${encodeURIComponent(required(String(id), 'TMDB id'))}/images`, signal, { include_image_language: 'zh,en,null' })
    if (!Array.isArray(value.posters)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed image metadata')
    return value.posters.map((poster) => {
      const row = object(poster, 'poster')
      if (typeof row.file_path !== 'string') throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'TMDB returned malformed poster')
      return {
        filePath: row.file_path,
        ...(typeof row.width === 'number' ? { width: row.width } : {}),
        ...(typeof row.height === 'number' ? { height: row.height } : {}),
        ...(typeof row.vote_average === 'number' ? { voteAverage: row.vote_average } : {}),
      }
    })
  }
}

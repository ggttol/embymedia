import { isIP } from 'node:net'
import { lookup } from 'node:dns/promises'
import type { EmbyClient, EmbyItem } from '../clients/emby.ts'
import type { TmdbClient, TmdbSearchResult } from '../clients/tmdb.ts'
import { ProxyHttpTransport } from '../clients/http.ts'
import { EmbymediaError } from '../errors.ts'

const MAX_IMAGE_BYTES = 5 * 1024 * 1024
const YEAR_PATTERN = /(?:^|[\s.\-_[(])((?:19|20)\d{2})(?:$|[\s.\-_\])])/g
const NOISE_PATTERN = /\b(?:2160p|1080p|720p|4k|bluray|blu-ray|web[-_. ]?dl|webrip|hdtv|remux|x26[45]|h26[45]|hevc|aac|dts(?:[-_. ]?5\.1)?|atmos|hdr(?:10(?:\+)?)?|dv|dolby|proper|repack|maxplus|edr|ddp(?:[-_. ]?5\.1)?|60fps|hq)\b/gi

export interface PosterCandidate {
  readonly tmdbId: string
  readonly name: string
  readonly year?: number
  readonly posterPath?: string
  readonly score: number
}

export interface PosterMismatch {
  readonly itemId: string
  readonly embyName: string
  readonly embyYear?: number
  readonly currentTmdbId?: string
  readonly reason: 'missing-tmdb' | 'name-mismatch' | 'year-mismatch'
  readonly candidates: readonly PosterCandidate[]
}

export function cleanMediaName(value: string): { name: string; year?: number } {
  const normalized = value.normalize('NFC').replace(/[._]+/g, ' ')
  const years = [...normalized.matchAll(YEAR_PATTERN)]
  const year = years.length === 0 ? undefined : Number(years.at(-1)?.[1])
  const name = normalized
    .replace(YEAR_PATTERN, ' ')
    .replace(NOISE_PATTERN, ' ')
    .replace(/\[[^\]]*]|\([^)]*(?:rip|字幕|国语|粤语|中字|全\d+集|完结|更新|臻彩|高码|4K|HD|简繁|双语|特别版)[^)]*\)/gi, ' ')
    .replace(/(?:\[(?:全\d+集|完结|更新至\d+集|臻彩|高码|4K|HD|简繁|双语|国粤双语|MAXPLUS|\d+fps)\])+/gi, ' ')
    .replace(/\s+/g, ' ')
    .replace(/[-–—_ ]+$/g, '')
    .trim()
  if (name.length === 0) throw new EmbymediaError('INVALID_INPUT', 'media name is empty after normalization')
  return { name, ...(year === undefined ? {} : { year }) }
}

function normalizedName(value: string): string {
  return cleanMediaName(value).name.toLocaleLowerCase('zh-CN').replace(/\s+/g, '')
}

function candidate(result: TmdbSearchResult, queryName: string, queryYear?: number): PosterCandidate | undefined {
  const name = result.title ?? result.name ?? result.original_title ?? result.original_name
  if (name === undefined) return undefined
  const date = result.release_date ?? result.first_air_date
  const year = date === undefined ? undefined : Number(date.slice(0, 4))
  let score = normalizedName(name) === normalizedName(queryName) ? 80 : 40
  if (queryYear !== undefined && year === queryYear) score += 20
  else if (queryYear !== undefined && year !== undefined && Math.abs(queryYear - year) === 1) score += 5
  return {
    tmdbId: String(result.id),
    name,
    ...(year === undefined || !Number.isFinite(year) ? {} : { year }),
    ...(result.poster_path === undefined ? {} : { posterPath: result.poster_path }),
    score,
  }
}

function isPrivateV4(address: string): boolean {
  const parts = address.split('.').map(Number)
  const [a, b] = parts
  return a === 0 || a === 10 || a === 127 || (a === 169 && b === 254) || (a === 172 && b !== undefined && b >= 16 && b <= 31) || (a === 192 && b === 168) || a! >= 224
}

function isPrivateAddress(address: string): boolean {
  if (isIP(address) === 4) return isPrivateV4(address)
  if (isIP(address) === 6) {
    const lower = address.toLowerCase()
    return lower === '::' || lower === '::1' || lower.startsWith('fc') || lower.startsWith('fd') || /^fe[89ab]/.test(lower) || lower.startsWith('ff')
  }
  return true
}

export async function assertPublicImageUrl(input: string): Promise<URL> {
  const url = new URL(input)
  if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new EmbymediaError('POLICY_DENIED', 'image URL must use HTTP or HTTPS')
  if (url.username.length > 0 || url.password.length > 0) throw new EmbymediaError('POLICY_DENIED', 'image URL credentials are forbidden')
  const addresses = isIP(url.hostname) === 0 ? await lookup(url.hostname, { all: true, verbatim: true }) : [{ address: url.hostname }]
  if (addresses.length === 0 || addresses.some(entry => isPrivateAddress(entry.address))) {
    throw new EmbymediaError('POLICY_DENIED', 'image URL resolves to a non-public address')
  }
  return url
}

export class PosterDomainService {
  constructor(
    private readonly embyClient: (signal: AbortSignal) => Promise<EmbyClient>,
    private readonly tmdbClient: (signal: AbortSignal) => Promise<TmdbClient>,
    private readonly imageTransport: ProxyHttpTransport,
  ) {}

  async search(name: string, year: number | undefined, type: 'movie' | 'tv', signal: AbortSignal): Promise<readonly PosterCandidate[]> {
    const cleaned = cleanMediaName(name)
    const queryYear = year ?? cleaned.year
    const response = await (await this.tmdbClient(signal)).search(type, cleaned.name, queryYear, 1, signal)
    return response.results
      .flatMap((result) => {
        const mapped = candidate(result, cleaned.name, queryYear)
        return mapped === undefined ? [] : [mapped]
      })
      .sort((left, right) => right.score - left.score || left.name.localeCompare(right.name))
  }

  async detect(items: readonly EmbyItem[], type: 'movie' | 'tv', signal: AbortSignal): Promise<readonly PosterMismatch[]> {
    const output: PosterMismatch[] = []
    for (const item of items) {
      signal.throwIfAborted()
      const candidates = await this.search(item.Name, item.ProductionYear, type, signal)
      const best = candidates[0]
      const currentTmdbId = item.ProviderIds?.Tmdb
      let reason: PosterMismatch['reason'] | undefined
      if (currentTmdbId === undefined) reason = 'missing-tmdb'
      else if (best !== undefined && normalizedName(best.name) !== normalizedName(item.Name)) reason = 'name-mismatch'
      else if (best?.year !== undefined && item.ProductionYear !== undefined && best.year !== item.ProductionYear) reason = 'year-mismatch'
      if (reason !== undefined) output.push({
        itemId: item.Id,
        embyName: item.Name,
        ...(item.ProductionYear === undefined ? {} : { embyYear: item.ProductionYear }),
        ...(currentTmdbId === undefined ? {} : { currentTmdbId }),
        reason,
        candidates,
      })
    }
    return output
  }

  async apply(itemId: string, tmdbId: string, signal: AbortSignal): Promise<EmbyItem> {
    const emby = await this.embyClient(signal)
    await emby.applyRemoteSearch(itemId, tmdbId, signal)
    await emby.refreshItem(itemId, true, signal)
    const verified = await emby.item(itemId, 'ProviderIds,Path', signal)
    if (verified?.ProviderIds?.Tmdb !== tmdbId) throw new EmbymediaError('VERIFICATION_FAILED', 'Emby metadata did not retain the selected TMDB id')
    return verified
  }

  async fixBatch(items: readonly { itemId: string; tmdbId: string }[], signal: AbortSignal): Promise<readonly EmbyItem[]> {
    const output: EmbyItem[] = []
    for (const item of items) {
      signal.throwIfAborted()
      output.push(await this.apply(item.itemId, item.tmdbId, signal))
    }
    return output
  }

  async refresh(itemId: string, signal: AbortSignal): Promise<EmbyItem> {
    const emby = await this.embyClient(signal)
    await emby.refreshItem(itemId, true, signal)
    const verified = await emby.item(itemId, 'ProviderIds,Path', signal)
    if (verified === undefined) throw new EmbymediaError('VERIFICATION_FAILED', 'refreshed Emby item is not visible')
    return verified
  }

  /**
   * Locks an Emby library or media item's poster images to prevent automatic refresh overwrite.
   * @param itemId Target Emby item ID.
   * @param signal Cancellation signal.
   */
  async lockPoster(itemId: string, signal: AbortSignal): Promise<void> {
    const emby = await this.embyClient(signal)
    await emby.updateItem(itemId, { LockData: true, LockedFields: ['PrimaryImage', 'All'] }, signal)
  }

  async proxyImage(input: string, signal: AbortSignal): Promise<{ contentType: string; bytes: Uint8Array }> {
    const url = await assertPublicImageUrl(input)
    const response = await this.imageTransport.request(url, signal, { method: 'GET', redirect: 'error' })
    if (!response.ok) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'image upstream request failed', { status: response.status })
    const contentType = response.headers.get('content-type')?.split(';', 1)[0]?.toLowerCase() ?? ''
    if (!contentType.startsWith('image/')) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'image upstream returned non-image content')
    const declared = Number(response.headers.get('content-length') ?? 0)
    if (declared > MAX_IMAGE_BYTES) throw new EmbymediaError('POLICY_DENIED', 'image exceeds 5 MiB')
    const bytes = new Uint8Array(await response.arrayBuffer())
    if (bytes.byteLength > MAX_IMAGE_BYTES) throw new EmbymediaError('POLICY_DENIED', 'image exceeds 5 MiB')
    return { contentType, bytes }
  }
}

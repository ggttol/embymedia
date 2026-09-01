import { createHash } from 'node:crypto'
import { EmbymediaError } from '../errors.ts'
import { Semaphore } from './semaphore.ts'

const USER_AGENT = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36'
const SNAP_PAGE_SIZE = 1000
const MAX_SNAP_PAGES = 20
const DIRECTORY_PAGE_SIZE = 1000
const MAX_DIRECTORY_PAGES = 50

export interface C115Options {
  readonly apiBaseUrl?: string
  readonly siteBaseUrl?: string
  readonly cookie: string
  readonly timeoutMs?: number
  readonly fetch?: typeof globalThis.fetch
  readonly semaphore?: Semaphore
}

export interface C115Share {
  readonly shareCode: string
  readonly receiveCode?: string
}

export interface C115SnapshotFile {
  readonly id?: string
  readonly name: string
  readonly size?: number
  readonly directory?: boolean
  readonly path?: string
}

export interface C115Snapshot extends C115Share {
  readonly title?: string
  readonly files: readonly C115SnapshotFile[]
  readonly evidence?: readonly C115SnapshotFile[]
}

/** One exact child returned by a 115 directory listing. */
export interface C115Entry {
  readonly id: string
  readonly name: string
  readonly directory: boolean
}

export interface C115Directory {
  readonly cid: string
  readonly name: string
}

export interface C115CidMatch {
  readonly cid: string
  readonly path: string
}

function required(value: string, label: string): string {
  const trimmed = value.trim()
  if (trimmed.length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return trimmed
}

function stringValue(value: unknown): string | undefined {
  if (typeof value === 'string') return value.trim() || undefined
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return undefined
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && /^\d+$/.test(value)) return Number(value)
  return undefined
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `115 returned malformed ${label}`)
  }
  return value as Record<string, unknown>
}

function validateCid(value: string): string {
  const cid = required(value, '115 CID')
  if (!/^\d+$/.test(cid)) throw new EmbymediaError('INVALID_INPUT', '115 CID must contain digits only')
  return cid
}

export function parseC115Share(input: string, password?: string): C115Share {
  const value = required(input, '115 share')
  if (value.length > 2_048) throw new EmbymediaError('INVALID_INPUT', '115 share input is too long')
  let shareCode: string | undefined
  let receiveCode = password?.trim() || undefined
  try {
    const url = new URL(value)
    const hostname = url.hostname.toLocaleLowerCase()
    const allowedHost = hostname === '115.com' || hostname.endsWith('.115.com')
      || hostname === '115cdn.com' || hostname.endsWith('.115cdn.com')
    if (!allowedHost) throw new EmbymediaError('INVALID_INPUT', '115 share URL must use a 115.com or 115cdn.com host')
    const match = /\/s\/([A-Za-z0-9_-]+)/.exec(url.pathname)
    shareCode = match?.[1]
    receiveCode ??= url.searchParams.get('password')?.trim() || url.searchParams.get('pwd')?.trim() || undefined
  } catch (error) {
    if (error instanceof EmbymediaError) throw error
    const [code, inlinePassword] = value.split(/\s+/, 2)
    if (/^[A-Za-z0-9_-]{4,128}$/.test(code ?? '')) shareCode = code
    receiveCode ??= inlinePassword?.trim() || undefined
  }
  if (shareCode === undefined || shareCode.length > 128) throw new EmbymediaError('INVALID_INPUT', 'unable to parse 115 share code')
  if (receiveCode !== undefined && receiveCode.length > 128) throw new EmbymediaError('INVALID_INPUT', '115 receive code is too long')
  return { shareCode, ...(receiveCode === undefined ? {} : { receiveCode }) }
}

export class C115Client {
  private readonly apiBaseUrl: string
  private readonly siteBaseUrl: string
  private readonly cookie: string
  private readonly timeoutMs: number
  private readonly fetchImpl: typeof globalThis.fetch
  private readonly semaphore: Semaphore

  constructor(options: C115Options) {
    this.apiBaseUrl = (options.apiBaseUrl ?? 'https://webapi.115.com').replace(/\/$/, '')
    this.siteBaseUrl = (options.siteBaseUrl ?? 'https://115.com').replace(/\/$/, '')
    this.cookie = required(options.cookie, '115 cookie')
    this.timeoutMs = options.timeoutMs ?? 45_000
    this.fetchImpl = options.fetch ?? globalThis.fetch
    this.semaphore = options.semaphore ?? new Semaphore(1)
    for (const base of [this.apiBaseUrl, this.siteBaseUrl]) {
      const protocol = new URL(base).protocol
      if (protocol !== 'http:' && protocol !== 'https:') throw new EmbymediaError('INVALID_INPUT', '115 base URL must use HTTP or HTTPS')
    }
  }

  private async requestJson(
    url: URL,
    init: RequestInit,
    signal: AbortSignal,
    label: string,
  ): Promise<Record<string, unknown>> {
    const timeout = AbortSignal.timeout(this.timeoutMs)
    const headers = new Headers(init.headers)
    headers.set('accept', 'application/json, text/plain, */*')
    headers.set('cookie', this.cookie)
    headers.set('referer', 'https://115.com/')
    headers.set('user-agent', USER_AGENT)
    let response: Response
    try {
      response = await this.fetchImpl(url, {
        ...init,
        redirect: 'error',
        signal: AbortSignal.any([signal, timeout]),
        headers,
      })
    } catch {
      if (signal.aborted) throw new EmbymediaError('CANCELLED', `${label} cancelled`)
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `${label} request failed`)
    }
    if (response.status === 401 || response.status === 403) throw new EmbymediaError('AUTH_REQUIRED', '115 authentication failed', { status: response.status })
    if (response.status === 404) throw new EmbymediaError('NOT_FOUND', `${label} resource not found`, { status: response.status })
    if (response.status === 409) throw new EmbymediaError('CONFLICT', `${label} conflict`, { status: response.status })
    if (response.status === 429) throw new EmbymediaError('RATE_LIMITED', '115 rate limit reached', { status: response.status })
    if (!response.ok) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `${label} returned HTTP ${String(response.status)}`, { status: response.status })
    try {
      return object(await response.json(), label)
    } catch (error) {
      if (error instanceof EmbymediaError) throw error
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', `115 returned malformed ${label} JSON`)
    }
  }

  async test(signal: AbortSignal): Promise<{ uid: string; used?: number }> {
    return this.semaphore.run(signal, async () => {
      const value = await this.requestJson(new URL(`${this.apiBaseUrl}/files/index_info`), { method: 'GET' }, signal, '115 cookie test')
      if (value.state !== true) throw new EmbymediaError('AUTH_REQUIRED', '115 rejected the cookie')
      const uid = /(?:^|;\s*)UID=([^_;]+)/i.exec(this.cookie)?.[1] ?? ''
      const data = typeof value.data === 'object' && value.data !== null ? value.data as Record<string, unknown> : {}
      return { uid, ...(numberValue(data.used) === undefined ? {} : { used: numberValue(data.used)! }) }
    })
  }

  async snapshot(input: string, password: string | undefined, fileIds: readonly string[] | undefined, signal: AbortSignal): Promise<C115Snapshot> {
    return this.semaphore.run(signal, () => this.snapshotUnlocked(input, password, fileIds, signal, true))
  }

  async snapshotPage(
    input: string,
    password: string | undefined,
    offset: number,
    limit: number,
    signal: AbortSignal,
  ): Promise<C115Snapshot & { readonly total: number; readonly offset: number; readonly limit: number; readonly hasMore: boolean }> {
    return this.semaphore.run(signal, async () => {
      const share = parseC115Share(input, password)
      const boundedOffset = Math.max(0, Math.floor(offset))
      const boundedLimit = Math.min(Math.max(1, Math.floor(limit)), 500)
      const url = new URL(`${this.apiBaseUrl}/share/snap`)
      url.searchParams.set('share_code', share.shareCode)
      url.searchParams.set('receive_code', share.receiveCode ?? '')
      url.searchParams.set('cid', '0')
      url.searchParams.set('offset', String(boundedOffset))
      url.searchParams.set('limit', String(boundedLimit))
      const response = await this.requestJson(url, { method: 'GET' }, signal, '115 share snapshot')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 share snapshot was rejected')
      const data = object(response.data ?? {}, 'share snapshot data')
      const list = Array.isArray(data.list) ? data.list : []
      const files = list.flatMap((entry): C115SnapshotFile[] => {
        const row = object(entry, 'share item')
        const fileId = stringValue(row.fid) ?? stringValue(row.file_id)
        const directoryId = stringValue(row.cid)
        const id = fileId ?? directoryId
        const name = stringValue(row.n) ?? stringValue(row.name)
        if (name === undefined) return []
        const size = numberValue(row.s ?? row.size)
        return [{ ...(id === undefined ? {} : { id }), name, ...(size === undefined ? {} : { size }), directory: fileId === undefined && directoryId !== undefined }]
      })
      const shareInfo = typeof data.shareinfo === 'object' && data.shareinfo !== null ? data.shareinfo as Record<string, unknown> : {}
      const title = stringValue(shareInfo.share_title) ?? stringValue(shareInfo.file_name)
      const total = numberValue(data.total ?? data.count ?? data.file_count) ?? boundedOffset + files.length
      return { ...share, ...(title === undefined ? {} : { title }), files, total, offset: boundedOffset, limit: boundedLimit, hasMore: boundedOffset + files.length < total }
    })
  }

  private async snapshotEntriesUnlocked(
    share: C115Share,
    cid: string,
    signal: AbortSignal,
  ): Promise<{ readonly title?: string; readonly files: readonly C115SnapshotFile[] }> {
    const files: C115SnapshotFile[] = []
    let title: string | undefined
    const pageFingerprints = new Set<string>()
    for (let page = 0; page < MAX_SNAP_PAGES; page++) {
      signal.throwIfAborted()
      const url = new URL(`${this.apiBaseUrl}/share/snap`)
      url.searchParams.set('share_code', share.shareCode)
      url.searchParams.set('receive_code', share.receiveCode ?? '')
      url.searchParams.set('cid', cid)
      url.searchParams.set('offset', String(page * SNAP_PAGE_SIZE))
      url.searchParams.set('limit', String(SNAP_PAGE_SIZE))
      const response = await this.requestJson(url, { method: 'GET' }, signal, '115 share snapshot')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 share snapshot was rejected')
      const data = object(response.data ?? {}, 'share snapshot data')
      const list = Array.isArray(data.list) ? data.list : []
      const fingerprint = createHash('sha256').update(JSON.stringify(list)).digest('hex')
      if (pageFingerprints.has(fingerprint)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 share snapshot pagination did not advance')
      pageFingerprints.add(fingerprint)
      const shareInfo = typeof data.shareinfo === 'object' && data.shareinfo !== null ? data.shareinfo as Record<string, unknown> : {}
      title ??= stringValue(shareInfo.share_title) ?? stringValue(shareInfo.file_name)
      for (const entry of list) {
        const row = object(entry, 'share item')
        const fileId = stringValue(row.fid) ?? stringValue(row.file_id)
        const directoryId = stringValue(row.cid)
        const id = fileId ?? directoryId
        const name = stringValue(row.n) ?? stringValue(row.name)
        if (name === undefined) continue
        const directory = fileId === undefined && directoryId !== undefined
        const size = numberValue(row.s ?? row.size)
        files.push({
          ...(id === undefined ? {} : { id }),
          name,
          ...(size === undefined ? {} : { size }),
          directory,
        })
      }
      const total = numberValue(data.total ?? data.count ?? data.file_count)
      if (list.length === 0 || list.length < SNAP_PAGE_SIZE || (total !== undefined && (page + 1) * SNAP_PAGE_SIZE >= total)) break
      if (page === MAX_SNAP_PAGES - 1) throw new EmbymediaError('POLICY_DENIED', '115 share snapshot exceeded pagination limit')
    }
    return { ...(title === undefined ? {} : { title }), files }
  }

  private async snapshotEvidenceUnlocked(
    share: C115Share,
    roots: readonly C115SnapshotFile[],
    signal: AbortSignal,
  ): Promise<readonly C115SnapshotFile[]> {
    const evidence: C115SnapshotFile[] = []
    const stack = roots.flatMap(root => root.directory === true && root.id !== undefined
      ? [{ cid: root.id, path: root.name, depth: 0 }]
      : [])
    const visited = new Set<string>()
    while (stack.length > 0) {
      const node = stack.pop()
      if (node === undefined) continue
      if (visited.has(node.cid)) continue
      visited.add(node.cid)
      if (node.depth > 20 || evidence.length >= 10_000) {
        throw new EmbymediaError('POLICY_DENIED', '115 share tree exceeded evidence bounds')
      }
      const children = (await this.snapshotEntriesUnlocked(share, node.cid, signal)).files
      for (const child of children) {
        if (evidence.length >= 10_000) throw new EmbymediaError('POLICY_DENIED', '115 share tree exceeded evidence bounds')
        const path = `${node.path}/${child.name}`
        evidence.push({ ...child, path })
        if (child.directory === true && child.id !== undefined) stack.push({ cid: child.id, path, depth: node.depth + 1 })
      }
    }
    return evidence
  }

  private async snapshotUnlocked(
    input: string,
    password: string | undefined,
    fileIds: readonly string[] | undefined,
    signal: AbortSignal,
    includeEvidence: boolean,
  ): Promise<C115Snapshot> {
    const share = parseC115Share(input, password)
    const top = await this.snapshotEntriesUnlocked(share, '0', signal)
    const wanted = fileIds === undefined ? undefined : new Set(fileIds.map(id => required(id, 'file id')))
    const files = wanted === undefined ? top.files : top.files.filter(file => file.id !== undefined && wanted.has(file.id))
    const evidence = includeEvidence ? await this.snapshotEvidenceUnlocked(share, files, signal) : []
    return {
      ...share,
      ...(top.title === undefined ? {} : { title: top.title }),
      files,
      ...(evidence.length === 0 ? {} : { evidence }),
    }
  }

  async saveShare(
    input: string,
    password: string | undefined,
    fileIds: readonly string[] | undefined,
    targetCid: string,
    signal: AbortSignal,
  ): Promise<{ count: number; cid: string }> {
    return this.semaphore.run(signal, async () => {
      const cid = validateCid(targetCid)
      const snapshot = await this.snapshotUnlocked(input, password, fileIds, signal, false)
      const ids = (fileIds ?? snapshot.files.flatMap(file => file.id === undefined ? [] : [file.id])).map(id => required(id, 'file id'))
      if (ids.length === 0) throw new EmbymediaError('INVALID_INPUT', '115 share contains no transferable file ids')
      const form = new URLSearchParams({
        share_code: snapshot.shareCode,
        receive_code: snapshot.receiveCode ?? '',
        file_id: ids.join(','),
        cid,
        user_id: /(?:^|;\s*)UID=([^_;]+)/i.exec(this.cookie)?.[1] ?? '',
      })
      const response = await this.requestJson(new URL(`${this.apiBaseUrl}/share/receive`), {
        method: 'POST', body: form, headers: { 'content-type': 'application/x-www-form-urlencoded' },
      }, signal, '115 share save')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 rejected share save')
      return { count: ids.length, cid }
    })
  }

  async offline(urlValue: string, targetCid: string, signal: AbortSignal): Promise<{ accepted: true; infoHash?: string }> {
    return this.semaphore.run(signal, async () => {
      const cid = validateCid(targetCid)
      const spaceUrl = new URL(`${this.siteBaseUrl}/`)
      spaceUrl.searchParams.set('ct', 'offline')
      spaceUrl.searchParams.set('ac', 'space')
      const space = await this.requestJson(spaceUrl, { method: 'GET' }, signal, '115 offline authorization')
      if (space.state !== true) throw new EmbymediaError('AUTH_REQUIRED', '115 offline authorization failed')
      const sign = stringValue(space.sign)
      const time = stringValue(space.time)
      if (sign === undefined || time === undefined) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 offline authorization response is incomplete')
      const addUrl = new URL(`${this.siteBaseUrl}/web/lixian/`)
      addUrl.searchParams.set('ct', 'lixian')
      addUrl.searchParams.set('ac', 'add_task_url')
      const body = new URLSearchParams({ url: required(urlValue, 'offline URL'), wp_path_id: cid, sign, time })
      const response = await this.requestJson(addUrl, {
        method: 'POST', body, headers: { 'content-type': 'application/x-www-form-urlencoded' },
      }, signal, '115 offline add')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 rejected offline task')
      const infoHash = stringValue(response.info_hash)
      return { accepted: true, ...(infoHash === undefined ? {} : { infoHash }) }
    })
  }

  async listEntries(cid: string, signal: AbortSignal): Promise<readonly C115Entry[]> {
    return this.semaphore.run(signal, () => this.listEntriesUnlocked(validateCid(cid), signal))
  }

  async listEntriesPage(cid: string, offset: number, limit: number, signal: AbortSignal): Promise<{ readonly entries: readonly C115Entry[]; readonly total: number; readonly offset: number; readonly limit: number; readonly hasMore: boolean }> {
    return this.semaphore.run(signal, async () => {
      const boundedOffset = Math.max(0, Math.floor(offset))
      const boundedLimit = Math.min(Math.max(1, Math.floor(limit)), 500)
      const url = new URL(`${this.apiBaseUrl}/files`)
      Object.entries({ aid: '1', cid: validateCid(cid), o: 'user_ptime', asc: '0', offset: String(boundedOffset), limit: String(boundedLimit), show_dir: '1', format: 'json' })
        .forEach(([key, value]) => { url.searchParams.set(key, value) })
      const response = await this.requestJson(url, { method: 'GET' }, signal, '115 directory listing')
      if (response.state !== true || !Array.isArray(response.data)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 returned malformed directory listing data')
      const entries = response.data.map((entry): C115Entry => {
        const row = object(entry, 'directory entry')
        const fileId = stringValue(row.fid) ?? stringValue(row.file_id)
        const id = fileId ?? stringValue(row.cid)
        const name = stringValue(row.n) ?? stringValue(row.name)
        if (id === undefined || name === undefined) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 returned a directory entry without id or name')
        return { id, name, directory: fileId === undefined }
      })
      const total = numberValue(response.count ?? response.total) ?? boundedOffset + entries.length
      return { entries, total, offset: boundedOffset, limit: boundedLimit, hasMore: boundedOffset + entries.length < total }
    })
  }

  async listDirectories(cid: string, signal: AbortSignal): Promise<readonly C115Directory[]> {
    return (await this.listEntries(cid, signal)).flatMap(entry => entry.directory ? [{ cid: entry.id, name: entry.name }] : [])
  }

  private async listDirectoriesUnlocked(cid: string, signal: AbortSignal): Promise<readonly C115Directory[]> {
    return (await this.listEntriesUnlocked(cid, signal)).flatMap(entry => entry.directory ? [{ cid: entry.id, name: entry.name }] : [])
  }

  /** Hash the bounded descendant id/name/type manifest of one 115 directory. */
  async treeHash(cid: string, signal: AbortSignal): Promise<string> {
    return this.semaphore.run(signal, async () => {
      const rootCid = validateCid(cid)
      const records: Array<readonly [string, string, boolean]> = []
      const stack: Array<{ cid: string; path: string; depth: number }> = [{ cid: rootCid, path: '', depth: 0 }]
      const visited = new Set<string>()
      let scanned = 0
      while (stack.length > 0) {
        const node = stack.pop()
        if (node === undefined) continue
        if (visited.has(node.cid)) continue
        visited.add(node.cid)
        if (node.depth > 20 || scanned >= 10_000) throw new EmbymediaError('POLICY_DENIED', '115 directory tree exceeded manifest bounds')
        scanned++
        const entries = await this.listEntriesUnlocked(node.cid, signal)
        for (const entry of entries) {
          const path = node.path.length === 0 ? entry.name : `${node.path}/${entry.name}`
          if (records.length >= 10_000) throw new EmbymediaError('POLICY_DENIED', '115 directory tree exceeded manifest bounds')
          records.push([path, entry.id, entry.directory])
          if (entry.directory) stack.push({ cid: entry.id, path, depth: node.depth + 1 })
        }
      }
      records.sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]))
      return createHash('sha256').update(JSON.stringify(records)).digest('hex')
    })
  }

  private async listEntriesUnlocked(cid: string, signal: AbortSignal): Promise<readonly C115Entry[]> {
    const entries: C115Entry[] = []
    const pageFingerprints = new Set<string>()
    for (let page = 0; page < MAX_DIRECTORY_PAGES; page++) {
      signal.throwIfAborted()
      const url = new URL(`${this.apiBaseUrl}/files`)
      Object.entries({ aid: '1', cid, o: 'user_ptime', asc: '0', offset: String(page * DIRECTORY_PAGE_SIZE), limit: String(DIRECTORY_PAGE_SIZE), show_dir: '1', format: 'json' })
        .forEach(([key, value]) =>{  url.searchParams.set(key, value) })
      const response = await this.requestJson(url, { method: 'GET' }, signal, '115 directory listing')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 rejected directory listing')
      if (!Array.isArray(response.data)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 returned malformed directory listing data')
      const data = response.data
      const fingerprint = createHash('sha256').update(JSON.stringify(data)).digest('hex')
      if (pageFingerprints.has(fingerprint)) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 directory pagination did not advance')
      pageFingerprints.add(fingerprint)
      for (const entry of data) {
        const row = object(entry, 'directory entry')
        const fileId = stringValue(row.fid) ?? stringValue(row.file_id)
        const directory = fileId === undefined
        const id = directory ? stringValue(row.cid) : fileId
        const name = stringValue(row.n) ?? stringValue(row.name)
        if (id === undefined || name === undefined) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 returned a directory entry without id or name')
        entries.push({ id, name, directory })
      }
      if (data.length < DIRECTORY_PAGE_SIZE) return entries
    }
    throw new EmbymediaError('POLICY_DENIED', '115 directory listing exceeded pagination limit')
  }

  async autoCid(
    targets: Readonly<Record<string, string>>,
    current: Readonly<Record<string, string>>,
    maxDepth: number,
    signal: AbortSignal,
  ): Promise<{ matches: Readonly<Record<string, readonly C115CidMatch[]>>; current: Readonly<Record<string, string>>; scanned: number }> {
    return this.semaphore.run(signal, async () => {
      const matches: Record<string, C115CidMatch[]> = Object.fromEntries(Object.keys(targets).map(key => [key, []]))
      const seen: Record<string, true> = {}
      const stack: Array<{ cid: string; path: string; depth: number }> = [{ cid: '0', path: '', depth: Math.min(Math.max(0, maxDepth), 5) }]
      let scanned = 0
      while (stack.length > 0 && scanned < 80) {
        signal.throwIfAborted()
        const node = stack.pop()!
        if (seen[node.cid]) continue
        seen[node.cid] = true
        scanned++
        const children = await this.listDirectoriesUnlocked(node.cid, signal)
        for (const child of children) {
          const path = node.path.length === 0 ? child.name : `${node.path}/${child.name}`
          for (const [library, target] of Object.entries(targets)) {
            if (child.name.normalize('NFC').toLocaleLowerCase() === target.normalize('NFC').toLocaleLowerCase()) {
              matches[library]!.push({ cid: child.cid, path })
            }
          }
          if (node.depth > 0) stack.push({ cid: child.cid, path, depth: node.depth - 1 })
        }
      }
      return { matches, current, scanned }
    })
  }

  async deleteIds(parentCid: string, ids: readonly string[], signal: AbortSignal): Promise<void> {
    return this.semaphore.run(signal, async () => {
      const body = new URLSearchParams({ pid: validateCid(parentCid), ignore_warn: '1' })
      ids.map(id => required(id, '115 delete id')).forEach((id, index) =>{  body.set(`fid[${String(index)}]`, id) })
      if (ids.length === 0) throw new EmbymediaError('INVALID_INPUT', '115 delete ids are required')
      const response = await this.requestJson(new URL(`${this.apiBaseUrl}/rb/delete`), {
        method: 'POST', body, headers: { 'content-type': 'application/x-www-form-urlencoded' },
      }, signal, '115 delete')
      if (response.state !== true) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', '115 rejected delete')
    })
  }
}

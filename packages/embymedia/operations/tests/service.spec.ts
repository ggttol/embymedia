import { Context } from '@deepseek-ai/cordis'
import {
  CredentialProvider,
  type CredentialInfo,
  type CredentialKey,
  type CredentialRecord,
  type CredentialRecordEntry,
  type CredentialRecordInfo,
  type CredentialRef,
  type ResolvedCredential,
} from '@deepseek-ai/dsh-credentials'
import { describe, expect, it, vi } from 'vitest'
import { EmbymediaService } from '../src/service.ts'
import type { ResourceSearchResult } from '../src/clients/resource-api.ts'
import { prepareOperationInput } from '../src/operations.ts'

class MemoryCredentials extends CredentialProvider {
  readonly records = new Map<CredentialKey, CredentialRecord>()

  resolve(_ref: CredentialRef): Promise<ResolvedCredential | undefined> {
    return Promise.resolve(undefined)
  }

  describe(_ref: CredentialRef): Promise<CredentialInfo> {
    return Promise.resolve({ configured: false, writable: true })
  }

  set(_ref: CredentialRef, _value: string): Promise<void> {
    return Promise.resolve()
  }

  unset(_ref: CredentialRef): Promise<void> {
    return Promise.resolve()
  }

  readRecord(key: CredentialKey): Promise<CredentialRecord | undefined> {
    return Promise.resolve(this.records.get(key))
  }

  describeRecord(key: CredentialKey): Promise<CredentialRecordInfo> {
    const record = this.records.get(key)
    return Promise.resolve({ configured: record !== undefined, writable: true, ...(record === undefined ? {} : { kind: record.kind }) })
  }

  listRecords(): Promise<readonly CredentialRecordEntry[]> {
    return Promise.resolve([...this.records].map(([key, record]) => ({ key, kind: record.kind })))
  }

  async modifyRecord(
    key: CredentialKey,
    mutate: (current: CredentialRecord | undefined) => Promise<CredentialRecord | undefined>,
  ): Promise<CredentialRecord | undefined> {
    const next = await mutate(this.records.get(key))
    if (next !== undefined) this.records.set(key, next)
    return next ?? this.records.get(key)
  }

  deleteRecord(key: CredentialKey): Promise<void> {
    this.records.delete(key)
    return Promise.resolve()
  }
}


interface CandidateHarness {
  publicResourceSearch(search: ResourceSearchResult, sessionId: string): unknown
  resolveResourceCandidate(kind: 'series.update' | 'resource.add_new', value: unknown, sessionId: string): { readonly input: unknown; readonly candidateIds: readonly string[] }
}

describe('opaque resource candidates', () => {
  it('keeps 115 access codes out of model-visible search and persisted plans', () => {
    const service = new EmbymediaService(new Context(), {}) as unknown as CandidateHarness
    const publicSearch = service.publicResourceSearch({
      items: [{ title: 'Show S01E03', url: 'https://115.com/s/fixture', password: 'access-code', sourceChannels: [] }],
      total: 1, limit: 80, offset: 0, hasMore: false, query: 'Show S01E03', exact: false, sort: 'relevance', diskTypes: [],
    }, 'session-1')
    expect(JSON.stringify(publicSearch)).not.toContain('access-code')
    const envelope = publicSearch as { items: Array<{ candidateId: string; accessCodeStaged: boolean }> }
    expect(envelope.items[0]?.accessCodeStaged).toBe(true)
    const resolved = service.resolveResourceCandidate('series.update', {
      libraryId: 'library',
      seriesId: 'series',
      candidate: { candidateId: envelope.items[0]?.candidateId },
      requestedEpisodes: [{ season: 1, episode: 3 }],
    }, 'session-1')
    const prepared = prepareOperationInput('series.update', resolved.input)
    expect(prepared.secret).toBe('access-code')
    expect(JSON.stringify(prepared.persistedValue)).not.toContain('access-code')
    expect(() => service.resolveResourceCandidate('series.update', {
      candidate: { candidateId: envelope.items[0]?.candidateId },
    }, 'other-session')).toThrow(/another session/)
  })

  it('resolves multiple same-session opaque candidates and stages every access code', () => {
    const service = new EmbymediaService(new Context(), {}) as unknown as CandidateHarness
    const publicSearch = service.publicResourceSearch({
      items: [
        { title: 'Show S01E01', url: 'https://115cdn.com/s/first', password: 'one', diskType: '115', sourceChannels: [] },
        { title: 'Show S01E02', url: 'https://115cdn.com/s/second', password: 'two', diskType: '115', sourceChannels: [] },
      ],
      total: 2, limit: 80, offset: 0, hasMore: false, query: 'Show', exact: false, sort: 'relevance', diskTypes: [{ diskType: '115', count: 2 }],
    }, 'session-1') as { items: Array<{ candidateId: string }> }
    const resolved = service.resolveResourceCandidate('resource.add_new', {
      candidates: publicSearch.items.map(item => ({ candidateId: item.candidateId, targetCid: '10' })),
      scan: { libraryId: 'library', libraryName: 'Shows', mediaFolder: 'Shows' },
    }, 'session-1')
    const prepared = prepareOperationInput('resource.add_new', resolved.input)
    expect(resolved.candidateIds).toHaveLength(2)
    expect(prepared.secrets).toEqual(['one', 'two'])
    expect(JSON.stringify(prepared.persistedValue)).not.toContain('one')
    expect(JSON.stringify(prepared.persistedValue)).not.toContain('two')
  })

  it('bounds and redacts untrusted resource display fields', () => {
    const service = new EmbymediaService(new Context(), {}) as unknown as CandidateHarness
    const publicSearch = service.publicResourceSearch({
      items: [{
        title: `\u001b[31mShow https://evil.test/s/code password=leak ${'x'.repeat(800)}`,
        url: 'https://115.com/s/fixture',
        source: 'source\u202Ehttps://evil.test',
        sourceChannels: Array.from({ length: 40 }, (_, index) => `channel-${String(index)} access_code=leak`),
      }],
      total: 1, limit: 80, offset: 0, hasMore: false, query: 'Show', exact: false, sort: 'relevance',
      diskTypes: [{ diskType: `115${'x'.repeat(100)}`, count: 1 }],
    }, 'session-1')
    const serialized = JSON.stringify(publicSearch)
    expect(serialized).not.toContain('evil.test')
    expect(serialized).not.toContain('password=leak')
    expect(serialized).not.toContain('access_code=leak')
    expect(serialized).not.toContain('\u001b')
    const envelope = publicSearch as { items: Array<{ candidateId: string; title: string; sourceChannels: string[] }>; diskTypes: Array<{ diskType: string }> }
    expect([...envelope.items[0]!.title]).toHaveLength(512)
    expect(envelope.items[0]!.sourceChannels).toHaveLength(16)
    expect([...envelope.diskTypes[0]!.diskType]).toHaveLength(64)
    const resolved = service.resolveResourceCandidate('series.update', { candidate: { candidateId: envelope.items[0]!.candidateId } }, 'session-1')
    expect(JSON.stringify(resolved.input)).not.toContain('evil.test')
    expect(JSON.stringify(resolved.input)).not.toContain('password=leak')
  })
})

it('dispatches bounded secret-free candidate and directory inspection', async () => {
  const service = new EmbymediaService(new Context(), {}) as unknown as CandidateHarness & Pick<EmbymediaService, 'invokeTool'>
  const search = service.publicResourceSearch({
    items: [{ title: 'Show S01E03 password=hidden', url: 'https://115.com/s/fixture', password: 'access-code', sourceChannels: [] }],
    total: 1, limit: 1, offset: 0, hasMore: false, query: 'Show', exact: false, sort: 'relevance', diskTypes: [],
  }, 'session-1') as { items: Array<{ candidateId: string }> }
  const evidence = Array.from({ length: 600 }, (_, index) => ({
    id: `leaf-${String(index)}`, name: index === 0 ? 'Show.S01E03.mkv' : `Episode.${String(index)}.mkv`,
    path: `Show/${String(index)}.mkv`, directory: false,
  }))
  const c115 = {
    snapshot: vi.fn(async () => ({ shareCode: 'fixture', files: [{ id: 'root', name: 'Show', directory: true }], evidence })),
    listEntriesPage: vi.fn(async (_cid: string, offset: number, limit: number) => ({
      entries: Array.from({ length: limit }, (_, index) => ({ id: String(offset + index), name: `Entry ${String(index)}`, directory: index % 2 === 0 })),
      total: 900, offset, limit, hasMore: true,
    })),
  }
  vi.spyOn(service as unknown as { c115Client: () => Promise<unknown> }, 'c115Client').mockResolvedValue(c115)
  const execution = { agent: { id: 'session-1' }, callId: 'call-1', signal: new AbortController().signal, approval: { request: async () => 'rejected' as const } } as never
  const inspected = await service.invokeTool('embymedia_resource', { action: 'inspect_candidate', input: { candidateId: search.items[0]!.candidateId } }, execution) as unknown as { data: { roots: Array<{ directory: boolean }>; evidence: unknown[]; evidenceTotal: number; truncated: boolean; coverage: { seasonEpisodeKeys: string[] } } }
  expect(inspected.data).toMatchObject({ roots: [{ directory: true }], evidenceTotal: 600, truncated: true, coverage: { seasonEpisodeKeys: ['1:3'] } })
  expect(inspected.data.evidence).toHaveLength(500)
  expect(JSON.stringify(inspected)).not.toContain('access-code')
  expect(JSON.stringify(inspected)).not.toContain('115.com')
  const listed = await service.invokeTool('embymedia_resource', { action: 'list_entries', limit: 500, input: { cid: '10', offset: 300 } }, execution) as unknown as { data: { entries: Array<{ directory: boolean }>; total: number; nextOffset: number } }
  expect(listed.data).toMatchObject({ total: 900, nextOffset: 800 })
  expect(listed.data.entries).toHaveLength(500)
  expect(c115.listEntriesPage).toHaveBeenCalledWith('10', 300, 500, expect.any(AbortSignal))
})


describe('Embymedia Host service', () => {
  it('opens one state owner, registers loopback health, and quiesces on disposal', async () => {
    if (process.env.EMBYMEDIA_TEST_DATABASE_URL === undefined) return
    const routes: Array<{ path: string; handler: unknown }> = []
    const context = new Context()
    context.provide('webServer', {
      register(route: { path: string; handler: unknown }) {
        routes.push(route)
        return () => { routes.splice(routes.indexOf(route), 1) }
      },
    } as never)
    context.provide('agents', {} as never)
    context.provide('typert', {} as never)
    await context.plugin(MemoryCredentials)
    const previous = process.env.EMBYMEDIA_DATABASE_URL
    process.env.EMBYMEDIA_DATABASE_URL = process.env.EMBYMEDIA_TEST_DATABASE_URL
    const fiber = context.plugin(EmbymediaService, { writeMode: 'disabled' })
    try {
      await fiber
      expect(context.embymedia.health()).toEqual({
        schemaVersion: 1,
        status: 'ok',
        writeMode: 'disabled',
        scheduler: 'disabled-for-cutover',
      })
      expect(routes.map(route => route.path)).toEqual(['/internal/embymedia/health', '/hooks/clouddrive2'])
    } finally {
      await fiber.dispose()
      if (previous === undefined) delete process.env.EMBYMEDIA_DATABASE_URL
      else process.env.EMBYMEDIA_DATABASE_URL = previous
    }
    expect(routes).toEqual([])
  })
})

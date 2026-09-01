import { describe, expect, it, vi } from 'vitest'
import { ResourceDomainService, type AddNewRequest } from '../src/domain/resource.ts'
import { PartialOperationError } from '../src/errors.ts'
import type { C115Client } from '../src/clients/c115.ts'
import type { ResourceApiClient } from '../src/clients/resource-api.ts'
import type { LibraryDomainService } from '../src/domain/library.ts'

const request: AddNewRequest = {
  candidate: {
    mode: 'share',
    url: 'https://115.com/s/abcde',
    title: 'Fixture',
    targetCid: '10',
  },
  scan: {
    libraryId: 'library-1',
    libraryName: 'Movies',
    mediaFolder: 'Movies',
    top: 'Fixture',
  },
}

function harness(verifyOk: boolean) {
  const order: string[] = []
  const c115 = {
    snapshot: vi.fn(async () => {
      order.push('snapshot')
      return { shareCode: 'abcde', files: [{ id: 'f1', name: 'Movie.mkv' }] }
    }),
    saveShare: vi.fn(async () => {
      order.push('save')
      return { count: 1, cid: '10' }
    }),
  } as unknown as C115Client
  const resource = {
    search: vi.fn(async () => ({ items: [], total: 0, limit: 80, offset: 0, hasMore: false, query: 'q', exact: false, sort: 'relevance', diskTypes: [] })),
  } as unknown as ResourceApiClient
  const libraries = {
    scan: vi.fn(async () => {
      order.push('scan')
      return {
        schemaVersion: 1, id: 'task-1', kind: 'library.scan', label: 'scan', status: 'done', progress: 1, total: 1,
        statusText: 'verified', cancelRequested: false, correlationId: 'correlation', queuedAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
      }
    }),
  } as unknown as LibraryDomainService
  const service = new ResourceDomainService(async () => c115, async () => resource, libraries, {
    waitVisible: async () => { order.push('visible'); return { visible: true } },
    inspectPoster: async () => { order.push('poster'); return { ok: true } },
    analyzeDedup: async () => { order.push('dedup'); return { duplicates: 0 } },
    handleOldVersion: async () => { order.push('old-version'); return { action: 'none' } },
    verify: async () => { order.push('verify'); return { ok: verifyOk } },
  })
  return { service, order, c115, libraries }
}

describe('resource onboarding pipeline', () => {
  it('runs the complete fixed pipeline and returns only after business verification', async () => {
    const { service, order } = harness(true)
    const progress: string[] = []
    const result = await service.executeAddNew(request, new AbortController().signal, (stage) => { progress.push(stage) })
    expect(order).toEqual(['snapshot', 'save', 'visible', 'scan', 'poster', 'dedup', 'old-version', 'verify'])
    expect(progress).toEqual([
      'validate-candidate', 'accept-115', 'wait-resource-visible', 'generate-strm-and-scan',
      'poster-check', 'deduplicate', 'old-version-policy', 'business-verification',
    ])
    expect(result.stages).toEqual(progress)
    expect(result.verification).toEqual({ ok: true })
  })

  it('validates and saves multiple shares before one visibility barrier and scan', async () => {
    const { service, order, c115, libraries } = harness(true)
    const batch: AddNewRequest = {
      ...request,
      candidates: [
        request.candidate,
        { mode: 'share', url: 'https://115cdn.com/s/fghij?password=code', password: 'code', title: 'Episode 2', targetCid: '10' },
      ],
      expectedNames: ['Episode.01.mkv', 'Episode.02.mkv'],
      expectedPaths: ['Episode.01.mkv', 'Episode.02.mkv'],
      scan: { libraryId: 'library-1', libraryName: 'Movies', mediaFolder: 'Movies' },
    }
    const result = await service.executeAddNew(batch, new AbortController().signal)
    expect(order).toEqual(['snapshot', 'snapshot', 'save', 'save', 'visible', 'scan', 'poster', 'dedup', 'old-version', 'verify'])
    expect(c115.snapshot).toHaveBeenCalledTimes(2)
    expect(c115.saveShare).toHaveBeenCalledTimes(2)
    expect(libraries.scan).toHaveBeenCalledOnce()
    expect(result.accepted).toMatchObject({ detail: { total: 2, items: [{ count: 1 }, { count: 1 }] } })
  })

  it('marks failures after 115 acceptance as partial external mutations', async () => {
    const { service, c115 } = harness(false)
    const result = service.executeAddNew(request, new AbortController().signal)
    await expect(result).rejects.toBeInstanceOf(PartialOperationError)
    await expect(result).rejects.toMatchObject({
      code: 'VERIFICATION_FAILED',
      detail: { stage: 'business-verification', externalEffectsMayHaveStarted: true },
    })
    expect(c115.saveShare).toHaveBeenCalledOnce()
  })
})

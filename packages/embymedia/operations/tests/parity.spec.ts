import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  CAPABILITIES,
  OPERATION_KINDS,
  PRODUCT_AREAS,
  SCHEDULE_KINDS,
  WIRE_TOOLS,
  resolveLegacyOperation,
  type LegacyOperation,
} from '../src/capabilities.ts'

interface OpenApiOperation {
  readonly operationId?: string
  readonly tags?: readonly string[]
}

interface OpenApiDocument {
  readonly paths: Readonly<Record<string, Readonly<Record<string, OpenApiOperation>>>>
}

const METHODS = new Set(['get', 'post', 'put', 'delete', 'patch'])

async function legacyOperations(): Promise<LegacyOperation[]> {
  const fixture = fileURLToPath(new URL('./fixtures/legacy-openapi.json', import.meta.url))
  const document = JSON.parse(await readFile(fixture, 'utf8')) as OpenApiDocument
  const operations: LegacyOperation[] = []
  for (const [path, pathItem] of Object.entries(document.paths)) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!METHODS.has(method)) continue
      if (operation.operationId === undefined) throw new Error(`${method} ${path} has no operationId`)
      operations.push({ method: method.toUpperCase(), path, operationId: operation.operationId, tag: operation.tags?.[0] ?? '' })
    }
  }
  return operations
}

describe('legacy parity registry', () => {
  it('closes every legacy route without ignored ownership', async () => {
    const operations = await legacyOperations()
    expect(operations).toHaveLength(109)
    for (const operation of operations) {
      expect(() => resolveLegacyOperation(operation), `${operation.method} ${operation.path}`).not.toThrow()
      const resolved = resolveLegacyOperation(operation)
      const owner = 'owner' in resolved ? resolved.owner : resolved
      expect(['tool', 'host-route', 'platform-replacement']).toContain(owner.kind)
    }
  })

  it('pins all product contract rosters with unique names', () => {
    expect(PRODUCT_AREAS).toHaveLength(16)
    expect(OPERATION_KINDS).toHaveLength(28)
    expect(WIRE_TOOLS).toHaveLength(13)
    expect(SCHEDULE_KINDS).toHaveLength(8)
    expect(new Set(PRODUCT_AREAS).size).toBe(PRODUCT_AREAS.length)
    expect(new Set(OPERATION_KINDS).size).toBe(OPERATION_KINDS.length)
    expect(new Set(WIRE_TOOLS).size).toBe(WIRE_TOOLS.length)
    expect(new Set(SCHEDULE_KINDS).size).toBe(SCHEDULE_KINDS.length)
    expect(new Set(CAPABILITIES.map(capability => capability.capabilityId)).size).toBe(CAPABILITIES.length)
  })

  it('routes every write through plan except cooperative task cancellation', async () => {
    for (const operation of await legacyOperations()) {
      const resolved = resolveLegacyOperation(operation)
      if (!('category' in resolved) || resolved.category === 'read') continue
      expect(
        resolved.owner.kind === 'tool'
        && (resolved.owner.tool === 'embymedia_plan' || resolved.capabilityId === 'task.cancel'),
      ).toBe(true)
    }
  })
})

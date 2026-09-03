import { describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import type { ToolDefinition, ToolRunContext } from '@deepseek-ai/dsh-tools'
import { apply, QUERY_ACTIONS } from '../src/tools/index.ts'
import { WIRE_TOOLS } from '../src/capabilities.ts'
import { normalizeWireInput } from '../src/service.ts'

describe('Embymedia wire tools', () => {
  it('registers exactly thirteen unique strict replay-stable definitions', () => {
    const definitions: ToolDefinition[] = []
    const invokeTool = vi.fn(async (tool: string) => tool === 'embymedia_health'
      ? { schemaVersion: 1, capabilityId: 'health.check', correlationId: 'correlation', data: {}, warnings: [] }
      : {})
    const context = {
      embymedia: { invokeTool },
      approval: { request: vi.fn() },
      tools: { register(definition: ToolDefinition) { definitions.push(definition); return () => {} } },
    } as unknown as Context
    apply(context)
    expect(definitions.map(definition => definition.name)).toEqual(WIRE_TOOLS)
    expect(new Set(definitions.map(definition => definition.name)).size).toBe(13)
    for (const definition of definitions) {
      expect(definition.output.schema).toMatchObject({ type: 'object', additionalProperties: false })
      expect(definition.timeoutMs).toBeGreaterThan(0)
    }

    const library = definitions.find(definition => definition.name === 'embymedia_library')!
    const task = definitions.find(definition => definition.name === 'embymedia_task')!
    expect(library.parameters).toMatchObject({
      type: 'object', properties: { libraryId: { type: 'string' }, libraryName: { type: 'string' }, itemTypes: { type: 'string' } },
    })
    expect(library.parameters).not.toHaveProperty('properties.taskId')
    expect(task.parameters).toMatchObject({ type: 'object', properties: { taskId: { type: 'string' } } })
    expect(task.parameters).not.toHaveProperty('properties.libraryId')
    const plan = definitions.find(definition => definition.name === 'embymedia_plan')!
    expect(plan.description).toContain('media.delete input={"libraryId":"<library id>","itemIds":["<Emby item id>"]}')
    expect(plan.description).toContain('Do not guess fields or inspect audit/resource logs for schemas')
    expect(library.description).toContain('one list_items call with libraryId or exact libraryName')
    expect(definitions.find(definition => definition.name === 'embymedia_resource')?.description).toContain('search and library_context require query')
  })


  it('advertises only actions with a Host dispatch path', () => {
    expect(QUERY_ACTIONS.embymedia_library).toEqual(['list_libraries', 'summary', 'count_items', 'list_items', 'list_strm', 'gaps_series', 'gaps_library'])
    expect(QUERY_ACTIONS.embymedia_resource).toEqual(['test_115', 'parse_share', 'stage_share', 'snapshot_share', 'inspect_candidate', 'list_entries', 'search', 'library_context', 'duplicates', 'transfer_preview'])
    expect(QUERY_ACTIONS.embymedia_series).toEqual(['status', 'workbench', 'resource_plan', 'gaps_summary'])
    expect(QUERY_ACTIONS.embymedia_analyze).toEqual(['dashboard', 'smart_summary', 'smart_list', 'smart_get', 'smart_policies', 'smart_inspect', 'smart_workbench', 'smart_verify', 'from_task', 'poster_search'])
  })

  it('lets the model execute a stored plan using only its stable id', () => {
    const definitions: ToolDefinition[] = []
    apply({
      embymedia: { invokeTool: vi.fn() },
      approval: { request: vi.fn() },
      tools: { register(definition: ToolDefinition) { definitions.push(definition); return () => {} } },
    } as unknown as Context)
    expect(definitions.find(definition => definition.name === 'embymedia_execute')?.parameters)
      .toEqual({ type: 'object', properties: { planId: { type: 'string' } }, required: ['planId'] })
  })

  it('accepts nested, JSON-string, and top-level query arguments', () => {
    expect(normalizeWireInput({ action: 'count_items', input: { libraryId: '119034' } })).toEqual({ libraryId: '119034' })
    expect(normalizeWireInput({ action: 'count_items', input: '{"libraryId":"119034"}' })).toEqual({ libraryId: '119034' })
    expect(normalizeWireInput({ action: 'count_items', libraryName: '电影', itemTypes: 'Movie' }))
      .toEqual({ libraryName: '电影', itemTypes: 'Movie' })
  })

  it('forwards the agent, call identity, and exact cancellation signal', async () => {
    const definitions: ToolDefinition[] = []
    const invokeTool = vi.fn(async () => ({
      schemaVersion: 1, capabilityId: 'health.check', correlationId: 'correlation', data: { ok: true }, warnings: [],
    }))
    const approval = { request: vi.fn() }
    apply({
      embymedia: { invokeTool },
      approval,
      tools: { register(definition: ToolDefinition) { definitions.push(definition); return () => {} } },
    } as unknown as Context)
    const signal = new AbortController().signal
    const agent = { id: 'session' }
    const health = definitions.find(definition => definition.name === 'embymedia_health')!
    await health.execute({}, { agent, callId: 'call-1', signal } as unknown as ToolRunContext)
    expect(invokeTool).toHaveBeenCalledWith('embymedia_health', {}, { agent, callId: 'call-1', signal, approval })
  })

  it('classifies task cancellation and every write tool as exclusive', () => {
    const definitions: ToolDefinition[] = []
    apply({
      embymedia: { invokeTool: vi.fn() },
      approval: { request: vi.fn() },
      tools: { register(definition: ToolDefinition) { definitions.push(definition); return () => {} } },
    } as unknown as Context)
    const task = definitions.find(definition => definition.name === 'embymedia_task')!
    expect(task.isConcurrencySafe?.({ action: 'list' })).toBe(true)
    expect(task.isConcurrencySafe?.({ action: 'cancel' })).toBe(false)
    for (const name of ['embymedia_plan', 'embymedia_execute', 'embymedia_verify']) {
      expect(definitions.find(definition => definition.name === name)?.isConcurrencySafe).toBeUndefined()
    }
  })
})

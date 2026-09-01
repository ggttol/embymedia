import type { Context } from '@deepseek-ai/cordis'
import { defineTool } from '@deepseek-ai/dsh-tools'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { OPERATION_KINDS, type WireTool } from '../capabilities.ts'
import type { JsonValue } from '../schemas.ts'
import { EmbymediaError } from '../errors.ts'
import type { ApprovalRequester } from '../execution.ts'
import { operationInputGuide } from '../operations.ts'

export const name = 'embymedia-tools'
export const inject = ['embymedia', 'tools', 'approval']

interface WireExecution {
  readonly agent: Agent
  readonly callId: string
  readonly signal: AbortSignal
  readonly approval: ApprovalRequester
}

export interface EmbymediaWireService {
  invokeTool(tool: WireTool, args: Readonly<Record<string, unknown>>, execution: WireExecution): Promise<JsonValue>
}

const QUERY_OUTPUT_SCHEMA = {
  type: 'object' as const,
  additionalProperties: false,
  properties: {
    schemaVersion: { type: 'integer' as const, required: true, const: 1 },
    capabilityId: { type: 'string' as const, required: true },
    correlationId: { type: 'string' as const, required: true },
    data: { type: 'json' as const, required: true },
    warnings: { type: 'array' as const, required: true, items: { type: 'string' as const } },
    page: {
      type: 'object' as const,
      additionalProperties: false,
      properties: {
        cursor: { type: 'string' as const },
        nextCursor: { type: 'string' as const },
        limit: { type: 'integer' as const, required: true },
        total: { type: 'integer' as const },
      },
    },
  },
} as const

const OPERATION_OUTPUT_SCHEMA = {
  type: 'object' as const,
  additionalProperties: false,
  properties: {
    schemaVersion: { type: 'integer' as const, required: true, const: 1 },
    id: { type: 'string' as const, required: true },
    kind: { type: 'string' as const, required: true, enum: [...OPERATION_KINDS] },
    status: { type: 'string' as const, required: true, enum: ['previewed', 'approved', 'cancelled', 'expired', 'queued', 'running', 'verifying', 'done', 'partial', 'failed', 'interrupted'] },
    previewHash: { type: 'string' as const, required: true },
    risk: { type: 'string' as const, required: true, enum: ['low', 'medium', 'high', 'critical'] },
    destructive: { type: 'boolean' as const, required: true },
    reversible: { type: 'boolean' as const, required: true },
    confirmation: { type: 'json' as const, required: true },
    targets: { type: 'array' as const, required: true, items: { type: 'json' as const } },
    steps: { type: 'array' as const, required: true, items: { type: 'string' as const } },
    verification: { type: 'json' as const, required: true },
    expiresAt: { type: 'string' as const, required: true },
    correlationId: { type: 'string' as const, required: true },
    taskId: { type: 'string' as const },
    result: { type: 'json' as const },
    error: { type: 'json' as const },
  },
} as const

export const QUERY_ACTIONS: Readonly<Record<Exclude<WireTool, 'embymedia_plan' | 'embymedia_execute' | 'embymedia_verify' | 'embymedia_health'>, readonly string[]>> = {
  embymedia_library: ['list_libraries', 'summary', 'count_items', 'list_items', 'list_strm', 'gaps_series', 'gaps_library'],
  embymedia_resource: ['test_115', 'parse_share', 'snapshot_share', 'inspect_candidate', 'list_entries', 'search', 'library_context', 'duplicates', 'transfer_preview'],
  embymedia_series: ['status', 'workbench', 'resource_plan', 'gaps_summary'],
  embymedia_analyze: ['dashboard', 'smart_summary', 'smart_list', 'smart_get', 'smart_policies', 'smart_inspect', 'smart_workbench', 'smart_verify', 'from_task', 'poster_search'],
  embymedia_task: ['list', 'get', 'cancel'],
  embymedia_audit: ['logs', 'audit', 'undo'],
  embymedia_user: ['list', 'get_policy'],
  embymedia_schedule: ['list', 'get'],
  embymedia_config: ['get', 'export', 'diagnostics', 'credential_status'],
}

function execution(
  exec: { readonly agent?: Agent; readonly callId: unknown; readonly signal: AbortSignal },
  approval: ApprovalRequester,
): WireExecution {
  if (exec.agent === undefined) throw new EmbymediaError('AUTH_REQUIRED', 'Embymedia tools require an agent session')
  return { agent: exec.agent, callId: String(exec.callId), signal: exec.signal, approval }
}

function render(value: JsonValue) {
  return [{ type: 'text' as const, text: JSON.stringify(value, null, 2) }]
}

export function apply(ctx: Context): void {
  const service = ctx.embymedia as unknown as EmbymediaWireService
  const approval = ctx.approval as ApprovalRequester
  ctx.tools.register(defineTool({
    name: 'embymedia_health',
    description: 'Check selected EmbyMedia dependencies without changing state.',
    parameters: {
      checks: { type: 'array', items: { type: 'string', enum: ['database', 'emby', 'tmdb', 'resource', 'c115', 'storage', 'scheduler', 'backup'] } },
    },
    timeoutMs: 60_000,
    isConcurrencySafe: () => true,
    output: { schema: QUERY_OUTPUT_SCHEMA, render: (_args, value) => render(value as JsonValue) },
    execute: async (args, exec) => await service.invokeTool('embymedia_health', args, execution(exec, approval)) as never,
  }))

  for (const [tool, actions] of Object.entries(QUERY_ACTIONS) as Array<[keyof typeof QUERY_ACTIONS, readonly string[]]>) {
    ctx.tools.register(defineTool({
      name: tool,
      description: tool === 'embymedia_resource'
        ? 'Query resources. search and library_context require query. inspect_candidate requires same-session candidateId. list_entries requires cid. Search returns opaque candidateId values for resource.add_new or series.update; protected links never go through parse_share or snapshot_share.'
        : tool === 'embymedia_library'
          ? 'Query libraries. Use list_libraries or summary first, then one list_items call with libraryId or exact libraryName and optional search to resolve Emby item IDs; search ignores punctuation, quote, symbol, spacing, width, and case differences. list_strm is a bounded filesystem inventory, not an item lookup. gaps_series requires libraryId+seriesId.'
          : tool === 'embymedia_series'
            ? 'Query continuing-Series state. gaps_summary requires libraryId; resource_plan requires libraryId+seriesId and returns the exact series.update inputs and opaque candidates.'
            : tool === 'embymedia_analyze'
              ? 'Query analysis. poster_search requires query; smart_get and smart_inspect require actionId; from_task requires taskId; dashboard needs no input.'
              : tool === 'embymedia_task'
                ? 'Query or cancel tasks. list needs no input; get and cancel require taskId.'
                : `Query the ${tool.slice('embymedia_'.length)} domain. Mutations are rejected except task cancellation.`,
      parameters: {
        action: { type: 'string', required: true, enum: [...actions] },
        input: { type: 'json' },
        cursor: { type: 'string' },
        limit: { type: 'integer' },
        ...(tool === 'embymedia_library' ? {
          libraryId: { type: 'string' as const }, libraryName: { type: 'string' as const }, seriesId: { type: 'string' as const },
          library: { type: 'string' as const }, itemTypes: { type: 'string' as const }, search: { type: 'string' as const },
          mode: { type: 'string' as const, enum: ['season', 'absolute'] },
        } : {}),
        ...(tool === 'embymedia_resource' ? {
          query: { type: 'string' as const }, q: { type: 'string' as const }, libraryId: { type: 'string' as const },
          diskType: { type: 'string' as const }, exact: { type: 'boolean' as const }, sort: { type: 'string' as const },
          offset: { type: 'integer' as const }, url: { type: 'string' as const }, cid: { type: 'string' as const }, candidateId: { type: 'string' as const },
          fileIds: { type: 'array' as const, items: { type: 'string' as const } },
        } : {}),
        ...(tool === 'embymedia_series' ? {
          libraryId: { type: 'string' as const }, seriesId: { type: 'string' as const },
          mode: { type: 'string' as const, enum: ['season', 'absolute'] },
        } : {}),
        ...(tool === 'embymedia_analyze' ? {
          query: { type: 'string' as const }, name: { type: 'string' as const },
          type: { type: 'string' as const, enum: ['tv', 'movie'] }, year: { type: 'integer' as const },
          actionId: { type: 'string' as const }, taskId: { type: 'string' as const },
        } : {}),
        ...(tool === 'embymedia_task' ? { taskId: { type: 'string' as const } } : {}),
        ...(tool === 'embymedia_user' ? { userId: { type: 'string' as const } } : {}),
        ...(tool === 'embymedia_schedule' ? { scheduleId: { type: 'string' as const } } : {}),
      },
      timeoutMs: 5 * 60_000,
      isConcurrencySafe: args => tool !== 'embymedia_task' || args.action !== 'cancel',
      output: { schema: QUERY_OUTPUT_SCHEMA, render: (_args, value) => render(value as JsonValue) },
      execute: async (args, exec) => await service.invokeTool(tool, args, execution(exec, approval)) as never,
    }))
  }

  ctx.tools.register(defineTool({
    name: 'embymedia_plan',
    description: `Create a canonical preview without executing it. For movie deletion, first use embymedia_library list_items, then call kind=media.delete with input={"libraryId":"<library id>","itemIds":["<Emby item id>"]}; an isolated movie root folder is supported. Do not guess fields or inspect audit/resource logs for schemas. ${operationInputGuide()}`,
    parameters: {
      kind: { type: 'string', required: true, enum: [...OPERATION_KINDS] },
      input: { type: 'json', required: true },
    },
    timeoutMs: 5 * 60_000,
    output: { schema: OPERATION_OUTPUT_SCHEMA, render: (_args, value) => render(value as JsonValue) },
    execute: async (args, exec) => await service.invokeTool('embymedia_plan', args, execution(exec, approval)) as never,
  }))

  ctx.tools.register(defineTool({
    name: 'embymedia_execute',
    description: 'Execute one stored canonical plan by planId. The Host binds the persisted hash and confirmation, performs approval when required, and runs independent verification before returning.',
    parameters: {
      planId: { type: 'string', required: true },
    },
    timeoutMs: 60 * 60_000,
    output: { schema: OPERATION_OUTPUT_SCHEMA, render: (_args, value) => render(value as JsonValue) },
    execute: async (args, exec) => await service.invokeTool('embymedia_execute', args, execution(exec, approval)) as never,
  }))

  ctx.tools.register(defineTool({
    name: 'embymedia_verify',
    description: 'Independently re-read external and database facts for a previously executed plan.',
    parameters: { planId: { type: 'string', required: true } },
    timeoutMs: 30 * 60_000,
    output: { schema: OPERATION_OUTPUT_SCHEMA, render: (_args, value) => render(value as JsonValue) },
    execute: async (args, exec) => await service.invokeTool('embymedia_verify', args, execution(exec, approval)) as never,
  }))

}

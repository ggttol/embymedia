#!/usr/bin/env node
import { randomUUID } from 'node:crypto'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js'
import { z } from 'zod'

const BRIDGE_URL = process.env.EMBYMEDIA_BRIDGE_URL ?? 'http://127.0.0.1:3080/internal/embymedia/tool'
const SESSION_ID = process.env.EMBYMEDIA_MCP_SESSION_ID ?? 'hermes-weixin'
const actions = {
  embymedia_library: ['list_libraries', 'summary', 'count_items', 'list_items', 'list_strm', 'gaps_series', 'gaps_library'],
  embymedia_resource: ['test_115', 'parse_share', 'snapshot_share', 'inspect_candidate', 'list_entries', 'search', 'library_context', 'duplicates', 'transfer_preview'],
  embymedia_series: ['status', 'workbench', 'resource_plan', 'gaps_summary'],
  embymedia_analyze: ['dashboard', 'smart_summary', 'smart_list', 'smart_get', 'smart_policies', 'smart_inspect', 'smart_workbench', 'smart_verify', 'from_task', 'poster_search'],
  embymedia_task: ['list', 'get', 'cancel'], embymedia_audit: ['logs', 'audit', 'undo'],
  embymedia_user: ['list', 'get_policy'], embymedia_schedule: ['list', 'get'],
  embymedia_config: ['get', 'export', 'diagnostics', 'credential_status'],
}
const operationKinds = [
  'library.create', 'library.scan', 'resource.save_share', 'resource.offline', 'resource.add_new', 'series.update', 'series.archive',
  'poster.apply', 'poster.fix_batch', 'metadata.refresh', 'media.delete', 'media.move', 'dedup.delete', 'dedup.replace',
  'cleanup.empty_strm', 'cleanup.empty_cloud', 'cleanup.execute', 'user.create', 'user.policy_update', 'user.delete',
  'schedule.upsert', 'schedule.delete', 'schedule.run', 'config.update', 'config.credential_rotate',
  'smart_action.policy_update', 'smart_action.dismiss', 'undo.execute',
]

async function callBridge(tool, args) {
  const response = await fetch(BRIDGE_URL, {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ tool, args, sessionId: SESSION_ID, callId: randomUUID() }),
    signal: AbortSignal.timeout(65 * 60_000),
  })
  const payload = await response.json().catch(() => ({ ok: false, error: { code: 'UPSTREAM_UNAVAILABLE', message: `bridge returned HTTP ${response.status}` } }))
  if (!response.ok || payload.ok !== true) {
    return { isError: true, content: [{ type: 'text', text: JSON.stringify(payload.error ?? { code: 'UPSTREAM_UNAVAILABLE', message: `bridge returned HTTP ${response.status}` }, null, 2) }] }
  }
  return { content: [{ type: 'text', text: JSON.stringify(payload.result, null, 2) }] }
}

const commonShape = {
  input: z.record(z.string(), z.unknown()).optional(), cursor: z.string().optional(), limit: z.number().int().optional(),
  libraryId: z.string().optional(), libraryName: z.string().optional(), library: z.string().optional(), seriesId: z.string().optional(),
  itemTypes: z.string().optional(), search: z.string().optional(), mode: z.enum(['season', 'absolute']).optional(),
  query: z.string().optional(), q: z.string().optional(), diskType: z.string().optional(), exact: z.boolean().optional(),
  sort: z.string().optional(), offset: z.number().int().optional(), url: z.string().optional(), cid: z.string().optional(),
  candidateId: z.string().optional(), fileIds: z.array(z.string()).optional(), name: z.string().optional(),
  type: z.enum(['tv', 'movie']).optional(), year: z.number().int().optional(), actionId: z.string().optional(),
  taskId: z.string().optional(), userId: z.string().optional(), scheduleId: z.string().optional(),
}
const descriptions = {
  embymedia_library: 'Query libraries. count_items gives exact counts by itemTypes (Movie, Series, Episode); list_items resolves IDs; gaps_series/gaps_library query missing episodes.',
  embymedia_resource: 'Query 115 resources. search returns same-session opaque candidateId values; inspect_candidate verifies leaf evidence; candidates feed resource.add_new or series.update.',
  embymedia_series: 'Query continuing-series state. gaps_summary requires libraryId; resource_plan requires libraryId+seriesId.',
  embymedia_analyze: 'Query dashboard, smart actions, poster search, and task-derived analysis.',
  embymedia_task: 'List/get tasks or cancel one interactive task by taskId.',
  embymedia_audit: 'Read bounded logs, audit records, or undo entries.', embymedia_user: 'List Emby users or inspect one policy.',
  embymedia_schedule: 'List/get schedules. Scheduler execution remains disabled.',
  embymedia_config: 'Read masked configuration, diagnostics, exports, or credential status. Secrets are never returned.',
}

const server = new McpServer({ name: 'embymedia', version: '1.0.0' })
server.tool('embymedia_health', 'Check selected EmbyMedia dependencies without changing state.', {
  checks: z.array(z.enum(['database', 'emby', 'tmdb', 'resource', 'c115', 'storage', 'scheduler', 'backup'])).optional(),
}, args => callBridge('embymedia_health', args))
for (const [tool, toolActions] of Object.entries(actions)) {
  server.tool(tool, descriptions[tool], { action: z.enum(toolActions), ...commonShape }, args => callBridge(tool, args))
}
server.tool('embymedia_plan', 'Create the same canonical operation preview used by DSH without executing it.', {
  kind: z.enum(operationKinds), input: z.record(z.string(), z.unknown()),
}, args => callBridge('embymedia_plan', args))
server.tool('embymedia_execute', 'Execute a canonical plan by planId. Self-use auto-allows approval; revalidation, audit, and verification remain enforced.', {
  planId: z.string(),
}, args => callBridge('embymedia_execute', args))
server.tool('embymedia_verify', 'Independently re-read external and database facts for an executed plan.', {
  planId: z.string(),
}, args => callBridge('embymedia_verify', args))
await server.connect(new StdioServerTransport())

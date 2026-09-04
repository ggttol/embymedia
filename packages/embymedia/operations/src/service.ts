import { randomUUID } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { Context, Service } from '@deepseek-ai/cordis'
import { TypertRemoteService } from '@deepseek-ai/dsh-typert-protocol'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type {} from '@deepseek-ai/dsh-credentials'
import type {} from '@deepseek-ai/dsh-host-webserver'
import type {} from '@deepseek-ai/dsh-typert-registry'
import { ConfigSchema, resolveConfig, type Config, type ResolvedConfig } from './config.ts'
import { Database } from './database/index.ts'
import { EmbymediaCredentialRecords, EmbymediaSecretsRemote } from './credentials.ts'
import { OperationPlanStore } from './plans.ts'
import { EmbyClient } from './clients/emby.ts'
import { CloudDriveWebhookService } from './webhook.ts'
import { PersistentJobService } from './jobs.ts'
import { BusinessMaintenanceService } from './maintenance.ts'
import type {} from '@deepseek-ai/dsh-user-approval'
import { OPERATION_KINDS, WIRE_TOOLS, type OperationKind, type WireTool } from './capabilities.ts'
import { OperationPlanner, prepareOperationInput } from './operations.ts'
import { OperationExecutionService, SAFE_AUTO_OPERATION_KINDS, type ApprovalRequester } from './execution.ts'
import { OperationVerificationService } from './verification.ts'
import { parseC115Share, C115Client } from './clients/c115.ts'
import { ResourceApiClient, type ResourceSearchItem, type ResourceSearchResult } from './clients/resource-api.ts'
import { TmdbClient } from './clients/tmdb.ts'
import { EmbymediaError } from './errors.ts'
import { SCHEMA_VERSION, isCredentialId, type JsonValue, type OperationProjection } from './schemas.ts'
import { maskConfiguration } from './domain/read.ts'
import { CredentialAvailabilityService } from './domain/admin.ts'
import { EmbymediaOperationRuntime } from './operation-runtime.ts'
import { EmbymediaAdminRemote } from './admin-remote.ts'
import { isVideoFileName } from './domain/library.ts'
import { absoluteEpisodeKeysFromText, episodeKeysFromText } from './domain/series.ts'

const INTERNAL_TOOL_MAX_BYTES = 1024 * 1024
const WIRE_TOOL_SET = new Set<string>(WIRE_TOOLS)
const RESOURCE_CANDIDATE_TTL_MS = 15 * 60_000
const MAX_STAGED_RESOURCE_CANDIDATES = 1_000
const MAX_STAGED_PLAN_SECRETS = 1_000
const DISPLAY_CONTROL = /[\u0000-\u001F\u007F-\u009F\u202A-\u202E\u2066-\u2069]/gu
const DISPLAY_ANSI = /\u001B\[[0-?]*[ -/]*[@-~]/gu
const DISPLAY_URL = /https?:\/\/\S+/giu
const DISPLAY_SECRET = /\b(password|pwd|receive[_-]?code|access[_-]?code)\s*[:=]\s*\S+/giu

function safeDisplay(value: string, maximum = 512): string {
  const normalized = value.normalize('NFC')
    .replace(DISPLAY_ANSI, '')
    .replace(DISPLAY_CONTROL, ' ')
    .replace(DISPLAY_URL, '[redacted-url]')
    .replace(DISPLAY_SECRET, '$1=[redacted]')
    .replace(/\s+/gu, ' ')
    .trim()
  const points = [...normalized]
  return points.length <= maximum ? normalized : `${points.slice(0, Math.max(0, maximum - 1)).join('')}…`
}

export const name = 'embymedia'
export const inject = ['webServer', 'agents', 'credentials', 'typert']

export interface EmbymediaHealth {
  readonly schemaVersion: typeof SCHEMA_VERSION
  readonly status: 'ok' | 'starting' | 'stopping'
  readonly writeMode: ResolvedConfig['writeMode']
  readonly scheduler: 'starting' | 'ready' | 'disabled-for-cutover'
}

export interface WireInvocation {
  readonly agent: Agent
  readonly callId: string
  readonly signal: AbortSignal
  readonly approval: ApprovalRequester
}

function inputString(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.trim().length === 0) throw new EmbymediaError('INVALID_INPUT', `${label} is required`)
  return value.trim()
}

function inputObject(value: unknown, label: string): Readonly<Record<string, unknown>> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new EmbymediaError('INVALID_INPUT', `${label} must be an object`)
  return value as Readonly<Record<string, unknown>>
}

export function normalizeWireInput(args: Readonly<Record<string, unknown>>): Readonly<Record<string, unknown>> {
  let nested: Readonly<Record<string, unknown>> = {}
  if (typeof args.input === 'object' && args.input !== null && !Array.isArray(args.input)) {
    nested = args.input as Readonly<Record<string, unknown>>
  } else if (typeof args.input === 'string') {
    try {
      const parsed: unknown = JSON.parse(args.input)
      if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) nested = parsed as Readonly<Record<string, unknown>>
    } catch {}
  }
  const direct = Object.fromEntries(Object.entries(args).filter(([key]) => !['action', 'input', 'cursor', 'limit'].includes(key)))
  return { ...direct, ...nested }
}

declare module '@deepseek-ai/cordis' {
  interface Context {
    embymedia: EmbymediaService
  }
}

interface StagedPlanSecret {
  readonly sessionId: string
  readonly value: string | readonly (string | undefined)[]
  readonly expiresAt: number
  readonly timer: NodeJS.Timeout
}

interface StagedResourceCandidate {
  readonly sessionId: string
  readonly item: ResourceSearchItem
  readonly expiresAt: number
  readonly timer: NodeJS.Timeout
}

export class EmbymediaService extends TypertRemoteService {
  static inject = inject
  static Config = ConfigSchema

  private readonly config: ResolvedConfig
  private readonly lifecycle: AbortController = new AbortController()
  private lifecycleStatus: EmbymediaHealth['status'] = 'starting'
  private schedulerReady = false
  private readonly backgroundStops: Array<() => Promise<void>> = []
  private database!: Database
  private credentialRecords!: EmbymediaCredentialRecords
  private plans!: OperationPlanStore
  private webhook!: CloudDriveWebhookService
  private jobs!: PersistentJobService
  private maintenance!: BusinessMaintenanceService
  private planner!: OperationPlanner
  private verification!: OperationVerificationService
  private operationRuntime!: EmbymediaOperationRuntime
  private credentialAvailability!: CredentialAvailabilityService
  private readonly planSecrets = new Map<string, StagedPlanSecret>()
  private readonly resourceCandidates = new Map<string, StagedResourceCandidate>()

  constructor(ctx: Context, config: Config = {}) {
    super(ctx, 'embymedia')
    this.config = resolveConfig(config)
  }

  protected async [Service.init](): Promise<void> {
    const unregisterHealth = this.ctx.webServer.register({
      kind: 'exact',
      path: '/internal/embymedia/health',
      handler: (_request, response) => {
        const body = JSON.stringify(this.health())
        response.writeHead(200, {
          'cache-control': 'no-store',
          'content-type': 'application/json; charset=utf-8',
          'content-length': Buffer.byteLength(body),
        })
        response.end(body)
      },
    })
    this.ctx.effect(() => unregisterHealth, 'embymedia.health-route')
    await this.startHostState()
    const unregisterInternalTool = this.ctx.webServer.register({
      kind: 'exact',
      path: '/internal/embymedia/tool',
      handler: (request, response) => { void this.handleInternalTool(request, response) },
    })
    this.ctx.effect(() => unregisterInternalTool, 'embymedia.internal-tool-route')
    this.ctx.effect(() => async () => {
      this.lifecycleStatus = 'stopping'
      this.lifecycle.abort(new Error('embymedia service stopping'))
      this.clearPlanSecrets()
      await this.stopBackgroundWork()
    }, 'embymedia.quiesce')
    this.lifecycleStatus = 'ok'
  }

  private async handleInternalTool(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const reply = (status: number, value: unknown): void => {
      const body = JSON.stringify(value)
      response.writeHead(status, {
        'cache-control': 'no-store',
        'content-type': 'application/json; charset=utf-8',
        'content-length': Buffer.byteLength(body),
      })
      response.end(body)
    }
    const remote = request.socket.remoteAddress ?? ''
    if (!['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(remote)) {
      reply(403, { ok: false, error: { code: 'POLICY_DENIED', message: 'internal tool bridge requires loopback' } })
      return
    }
    if (request.method !== 'POST') {
      reply(405, { ok: false, error: { code: 'NOT_FOUND', message: 'internal tool bridge accepts POST only' } })
      return
    }
    const contentType = request.headers['content-type']?.split(';', 1)[0]?.trim().toLowerCase()
    if (contentType !== 'application/json') {
      reply(400, { ok: false, error: { code: 'INVALID_INPUT', message: 'internal tool bridge requires application/json' } })
      return
    }
    try {
      const chunks: Buffer[] = []
      let bytes = 0
      for await (const chunk of request) {
        const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)
        bytes += buffer.length
        if (bytes > INTERNAL_TOOL_MAX_BYTES) throw new EmbymediaError('INVALID_INPUT', 'internal tool request exceeds 1 MiB')
        chunks.push(buffer)
      }
      const payload = inputObject(JSON.parse(Buffer.concat(chunks).toString('utf8')) as unknown, 'internal tool request')
      const toolValue = inputString(payload.tool, 'tool')
      if (!WIRE_TOOL_SET.has(toolValue)) throw new EmbymediaError('NOT_FOUND', `unknown tool ${toolValue}`)
      const args = payload.args === undefined ? {} : inputObject(payload.args, 'args')
      const sessionId = typeof payload.sessionId === 'string' && /^[A-Za-z0-9:_-]{1,128}$/u.test(payload.sessionId)
        ? payload.sessionId
        : 'hermes-weixin'
      const callId = typeof payload.callId === 'string' && payload.callId.trim().length > 0 ? payload.callId.trim() : randomUUID()
      const agent = { id: sessionId } as Agent
      const approval: ApprovalRequester = { request: async () => 'allowed-once' }
      const requestAbort = new AbortController()
      const abortOnDisconnect = (): void => { requestAbort.abort(new Error('client disconnected')) }
      request.on('aborted', abortOnDisconnect)
      response.on('close', () => {
        if (!response.writableEnded) abortOnDisconnect()
      })
      const signal = AbortSignal.any([this.lifecycle.signal, requestAbort.signal])
      const result = await this.invokeTool(toolValue as WireTool, args, {
        agent,
        callId,
        signal,
        approval,
      })
      reply(200, { ok: true, result })
    } catch (error) {
      const failure = error instanceof EmbymediaError ? error : new EmbymediaError('UPSTREAM_UNAVAILABLE', error instanceof Error ? error.message : String(error))
      reply(failure.httpStatus === 499 ? 499 : failure.httpStatus, { ok: false, error: failure.toJSON() })
    }
  }

  health(): EmbymediaHealth {
    return {
      schemaVersion: SCHEMA_VERSION,
      status: this.lifecycleStatus,
      writeMode: this.config.writeMode,
      scheduler: (this.config.writeMode === 'disabled' || !this.config.schedulerEnabled)
        ? 'disabled-for-cutover'
        : this.schedulerReady ? 'ready' : 'starting',
    }
  }

  protected async startHostState(): Promise<void> {
    this.lifecycle.signal.throwIfAborted()
    const connectionString = process.env[this.config.databaseUrlEnv]
    if (connectionString === undefined || connectionString.trim().length === 0) {
      throw new Error(`${this.config.databaseUrlEnv} is required`)
    }
    this.database = new Database(connectionString, this.config.taskConcurrency + this.config.cloudConcurrency + 2)
    this.ctx.effect(() => () => this.database.close(), 'embymedia.database')
    await this.database.initialize(this.lifecycle.signal)
    const recovered = await this.database.recoverInterrupted()
    this.plans = new OperationPlanStore(this.database)
    await this.plans.expireDue()
    if (recovered.tasks > 0 || recovered.plans > 0) {
      this.ctx.logger.warn(
        `embymedia: marked ${String(recovered.tasks)} task(s) and ${String(recovered.plans)} plan(s) interrupted`,
      )
    }
    this.credentialRecords = new EmbymediaCredentialRecords(this.ctx.credentials, this.database)
    const orphanCount = await this.credentialRecords.cleanupOrphans()
    if (orphanCount > 0) this.ctx.logger.info(`embymedia: removed ${String(orphanCount)} orphan pending credential(s)`)
    void new EmbymediaSecretsRemote(this.ctx, this.credentialRecords)
    this.jobs = new PersistentJobService(this.database, this.config.taskConcurrency)
    this.registerBackgroundStop(() => this.jobs.close())
    this.maintenance = new BusinessMaintenanceService(this.database, (error) => {
      this.ctx.logger.error(`embymedia maintenance failed: ${error instanceof Error ? error.message : String(error)}`)
    })
    this.maintenance.start()
    this.registerBackgroundStop(() => this.maintenance.close())
    this.webhook = new CloudDriveWebhookService(
      this.database,
      this.credentialRecords,
      this.config.mediaRoot,
      this.config.strmRoot,
      async () => new EmbyClient({
        baseUrl: this.config.embyBaseUrl,
        token: await this.credentialRecords.read('emby-api-key'),
      }),
      undefined,
      () => this.config.writeMode === 'enabled',
    )
    this.ctx.effect(() => this.webhook.register(this.ctx.webServer), 'embymedia.webhook-route')
    this.ctx.effect(() => () => { this.webhook.endRotation() }, 'embymedia.webhook-rotation')
    this.operationRuntime = new EmbymediaOperationRuntime({
      database: this.database,
      config: this.config,
      credentials: this.credentialRecords,
      embyClient: async () => this.embyClient(),
      c115Client: async () => this.c115Client(),
      resourceClient: async () => this.resourceApiClient(),
      planSecret: planId => this.readPlanSecret(planId),
    })
    this.credentialAvailability = new CredentialAvailabilityService(this.credentialRecords, {
      validate: (id, value, signal) => this.operationRuntime.validateCredential(id, value, signal),
    })
    void new EmbymediaAdminRemote(this.ctx, this)
    this.planner = new OperationPlanner(this.plans, this.config, this.operationRuntime.previewers)
    this.verification = new OperationVerificationService(
      this.database,
      this.plans,
      this.operationRuntime.verifiers,
      this.config.principal,
    )
  }

  async invokeTool(tool: WireTool, args: Readonly<Record<string, unknown>>, invocation: WireInvocation): Promise<JsonValue> {
    invocation.signal.throwIfAborted()
    if (tool === 'embymedia_plan') {
      const kind = args.kind as OperationKind
      const resolved = this.resolveResourceCandidate(kind, args.input, invocation.agent.id)
      const prepared = prepareOperationInput(kind, resolved.input)
      const plan = await this.planner.create(kind, prepared.previewValue, {
        principal: this.config.principal,
        sessionId: invocation.agent.id,
        idempotencyKey: invocation.callId,
      }, invocation.signal, prepared.persistedValue)
      if (prepared.secrets !== undefined) this.stagePlanSecret(plan, invocation.agent.id, prepared.secrets)
      else if (prepared.secret !== undefined) this.stagePlanSecret(plan, invocation.agent.id, prepared.secret)
      for (const candidateId of resolved.candidateIds) this.dropResourceCandidate(candidateId)
      return plan as unknown as JsonValue
    }
    if (tool === 'embymedia_execute') {
      const execution = new OperationExecutionService(
        this.database,
        this.plans,
        invocation.approval,
        this.operationRuntime.handlers,
        this.config.principal,
        (plan, signal) => this.operationRuntime.revalidate(plan, signal, {
          principal: this.config.principal,
          sessionId: invocation.agent.id,
          idempotencyKey: invocation.callId,
        }),
        this.config.approvalMode === 'safe-auto' ? SAFE_AUTO_OPERATION_KINDS : new Set(),
      )
      const planId = inputString(args.planId, 'plan id')
      const canonical = await this.plans.get(planId)
      try {
        const executed = await execution.execute(invocation.agent, {
          planId,
          previewHash: canonical.previewHash,
          confirmation: canonical.confirmation,
        }, invocation.signal)
        const result = executed.status === 'verifying'
          ? await this.verification.verify(planId, invocation.agent.id, invocation.signal)
          : executed
        if (result.status !== 'previewed') this.dropPlanSecret(planId)
        return result as unknown as JsonValue
      } catch (error) {
        const current = await this.plans.get(planId).catch(() => undefined)
        if (current !== undefined && current.status !== 'previewed') this.dropPlanSecret(planId)
        throw error
      }
    }
    if (tool === 'embymedia_verify') {
      return this.verification.verify(String(args.planId), invocation.agent.id, invocation.signal) as unknown as JsonValue
    }
    if (tool === 'embymedia_health') return this.wireResult('health.check', {
      health: this.health() as unknown as JsonValue,
      operations: this.operationSupport(),
    })

    const action = String(args.action)
    const input = normalizeWireInput(args)
    const limit = typeof args.limit === 'number' ? Math.min(Math.max(args.limit, 1), 500) : 100
    if (tool === 'embymedia_task') {
      if (action === 'list') return this.wireResult('task.list', await this.jobs.listPage(limit, typeof args.cursor === 'string' ? args.cursor : undefined) as unknown as JsonValue)
      if (action === 'get') return this.wireResult('task.get', await this.jobs.get(inputString(input.taskId, 'task id')) as unknown as JsonValue)
      if (action === 'cancel') return this.wireResult('task.cancel', await this.jobs.cancel(inputString(input.taskId, 'task id'), this.config.principal) as unknown as JsonValue)
    }
    if (tool === 'embymedia_audit') {
      const table = action === 'logs' ? 'app_logs' : action === 'audit' ? 'audit_logs' : action === 'undo' ? 'undo_entries' : undefined
      if (table !== undefined) {
        const rows = await this.database.query<Record<string, JsonValue>>(`SELECT * FROM ${table} ORDER BY created_at DESC LIMIT $1`, [limit])
        return this.wireResult(`audit.${action}`, rows.rows.map(row => maskConfiguration(row)))
      }
    }
    if (tool === 'embymedia_config') {
      if (action === 'credential_status') return this.wireResult('config.credential_status', await this.credentialRecords.status() as unknown as JsonValue)
      const rows = await this.database.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings ORDER BY key')
      const settings = maskConfiguration(Object.fromEntries(rows.rows.map(row => [row.key, row.value])))
      if (action === 'get') return this.wireResult('config.get', settings)
      if (action === 'export') return this.wireResult('config.export', { settings, exportedAt: new Date().toISOString() })
      if (action === 'diagnostics') return this.wireResult('config.diagnostics', { configuration: settings, health: this.health() } as unknown as JsonValue)
    }
    if (tool === 'embymedia_user') {
      const emby = await this.embyClient()
      const users = await emby.users(invocation.signal)
      return this.wireResult(action === 'get_policy' ? 'user.get-policy' : 'user.list', action === 'get_policy'
        ? (users.find(user => user.Id === inputString(input.userId, 'user id'))?.Policy ?? null) as JsonValue
        : users as unknown as JsonValue)
    }
    if (tool === 'embymedia_schedule') {
      const rows = action === 'get'
        ? await this.database.query<Record<string, JsonValue>>('SELECT * FROM schedule_jobs WHERE id=$1', [inputString(input.scheduleId, 'schedule id')])
        : await this.database.query<Record<string, JsonValue>>('SELECT * FROM schedule_jobs ORDER BY created_at')
      return this.wireResult(`schedule.${action}`, rows.rows)
    }
    if (tool === 'embymedia_library') {
      if (action === 'list_libraries') return this.wireResult('library.list', await this.operationRuntime.libraries.libraries(invocation.signal) as unknown as JsonValue)
      if (action === 'summary') return this.wireResult('library.summary', await this.operationRuntime.libraries.summary(invocation.signal) as unknown as JsonValue)
      const itemLimit = typeof input.limit === 'number' ? Math.min(Math.max(input.limit, 1), 500) : limit
      if (action === 'list_strm') return this.wireResult('library.strm', await this.operationRuntime.libraries.listStrm(typeof input.library === 'string' ? input.library : undefined, itemLimit, invocation.signal) as unknown as JsonValue)
      const search = typeof input.search === 'string' ? input.search : undefined
      let libraryId = typeof input.libraryId === 'string' && input.libraryId.trim().length > 0 ? input.libraryId.trim() : undefined
      if (libraryId === undefined && typeof input.libraryName === 'string' && input.libraryName.trim().length > 0) {
        const name = input.libraryName.trim()
        const matches = (await this.operationRuntime.libraries.libraries(invocation.signal)).filter(library => library.name === name)
        if (matches.length !== 1) throw new EmbymediaError(matches.length === 0 ? 'NOT_FOUND' : 'CONFLICT', `library name ${name} resolved ${String(matches.length)} matches`)
        libraryId = matches[0]!.id
      }
      if (libraryId === undefined) throw new EmbymediaError('INVALID_INPUT', 'library id or exact library name is required')
      const mode = input.mode === 'absolute' ? 'absolute' : 'season'
      if (action === 'gaps_series') return this.wireResult('library.gaps-series', await this.operationRuntime.series.detail(
        libraryId, inputString(input.seriesId, 'series id'), mode, invocation.signal,
      ) as unknown as JsonValue)
      if (action === 'gaps_library') return this.wireResult('library.gaps-library', await this.operationRuntime.series.workbench(libraryId, mode, invocation.signal) as unknown as JsonValue)
      if (action === 'count_items') {
        const itemTypes = typeof input.itemTypes === 'string' ? input.itemTypes : 'Movie'
        const total = await this.operationRuntime.libraries.itemCount(libraryId, invocation.signal, search, itemTypes)
        return this.wireResult('library.count-items', { libraryId, itemTypes, ...(search === undefined ? {} : { search }), total })
      }
      if (action === 'list_items') {
        const itemTypes = typeof input.itemTypes === 'string' ? input.itemTypes : 'Movie,Series,Episode'
        const offset = typeof input.offset === 'number' ? Math.max(0, Math.floor(input.offset)) : 0
        const page = search === undefined
          ? await this.operationRuntime.libraries.itemPage(libraryId, itemLimit, invocation.signal, undefined, itemTypes, offset)
          : await this.operationRuntime.libraries.searchItems(libraryId, search, itemLimit, offset, invocation.signal, itemTypes)
        const hasMore = offset + page.items.length < page.total
        return this.wireResult('library.items', { libraryId, itemTypes, ...(search === undefined ? {} : { search }), items: page.items, total: page.total, offset, limit: itemLimit, hasMore, nextOffset: hasMore ? offset + page.items.length : null } as unknown as JsonValue)
      }
    }
    if (tool === 'embymedia_resource' && action === 'stage_share') {
      const share = parseC115Share(inputString(input.url, 'resource URL'), typeof input.password === 'string' ? input.password : undefined)
      const candidateId = this.stageResourceCandidate({
        title: inputString(input.title, 'resource title'),
        diskType: '115',
        url: `https://115.com/s/${share.shareCode}`,
        ...(share.receiveCode === undefined ? {} : { password: share.receiveCode }),
        sourceChannels: [],
      }, invocation.agent.id)
      return this.wireResult('resource.stage_share', {
        candidateId,
        title: safeDisplay(inputString(input.title, 'resource title')),
        diskType: '115',
        accessCodeStaged: share.receiveCode !== undefined,
      })
    }

    if (tool === 'embymedia_resource') {
      if (action === 'parse_share') {
        const share = parseC115Share(inputString(input.url, 'resource URL'), typeof input.password === 'string' ? input.password : undefined)
        if (share.receiveCode !== undefined) throw new EmbymediaError('POLICY_DENIED', 'protected shares require a candidateId from resource search or resource.stage_share')
        return this.wireResult('resource.parse_share', { shareCode: share.shareCode, accessCodeStaged: false })
      }
      if (action === 'library_context' || action === 'duplicates') {
        const query = typeof input.query === 'string' ? input.query : typeof input.q === 'string' ? input.q : ''
        const context = await this.operationRuntime.libraries.context(query, typeof input.libraryId === 'string' ? input.libraryId : undefined, limit, invocation.signal)
        return this.wireResult(`resource.${action}`, action === 'duplicates'
          ? { query: context.query, duplicateGroups: context.duplicateGroups, totalMatches: context.totalMatches } as unknown as JsonValue
          : context as unknown as JsonValue)
      }
      if (action === 'transfer_preview') {
        const resolved = this.resolveResourceCandidate('resource.add_new', input, invocation.agent.id)
        return this.wireResult('resource.transfer_preview', await this.operationRuntime.previewResourceAddNew(
          inputObject(resolved.input, 'resource.add_new input'), invocation.signal,
        ))
      }
      const c115 = await this.c115Client()
      if (action === 'test_115') return this.wireResult('resource.test_115', await c115.test(invocation.signal))
      if (action === 'inspect_candidate') {
        const candidateId = inputString(input.candidateId, 'resource candidate id')
        const staged = this.resourceCandidates.get(candidateId)
        if (staged === undefined || staged.sessionId !== invocation.agent.id || staged.expiresAt <= Date.now()) {
          this.dropResourceCandidate(candidateId)
          throw new EmbymediaError('CONFLICT', 'resource candidate expired or belongs to another session')
        }
        const snapshot = await c115.snapshot(staged.item.url, staged.item.password, undefined, invocation.signal)
        const evidence = snapshot.evidence ?? []
        const evidenceLeaves = evidence.filter(file => file.directory === false && isVideoFileName(file.name))
        const rootLeaves = snapshot.files.filter(file => file.directory === false && isVideoFileName(file.name))
        const leaves = [...rootLeaves, ...evidenceLeaves]
        const seasonEpisodeKeys = [...new Set(leaves.flatMap(file => [...episodeKeysFromText(file.name)]))].sort()
        const absoluteEpisodeKeys = [...new Set(leaves.flatMap(file => [...absoluteEpisodeKeysFromText(file.name)]))].sort()
        return this.wireResult('resource.inspect_candidate', {
          candidateId,
          title: safeDisplay(staged.item.title),
          ...(staged.item.diskType === undefined ? {} : { diskType: safeDisplay(staged.item.diskType, 64) }),
          roots: snapshot.files.map(file => ({
            id: file.id ?? null,
            name: safeDisplay(file.name),
            size: file.size ?? null,
            directory: file.directory === true,
          })),
          evidence: evidence.slice(0, 500).map(file => ({
            id: file.id ?? null,
            name: safeDisplay(file.name),
            path: file.path === undefined ? null : safeDisplay(file.path, 2_048),
            size: file.size ?? null,
            directory: file.directory === true,
          })),
          evidenceTotal: evidence.length,
          coverage: {
            videoLeafCount: leaves.length,
            seasonEpisodeKeys: seasonEpisodeKeys.slice(0, 500),
            seasonEpisodeTotal: seasonEpisodeKeys.length,
            seasonEpisodeTruncated: seasonEpisodeKeys.length > 500,
            absoluteEpisodeKeys: absoluteEpisodeKeys.slice(0, 500),
            absoluteEpisodeTotal: absoluteEpisodeKeys.length,
            absoluteEpisodeTruncated: absoluteEpisodeKeys.length > 500,
          },
          truncated: evidence.length > 500,
        } as unknown as JsonValue)
      }
      if (action === 'list_entries') {
        const cid = inputString(input.cid, '115 cid')
        const offset = typeof input.offset === 'number' ? Math.max(0, Math.floor(input.offset)) : 0
        const page = await c115.listEntriesPage(cid, offset, limit, invocation.signal)
        return this.wireResult('resource.list_entries', {
          cid,
          entries: page.entries.map(entry => ({ ...entry, name: safeDisplay(entry.name) })),
          total: page.total,
          offset: page.offset,
          limit: page.limit,
          hasMore: page.hasMore,
          nextOffset: page.hasMore ? page.offset + page.entries.length : null,
        } as unknown as JsonValue)
      }
      if (action === 'snapshot_share') {
        if (Array.isArray(input.fileIds) && input.fileIds.length > 0) {
          throw new EmbymediaError('INVALID_INPUT', 'paged snapshot_share does not accept fileIds; inspect an opaque candidate instead')
        }
        const offset = typeof input.offset === 'number' ? Math.max(0, Math.floor(input.offset)) : 0
        const snapshot = await c115.snapshotPage(
          inputString(input.url, 'resource URL'),
          typeof input.password === 'string' ? input.password : undefined,
          offset,
          limit,
          invocation.signal,
        )
        if (snapshot.receiveCode !== undefined) throw new EmbymediaError('POLICY_DENIED', 'protected shares require a candidateId from resource search or resource.stage_share')
        return this.wireResult('resource.snapshot_share', {
          shareCode: snapshot.shareCode,
          title: snapshot.title === undefined ? null : safeDisplay(snapshot.title),
          files: snapshot.files.map(file => ({ id: file.id ?? null, name: safeDisplay(file.name), size: file.size ?? null, directory: file.directory === true })),
          total: snapshot.total,
          offset: snapshot.offset,
          limit: snapshot.limit,
          hasMore: snapshot.hasMore,
          nextOffset: snapshot.hasMore ? snapshot.offset + snapshot.files.length : null,
          accessCodeStaged: false,
        } as unknown as JsonValue)
      }
      if (action === 'search') {
        const resource = await this.resourceApiClient()
        const search = await resource.search(inputString(input.query, 'resource query'), {
          limit,
          ...(typeof input.offset === 'number' ? { offset: Math.max(0, input.offset) } : {}),
          ...(typeof input.exact === 'boolean' ? { exact: input.exact } : {}),
          ...(typeof input.sort === 'string' ? { sort: input.sort } : {}),
          ...(typeof input.diskType === 'string' ? { diskType: input.diskType } : {}),
        }, invocation.signal)
        return this.wireResult('resource.search', this.publicResourceSearch(search, invocation.agent.id))
      }
    }
    if (tool === 'embymedia_series') {
      const libraryId = inputString(input.libraryId, 'library id')
      const mode = input.mode === 'absolute' ? 'absolute' : 'season'
      const offset = typeof input.offset === 'number' ? Math.max(0, Math.floor(input.offset)) : 0
      const seriesLimit = Math.min(limit, 100)
      if (action === 'status') return this.wireResult('series.status', await this.operationRuntime.series.statusPage(libraryId, mode, invocation.signal, seriesLimit, offset) as unknown as JsonValue)
      if (action === 'workbench') return this.wireResult('series.workbench', await this.operationRuntime.series.workbench(libraryId, mode, invocation.signal, seriesLimit, offset) as unknown as JsonValue)
      if (action === 'gaps_summary') return this.wireResult('series.gaps_summary', await this.operationRuntime.series.gapsSummary(libraryId, mode, invocation.signal, seriesLimit, offset) as unknown as JsonValue)
      if (action === 'resource_plan') {
        const plan = await this.operationRuntime.series.resourcePlan(
          libraryId, inputString(input.seriesId, 'series id'), mode, invocation.signal,
        )
        return this.wireResult('series.resource_plan', {
          ...plan,
          search: this.publicResourceSearch(plan.search, invocation.agent.id),
        } as unknown as JsonValue)
      }
    }
    if (tool === 'embymedia_analyze') {
      if (action === 'dashboard') {
        const [history, active, recent] = await Promise.all([
          this.database.query<{ status: string; count: string }>(
            "SELECT status,count(*)::text AS count FROM task_runs WHERE status IN ('partial','error','cancelled','interrupted') GROUP BY status ORDER BY status",
          ),
          this.database.query<{ status: string; count: string }>(
            "SELECT status,count(*)::text AS count FROM task_runs WHERE status IN ('queued','running','verifying') GROUP BY status ORDER BY status",
          ),
          this.database.query<Record<string, JsonValue>>(
            "SELECT id,kind,label,source,status,updated_at::text AS updated_at FROM task_runs WHERE status IN ('partial','error','cancelled','interrupted') ORDER BY updated_at DESC LIMIT 10",
          ),
        ])
        const activeByStatus = Object.fromEntries(active.rows.map(row => [row.status, Number(row.count)]))
        return this.wireResult('analyze.dashboard', {
          tasks: {
            activeCount: Object.values(activeByStatus).reduce((sum, count) => sum + count, 0),
            activeByStatus,
            historicalTerminalByStatus: Object.fromEntries(history.rows.map(row => [row.status, Number(row.count)])),
            recentHistoricalTerminal: recent.rows,
            interpretation: 'partial/error/cancelled/interrupted are immutable history, not current incidents; current impact is activeCount plus a fresh fact recheck',
          },
          scheduler: {
            enabled: this.config.schedulerEnabled,
            interpretation: this.config.schedulerEnabled ? 'scheduler enabled' : 'scheduler intentionally disabled by deployment policy',
          },
        } as unknown as JsonValue)
      }
      if (action === 'poster_search') {
        const type = input.type === 'movie' ? 'movie' : 'tv'
        const year = typeof input.year === 'number' ? input.year : undefined
        const query = typeof input.query === 'string' ? input.query : typeof input.name === 'string' ? input.name : ''
        const response = await (await this.tmdbClient()).search(type, inputString(query, 'poster search query'), year, 1, invocation.signal)
        return this.wireResult('analyze.poster_search', {
          ...response,
          results: response.results.slice(0, Math.min(limit, 20)),
        } as unknown as JsonValue)
      }
      if (action === 'smart_list') {
        const allowed = ['suggested', 'ready', 'executing', 'done', 'failed', 'dismissed'] as const
        const status = input.status
        if (status !== undefined && (typeof status !== 'string' || !(allowed as readonly string[]).includes(status))) throw new EmbymediaError('INVALID_INPUT', 'invalid Smart Action status')
        return this.wireResult('analyze.smart_list', await this.operationRuntime.smartActions.list(status as typeof allowed[number] | undefined) as unknown as JsonValue)
      }
      if (action === 'smart_get' || action === 'smart_inspect' || action === 'smart_verify') return this.wireResult(`analyze.${action}`, await this.operationRuntime.smartActions.get(inputString(input.actionId ?? input.id, 'Smart Action id')) as unknown as JsonValue)
      if (action === 'smart_policies') return this.wireResult('analyze.smart_policies', await this.operationRuntime.smartActions.policies())
      if (action === 'smart_workbench' || action === 'smart_summary') return this.wireResult(`analyze.${action}`, await this.operationRuntime.smartActions.workbench() as unknown as JsonValue)
      if (action === 'from_task') return this.wireResult('analyze.from_task', await this.operationRuntime.smartActions.fromTask(inputString(input.taskId, 'task id')) as unknown as JsonValue)
    }
    throw new EmbymediaError('INVALID_INPUT', `unsupported ${tool} action ${action}`)
  }

  async adminSnapshot(signal: AbortSignal): Promise<JsonValue> {
    signal.throwIfAborted()
    const emby = await this.embyClient()
    const [libraries, users, taskCounts, recentTasks, schedules, recentAudit, settings, credentials] = await Promise.all([
      emby.libraries(signal),
      emby.users(signal),
      this.database.query<{ status: string; count: string }>('SELECT status,count(*)::text AS count FROM task_runs GROUP BY status ORDER BY status'),
      this.database.query<Record<string, JsonValue>>('SELECT id,kind,label,source,status,progress,total,status_text,updated_at::text AS updated_at,ended_at::text AS ended_at,error FROM task_runs ORDER BY updated_at DESC LIMIT 24'),
      this.database.query<Record<string, JsonValue>>('SELECT id,name,kind,enabled,version,risk,next_run_at::text AS next_run_at,last_run_at::text AS last_run_at,last_status FROM schedule_jobs ORDER BY created_at'),
      this.database.query<Record<string, JsonValue>>('SELECT id,actor,action,detail,destructive,created_at::text AS created_at FROM audit_logs ORDER BY created_at DESC LIMIT 16'),
      this.database.query<{ key: string; value: JsonValue }>('SELECT key,value FROM app_settings ORDER BY key'),
      this.credentialRecords.status(),
    ])
    const supported = this.operationSupport().supported
    return {
      schemaVersion: SCHEMA_VERSION,
      generatedAt: new Date().toISOString(),
      health: this.health() as unknown as JsonValue,
      users: users as unknown as JsonValue,
      libraries: libraries as unknown as JsonValue,
      tasks: {
        counts: Object.fromEntries(taskCounts.rows.map(row => [row.status, Number(row.count)])),
        recent: recentTasks.rows.map(row => maskConfiguration(row)),
      },
      schedules: schedules.rows.map(row => maskConfiguration(row)),
      audit: recentAudit.rows.map(row => maskConfiguration(row)),
      settings: maskConfiguration(Object.fromEntries(settings.rows.map(row => [row.key, row.value]))),
      credentials: credentials as unknown as JsonValue,
      operations: { supported },
    }
  }
  private stageResourceCandidate(item: ResourceSearchItem, sessionId: string): string {
    if (this.resourceCandidates.size >= MAX_STAGED_RESOURCE_CANDIDATES) {
      let oldestKey: string | undefined
      let oldestExpiresAt = Number.POSITIVE_INFINITY
      for (const [key, staged] of this.resourceCandidates.entries()) {
        if (staged.expiresAt < oldestExpiresAt) {
          oldestExpiresAt = staged.expiresAt
          oldestKey = key
        }
      }
      if (oldestKey !== undefined) this.dropResourceCandidate(oldestKey)
    }
    const candidateId = randomUUID()
    const expiresAt = Date.now() + RESOURCE_CANDIDATE_TTL_MS
    const timer = setTimeout(() => { this.dropResourceCandidate(candidateId) }, RESOURCE_CANDIDATE_TTL_MS)
    timer.unref()
    this.resourceCandidates.set(candidateId, { sessionId, item, expiresAt, timer })
    return candidateId
  }

  private publicResourceSearch(search: ResourceSearchResult, sessionId: string): JsonValue {
    const items = search.items.flatMap((item) => {
      let share: ReturnType<typeof parseC115Share> | undefined
      try { share = parseC115Share(item.url, item.password) } catch {}
      if (item.diskType === '115' && share === undefined) return []
      const candidateId = this.stageResourceCandidate(item, sessionId)
      return [{
        candidateId,
        title: safeDisplay(item.title),
        ...(item.type === undefined ? {} : { type: safeDisplay(item.type, 64) }),
        ...(item.diskType === undefined ? {} : { diskType: safeDisplay(item.diskType, 64) }),
        accessCodeStaged: share?.receiveCode !== undefined,
        ...(item.source === undefined ? {} : { source: safeDisplay(item.source, 256) }),
        sourceChannels: item.sourceChannels.slice(0, 16).map(channel => safeDisplay(channel, 256)),
      }]
    })
    return {
      ...search,
      query: safeDisplay(search.query, 512),
      sort: safeDisplay(search.sort, 64),
      diskTypes: search.diskTypes.slice(0, 32).map(entry => ({ diskType: safeDisplay(entry.diskType, 64), count: entry.count })),
      items,
    }
  }

  private resolveResourceCandidate(
    kind: OperationKind,
    value: unknown,
    sessionId: string,
  ): { readonly input: unknown; readonly candidateIds: readonly string[] } {
    if (kind !== 'resource.add_new' && kind !== 'series.update') return { input: value, candidateIds: [] }
    const input = inputObject(value, `${kind} input`)
    const resolveOne = (candidateValue: unknown): { readonly candidate: Readonly<Record<string, unknown>>; readonly candidateId?: string } => {
      const candidate = inputObject(candidateValue, 'resource candidate')
      const candidateId = typeof candidate.candidateId === 'string' ? candidate.candidateId : undefined
      if (candidateId === undefined) {
        const password = typeof candidate.password === 'string' ? candidate.password.trim() : ''
        const url = typeof candidate.url === 'string' ? candidate.url : ''
        let inlineCode = false
        try { inlineCode = parseC115Share(url, password || undefined).receiveCode !== undefined } catch {}
        if (password.length > 0 || inlineCode) throw new EmbymediaError('POLICY_DENIED', '115 access-code resources require an opaque candidateId from resource search or resource.stage_share')
        return { candidate }
      }
      const staged = this.resourceCandidates.get(candidateId)
      if (staged === undefined || staged.sessionId !== sessionId || staged.expiresAt <= Date.now()) {
        this.dropResourceCandidate(candidateId)
        throw new EmbymediaError('CONFLICT', 'resource candidate expired or belongs to another session')
      }
      const resolved: Record<string, unknown> = {
        ...candidate, mode: 'share', url: staged.item.url, title: safeDisplay(staged.item.title),
        ...(staged.item.password === undefined ? {} : { password: staged.item.password }),
      }
      delete resolved.candidateId
      return { candidate: resolved, candidateId }
    }
    if (Array.isArray(input.candidates)) {
      const resolved = input.candidates.map(resolveOne)
      return {
        input: { ...input, candidates: resolved.map(item => item.candidate) },
        candidateIds: resolved.flatMap(item => item.candidateId === undefined ? [] : [item.candidateId]),
      }
    }
    const resolved = resolveOne(input.candidate)
    return {
      input: { ...input, candidate: resolved.candidate },
      candidateIds: resolved.candidateId === undefined ? [] : [resolved.candidateId],
    }
  }

  private dropResourceCandidate(candidateId: string): void {
    const staged = this.resourceCandidates.get(candidateId)
    if (staged !== undefined) clearTimeout(staged.timer)
    this.resourceCandidates.delete(candidateId)
  }


  private operationSupport(): { readonly supported: readonly string[]; readonly unavailable: readonly string[] } {
    const supported = OPERATION_KINDS.filter(kind =>
      this.operationRuntime.previewers[kind] !== undefined
      && this.operationRuntime.handlers[kind] !== undefined
      && this.operationRuntime.verifiers[kind] !== undefined)
    return { supported, unavailable: OPERATION_KINDS.filter(kind => !supported.includes(kind)) }
  }

  private stagePlanSecret(plan: OperationProjection, sessionId: string, value: string | readonly (string | undefined)[]): void {
    this.dropPlanSecret(plan.id)
    if (this.planSecrets.size >= MAX_STAGED_PLAN_SECRETS) {
      let oldestKey: string | undefined
      let oldestExpiresAt = Number.POSITIVE_INFINITY
      for (const [key, staged] of this.planSecrets.entries()) {
        if (staged.expiresAt < oldestExpiresAt) {
          oldestExpiresAt = staged.expiresAt
          oldestKey = key
        }
      }
      if (oldestKey !== undefined) this.dropPlanSecret(oldestKey)
    }
    const expiresAt = Date.parse(plan.expiresAt)
    const timer = setTimeout(() => { this.dropPlanSecret(plan.id) }, Math.max(0, expiresAt - Date.now()))
    timer.unref()
    this.planSecrets.set(plan.id, { sessionId, value, expiresAt, timer })
  }

  private readPlanSecret(planId: string): string | readonly (string | undefined)[] | undefined {
    const staged = this.planSecrets.get(planId)
    if (staged === undefined) return undefined
    if (staged.expiresAt <= Date.now()) {
      this.dropPlanSecret(planId)
      return undefined
    }
    return staged.value
  }

  private dropPlanSecret(planId: string): void {
    const staged = this.planSecrets.get(planId)
    if (staged !== undefined) clearTimeout(staged.timer)
    this.planSecrets.delete(planId)
  }

  private clearPlanSecrets(): void {
    for (const planId of this.planSecrets.keys()) this.dropPlanSecret(planId)
    for (const candidateId of this.resourceCandidates.keys()) this.dropResourceCandidate(candidateId)
  }

  adminCredentialCheck(id: string, signal: AbortSignal): Promise<JsonValue> {
    if (!isCredentialId(id)) throw new EmbymediaError('INVALID_INPUT', 'unknown credential id')
    return this.credentialAvailability.check(id, signal) as unknown as Promise<JsonValue>
  }

  private wireResult(capabilityId: string, data: JsonValue): JsonValue {
    return { schemaVersion: SCHEMA_VERSION, capabilityId, correlationId: randomUUID(), data, warnings: [] }
  }

  private async embyClient(): Promise<EmbyClient> {
    return new EmbyClient({ baseUrl: this.config.embyBaseUrl, token: await this.credentialRecords.read('emby-api-key') })
  }

  private async c115Client(): Promise<C115Client> {
    return new C115Client({ cookie: await this.credentialRecords.read('c115-cookie') })
  }

  private async resourceApiClient(): Promise<ResourceApiClient> {
    const baseUrl = await this.setting('resource_api_base_url', 'http://gaotao.cc:8100')
    let proxyUrl: string | undefined
    try { proxyUrl = await this.credentialRecords.read('outbound-proxy-url') } catch {}
    let token: string | undefined
    try { token = await this.credentialRecords.read('resource-api-token') } catch {}
    return new ResourceApiClient({
      baseUrl,
      ...(token === undefined ? {} : { token }),
      allowInsecureHttp: this.config.allowInsecureResourceApi,
      ...(proxyUrl === undefined ? {} : { proxyUrl }),
    })
  }

  private async tmdbClient(): Promise<TmdbClient> {
    let proxyUrl: string | undefined
    try { proxyUrl = await this.credentialRecords.read('outbound-proxy-url') } catch {}
    return new TmdbClient({
      apiKey: await this.credentialRecords.read('tmdb-api-key'),
      baseUrl: await this.setting('tmdb_base_url', 'https://api.themoviedb.org'),
      ...(proxyUrl === undefined ? {} : { proxyUrl }),
    })
  }

  private async setting(key: string, fallback: string): Promise<string> {
    const row = (await this.database.query<{ value: JsonValue }>('SELECT value FROM app_settings WHERE key=$1', [key])).rows[0]
    return typeof row?.value === 'string' && row.value.trim().length > 0 ? row.value : fallback
  }


  protected registerBackgroundStop(stop: () => Promise<void>): () => void {
    this.backgroundStops.push(stop)
    return () => {
      const index = this.backgroundStops.indexOf(stop)
      if (index >= 0) this.backgroundStops.splice(index, 1)
    }
  }

  private async stopBackgroundWork(): Promise<void> {
    this.schedulerReady = false
    const failures: unknown[] = []
    for (const stop of [...this.backgroundStops].reverse()) {
      try {
        await stop()
      } catch (error) {
        failures.push(error)
      }
    }
    this.backgroundStops.length = 0
    if (failures.length > 0) throw new AggregateError(failures, 'embymedia background shutdown failed')
  }

  protected setSchedulerReady(ready: boolean): void {
    this.schedulerReady = ready
  }
}

export default EmbymediaService

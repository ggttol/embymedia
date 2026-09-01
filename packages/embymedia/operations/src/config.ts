import z from '@deepseek-ai/schemastery'
import { WRITE_MODES, type WriteMode } from './schemas.ts'

export const APPROVAL_MODES = ['manual', 'safe-auto'] as const
export type ApprovalMode = (typeof APPROVAL_MODES)[number]

export interface Config {
  databaseUrlEnv?: string
  mediaRoot?: string
  strmRoot?: string
  embyBaseUrl?: string
  principal?: string
  taskConcurrency?: number
  cloudConcurrency?: number
  helperSocket?: string
  writeMode?: WriteMode
  stagingLibraryIds?: string[]
  stagingCids?: string[]
  schedulerEnabled?: boolean
  allowInsecureResourceApi?: boolean
  approvalMode?: ApprovalMode
}

export interface ResolvedConfig {
  readonly databaseUrlEnv: string
  readonly mediaRoot: string
  readonly strmRoot: string
  readonly embyBaseUrl: string
  readonly principal: string
  readonly taskConcurrency: number
  readonly cloudConcurrency: number
  readonly helperSocket: string
  readonly writeMode: WriteMode
  readonly stagingLibraryIds: ReadonlySet<string>
  readonly stagingCids: ReadonlySet<string>
  readonly schedulerEnabled: boolean
  readonly allowInsecureResourceApi: boolean
  readonly approvalMode: ApprovalMode
}

export const ConfigSchema: z<Config> = z.object({
  databaseUrlEnv: z.string().default('EMBYMEDIA_DATABASE_URL'),
  mediaRoot: z.string().default('/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive'),
  strmRoot: z.string().default('/srv/embymedia/data/strm'),
  embyBaseUrl: z.string().default('http://127.0.0.1:8096/emby'),
  principal: z.string().default('gaotao'),
  taskConcurrency: z.natural().min(1).max(32).default(3),
  cloudConcurrency: z.natural().min(1).max(1).default(1),
  helperSocket: z.string().default('/run/embymedia-control/control.sock'),
  writeMode: z.union(WRITE_MODES.map(mode => z.const(mode))).default('disabled'),
  stagingLibraryIds: z.array(String).default([]),
  stagingCids: z.array(String).default([]),
  schedulerEnabled: z.boolean().default(false),
  allowInsecureResourceApi: z.boolean().default(false),
  approvalMode: z.union(APPROVAL_MODES.map(mode => z.const(mode))).default('manual'),
})
function booleanEnv(value: string | undefined, fallback: boolean, name: string): boolean {
  if (value === undefined) return fallback
  if (value === '1' || value === 'true' || value === 'yes') return true
  if (value === '0' || value === 'false' || value === 'no') return false
  throw new Error(`${name} must be 0 or 1`)
}

function approvalMode(value: string | undefined, fallback: ApprovalMode): ApprovalMode {
  const candidate = value ?? fallback
  if (!(APPROVAL_MODES as readonly string[]).includes(candidate)) {
    throw new Error(`EMBYMEDIA_APPROVAL_MODE must be one of ${APPROVAL_MODES.join('|')}`)
  }
  return candidate as ApprovalMode
}


function writeMode(value: string | undefined, fallback: WriteMode): WriteMode {
  const candidate = value ?? fallback
  if (!(WRITE_MODES as readonly string[]).includes(candidate)) {
    throw new Error(`EMBYMEDIA_WRITE_MODE must be one of ${WRITE_MODES.join('|')}`)
  }
  return candidate as WriteMode
}

function uniqueNonEmpty(values: readonly string[], label: string): ReadonlySet<string> {
  const normalized = values.map(value => value.trim())
  if (normalized.some(value => value.length === 0)) throw new Error(`${label} must not contain empty values`)
  return new Set(normalized)
}

export function resolveConfig(config: Config = {}, env: NodeJS.ProcessEnv = process.env): ResolvedConfig {
  const parsed = ConfigSchema(config)
  return {
    databaseUrlEnv: parsed.databaseUrlEnv ?? 'EMBYMEDIA_DATABASE_URL',
    mediaRoot: parsed.mediaRoot ?? '/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive',
    strmRoot: parsed.strmRoot ?? '/srv/embymedia/data/strm',
    embyBaseUrl: (parsed.embyBaseUrl ?? 'http://127.0.0.1:8096/emby').replace(/\/$/, ''),
    principal: parsed.principal ?? 'gaotao',
    taskConcurrency: parsed.taskConcurrency ?? 3,
    cloudConcurrency: parsed.cloudConcurrency ?? 1,
    helperSocket: parsed.helperSocket ?? '/run/embymedia-control/control.sock',
    writeMode: writeMode(env.EMBYMEDIA_WRITE_MODE, parsed.writeMode ?? 'disabled'),
    schedulerEnabled: booleanEnv(env.EMBYMEDIA_SCHEDULER_ENABLED, parsed.schedulerEnabled ?? false, 'EMBYMEDIA_SCHEDULER_ENABLED'),
    allowInsecureResourceApi: booleanEnv(env.EMBYMEDIA_ALLOW_INSECURE_RESOURCE_API, parsed.allowInsecureResourceApi ?? false, 'EMBYMEDIA_ALLOW_INSECURE_RESOURCE_API'),
    approvalMode: approvalMode(env.EMBYMEDIA_APPROVAL_MODE, parsed.approvalMode ?? 'manual'),
    stagingLibraryIds: uniqueNonEmpty(
      env.EMBYMEDIA_STAGING_LIBRARY_IDS === undefined
        ? parsed.stagingLibraryIds ?? []
        : env.EMBYMEDIA_STAGING_LIBRARY_IDS.split(',').map(item => item.trim()).filter(Boolean),
      'staging library ids',
    ),
    stagingCids: uniqueNonEmpty(
      env.EMBYMEDIA_STAGING_CIDS === undefined
        ? parsed.stagingCids ?? []
        : env.EMBYMEDIA_STAGING_CIDS.split(',').map(item => item.trim()).filter(Boolean),
      'staging cids',
    ),
  }
}

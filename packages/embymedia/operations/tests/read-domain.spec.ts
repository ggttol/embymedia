import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'
import { SystemReadService } from '../src/domain/read.ts'
import type { EmbymediaCredentialRecords } from '../src/credentials.ts'
import { resolveConfig } from '../src/config.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeRead = databaseUrl === undefined ? describe.skip : describe

describeRead('system read-only operations', () => {
  let database: Database
  let root: string
  let service: SystemReadService

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    root = await mkdtemp(join(tmpdir(), 'embymedia-read-'))
    await database.query("INSERT INTO app_settings(key,value) VALUES ('public_setting','\"visible\"') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
    await database.query("INSERT INTO app_settings(key,value) VALUES ('nested','{\"api_token\":\"hidden\",\"safe\":true}') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
    await database.query("INSERT INTO app_settings(key,value) VALUES ('tmdb_key','\"secret-tmdb\"') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
    await database.query("INSERT INTO app_settings(key,value) VALUES ('outbound_proxy_url','\"http://user:proxy-pass@host:1080\"') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
    await database.query(
      'INSERT INTO app_logs(level,message,detail,correlation_id) VALUES (\'error\',\'fixture\',\'{}\',$1)',
      [randomUUID()],
    )
    const credentials = {
      status: async () => [{ id: 'emby-api-key', configured: true, writable: true, kind: 'grant' }],
    } as unknown as EmbymediaCredentialRecords
    service = new SystemReadService(
      database,
      resolveConfig({ mediaRoot: root, strmRoot: root }),
      credentials,
      {
        emby: async () => ({ reachable: true, apiKey: 'must-mask' }),
        tmdb: async () => ({ reachable: true }),
        resource: async () => ({ reachable: true }),
        c115: async () => ({ reachable: true, cookie: 'must-mask' }),
        scheduler: async () => ({ ready: true }),
        backup: async () => ({ lastSnapshot: 'fixture' }),
      },
    )
  })

  afterAll(async () => {
    await database.close()
    await rm(root, { recursive: true, force: true })
  })

  it('derives health from probes, filesystem, and database without host control access', async () => {
    const health = await service.health([], new AbortController().signal)
    expect(health.capabilityId).toBe('health.check')
    expect(health.data).toMatchObject({ ok: true })
    expect(JSON.stringify(health.data)).not.toContain('must-mask')
    expect(JSON.stringify(health.data)).not.toContain('docker')
  })

  it('returns stable dashboard, log, and credential projections', async () => {
    const dashboard = await service.dashboard(new AbortController().signal)
    expect(dashboard.capabilityId).toBe('analyze.dashboard')
    const logs = await service.logs('logs', 10)
    expect(logs.capabilityId).toBe('audit.logs')
    expect(logs.page).toEqual({ limit: 10 })
    const credentials = await service.configuration('credential_status', new AbortController().signal)
    expect(credentials.data).toEqual([{ id: 'emby-api-key', configured: true, writable: true, kind: 'grant' }])
  })

  it('recursively masks secrets in get, export, and diagnostics', async () => {
    for (const action of ['get', 'export', 'diagnostics'] as const) {
      const view = await service.configuration(action, new AbortController().signal)
      const text = JSON.stringify(view)
      expect(text).not.toContain('hidden')
      expect(text).not.toContain('secret-tmdb')
      expect(text).not.toContain('proxy-pass')
      expect(text).toContain('[REDACTED]')
      expect(text).toContain('visible')
    }
  })
})

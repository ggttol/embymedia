import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { Pool } from 'pg'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '@embymedia/dsh-operations'
import { importState, inventory, verifyState } from '../src/migration.ts'

const adminUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeMigration = adminUrl === undefined ? describe.skip : describe
const SOURCE_DB = 'embymedia_migrate_source'
const TARGET_DB = 'embymedia_migrate_target'

function databaseUrl(name: string): string {
  const url = new URL(adminUrl!)
  url.pathname = `/${name}`
  return url.toString()
}

describeMigration('migration CLI contract', () => {
  let root: string
  let admin: Pool

  beforeAll(async () => {
    admin = new Pool({ connectionString: adminUrl, max: 1 })
    for (const database of [SOURCE_DB, TARGET_DB]) {
      await admin.query('SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1', [database])
      await admin.query(`DROP DATABASE IF EXISTS ${database}`)
      await admin.query(`CREATE DATABASE ${database}`)
    }
    const source = new Database(databaseUrl(SOURCE_DB), 4)
    await source.initialize()
    const settings: Readonly<Record<string, string>> = {
      api_key: 'emby-secret-value',
      c115_cookie: 'c115-secret-value',
      tmdb_api_key: 'tmdb-secret-value',
      tg_resource_api_token: 'resource-secret-value',
      cd2_webhook_secret: 'webhook-secret-value',
      outbound_proxy_url: 'socks5://user:password@example.invalid:1080',
      public_setting: 'retained',
    }
    for (const [key, value] of Object.entries(settings)) {
      await source.query('INSERT INTO app_settings(key,value) VALUES ($1,$2)', [key, JSON.stringify(value)])
    }
    await source.query(
      'INSERT INTO task_runs(id,kind,label,status,correlation_id) VALUES ($1,\'doctor\',\'fixture\',\'done\',$2)',
      [randomUUID(), randomUUID()],
    )
    await source.query(
      `INSERT INTO schedule_jobs(
        id,name,kind,params,cadence,enabled,version,version_hash,risk,approved_at,approved_by
      ) VALUES ($1,'fixture','doctor','{}','{}',false,1,'fixture','medium',now(),'fixture')`,
      [randomUUID()],
    )
    await source.close()
    root = await mkdtemp(join(tmpdir(), 'embymedia-migrate-'))
    await writeFile(join(root, 'source-url'), `${databaseUrl(SOURCE_DB)}\n`, { mode: 0o600 })
    await writeFile(join(root, 'target-url'), `${databaseUrl(TARGET_DB)}\n`, { mode: 0o600 })
    await writeFile(join(root, 'deepseek-key'), 'deepseek-secret-value\n', { mode: 0o640 })
  })

  afterAll(async () => {
    for (const database of [SOURCE_DB, TARGET_DB]) {
      await admin.query('SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1', [database])
      await admin.query(`DROP DATABASE IF EXISTS ${database}`)
    }
    await admin.end()
    await rm(root, { recursive: true, force: true })
  })

  it('inventories, imports, and verifies without secret disclosure', async () => {
    const inventoryPath = join(root, 'inventory.json')
    const importPath = join(root, 'import.json')
    const verifyPath = join(root, 'verify.json')
    const dshHome = join(root, 'dsh')
    const inventoried = await inventory({
      databaseUrlFile: join(root, 'source-url'), dshHome, output: inventoryPath,
    })
    expect(inventoried.credentials.every(item => item.configured && item.sha256?.length === 64)).toBe(true)
    const imported = await importState({
      databaseUrlFile: join(root, 'source-url'),
      targetUrlFile: join(root, 'target-url'),
      deepseekApiKeyFile: join(root, 'deepseek-key'),
      dshHome,
      report: importPath,
    })
    expect(imported.credentialRecords).toHaveLength(6)
    const verified = await verifyState({
      source: inventoryPath,
      targetUrlFile: join(root, 'target-url'),
      dshHome,
      report: verifyPath,
    })
    expect(verified).toMatchObject({
      ok: true,
      credentialsMode: '0600',
      tableCountsMatch: true,
      activeSchedules: 0,
      runningTasks: 0,
      deepseekConfigured: true,
      secretSettingKeys: [],
    })
    const reports = await Promise.all([inventoryPath, importPath, verifyPath].map(path => readFile(path, 'utf8')))
    const combined = reports.join('\n')
    for (const secret of [
      'emby-secret-value', 'c115-secret-value', 'tmdb-secret-value', 'resource-secret-value',
      'webhook-secret-value', 'deepseek-secret-value', 'user:password',
    ]) expect(combined).not.toContain(secret)
  })
})

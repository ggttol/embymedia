import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { PersistentJobService } from '../src/jobs.ts'
import { PersistentScheduler, nextRun } from '../src/scheduler.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeScheduler = databaseUrl === undefined ? describe.skip : describe

describeScheduler('persistent authorized scheduler', () => {
  let database: Database
  let jobs: PersistentJobService
  let scheduler: PersistentScheduler
  const run = vi.fn(async () => ({ result: { ok: true } }))

  beforeAll(async () => {
    database = new Database(databaseUrl!, 8)
    await database.initialize()
    await database.query('TRUNCATE schedule_versions,schedule_jobs CASCADE')
    jobs = new PersistentJobService(database, 3)
    scheduler = new PersistentScheduler(database, jobs, {
      scan_all: { operation: 'library.scan', run },
      doctor: { operation: 'schedule.run', run },
    }, (error) => { throw error })
  })

  afterAll(async () => {
    await scheduler.close()
    await jobs.close()
    await database.close()
  })

  it('calculates deterministic interval, hourly, and daily next runs', () => {
    const base = new Date('2026-01-01T10:30:30Z')
    expect(nextRun({ mode: 'interval', minutes: 15 }, base).toISOString()).toBe('2026-01-01T10:45:30.000Z')
    expect(nextRun({ mode: 'hourly', minute: 20 }, base).toISOString()).toBe('2026-01-01T11:20:00.000Z')
    expect(nextRun({ mode: 'daily', hour: 4, minute: 30 }, base).toISOString()).toBe('2026-01-02T04:30:00.000Z')
  })

  it('creates immutable approved versions for every edit', async () => {
    const first = await scheduler.upsertApproved({
      name: 'Scan all', kind: 'scan_all', params: { libraries: ['one'] }, cadence: { mode: 'daily', hour: 3, minute: 0 },
      enabled: true, approvedRisk: 'medium', approvedBy: 'gaotao',
    })
    const second = await scheduler.upsertApproved({
      scheduleId: first.id, name: 'Scan all', kind: 'scan_all', params: { libraries: ['one', 'two'] }, cadence: { mode: 'daily', hour: 3, minute: 0 },
      enabled: true, approvedRisk: 'medium', approvedBy: 'gaotao',
    })
    expect(first.version).toBe(1)
    expect(second.version).toBe(2)
    expect(second.versionHash).not.toBe(first.versionHash)
    const versions = await database.query<{ version: number; version_hash: string }>(
      'SELECT version,version_hash FROM schedule_versions WHERE schedule_id=$1 ORDER BY version', [first.id],
    )
    expect(versions.rows).toEqual([
      { version: 1, version_hash: first.versionHash },
      { version: 2, version_hash: second.versionHash },
    ])
  })

  it('uses advisory locking, records authorizing version, and never runs disabled schedules', async () => {
    const enabled = await scheduler.upsertApproved({
      name: 'Due scan', kind: 'scan_all', params: {}, cadence: { mode: 'interval', minutes: 5 },
      enabled: true, approvedRisk: 'medium', approvedBy: 'gaotao',
    })
    const disabled = await scheduler.upsertApproved({
      name: 'Disabled doctor', kind: 'doctor', params: {}, cadence: { mode: 'interval', minutes: 5 },
      enabled: false, approvedRisk: 'low', approvedBy: 'gaotao',
    })
    await database.query('UPDATE schedule_jobs SET next_run_at=now()-interval \'1 minute\' WHERE id=$1', [enabled.id])
    const peer = new PersistentScheduler(database, jobs, {
      scan_all: { operation: 'library.scan', run },
    }, (error) => { throw error })
    const counts = await Promise.all([scheduler.runDue(), peer.runDue()])
    expect(counts.reduce((sum, count) => sum + count, 0)).toBe(1)
    await vi.waitFor(async () => {
      const tasks = await database.query<{ status: string; authorizing_schedule_id: string; authorizing_schedule_version: number }>(
        'SELECT status,authorizing_schedule_id,authorizing_schedule_version FROM task_runs WHERE authorizing_schedule_id=$1', [enabled.id],
      )
      expect(tasks.rows).toEqual([
        { status: 'done', authorizing_schedule_id: enabled.id, authorizing_schedule_version: enabled.version },
      ])
    })
    expect(run).toHaveBeenCalled()
    const disabledTasks = await database.query<{ count: string }>('SELECT count(*)::text AS count FROM task_runs WHERE authorizing_schedule_id=$1', [disabled.id])
    expect(disabledTasks.rows[0]?.count).toBe('0')
  })
})

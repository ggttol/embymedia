import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { PersistentJobService } from '../src/jobs.ts'
import { abortableSleep } from '../src/cancellation.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeJobs = databaseUrl === undefined ? describe.skip : describe

describeJobs('persistent cancellable jobs', () => {
  let database: Database
  let jobs: PersistentJobService

  beforeAll(async () => {
    database = new Database(databaseUrl!, 8)
    await database.initialize()
    jobs = new PersistentJobService(database, 3)
  })

  afterAll(async () => {
    await jobs.close()
    await database.close()
  })

  it('limits ordinary work to three and Cloud/115 work to one', async () => {
    let active = 0
    let maxActive = 0
    let cloudActive = 0
    let maxCloudActive = 0
    const started = await Promise.all(Array.from({ length: 6 }, (_, index) => jobs.start({
      kind: `fixture-${String(index)}`,
      label: `Fixture ${String(index)}`,
      source: 'interactive',
      params: {},
      cloud: index < 3,
      run: async (signal) => {
        active++
        maxActive = Math.max(maxActive, active)
        if (index < 3) {
          cloudActive++
          maxCloudActive = Math.max(maxCloudActive, cloudActive)
        }
        await abortableSleep(20, signal)
        if (index < 3) cloudActive--
        active--
        return { result: { index } }
      },
    })))
    await vi.waitFor(async () => {
      const states = await Promise.all(started.map(job => jobs.get(job.id)))
      expect(states.every(job => job.status === 'done')).toBe(true)
    })
    expect(maxActive).toBeLessThanOrEqual(3)
    expect(maxCloudActive).toBe(1)
  })

  it('cooperatively cancels and waits for work to become still before settlement', async () => {
    let stopped = false
    const job = await jobs.start({
      kind: 'cancel-fixture', label: 'Cancel Fixture', source: 'interactive', params: {},
      run: async (signal) => {
        try {
          await abortableSleep(10_000, signal)
          return { result: {} }
        } finally {
          await abortableSleep(5, new AbortController().signal)
          stopped = true
        }
      },
    })
    await vi.waitFor(async () => { expect((await jobs.get(job.id)).status).toBe('running') })
    const cancelled = await jobs.cancel(job.id, 'gaotao')
    expect(cancelled.status).toBe('cancelled')
    expect(cancelled.cancelRequested).toBe(true)
    expect(stopped).toBe(true)
  })

  it('returns stored terminal results and refuses foreign non-running control', async () => {
    const task = await jobs.start({
      kind: 'terminal-fixture', label: 'Terminal Fixture', source: 'maintenance', params: {},
      run: async () => ({ status: 'partial', result: { completed: 1, failed: 1 } }),
    })
    await vi.waitFor(async () => { expect((await jobs.get(task.id)).status).toBe('partial') })
    await expect(jobs.cancel(task.id, 'gaotao')).resolves.toMatchObject({ status: 'partial', result: { completed: 1, failed: 1 } })
  })

  it('refuses to cancel a running system-source task via the tool path', async () => {
    const task = await jobs.start({
      kind: 'scheduled-fixture', label: 'Scheduled Fixture', source: 'schedule', params: {},
      run: async (signal) => {
        await abortableSleep(10_000, signal)
        return { result: {} }
      },
    })
    await vi.waitFor(async () => { expect((await jobs.get(task.id)).status).toBe('running') })
    await expect(jobs.cancel(task.id, 'gaotao')).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })
})

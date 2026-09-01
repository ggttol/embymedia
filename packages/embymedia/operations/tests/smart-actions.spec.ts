import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Database } from '../src/database/index.ts'
import { SMART_ACTION_TYPES, SmartActionEngine, type SmartFacts } from '../src/domain/smart-actions.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeSmart = databaseUrl === undefined ? describe.skip : describe

function fact(id: string, values: Partial<SmartFacts>): SmartFacts {
  return { subject: { kind: 'fixture', id, label: id }, evidence: { source: 'test' }, ...values }
}

describeSmart('deterministic Smart Action engine', () => {
  let database: Database
  const execute = vi.fn(async () => ({ executed: true }))
  const verify = vi.fn(async () => ({ ok: true }))
  let engine: SmartActionEngine

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    await database.query('TRUNCATE smart_action_runs,smart_action_policies,smart_action_workbench_reports')
    engine = new SmartActionEngine(database, {
      library_scan: { execute, verify },
    })
  })

  afterAll(async () => {
    await database.close()
  })

  it('covers the complete fixed action roster with deterministic confidence tiers', () => {
    const proposals = [
      ...engine.evaluate(fact('resource', { resourceCandidate: true, alreadyInLibrary: false })),
      ...engine.evaluate(fact('series', { missingEpisodes: 5 })),
      ...engine.evaluate(fact('dedup-high', { duplicateCount: 2, dedupConfidence: 90 })),
      ...engine.evaluate(fact('dedup-review', { duplicateCount: 2, dedupConfidence: 40 })),
      ...engine.evaluate(fact('poster', { posterMismatch: true })),
      ...engine.evaluate(fact('metadata', { noRating: true })),
      ...engine.evaluate(fact('scan', { pendingMediaFiles: 3 })),
      ...engine.evaluate(fact('archive', { seriesEnded: true, missingEpisodes: 0 })),
      ...engine.evaluate(fact('cleanup', { emptyFolder: true })),
      ...engine.evaluate(fact('task', { failedTask: true })),
    ]
    expect(new Set(proposals.map(action => action.type))).toEqual(new Set(SMART_ACTION_TYPES))
    expect(proposals.find(action => action.type === 'poster_fix')?.confidence).toBe('high')
    expect(proposals.find(action => action.type === 'metadata_refresh')?.confidence).toBe('medium')
    expect(proposals.find(action => action.type === 'dedup_review')?.confidence).toBe('suggestion')
  })

  it('persists refresh/workbench, policies, dismiss, and from-task behavior', async () => {
    const actions = await engine.refresh([fact('scan', { pendingMediaFiles: 1 })])
    expect(actions[0]).toMatchObject({ type: 'library_scan', status: 'ready', confidenceScore: 85 })
    await engine.updatePolicy('library_scan', true, 'confirm', 'medium', { scope: ['library-1'] })
    await expect(engine.policies()).resolves.toEqual([
      { key: 'library_scan', enabled: true, mode: 'confirm', maxRisk: 'medium', params: { scope: ['library-1'] } },
    ])
    const dismissed = await engine.refresh([fact('review', { duplicateCount: 2, dedupConfidence: 40 })])
    await expect(engine.dismiss(dismissed[0]!.id, 'reviewed')).resolves.toMatchObject({ status: 'dismissed' })
    const taskId = randomUUID()
    await database.query(
      'INSERT INTO task_runs(id,kind,label,status,error,correlation_id) VALUES ($1,\'fixture\',\'failed task\',\'error\',\'{}\',$2)',
      [taskId, randomUUID()],
    )
    await expect(engine.fromTask(taskId)).resolves.toEqual([
      expect.objectContaining({ type: 'task_retry_or_diagnose', status: 'ready' }),
    ])
    const workbench = await engine.workbench()
    expect(workbench.counts.ready).toBeGreaterThanOrEqual(2)
    expect(workbench.counts.dismissed).toBe(1)
  })

  it('executes and verifies only through deterministic type executors', async () => {
    const action = (await engine.refresh([fact('execute-scan', { pendingMediaFiles: 2 })]))[0]!
    await expect(engine.execute(action.id, new AbortController().signal)).resolves.toMatchObject({ status: 'done' })
    expect(execute).toHaveBeenCalledOnce()
    expect(verify).toHaveBeenCalledOnce()
    const unsupported = (await engine.refresh([fact('poster', { posterMismatch: true })]))[0]!
    await expect(engine.execute(unsupported.id, new AbortController().signal)).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })
})

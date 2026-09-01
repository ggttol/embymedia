import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'
import { BusinessMaintenanceService } from '../src/maintenance.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeMaintenance = databaseUrl === undefined ? describe.skip : describe

describeMaintenance('audited business maintenance', () => {
  let database: Database
  let maintenance: BusinessMaintenanceService

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
    maintenance = new BusinessMaintenanceService(database, (error) => { throw error })
  })

  afterAll(async () => {
    await maintenance.close()
    await database.close()
  })

  it('applies task/log/audit retention while preserving destructive summaries and undo payloads', async () => {
    const taskId = randomUUID()
    const planId = randomUUID()
    const undoId = randomUUID()
    await database.query("DELETE FROM audit_logs WHERE action IN ('old-normal','old-destructive')")
    await database.query(
      `INSERT INTO task_runs(id,kind,label,status,correlation_id,updated_at,ended_at)
       VALUES ($1,'old','old','done',$2,now()-interval '100 days',now()-interval '100 days')`,
      [taskId, randomUUID()],
    )
    await database.query(
      'INSERT INTO app_logs(level,message,detail,created_at) VALUES (\'info\',\'old-maintenance-fixture\',\'{}\',now()-interval \'31 days\')',
    )
    await database.query(
      `INSERT INTO audit_logs(actor,action,detail,correlation_id,destructive,created_at) VALUES
       ('fixture','old-normal','{}',$1,false,now()-interval '366 days'),
       ('fixture','old-destructive','{}',$2,true,now()-interval '1000 days')`,
      [randomUUID(), randomUUID()],
    )
    await database.query(
      `INSERT INTO operation_plans(
        id,kind,status,requested_by,session_id,preview,preview_hash,risk,destructive,reversible,
        confirmation,targets,steps,idempotency_key,expires_at,correlation_id
       ) VALUES ($1,'library.scan','previewed','gaotao','session','{}','hash','medium',false,false,'{}','[]','[]',$2,now()-interval '1 day',$3)`,
      [planId, randomUUID(), randomUUID()],
    )
    await database.query(
      'INSERT INTO smart_action_workbench_reports(id,params,report,expires_at) VALUES ($1,\'{}\',\'{}\',now()-interval \'1 day\')',
      [randomUUID()],
    )
    await database.query(
      'INSERT INTO undo_entries(id,plan_id,op,payload) VALUES ($1,$2,\'move\',$3)',
      [undoId, randomUUID(), JSON.stringify({ restore: 'required' })],
    )

    const counts = await maintenance.run()
    expect(counts.expiredPlans).toBeGreaterThanOrEqual(1)
    expect(counts).toMatchObject({ workbenchReports: 1, tasks: 1 })
    await expect(database.query<{ status: string }>('SELECT status FROM operation_plans WHERE id=$1', [planId]))
      .resolves.toMatchObject({ rows: [{ status: 'expired' }] })
    const task = await database.query<{ count: string }>('SELECT count(*)::text AS count FROM task_runs WHERE id=$1', [taskId])
    expect(task.rows[0]?.count).toBe('0')
    const audit = await database.query<{ action: string }>("SELECT action FROM audit_logs WHERE action IN ('old-normal','old-destructive') ORDER BY action")
    expect(audit.rows).toEqual([{ action: 'old-destructive' }])
    const undo = await database.query<{ payload: { restore: string } }>('SELECT payload FROM undo_entries WHERE id=$1', [undoId])
    expect(undo.rows[0]?.payload).toEqual({ restore: 'required' })
    const record = await database.query<{ detail: object }>("SELECT detail FROM audit_logs WHERE action='maintenance.cleanup' ORDER BY created_at DESC LIMIT 1")
    expect(record.rows[0]?.detail).toMatchObject({ tasks: 1 })
  })
})

import { randomUUID } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { Database } from '../src/database/index.ts'

const databaseUrl = process.env.EMBYMEDIA_TEST_DATABASE_URL
const describeDatabase = databaseUrl === undefined ? describe.skip : describe

describeDatabase('PostgreSQL business state', () => {
  let database: Database

  beforeAll(async () => {
    database = new Database(databaseUrl!, 4)
    await database.initialize()
  })

  afterAll(async () => {
    await database.close()
  })

  it('applies the ordered schema idempotently', async () => {
    await database.initialize()
    const migrations = await database.query<{ version: number }>(
      'SELECT version FROM embymedia_schema_migrations ORDER BY version',
    )
    expect(migrations.rows.map(row => row.version)).toEqual([1, 2, 3, 4, 5])
    const tables = await database.query<{ name: string }>(`
      SELECT table_name AS name
      FROM information_schema.tables
      WHERE table_schema = 'public'
        AND table_name IN ('operation_plans','task_runs','schedule_jobs','audit_logs','smart_action_runs')
      ORDER BY table_name
    `)
    expect(tables.rows.map(row => row.name)).toEqual([
      'audit_logs', 'operation_plans', 'schedule_jobs', 'smart_action_runs', 'task_runs',
    ])
  })

  it('aligns BIGSERIAL sequences after explicit legacy ids are imported', async () => {
    const maxima = await database.query<{ audit_max: string; app_max: string }>(
      'SELECT COALESCE((SELECT MAX(id) FROM audit_logs),0)::text AS audit_max,COALESCE((SELECT MAX(id) FROM app_logs),0)::text AS app_max',
    )
    const auditId = Number(maxima.rows[0]!.audit_max) + 1_000
    const appId = Number(maxima.rows[0]!.app_max) + 2_000
    await database.query(
      'INSERT INTO audit_logs(id,actor,action,correlation_id) VALUES ($1,\'fixture\',\'sequence.import\',$2)',
      [auditId, randomUUID()],
    )
    await database.query(
      'INSERT INTO app_logs(id,level,message,correlation_id) VALUES ($1,\'info\',\'sequence import\',$2)',
      [appId, randomUUID()],
    )
    await database.query("SELECT setval('audit_logs_id_seq',1,false),setval('app_logs_id_seq',1,false)")

    await database.initialize()

    const audit = await database.query<{ last_value: string; is_called: boolean }>('SELECT last_value::text,is_called FROM audit_logs_id_seq')
    const app = await database.query<{ last_value: string; is_called: boolean }>('SELECT last_value::text,is_called FROM app_logs_id_seq')
    expect(Number(audit.rows[0]!.last_value)).toBeGreaterThanOrEqual(auditId)
    expect(audit.rows[0]!.is_called).toBe(true)
    expect(Number(app.rows[0]!.last_value)).toBeGreaterThanOrEqual(appId)
    expect(app.rows[0]!.is_called).toBe(true)
  })

  it('interrupts unknown-completion work without replaying it', async () => {
    const taskId = randomUUID()
    const planId = randomUUID()
    const approvedPlanId = randomUUID()
    await database.query(
      `INSERT INTO operation_plans(
        id,kind,status,requested_by,session_id,preview,preview_hash,risk,destructive,reversible,
        confirmation,targets,steps,idempotency_key,expires_at,correlation_id
      ) VALUES ($1,'library.scan','running','gaotao','session-1','{}','hash','medium',false,false,
        '{}','[]','[]',$2,now() + interval '10 minutes',$3)`,
      [planId, `test-${planId}`, randomUUID()],
    )
    await database.query(
      `INSERT INTO operation_plans(
        id,kind,status,requested_by,session_id,preview,preview_hash,risk,destructive,reversible,
        confirmation,targets,steps,idempotency_key,expires_at,correlation_id,approved_at
      ) VALUES ($1,'resource.add_new','approved','gaotao','session-1','{}','approved-hash','high',false,false,
        '{}','[]','[]',$2,now() + interval '10 minutes',$3,now())`,
      [approvedPlanId, `test-${approvedPlanId}`, randomUUID()],
    )
    await database.query(
      `INSERT INTO task_runs(id,plan_id,kind,label,status,correlation_id)
       VALUES ($1,$2,'library.scan','scan fixture','running',$3)`,
      [taskId, planId, randomUUID()],
    )

    const recovered = await database.recoverInterrupted()
    expect(recovered.tasks).toBeGreaterThanOrEqual(1)
    expect(recovered.plans).toBeGreaterThanOrEqual(1)
    const task = await database.query<{ status: string }>('SELECT status FROM task_runs WHERE id = $1', [taskId])
    const plan = await database.query<{ status: string }>('SELECT status FROM operation_plans WHERE id = $1', [planId])
    const approvedPlan = await database.query<{ status: string }>('SELECT status FROM operation_plans WHERE id = $1', [approvedPlanId])
    expect(task.rows[0]?.status).toBe('interrupted')
    expect(plan.rows[0]?.status).toBe('interrupted')
    expect(approvedPlan.rows[0]?.status).toBe('interrupted')
    await expect(database.recoverInterrupted()).resolves.toEqual({ tasks: 0, plans: 0 })
  })
})

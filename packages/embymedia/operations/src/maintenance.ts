import { randomUUID } from 'node:crypto'
import type { Database } from './database/index.ts'

export interface MaintenanceCounts {
  readonly expiredPlans: number
  readonly workbenchReports: number
  readonly tasks: number
  readonly appLogs: number
  readonly auditLogs: number
  readonly loginAttempts: number
}

export class BusinessMaintenanceService {
  private timer: NodeJS.Timeout | undefined
  private running: Promise<MaintenanceCounts> | undefined

  constructor(
    private readonly database: Database,
    private readonly onError: (error: unknown) => void,
  ) {}

  async run(): Promise<MaintenanceCounts> {
    if (this.running !== undefined) return this.running
    const running = this.perform()
    this.running = running
    try {
      return await running
    } finally {
      if (this.running === running) this.running = undefined
    }
  }

  start(intervalMs = 24 * 60 * 60_000): void {
    if (this.timer !== undefined) throw new Error('business maintenance already started')
    this.timer = setInterval(() => { void this.run().catch(this.onError) }, intervalMs)
  }

  async close(): Promise<void> {
    if (this.timer !== undefined) clearInterval(this.timer)
    this.timer = undefined
    await this.running
  }

  private async perform(): Promise<MaintenanceCounts> {
    const counts = await this.database.transaction(async (client) => {
      const expiredPlans = await client.query(
        `UPDATE operation_plans SET status='expired',updated_at=now()
         WHERE status='previewed' AND expires_at <= now()`,
      )
      const workbenchReports = await client.query('DELETE FROM smart_action_workbench_reports WHERE expires_at <= now()')
      const tasks = await client.query(
        `DELETE FROM task_runs t
         WHERE t.updated_at < now() - interval '90 days'
           AND t.status IN ('done','partial','error','cancelled','interrupted')
           AND NOT EXISTS (SELECT 1 FROM operation_plans p WHERE p.task_id=t.id)
           AND NOT EXISTS (SELECT 1 FROM smart_action_runs s WHERE s.task_id=t.id)`,
      )
      const appLogs = await client.query("DELETE FROM app_logs WHERE created_at < now() - interval '30 days'")
      const auditLogs = await client.query("DELETE FROM audit_logs WHERE NOT destructive AND created_at < now() - interval '365 days'")
      const loginTable = await client.query<{ exists: boolean }>("SELECT to_regclass('public.login_attempts') IS NOT NULL AS exists")
      const loginAttempts = loginTable.rows[0]?.exists === true
        ? await client.query("DELETE FROM login_attempts WHERE created_at < now() - interval '1 day'")
        : undefined
      return {
        expiredPlans: expiredPlans.rowCount ?? 0,
        workbenchReports: workbenchReports.rowCount ?? 0,
        tasks: tasks.rowCount ?? 0,
        appLogs: appLogs.rowCount ?? 0,
        auditLogs: auditLogs.rowCount ?? 0,
        loginAttempts: loginAttempts?.rowCount ?? 0,
      }
    })
    await this.database.query(
      `INSERT INTO audit_logs(actor,action,detail,correlation_id)
       VALUES ('system','maintenance.cleanup',$1,$2)`,
      [JSON.stringify(counts), randomUUID()],
    )
    return counts
  }
}

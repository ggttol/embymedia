import { Pool, type PoolClient, type QueryResult, type QueryResultRow } from 'pg'
import { EmbymediaError } from '../errors.ts'

interface Migration {
  readonly version: number
  readonly name: string
  readonly sql: string
}

const MIGRATIONS: readonly Migration[] = [
  {
    version: 1,
    name: 'business-state',
    sql: `
      CREATE TABLE IF NOT EXISTS embymedia_schema_migrations (
        version INTEGER PRIMARY KEY,
        name TEXT NOT NULL,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );

      CREATE TABLE IF NOT EXISTS app_settings (
        key TEXT PRIMARY KEY,
        value JSONB NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );

      CREATE TABLE IF NOT EXISTS task_runs (
        id UUID PRIMARY KEY,
        plan_id UUID,
        kind TEXT NOT NULL,
        label TEXT NOT NULL DEFAULT '',
        source TEXT NOT NULL DEFAULT 'interactive',
        params JSONB NOT NULL DEFAULT '{}'::jsonb,
        status TEXT NOT NULL CHECK (status IN ('queued','running','verifying','done','partial','error','cancelled','interrupted')),
        progress BIGINT NOT NULL DEFAULT 0 CHECK (progress >= 0),
        total BIGINT NOT NULL DEFAULT 0 CHECK (total >= 0),
        status_text TEXT NOT NULL DEFAULT '',
        result JSONB,
        error JSONB,
        cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
        correlation_id UUID NOT NULL,
        authorizing_schedule_id UUID,
        authorizing_schedule_version INTEGER,
        queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        started_at TIMESTAMPTZ,
        ended_at TIMESTAMPTZ,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );
      CREATE INDEX IF NOT EXISTS idx_task_runs_updated ON task_runs(updated_at DESC);
      CREATE INDEX IF NOT EXISTS idx_task_runs_status ON task_runs(status, updated_at DESC);
      CREATE INDEX IF NOT EXISTS idx_task_runs_plan ON task_runs(plan_id);

      CREATE TABLE IF NOT EXISTS schedule_jobs (
        id UUID PRIMARY KEY,
        name TEXT NOT NULL,
        kind TEXT NOT NULL,
        params JSONB NOT NULL DEFAULT '{}'::jsonb,
        cadence JSONB NOT NULL,
        enabled BOOLEAN NOT NULL DEFAULT TRUE,
        version INTEGER NOT NULL CHECK (version > 0),
        version_hash TEXT NOT NULL,
        risk TEXT NOT NULL CHECK (risk IN ('low','medium')),
        approved_at TIMESTAMPTZ NOT NULL,
        approved_by TEXT NOT NULL,
        next_run_at TIMESTAMPTZ,
        last_run_at TIMESTAMPTZ,
        last_ended_at TIMESTAMPTZ,
        last_status TEXT,
        last_task_id UUID,
        last_error JSONB,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        UNIQUE(id, version),
        UNIQUE(id, version_hash)
      );
      CREATE INDEX IF NOT EXISTS idx_schedule_due ON schedule_jobs(enabled, next_run_at) WHERE enabled;

      CREATE TABLE IF NOT EXISTS undo_entries (
        id UUID PRIMARY KEY,
        plan_id UUID NOT NULL,
        op TEXT NOT NULL,
        payload JSONB NOT NULL,
        undone BOOLEAN NOT NULL DEFAULT FALSE,
        expires_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );
      CREATE INDEX IF NOT EXISTS idx_undo_available ON undo_entries(undone, created_at DESC);

      CREATE TABLE IF NOT EXISTS audit_logs (
        id BIGSERIAL PRIMARY KEY,
        actor TEXT NOT NULL,
        action TEXT NOT NULL,
        detail JSONB NOT NULL DEFAULT '{}'::jsonb,
        session_id TEXT,
        agent_id TEXT,
        tool_name TEXT,
        call_id TEXT,
        correlation_id UUID NOT NULL,
        destructive BOOLEAN NOT NULL DEFAULT FALSE,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );
      CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC);
      CREATE INDEX IF NOT EXISTS idx_audit_destructive ON audit_logs(destructive, created_at DESC) WHERE destructive;

      CREATE TABLE IF NOT EXISTS app_logs (
        id BIGSERIAL PRIMARY KEY,
        level TEXT NOT NULL,
        message TEXT NOT NULL,
        detail JSONB NOT NULL DEFAULT '{}'::jsonb,
        correlation_id UUID,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );
      CREATE INDEX IF NOT EXISTS idx_app_logs_created ON app_logs(created_at DESC);

      CREATE TABLE IF NOT EXISTS autostrm_seen (
        id BIGSERIAL PRIMARY KEY,
        lib TEXT NOT NULL,
        top TEXT NOT NULL,
        mtime BIGINT NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        UNIQUE(lib, top, mtime)
      );

      CREATE TABLE IF NOT EXISTS autostrm_unmatched (
        id BIGSERIAL PRIMARY KEY,
        lib TEXT NOT NULL,
        top TEXT NOT NULL,
        emby_id TEXT,
        name TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        UNIQUE(lib, top)
      );

      CREATE TABLE IF NOT EXISTS smart_action_runs (
        id UUID PRIMARY KEY,
        action_type TEXT NOT NULL,
        status TEXT NOT NULL,
        subject JSONB NOT NULL,
        title TEXT NOT NULL,
        summary TEXT NOT NULL,
        recommendation JSONB NOT NULL,
        evidence JSONB NOT NULL,
        plan JSONB NOT NULL,
        risk JSONB NOT NULL,
        policy JSONB NOT NULL,
        verification JSONB NOT NULL,
        source TEXT NOT NULL DEFAULT 'smart_action_runs',
        tab TEXT NOT NULL DEFAULT 'smart-actions',
        action_label TEXT NOT NULL DEFAULT '查看详情',
        task_id UUID REFERENCES task_runs(id),
        result JSONB,
        error JSONB,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        expires_at TIMESTAMPTZ
      );
      CREATE INDEX IF NOT EXISTS idx_smart_action_runs_status ON smart_action_runs(status, updated_at DESC);
      CREATE INDEX IF NOT EXISTS idx_smart_action_runs_action_type ON smart_action_runs(action_type, updated_at DESC);
      CREATE INDEX IF NOT EXISTS idx_smart_action_runs_subject ON smart_action_runs USING gin(subject);
      CREATE INDEX IF NOT EXISTS idx_smart_action_runs_expires ON smart_action_runs(expires_at);

      CREATE TABLE IF NOT EXISTS smart_action_policies (
        key TEXT PRIMARY KEY,
        enabled BOOLEAN NOT NULL DEFAULT TRUE,
        mode TEXT NOT NULL DEFAULT 'confirm',
        max_risk TEXT NOT NULL DEFAULT 'medium',
        params JSONB NOT NULL DEFAULT '{}'::jsonb,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );

      CREATE TABLE IF NOT EXISTS smart_action_workbench_reports (
        id UUID PRIMARY KEY,
        params JSONB NOT NULL,
        report JSONB NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        expires_at TIMESTAMPTZ NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_smart_action_reports_expires ON smart_action_workbench_reports(expires_at);
    `,
  },
  {
    version: 2,
    name: 'operation-plans',
    sql: `
      CREATE TABLE IF NOT EXISTS operation_plans (
        id UUID PRIMARY KEY,
        kind TEXT NOT NULL,
        status TEXT NOT NULL CHECK (status IN ('previewed','approved','cancelled','expired','queued','running','verifying','done','partial','failed','interrupted')),
        requested_by TEXT NOT NULL,
        session_id TEXT NOT NULL,
        preview JSONB NOT NULL,
        preview_hash TEXT NOT NULL,
        risk TEXT NOT NULL CHECK (risk IN ('low','medium','high','critical')),
        destructive BOOLEAN NOT NULL,
        reversible BOOLEAN NOT NULL,
        confirmation JSONB NOT NULL,
        targets JSONB NOT NULL,
        steps JSONB NOT NULL,
        idempotency_key TEXT NOT NULL UNIQUE,
        expires_at TIMESTAMPTZ NOT NULL,
        approved_at TIMESTAMPTZ,
        task_id UUID REFERENCES task_runs(id),
        result JSONB,
        verification JSONB,
        error JSONB,
        correlation_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
      );
      CREATE INDEX IF NOT EXISTS idx_operation_plans_status ON operation_plans(status, updated_at DESC);
      CREATE INDEX IF NOT EXISTS idx_operation_plans_session ON operation_plans(session_id, created_at DESC);
      CREATE INDEX IF NOT EXISTS idx_operation_plans_expiry ON operation_plans(status, expires_at);
      ALTER TABLE task_runs
        ADD CONSTRAINT fk_task_plan FOREIGN KEY (plan_id) REFERENCES operation_plans(id) DEFERRABLE INITIALLY DEFERRED;
    `,
  },
  {
    version: 3,
    name: 'legacy-undo-identity',
    sql: `
      ALTER TABLE undo_entries ADD COLUMN IF NOT EXISTS legacy_id TEXT;
      CREATE INDEX IF NOT EXISTS idx_undo_legacy_id ON undo_entries(legacy_id) WHERE legacy_id IS NOT NULL;
    `,
  },
  {
    version: 4,
    name: 'schedule-versions',
    sql: `
      CREATE TABLE IF NOT EXISTS schedule_versions (
        schedule_id UUID NOT NULL,
        version INTEGER NOT NULL CHECK (version > 0),
        version_hash TEXT NOT NULL,
        kind TEXT NOT NULL,
        params JSONB NOT NULL,
        cadence JSONB NOT NULL,
        risk TEXT NOT NULL CHECK (risk IN ('low','medium')),
        approved_at TIMESTAMPTZ NOT NULL,
        approved_by TEXT NOT NULL,
        PRIMARY KEY(schedule_id,version),
        UNIQUE(schedule_id,version_hash)
      );
      CREATE INDEX IF NOT EXISTS idx_schedule_versions_hash ON schedule_versions(version_hash);
    `,
  },
  {
    version: 5,
    name: 'canonical-resource-api-setting',
    sql: `
      INSERT INTO app_settings(key,value,updated_at)
      SELECT 'resource_api_base_url',value,updated_at FROM app_settings WHERE key='tg_resource_api_base_url'
      ON CONFLICT(key) DO NOTHING;
      DELETE FROM app_settings WHERE key='tg_resource_api_base_url';
    `,
  },
]

export class Database {
  readonly pool: Pool
  private accepting = true

  constructor(connectionString: string, maxConnections: number) {
    if (connectionString.trim().length === 0) throw new EmbymediaError('AUTH_REQUIRED', 'database URL is not configured')
    this.pool = new Pool({
      connectionString,
      max: Math.max(4, maxConnections),
      idleTimeoutMillis: 30_000,
      connectionTimeoutMillis: 5_000,
      allowExitOnIdle: false,
    })
  }

  async initialize(signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted()
    const client = await this.pool.connect()
    try {
      await client.query('BEGIN')
      await client.query("SELECT pg_advisory_xact_lock(hashtext('embymedia-schema-migrations'))")
      await client.query(`
        CREATE TABLE IF NOT EXISTS embymedia_schema_migrations (
          version INTEGER PRIMARY KEY,
          name TEXT NOT NULL,
          applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )
      `)
      const current = await client.query<{ version: number }>('SELECT version FROM embymedia_schema_migrations')
      const applied: Record<number, true> = Object.fromEntries(current.rows.map(row => [row.version, true]))
      for (const migration of MIGRATIONS) {
        signal?.throwIfAborted()
        if (applied[migration.version]) continue
        await client.query(migration.sql)
        await client.query(
          'INSERT INTO embymedia_schema_migrations(version, name) VALUES ($1, $2)',
          [migration.version, migration.name],
        )
      }
      // Legacy import preserves explicit BIGSERIAL ids. PostgreSQL sequences are
      // independent objects, so align them on every boot before any audit/log
      // writer can allocate a duplicate primary key.
      await client.query(`
        SELECT setval(pg_get_serial_sequence('audit_logs','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM audit_logs;
        SELECT setval(pg_get_serial_sequence('app_logs','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM app_logs;
        SELECT setval(pg_get_serial_sequence('autostrm_seen','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM autostrm_seen;
        SELECT setval(pg_get_serial_sequence('autostrm_unmatched','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM autostrm_unmatched;
      `)
      await client.query('COMMIT')
    } catch (error) {
      await client.query('ROLLBACK')
      throw error
    } finally {
      client.release()
    }
  }

  async recoverInterrupted(): Promise<{ tasks: number; plans: number }> {
    return this.transaction(async (client) => {
      const tasks = await client.query(`
        UPDATE task_runs
        SET status = 'interrupted', ended_at = now(), updated_at = now(),
            error = jsonb_build_object('code','CANCELLED','message','service restarted before completion')
        WHERE status IN ('running','verifying')
      `)
      const plans = await client.query(`
        UPDATE operation_plans
        SET status = 'interrupted', updated_at = now(),
            error = jsonb_build_object('code','CANCELLED','message','service restarted before completion')
        WHERE status IN ('approved','queued','running','verifying')
      `)
      return { tasks: tasks.rowCount ?? 0, plans: plans.rowCount ?? 0 }
    })
  }

  async query<Row extends QueryResultRow = QueryResultRow>(
    text: string,
    values: readonly unknown[] = [],
  ): Promise<QueryResult<Row>> {
    if (!this.accepting) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'database is stopping')
    return this.pool.query<Row>(text, [...values])
  }

  async transaction<T>(work: (client: PoolClient) => Promise<T>): Promise<T> {
    if (!this.accepting) throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'database is stopping')
    const client = await this.pool.connect()
    try {
      await client.query('BEGIN')
      const result = await work(client)
      await client.query('COMMIT')
      return result
    } catch (error) {
      await client.query('ROLLBACK')
      throw error
    } finally {
      client.release()
    }
  }

  async health(signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted()
    await this.pool.query('SELECT 1')
  }

  async close(): Promise<void> {
    this.accepting = false
    await this.pool.end()
  }
}

package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open opens or initializes a SQLite database file with WAL mode
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	rawDB.SetMaxOpenConns(1) // SQLite single writer safety
	rawDB.SetMaxIdleConns(1)

	db := &DB{db: rawDB}
	if err := db.migrate(); err != nil {
		rawDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS drive_accounts (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		cookie TEXT,
		token TEXT,
		is_default INTEGER DEFAULT 0,
		status TEXT DEFAULT 'active',
		quota_used INTEGER DEFAULT 0,
		quota_total INTEGER DEFAULT 0,
		vip_level INTEGER DEFAULT 0,
		vip_expires_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS offline_tasks (
		info_hash TEXT PRIMARY KEY,
		name TEXT,
		size INTEGER DEFAULT 0,
		status INTEGER DEFAULT 0,
		percent REAL DEFAULT 0,
		url TEXT,
		account_id TEXT,
		file_id TEXT,
		created_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		cron_expr TEXT,
		status TEXT DEFAULT 'pending',
		progress REAL DEFAULT 0,
		params TEXT,
		result TEXT,
		error TEXT,
		last_run_at DATETIME,
		next_run_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS async_tasks (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		schedule_id TEXT,
		payload TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL,
		progress REAL NOT NULL DEFAULT 0,
		result TEXT,
		error TEXT,
		attempts INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS task_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES async_tasks(id) ON DELETE CASCADE,
		attempt INTEGER NOT NULL,
		status TEXT NOT NULL,
		progress REAL NOT NULL DEFAULT 0,
		logs TEXT NOT NULL DEFAULT '[]',
		error TEXT,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		UNIQUE(task_id, attempt)
	);
	CREATE INDEX IF NOT EXISTS task_runs_task_id_idx ON task_runs(task_id, id);

	CREATE TABLE IF NOT EXISTS agent_tokens (
		id TEXT PRIMARY KEY,
		token TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		role TEXT DEFAULT 'agent',
		scopes TEXT,
		rate_limit INTEGER DEFAULT 20,
		enabled INTEGER NOT NULL DEFAULT 1,
		last_used_at DATETIME,
		created_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		caller TEXT NOT NULL,
		token_id TEXT,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		details TEXT,
		ip TEXT,
		created_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS agent_audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		caller TEXT NOT NULL,
		token_id TEXT,
		agent_name TEXT,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		input TEXT,
		output TEXT,
		status TEXT NOT NULL,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		ip TEXT,
		created_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS agent_audit_logs_created_at_idx ON agent_audit_logs(created_at DESC);

	CREATE TABLE IF NOT EXISTS destructive_approvals (
		id TEXT PRIMARY KEY,
		action TEXT NOT NULL,
		account_id TEXT NOT NULL,
		parent_cid TEXT NOT NULL,
		targets TEXT NOT NULL,
		status TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		approved_by TEXT,
		error TEXT,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		decided_at DATETIME,
		executed_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS destructive_approvals_status_idx ON destructive_approvals(status, expires_at);

	CREATE TABLE IF NOT EXISTS system_configs (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		description TEXT,
		updated_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS favorites (
		id INTEGER PRIMARY KEY,
		disk_type TEXT NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		password TEXT,
		saved_at DATETIME
	);
	`

	if _, err := d.db.Exec(schema); err != nil {
		return err
	}
	for _, migration := range []struct{ table, column, definition string }{
		{"agent_tokens", "enabled", "INTEGER NOT NULL DEFAULT 1"},
		{"async_tasks", "schedule_id", "TEXT"},
		{"drive_accounts", "vip_level", "INTEGER NOT NULL DEFAULT 0"},
		{"drive_accounts", "vip_expires_at", "DATETIME"},
	} {
		if err := d.ensureColumn(migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}
	if _, err := d.db.Exec(`CREATE INDEX IF NOT EXISTS async_tasks_schedule_id_idx ON async_tasks(schedule_id, created_at DESC)`); err != nil {
		return err
	}
	if _, err := d.db.Exec(`
		UPDATE drive_accounts
		SET cookie = CASE
				WHEN TRIM(type) = '' THEN COALESCE(NULLIF((SELECT value FROM system_settings WHERE key = '115_cookie'), ''), cookie)
				ELSE cookie
			END,
			type = CASE WHEN TRIM(type) = '' THEN '115' ELSE type END,
			name = CASE WHEN TRIM(name) = '' THEN '默认账号' ELSE name END
		WHERE TRIM(type) = '' OR TRIM(name) = ''
	`); err != nil {
		return err
	}
	return nil
}

func (d *DB) ensureColumn(table, column, definition string) error {
	rows, err := d.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = d.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

// Drive Accounts
func (d *DB) ListAccounts() ([]domain.DriveAccount, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT id, type, name, cookie, token, is_default, status, quota_used, quota_total, vip_level, vip_expires_at, created_at, updated_at FROM drive_accounts ORDER BY is_default DESC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.DriveAccount
	for rows.Next() {
		var a domain.DriveAccount
		var isDefault int
		var vipExpires sql.NullTime
		var created, updated time.Time
		if err := rows.Scan(&a.ID, &a.Type, &a.Name, &a.Cookie, &a.Token, &isDefault, &a.Status, &a.QuotaUsed, &a.QuotaTotal, &a.VIPLevel, &vipExpires, &created, &updated); err != nil {
			return nil, err
		}
		if vipExpires.Valid {
			a.VIPExpiresAt = &vipExpires.Time
		}
		a.IsDefault = isDefault == 1
		a.CreatedAt = created
		a.UpdatedAt = updated
		res = append(res, a)
	}
	return res, nil
}

func (d *DB) SaveAccount(account *domain.DriveAccount) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if account.ID == "" || account.Type == "" || account.Name == "" {
		return fmt.Errorf("account id, type, and name are required")
	}
	now := time.Now()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	isDefault := 0
	if account.IsDefault {
		isDefault = 1
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if account.IsDefault {
		if _, err := tx.Exec(`UPDATE drive_accounts SET is_default = 0 WHERE id != ?`, account.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO drive_accounts (id, type, name, cookie, token, is_default, status, quota_used, quota_total, vip_level, vip_expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			cookie = excluded.cookie,
			token = excluded.token,
			is_default = excluded.is_default,
			status = excluded.status,
			quota_used = excluded.quota_used,
			quota_total = excluded.quota_total,
			vip_level = excluded.vip_level,
			vip_expires_at = excluded.vip_expires_at,
			updated_at = excluded.updated_at
	`, account.ID, account.Type, account.Name, account.Cookie, account.Token, isDefault, account.Status, account.QuotaUsed, account.QuotaTotal, account.VIPLevel, account.VIPExpiresAt, account.CreatedAt, account.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) DeleteAccount(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`DELETE FROM drive_accounts WHERE id = ?`, id)
	return err
}

// Scheduled Tasks
func (d *DB) ListTasks() ([]domain.ScheduledTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT id, type, name, cron_expr, status, progress, params, result, error, last_run_at, next_run_at, created_at, updated_at FROM scheduled_tasks ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.ScheduledTask
	for rows.Next() {
		var t domain.ScheduledTask
		var lastRun, nextRun sql.NullTime
		var params, result, errStr, cronExpr sql.NullString
		if err := rows.Scan(&t.ID, &t.Type, &t.Name, &cronExpr, &t.Status, &t.Progress, &params, &result, &errStr, &lastRun, &nextRun, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if cronExpr.Valid {
			t.CronExpr = cronExpr.String
		}
		if params.Valid {
			t.Params = params.String
		}
		if result.Valid {
			t.Result = result.String
		}
		if errStr.Valid {
			t.Error = errStr.String
		}
		if lastRun.Valid {
			t.LastRunAt = &lastRun.Time
		}
		if nextRun.Valid {
			t.NextRunAt = &nextRun.Time
		}
		t.Enabled = t.Status != "paused"
		res = append(res, t)
	}
	return res, rows.Err()
}

func (d *DB) SaveTask(t *domain.ScheduledTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	_, err := d.db.Exec(`
		INSERT INTO scheduled_tasks (id, type, name, cron_expr, status, progress, params, result, error, last_run_at, next_run_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			cron_expr = excluded.cron_expr,
			status = excluded.status,
			progress = excluded.progress,
			params = excluded.params,
			result = excluded.result,
			error = excluded.error,
			last_run_at = excluded.last_run_at,
			next_run_at = excluded.next_run_at,
			updated_at = excluded.updated_at
	`, t.ID, t.Type, t.Name, t.CronExpr, t.Status, t.Progress, t.Params, t.Result, t.Error, t.LastRunAt, t.NextRunAt, t.CreatedAt, t.UpdatedAt)
	return err
}

func (d *DB) DeleteTask(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`DELETE FROM scheduled_tasks WHERE id = ?`, id)
	return err
}

func (d *DB) GetTask(id string) (*domain.ScheduledTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRow(`
		SELECT id, type, name, cron_expr, status, progress, params, result, error, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_tasks WHERE id = ?
	`, id)

	var t domain.ScheduledTask
	var lastRun, nextRun sql.NullTime
	if err := row.Scan(&t.ID, &t.Type, &t.Name, &t.CronExpr, &t.Status, &t.Progress, &t.Params, &t.Result, &t.Error, &lastRun, &nextRun, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if lastRun.Valid {
		t.LastRunAt = &lastRun.Time
	}
	if nextRun.Valid {
		t.NextRunAt = &nextRun.Time
	}
	t.Enabled = t.Status != "paused"
	return &t, nil
}

// Async Tasks
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAsyncTask(row rowScanner) (*domain.AsyncTask, error) {
	var task domain.AsyncTask
	var scheduleID, payload, result, errText sql.NullString
	if err := row.Scan(
		&task.ID, &task.Type, &scheduleID, &payload, &task.Status, &task.Progress, &result, &errText,
		&task.Attempts, &task.MaxAttempts, &task.CreatedAt, &task.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if scheduleID.Valid {
		task.ScheduleID = scheduleID.String
	}
	if payload.Valid && payload.String != "" {
		if err := json.Unmarshal([]byte(payload.String), &task.Payload); err != nil {
			return nil, fmt.Errorf("decode async task payload: %w", err)
		}
	}
	if result.Valid {
		task.Result = result.String
	}
	if errText.Valid {
		task.Error = errText.String
	}
	return &task, nil
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func prepareAsyncTask(task *domain.AsyncTask) (string, error) {
	payload, err := json.Marshal(task.Payload)
	if err != nil {
		return "", fmt.Errorf("encode async task payload: %w", err)
	}
	if task.Status == "" {
		task.Status = "pending"
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 1
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	task.UpdatedAt = task.CreatedAt
	return string(payload), nil
}

func insertAsyncTask(execer sqlExecer, task *domain.AsyncTask, payload string) error {
	_, err := execer.Exec(`
		INSERT INTO async_tasks (id, type, schedule_id, payload, status, progress, result, error, attempts, max_attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.Type, task.ScheduleID, payload, task.Status, task.Progress, task.Result, task.Error, task.Attempts, task.MaxAttempts, task.CreatedAt, task.UpdatedAt)
	return err
}

// CreateAsyncTask persists one standalone background execution.
func (d *DB) CreateAsyncTask(task *domain.AsyncTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	payload, err := prepareAsyncTask(task)
	if err != nil {
		return err
	}
	return insertAsyncTask(d.db, task, payload)
}

// CreateScheduledAsyncTask atomically creates one execution and records it as the schedule's latest launch.
func (d *DB) CreateScheduledAsyncTask(task *domain.AsyncTask, scheduleID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	task.ScheduleID = scheduleID
	payload, err := prepareAsyncTask(task)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertAsyncTask(tx, task, payload); err != nil {
		return err
	}
	updated, err := tx.Exec(`
		UPDATE scheduled_tasks SET last_run_at = ?, result = ?, error = '', updated_at = ? WHERE id = ?
	`, task.CreatedAt, task.ID, task.CreatedAt, scheduleID)
	if err != nil {
		return err
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("scheduled task %s not found", scheduleID)
	}
	return tx.Commit()
}

func (d *DB) ListAsyncTasks(status string, limit int) ([]domain.AsyncTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	query := `SELECT id, type, schedule_id, payload, status, progress, result, error, attempts, max_attempts, created_at, updated_at FROM async_tasks`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAsyncTasks(rows)
}

func collectAsyncTasks(rows *sql.Rows) ([]domain.AsyncTask, error) {
	result := make([]domain.AsyncTask, 0)
	for rows.Next() {
		task, err := scanAsyncTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *task)
	}
	return result, rows.Err()
}

// ListPendingAsyncTasks returns the oldest queued executions first.
func (d *DB) ListPendingAsyncTasks(limit int) ([]domain.AsyncTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	query := `SELECT id, type, schedule_id, payload, status, progress, result, error, attempts, max_attempts, created_at, updated_at FROM async_tasks WHERE status = 'pending' ORDER BY created_at ASC, id ASC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAsyncTasks(rows)

}

func (d *DB) GetAsyncTask(id string) (*domain.AsyncTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return scanAsyncTask(d.db.QueryRow(`
		SELECT id, type, schedule_id, payload, status, progress, result, error, attempts, max_attempts, created_at, updated_at
		FROM async_tasks WHERE id = ?
	`, id))
}

// BeginAsyncTask atomically claims a pending task and creates its execution record.
func (d *DB) BeginAsyncTask(id string) (*domain.TaskRun, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	now := time.Now()
	result, err := tx.Exec(`
		UPDATE async_tasks
		SET status = 'running', progress = 0, result = '', error = '', attempts = attempts + 1, updated_at = ?
		WHERE id = ? AND status = 'pending' AND attempts < max_attempts
	`, now, id)
	if err != nil {
		return nil, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return nil, false, err
	}
	var attempt int
	if err := tx.QueryRow(`SELECT attempts FROM async_tasks WHERE id = ?`, id).Scan(&attempt); err != nil {
		return nil, false, err
	}
	runResult, err := tx.Exec(`
		INSERT INTO task_runs (task_id, attempt, status, progress, logs, started_at)
		VALUES (?, ?, 'running', 0, '[]', ?)
	`, id, attempt, now)
	if err != nil {
		return nil, false, err
	}
	runID, err := runResult.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &domain.TaskRun{ID: runID, TaskID: id, Attempt: attempt, Status: "running", Logs: []string{}, StartedAt: now}, true, nil
}

// UpdateAsyncTaskProgress persists progress for the task and its active run.
func (d *DB) UpdateAsyncTaskProgress(id string, progress float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE async_tasks SET progress = ?, updated_at = ? WHERE id = ? AND status = 'running'`, progress, time.Now(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE task_runs SET progress = ? WHERE id = (SELECT id FROM task_runs WHERE task_id = ? ORDER BY id DESC LIMIT 1)`, progress, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendTaskLog appends one durable message to the active task run.
func (d *DB) AppendTaskLog(id, message string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var runID int64
	var encoded string
	if err := d.db.QueryRow(`SELECT id, logs FROM task_runs WHERE task_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&runID, &encoded); err != nil {
		return err
	}
	logs := make([]string, 0)
	if err := json.Unmarshal([]byte(encoded), &logs); err != nil {
		return fmt.Errorf("decode task logs: %w", err)
	}
	logs = append(logs, message)
	data, err := json.Marshal(logs)
	if err != nil {
		return fmt.Errorf("encode task logs: %w", err)
	}
	_, err = d.db.Exec(`UPDATE task_runs SET logs = ? WHERE id = ?`, string(data), runID)
	return err
}

// FinishAsyncTask atomically records one terminal result and its final log message.
func (d *DB) FinishAsyncTask(id, status string, progress float64, resultText, errText, logMessage string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var runID int64
	var encodedLogs string
	if err := tx.QueryRow(`SELECT id, logs FROM task_runs WHERE task_id = ? AND status = 'running' ORDER BY id DESC LIMIT 1`, id).Scan(&runID, &encodedLogs); err != nil {
		return fmt.Errorf("task %s has no running execution record: %w", id, err)
	}
	logs := make([]string, 0)
	if err := json.Unmarshal([]byte(encodedLogs), &logs); err != nil {
		return fmt.Errorf("decode task logs: %w", err)
	}
	if logMessage != "" {
		logs = append(logs, logMessage)
	}
	encodedLogsBytes, err := json.Marshal(logs)
	if err != nil {
		return fmt.Errorf("encode task logs: %w", err)
	}
	now := time.Now()
	taskUpdate, err := tx.Exec(`
		UPDATE async_tasks SET status = ?, progress = ?, result = ?, error = ?, updated_at = ?
		WHERE id = ? AND status = 'running'
	`, status, progress, resultText, errText, now, id)
	if err != nil {
		return err
	}
	changed, err := taskUpdate.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("task %s is no longer owned by the running worker", id)
	}
	runUpdate, err := tx.Exec(`
		UPDATE task_runs SET status = ?, progress = ?, logs = ?, error = ?, completed_at = ?
		WHERE id = ? AND status = 'running'
	`, status, progress, string(encodedLogsBytes), errText, now, runID)
	if err != nil {
		return err
	}
	changed, err = runUpdate.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("task %s lost its running execution record", id)
	}
	return tx.Commit()
}

// CancelPendingAsyncTask cancels a task that has not started.
func (d *DB) CancelPendingAsyncTask(id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE async_tasks SET status = 'cancelled', error = 'cancelled by operator', updated_at = ? WHERE id = ? AND status = 'pending'`, time.Now(), id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// RecoverInterruptedAsyncTasks fails running work without replaying possibly effectful operations.
func (d *DB) RecoverInterruptedAsyncTasks() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	message := "service restarted while task was running; verify upstream state before retrying"
	if _, err := tx.Exec(`UPDATE async_tasks SET status = 'failed', error = ?, updated_at = ? WHERE status = 'running'`, message, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE task_runs SET status = 'failed', error = ?, completed_at = ? WHERE status = 'running'`, message, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ListTaskRuns returns every execution attempt for a task in chronological order.
func (d *DB) ListTaskRuns(taskID string) ([]domain.TaskRun, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	rows, err := d.db.Query(`SELECT id, task_id, attempt, status, progress, logs, error, started_at, completed_at FROM task_runs WHERE task_id = ? ORDER BY attempt`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]domain.TaskRun, 0)
	for rows.Next() {
		var run domain.TaskRun
		var encoded string
		var errText sql.NullString
		var completed sql.NullTime
		if err := rows.Scan(&run.ID, &run.TaskID, &run.Attempt, &run.Status, &run.Progress, &encoded, &errText, &run.StartedAt, &completed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &run.Logs); err != nil {
			return nil, fmt.Errorf("decode task logs: %w", err)
		}
		if errText.Valid {
			run.Error = errText.String
		}
		if completed.Valid {
			run.CompletedAt = &completed.Time
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// Agent Tokens
func (d *DB) ListTokens() ([]domain.AgentToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	rows, err := d.db.Query(`SELECT id, token, name, role, scopes, rate_limit, enabled, last_used_at, created_at FROM agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AgentToken, 0)
	for rows.Next() {
		var token domain.AgentToken
		var scopes sql.NullString
		var enabled int
		var lastUsed sql.NullTime
		if err := rows.Scan(&token.ID, &token.Token, &token.Name, &token.Role, &scopes, &token.RateLimit, &enabled, &lastUsed, &token.CreatedAt); err != nil {
			return nil, err
		}
		token.Enabled = enabled == 1
		if lastUsed.Valid {
			token.LastUsedAt = &lastUsed.Time
		}
		if scopes.Valid && scopes.String != "" {
			if err := json.Unmarshal([]byte(scopes.String), &token.Scopes); err != nil {
				return nil, fmt.Errorf("decode token scopes: %w", err)
			}
		}
		result = append(result, token)
	}
	return result, rows.Err()
}

func (d *DB) SaveToken(token *domain.AgentToken) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if token.CreatedAt.IsZero() {
		token.CreatedAt = time.Now()
	}
	scopes, err := json.Marshal(token.Scopes)
	if err != nil {
		return fmt.Errorf("encode token scopes: %w", err)
	}
	enabled := 0
	if token.Enabled {
		enabled = 1
	}
	_, err = d.db.Exec(`
		INSERT INTO agent_tokens (id, token, name, role, scopes, rate_limit, enabled, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			token = excluded.token,
			name = excluded.name,
			role = excluded.role,
			scopes = excluded.scopes,
			rate_limit = excluded.rate_limit,
			enabled = excluded.enabled,
			last_used_at = excluded.last_used_at
	`, token.ID, token.Token, token.Name, token.Role, string(scopes), token.RateLimit, enabled, token.LastUsedAt, token.CreatedAt)
	return err
}

// GetTokenByDigest resolves an Agent token from its stored SHA-256 digest.
func (d *DB) GetTokenByDigest(digest string) (*domain.AgentToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var token domain.AgentToken
	var scopes sql.NullString
	var enabled int
	var lastUsed sql.NullTime
	err := d.db.QueryRow(`
		SELECT id, token, name, role, scopes, rate_limit, enabled, last_used_at, created_at
		FROM agent_tokens WHERE token = ?
	`, digest).Scan(&token.ID, &token.Token, &token.Name, &token.Role, &scopes, &token.RateLimit, &enabled, &lastUsed, &token.CreatedAt)
	if err != nil {
		return nil, err
	}
	token.Enabled = enabled == 1
	if scopes.Valid && scopes.String != "" {
		if err := json.Unmarshal([]byte(scopes.String), &token.Scopes); err != nil {
			return nil, fmt.Errorf("decode token scopes: %w", err)
		}
	}
	if lastUsed.Valid {
		token.LastUsedAt = &lastUsed.Time
	}
	return &token, nil
}

// TouchToken records successful Agent token use.
func (d *DB) TouchToken(id string, usedAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE agent_tokens SET last_used_at = ? WHERE id = ?`, usedAt, id)
	return err
}

func (d *DB) DeleteToken(id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`DELETE FROM agent_tokens WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// Audit Logs
func (d *DB) AddAuditLog(logEntry *domain.AuditLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if logEntry.CreatedAt.IsZero() {
		logEntry.CreatedAt = time.Now()
	}
	result, err := d.db.Exec(`
		INSERT INTO agent_audit_logs (caller, token_id, agent_name, action, target, input, output, status, latency_ms, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, logEntry.Caller, logEntry.TokenID, logEntry.AgentName, logEntry.Action, logEntry.Target, logEntry.Input, logEntry.Output, logEntry.Status, logEntry.LatencyMS, logEntry.IP, logEntry.CreatedAt)
	if err != nil {
		return err
	}
	logEntry.ID, err = result.LastInsertId()
	return err
}

func (d *DB) ListAuditLogs(limit int) ([]domain.AuditLog, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.db.Query(`
		SELECT id, caller, token_id, agent_name, action, target, input, output, status, latency_ms, ip, created_at
		FROM agent_audit_logs ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AuditLog, 0)
	for rows.Next() {
		var entry domain.AuditLog
		var tokenID, agentName, input, output, ip sql.NullString
		if err := rows.Scan(
			&entry.ID, &entry.Caller, &tokenID, &agentName, &entry.Action, &entry.Target,
			&input, &output, &entry.Status, &entry.LatencyMS, &ip, &entry.CreatedAt,
		); err != nil {
			return nil, err
		}
		if tokenID.Valid {
			entry.TokenID = tokenID.String
		}
		if agentName.Valid {
			entry.AgentName = agentName.String
		}
		if input.Valid {
			entry.Input = input.String
		}
		if output.Valid {
			entry.Output = output.String
		}
		if ip.Valid {
			entry.IP = ip.String
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func encodeApprovalTargets(targets []domain.DestructiveTarget) (string, error) {
	encoded, err := json.Marshal(targets)
	if err != nil {
		return "", fmt.Errorf("encode destructive approval targets: %w", err)
	}
	return string(encoded), nil
}

func scanDestructiveApproval(scanner interface{ Scan(...any) error }) (*domain.DestructiveApproval, error) {
	var approval domain.DestructiveApproval
	var targets string
	var approvedBy, errorText sql.NullString
	var decidedAt, executedAt sql.NullTime
	if err := scanner.Scan(&approval.ID, &approval.Action, &approval.AccountID, &approval.ParentCID, &targets, &approval.Status, &approval.RequestedBy, &approvedBy, &errorText, &approval.ExpiresAt, &approval.CreatedAt, &decidedAt, &executedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(targets), &approval.Targets); err != nil {
		return nil, fmt.Errorf("decode destructive approval targets: %w", err)
	}
	if approvedBy.Valid {
		approval.ApprovedBy = approvedBy.String
	}
	if errorText.Valid {
		approval.Error = errorText.String
	}
	if decidedAt.Valid {
		approval.DecidedAt = &decidedAt.Time
	}
	if executedAt.Valid {
		approval.ExecutedAt = &executedAt.Time
	}
	return &approval, nil
}

// CreateDestructiveApproval stores one pending target-bound user decision.
func (d *DB) CreateDestructiveApproval(approval *domain.DestructiveApproval) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	targets, err := encodeApprovalTargets(approval.Targets)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO destructive_approvals (id, action, account_id, parent_cid, targets, status, requested_by, expires_at, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?)`, approval.ID, approval.Action, approval.AccountID, approval.ParentCID, targets, approval.RequestedBy, approval.ExpiresAt, approval.CreatedAt)
	return err
}

// ListPendingDestructiveApprovals returns unexpired requests awaiting a browser user.
func (d *DB) ListPendingDestructiveApprovals(now time.Time) ([]domain.DestructiveApproval, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.db.Exec(`UPDATE destructive_approvals SET status = 'expired' WHERE status = 'pending' AND expires_at <= ?`, now); err != nil {
		return nil, err
	}
	rows, err := d.db.Query(`SELECT id, action, account_id, parent_cid, targets, status, requested_by, approved_by, error, expires_at, created_at, decided_at, executed_at FROM destructive_approvals WHERE status = 'pending' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.DestructiveApproval, 0)
	for rows.Next() {
		approval, err := scanDestructiveApproval(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *approval)
	}
	return result, rows.Err()
}

// DecideDestructiveApproval records one browser user's pending approval decision.
func (d *DB) DecideDestructiveApproval(id, user, status string, now time.Time) (*domain.DestructiveApproval, error) {
	if status != "approved" && status != "rejected" {
		return nil, fmt.Errorf("invalid destructive approval decision")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE destructive_approvals SET status = ?, approved_by = ?, decided_at = ? WHERE id = ? AND status = 'pending' AND expires_at > ?`, status, user, now, id, now)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, fmt.Errorf("destructive approval is missing, expired, or already decided")
	}
	return scanDestructiveApproval(d.db.QueryRow(`SELECT id, action, account_id, parent_cid, targets, status, requested_by, approved_by, error, expires_at, created_at, decided_at, executed_at FROM destructive_approvals WHERE id = ?`, id))
}

// ClaimDestructiveApproval consumes one approved request before the provider mutation starts.
func (d *DB) ClaimDestructiveApproval(id, action string, now time.Time) (*domain.DestructiveApproval, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE destructive_approvals SET status = 'executing' WHERE id = ? AND action = ? AND status = 'approved' AND expires_at > ?`, id, action, now)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, fmt.Errorf("destructive approval is missing, expired, already used, or not approved")
	}
	return scanDestructiveApproval(d.db.QueryRow(`SELECT id, action, account_id, parent_cid, targets, status, requested_by, approved_by, error, expires_at, created_at, decided_at, executed_at FROM destructive_approvals WHERE id = ?`, id))
}

// FinishDestructiveApproval records the terminal provider result without permitting replay.
func (d *DB) FinishDestructiveApproval(id, status, errorText string, now time.Time) error {
	if status != "executed" && status != "failed" {
		return fmt.Errorf("invalid destructive approval terminal status")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE destructive_approvals SET status = ?, error = ?, executed_at = ? WHERE id = ? AND status = 'executing'`, status, errorText, now, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("destructive approval is not executing")
	}
	return nil
}

// Settings
func (d *DB) GetAllSettings() (map[string]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			res[k] = v
		}
	}
	return res, nil
}

func (d *DB) GetSetting(key string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val string
	err := d.db.QueryRow(`SELECT value FROM system_settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (d *DB) SetSetting(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		INSERT INTO system_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, time.Now())
	return err
}

// SetSettings atomically upserts deployment settings.
func (d *DB) SetSettings(settings map[string]string) error {
	return d.SetSettingsAndDefaultAccountCookie(settings, "")
}

// SetSettingsAndDefaultAccountCookie atomically updates settings and the managed default account credential.
func (d *DB) SetSettingsAndDefaultAccountCookie(settings map[string]string, cookie string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	for key, value := range settings {
		if _, err := tx.Exec(`
			INSERT INTO system_settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, now); err != nil {
			return err
		}
	}
	if cookie != "" {
		if _, err := tx.Exec(`
			UPDATE drive_accounts
			SET cookie = ?,
				type = CASE WHEN TRIM(type) = '' THEN '115' ELSE type END,
				name = CASE WHEN TRIM(name) = '' THEN '默认账号' ELSE name END,
				updated_at = ?
			WHERE id = (
				SELECT id FROM drive_accounts
				ORDER BY is_default DESC, created_at ASC
				LIMIT 1
			)
		`, cookie, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// System Config
func (d *DB) GetConfig(key string) (string, error) {
	val, err := d.GetSetting(key)
	if err == nil && val != "" {
		return val, nil
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	var configVal string
	err = d.db.QueryRow(`SELECT value FROM system_configs WHERE key = ?`, key).Scan(&configVal)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return configVal, err
}

func (d *DB) SetConfig(key, val, desc string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		INSERT INTO system_configs (key, value, description, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			description = excluded.description,
			updated_at = excluded.updated_at
	`, key, val, desc, time.Now())
	return err
}

func (d *DB) ListConfigs() (map[string]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT key, value FROM system_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		res[k] = v
	}
	return res, nil
}

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

	CREATE TABLE IF NOT EXISTS agent_tokens (
		id TEXT PRIMARY KEY,
		token TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		role TEXT DEFAULT 'agent',
		scopes TEXT,
		rate_limit INTEGER DEFAULT 60,
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

	_, err := d.db.Exec(schema)
	return err
}

// Drive Accounts
func (d *DB) ListAccounts() ([]domain.DriveAccount, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT id, type, name, cookie, token, is_default, status, quota_used, quota_total, created_at, updated_at FROM drive_accounts ORDER BY is_default DESC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.DriveAccount
	for rows.Next() {
		var a domain.DriveAccount
		var isDef int
		var created, updated time.Time
		if err := rows.Scan(&a.ID, &a.Type, &a.Name, &a.Cookie, &a.Token, &isDef, &a.Status, &a.QuotaUsed, &a.QuotaTotal, &created, &updated); err != nil {
			return nil, err
		}
		a.IsDefault = isDef == 1
		a.CreatedAt = created
		a.UpdatedAt = updated
		res = append(res, a)
	}
	return res, nil
}

func (d *DB) SaveAccount(a *domain.DriveAccount) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	isDef := 0
	if a.IsDefault {
		isDef = 1
		// Clear other defaults
		_, _ = d.db.Exec(`UPDATE drive_accounts SET is_default = 0 WHERE id != ?`, a.ID)
	}

	_, err := d.db.Exec(`
		INSERT INTO drive_accounts (id, type, name, cookie, token, is_default, status, quota_used, quota_total, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			cookie = excluded.cookie,
			token = excluded.token,
			is_default = excluded.is_default,
			status = excluded.status,
			quota_used = excluded.quota_used,
			quota_total = excluded.quota_total,
			updated_at = excluded.updated_at
	`, a.ID, a.Type, a.Name, a.Cookie, a.Token, isDef, a.Status, a.QuotaUsed, a.QuotaTotal, a.CreatedAt, a.UpdatedAt)
	return err
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
		res = append(res, t)
	}
	return res, nil
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
func (d *DB) CreateAsyncTask(t *domain.AsyncTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var payloadStr string
	if t.Payload != nil {
		b, _ := json.Marshal(t.Payload)
		payloadStr = string(b)
	}

	_, err := d.db.Exec(`
		INSERT INTO scheduled_tasks (id, type, name, cron_expr, status, progress, params, error, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
	`, t.ID, t.Type, t.Type, t.Status, t.Progress, payloadStr, t.Error, t.CreatedAt, time.Now())
	return err
}

func (d *DB) ListAsyncTasks(status string, limit int) ([]domain.AsyncTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, type, params, status, progress, error, created_at, updated_at FROM scheduled_tasks`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.AsyncTask
	for rows.Next() {
		var t domain.AsyncTask
		var params, errStr sql.NullString
		if err := rows.Scan(&t.ID, &t.Type, &params, &t.Status, &t.Progress, &errStr, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if params.Valid && params.String != "" {
			_ = json.Unmarshal([]byte(params.String), &t.Payload)
		}
		if errStr.Valid {
			t.Error = errStr.String
		}
		res = append(res, t)
	}
	return res, nil
}

func (d *DB) GetAsyncTask(id string) (*domain.AsyncTask, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRow(`
		SELECT id, type, params, status, progress, error, created_at, updated_at
		FROM scheduled_tasks WHERE id = ?
	`, id)

	var t domain.AsyncTask
	var params, errStr sql.NullString
	if err := row.Scan(&t.ID, &t.Type, &params, &t.Status, &t.Progress, &errStr, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if params.Valid && params.String != "" {
		_ = json.Unmarshal([]byte(params.String), &t.Payload)
	}
	if errStr.Valid {
		t.Error = errStr.String
	}
	return &t, nil
}

func (d *DB) UpdateAsyncTaskStatus(id, status string, progress float64, errStr string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE scheduled_tasks SET status = ?, progress = ?, error = ?, updated_at = ?
		WHERE id = ?
	`, status, progress, errStr, time.Now(), id)
	return err
}

// Agent Tokens
func (d *DB) ListTokens() ([]domain.AgentToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT id, token, name, role, scopes, rate_limit, last_used_at, created_at FROM agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.AgentToken
	for rows.Next() {
		var tok domain.AgentToken
		var scopes sql.NullString
		var lastUsed sql.NullTime
		if err := rows.Scan(&tok.ID, &tok.Token, &tok.Name, &tok.Role, &scopes, &tok.RateLimit, &lastUsed, &tok.CreatedAt); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			tok.LastUsedAt = &lastUsed.Time
		}
		res = append(res, tok)
	}
	return res, nil
}

func (d *DB) SaveToken(tok *domain.AgentToken) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if tok.CreatedAt.IsZero() {
		tok.CreatedAt = time.Now()
	}

	_, err := d.db.Exec(`
		INSERT INTO agent_tokens (id, token, name, role, scopes, rate_limit, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			token = excluded.token,
			name = excluded.name,
			role = excluded.role,
			scopes = excluded.scopes,
			rate_limit = excluded.rate_limit,
			last_used_at = excluded.last_used_at
	`, tok.ID, tok.Token, tok.Name, tok.Role, "", tok.RateLimit, tok.LastUsedAt, tok.CreatedAt)
	return err
}

func (d *DB) DeleteToken(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`DELETE FROM agent_tokens WHERE id = ?`, id)
	return err
}

// Audit Logs
func (d *DB) AddAuditLog(log *domain.AuditLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}

	_, err := d.db.Exec(`
		INSERT INTO audit_logs (caller, token_id, action, target, details, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, log.Caller, log.TokenID, log.Action, log.Target, log.Details, log.IP, log.CreatedAt)
	return err
}

func (d *DB) ListAuditLogs(limit int) ([]domain.AuditLog, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	rows, err := d.db.Query(`SELECT id, caller, token_id, action, target, details, ip, created_at FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []domain.AuditLog
	for rows.Next() {
		var l domain.AuditLog
		var tokenID, details, ip sql.NullString
		if err := rows.Scan(&l.ID, &l.Caller, &tokenID, &l.Action, &l.Target, &details, &ip, &l.CreatedAt); err != nil {
			return nil, err
		}
		if tokenID.Valid {
			l.TokenID = tokenID.String
		}
		if details.Valid {
			l.Details = details.String
		}
		if ip.Valid {
			l.IP = ip.String
		}
		res = append(res, l)
	}
	return res, nil
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
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for key, value := range settings {
		if _, err := tx.Exec(`
			INSERT INTO system_settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, time.Now()); err != nil {
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

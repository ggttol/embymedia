package storage

import (
	"context"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

type AuditQuery struct {
	Limit    int
	Offset   int
	Protocol string
	Agent    string
	Action   string
	Status   string
	From     *time.Time
	To       *time.Time
	Q        string
}

type AuditSummary struct {
	Total        int64   `json:"total"`
	Success      int64   `json:"success"`
	Error        int64   `json:"error"`
	Denied       int64   `json:"denied"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

type AuditToolSummary struct {
	Action       string  `json:"action"`
	Total        int64   `json:"total"`
	Error        int64   `json:"error"`
	Denied       int64   `json:"denied"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

type AuditQueryResult struct {
	Logs    []domain.AuditLog  `json:"logs"`
	Total   int64              `json:"total"`
	Summary AuditSummary       `json:"summary"`
	Tools   []AuditToolSummary `json:"tools"`
	Agents  []string           `json:"agents"`
}

// modernc stores time.Time.String(), including a numeric zone and optional
// fractional seconds. Normalize whole seconds to UTC and retain nanoseconds;
// SQLite's Julian-day conversion alone rounds sub-millisecond boundaries.
const auditQuerySource = `WITH audit_clock AS (
	SELECT *, instr(substr(created_at, 20), ' ') + 19 AS zone_start FROM agent_audit_logs
), audits AS (
	SELECT *, strftime('%Y-%m-%dT%H:%M:%S', substr(created_at, 1, 19) ||
		substr(created_at, zone_start + 1, 3) || ':' || substr(created_at, zone_start + 4, 2)) || '.' ||
		substr(CASE WHEN substr(created_at, 20, 1) = '.'
			THEN substr(created_at, 21, zone_start - 21) ELSE '' END || '000000000', 1, 9) AS audit_time
	FROM audit_clock
) `

const auditTimeLayout = "2006-01-02T15:04:05.000000000"

// QueryAuditLogs returns stored redacted records and aggregates from one read snapshot.
// Pagination affects only Logs; Agents contains global MCP agent names.
func (d *DB) QueryAuditLogs(ctx context.Context, query AuditQuery) (AuditQueryResult, error) {
	result := AuditQueryResult{Logs: []domain.AuditLog{}, Tools: []AuditToolSummary{}, Agents: []string{}}
	conditions := make([]string, 0, 7)
	args := make([]any, 0, 7)
	if query.Protocol == "mcp" {
		conditions = append(conditions, "target = ?")
		args = append(args, "mcp/tools/call")
	}
	for _, filter := range []struct{ column, value string }{{"agent_name", query.Agent}, {"action", query.Action}, {"status", query.Status}} {
		if filter.value != "" {
			conditions = append(conditions, filter.column+" = ?")
			args = append(args, filter.value)
		}
	}
	if query.From != nil {
		conditions = append(conditions, "audit_time >= ?")
		args = append(args, query.From.UTC().Format(auditTimeLayout))
	}
	if query.To != nil {
		conditions = append(conditions, "audit_time <= ?")
		args = append(args, query.To.UTC().Format(auditTimeLayout))
	}
	if query.Q != "" {
		conditions = append(conditions, "(instr(action, ?) > 0 OR instr(agent_name, ?) > 0 OR instr(input, ?) > 0 OR instr(output, ?) > 0)")
		args = append(args, query.Q, query.Q, query.Q, query.Q)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, auditQuerySource+`SELECT count(*),
		coalesce(sum(status = 'success'), 0), coalesce(sum(status = 'error'), 0),
		coalesce(sum(status = 'denied'), 0), coalesce(avg(latency_ms), 0)
		FROM audits`+where, args...).Scan(&result.Summary.Total, &result.Summary.Success,
		&result.Summary.Error, &result.Summary.Denied, &result.Summary.AvgLatencyMS); err != nil {
		return result, err
	}
	result.Total = result.Summary.Total
	rows, err := tx.QueryContext(ctx, auditQuerySource+`SELECT action, count(*),
		coalesce(sum(status = 'error'), 0), coalesce(sum(status = 'denied'), 0), coalesce(avg(latency_ms), 0)
		FROM audits`+where+` GROUP BY action ORDER BY count(*) DESC, action ASC`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var tool AuditToolSummary
		if err := rows.Scan(&tool.Action, &tool.Total, &tool.Error, &tool.Denied, &tool.AvgLatencyMS); err != nil {
			rows.Close()
			return result, err
		}
		result.Tools = append(result.Tools, tool)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	pageArgs := append(args, query.Limit, query.Offset)
	rows, err = tx.QueryContext(ctx, auditQuerySource+`SELECT l.id, l.caller, coalesce(l.token_id, ''),
		coalesce(l.agent_name, ''), l.action, l.target, coalesce(l.input, ''), coalesce(l.output, ''),
		l.status, l.latency_ms, coalesce(l.ip, ''), l.created_at FROM agent_audit_logs l
		JOIN (SELECT id, audit_time FROM audits`+where+` ORDER BY audit_time DESC, id DESC LIMIT ? OFFSET ?) page
		ON page.id = l.id ORDER BY page.audit_time DESC, l.id DESC`, pageArgs...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var entry domain.AuditLog
		if err := rows.Scan(&entry.ID, &entry.Caller, &entry.TokenID, &entry.AgentName, &entry.Action,
			&entry.Target, &entry.Input, &entry.Output, &entry.Status, &entry.LatencyMS, &entry.IP, &entry.CreatedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Logs = append(result.Logs, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT agent_name FROM agent_audit_logs
		WHERE target = ? AND agent_name IS NOT NULL AND agent_name <> '' ORDER BY agent_name`, "mcp/tools/call")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return result, err
		}
		result.Agents = append(result.Agents, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

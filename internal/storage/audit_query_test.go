package storage

import (
	"context"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestQueryAuditLogsFiltersAndAggregatesBeyondPage(t *testing.T) {
	db, err := Open(t.TempDir() + "/audit.db")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	base := time.Date(2026, time.September, 6, 1, 23, 37, 581779321, time.FixedZone("", 8*60*60))
	entries := []domain.AuditLog{
		{Caller: "agent", AgentName: "Hermes", Action: "c115_search", Target: "mcp/tools/call", Input: `{"q":"100%_match"}`, Status: "success", LatencyMS: 10, CreatedAt: base},
		{Caller: "agent", AgentName: "Hermes", Action: "c115_search", Target: "mcp/tools/call", Output: `{"error":"100%_match"}`, Status: "error", LatencyMS: 30, CreatedAt: base},
		{Caller: "agent", AgentName: "Claude", Action: "task_query", Target: "mcp/tools/call", Status: "denied", LatencyMS: 50, CreatedAt: base.Add(2 * time.Second)},
		{Caller: "agent", AgentName: "Hermes", Action: "GET /api/v1/files", Target: "/api/v1/files", Status: "success", LatencyMS: 70, CreatedAt: base.Add(3 * time.Second)},
	}
	for i := range entries {
		if err := db.AddAuditLog(&entries[i]); err != nil {
			t.Fatalf("add audit %d: %v", i, err)
		}
	}
	from := base
	to := base
	result, err := db.QueryAuditLogs(context.Background(), AuditQuery{Limit: 1, Protocol: "mcp", Agent: "Hermes", Action: "c115_search", From: &from, To: &to, Q: "%_"})
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if len(result.Logs) != 1 || result.Logs[0].Status != "error" {
		t.Fatalf("newest page = %+v", result.Logs)
	}
	if !result.Logs[0].CreatedAt.Equal(base) {
		t.Fatalf("audit timestamp = %s, want instant %s", result.Logs[0].CreatedAt, base)
	}
	if result.Total != 2 || result.Summary.Total != 2 || result.Summary.Success != 1 || result.Summary.Error != 1 || result.Summary.AvgLatencyMS != 20 {
		t.Fatalf("summary = %+v, total = %d", result.Summary, result.Total)
	}
	if len(result.Tools) != 1 || result.Tools[0].Total != 2 || result.Tools[0].AvgLatencyMS != 20 {
		t.Fatalf("tools = %+v", result.Tools)
	}
	if len(result.Agents) != 2 || result.Agents[0] != "Claude" || result.Agents[1] != "Hermes" {
		t.Fatalf("agents = %+v", result.Agents)
	}
	statusResult, err := db.QueryAuditLogs(context.Background(), AuditQuery{Limit: 20, Protocol: "mcp", Status: "denied"})
	if err != nil || statusResult.Total != 1 || len(statusResult.Logs) != 1 || statusResult.Logs[0].AgentName != "Claude" {
		t.Fatalf("status-filtered result = %+v, err = %v", statusResult, err)
	}
}

func TestQueryAuditLogsReturnsEmptyArrays(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	result, err := db.QueryAuditLogs(context.Background(), AuditQuery{Limit: 20, Protocol: "all"})
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if result.Logs == nil || result.Tools == nil || result.Agents == nil {
		t.Fatalf("empty collections must be arrays: %+v", result)
	}
}

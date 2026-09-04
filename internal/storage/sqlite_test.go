package storage

import (
	"os"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestStorageOperations(t *testing.T) {
	dbFile := "/tmp/test_embymedia.db"
	_ = os.Remove(dbFile)
	defer os.Remove(dbFile)

	db, err := Open(dbFile)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	// 1. Test DriveAccount
	acc := &domain.DriveAccount{
		ID:        "acc-115-test",
		Type:      "115",
		Name:      "Test 115 Drive",
		Cookie:    "UID=test; CID=test; SEID=test",
		IsDefault: true,
		Status:    "active",
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("failed to save account: %v", err)
	}

	accs, err := db.ListAccounts()
	if err != nil || len(accs) != 1 {
		t.Fatalf("expected 1 account, got %d: %v", len(accs), err)
	}
	if accs[0].Name != "Test 115 Drive" || !accs[0].IsDefault {
		t.Fatalf("unexpected account: %+v", accs[0])
	}

	// 2. Test ScheduledTask
	task := &domain.ScheduledTask{
		ID:       "task-1",
		Type:     "refresh_library",
		Name:     "Periodic Refresh",
		CronExpr: "0 * * * *",
		Status:   "pending",
	}
	if err := db.SaveTask(task); err != nil {
		t.Fatalf("failed to save task: %v", err)
	}

	tasks, err := db.ListTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d: %v", len(tasks), err)
	}

	// 3. Test AgentToken
	tok := &domain.AgentToken{
		ID:        "tok-1",
		Token:     "embymedia_agent_secret_key",
		Name:      "Claude Code Agent",
		Role:      "agent",
		RateLimit: 120,
	}
	if err := db.SaveToken(tok); err != nil {
		t.Fatalf("failed to save token: %v", err)
	}

	tokens, err := db.ListTokens()
	if err != nil || len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d: %v", len(tokens), err)
	}

	// 4. Test AuditLog
	if err := db.AddAuditLog(&domain.AuditLog{
		Caller:  "agent",
		TokenID: tok.ID,
		Action:  "115_add_offline",
		Target:  "magnet:?xt=urn:btih:test",
		Details: "Offline task submitted",
	}); err != nil {
		t.Fatalf("failed to add audit log: %v", err)
	}

	logs, err := db.ListAuditLogs(10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d: %v", len(logs), err)
	}

	// 5. Test Config
	if err := db.SetConfig("emby_url", "http://127.0.0.1:8096", "Emby server URL"); err != nil {
		t.Fatalf("failed to set config: %v", err)
	}
	val, err := db.GetConfig("emby_url")
	if err != nil || val != "http://127.0.0.1:8096" {
		t.Fatalf("expected config value, got %s: %v", val, err)
	}
}

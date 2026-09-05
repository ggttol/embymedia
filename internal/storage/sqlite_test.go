package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if !tasks[0].Enabled {
		t.Fatal("persisted non-paused schedule was not restored as enabled")
	}

	// 3. Test AgentToken
	tok := &domain.AgentToken{
		ID:        "tok-1",
		Token:     "embymedia_agent_secret_key",
		Name:      "Claude Code Agent",
		Role:      "agent",
		RateLimit: 120,
		Enabled:   true,
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
		Output:  "Offline task submitted",
		Status:  "success",
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

func TestDestructiveApprovalRequiresDecisionAndCannotReplay(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "approval.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	now := time.Now()
	approval := &domain.DestructiveApproval{ID: "approval-1", Action: "c115.delete", AccountID: "account", ParentCID: "parent", Targets: []domain.DestructiveTarget{{FileID: "file", Name: "Movie.mkv"}}, Status: "pending", RequestedBy: "agent", ExpiresAt: now.Add(time.Minute), CreatedAt: now}
	if err := db.CreateDestructiveApproval(approval); err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := db.ClaimDestructiveApproval(approval.ID, approval.Action, now); err == nil {
		t.Fatal("unapproved deletion was claimable")
	}
	pending, err := db.ListPendingDestructiveApprovals(now)
	if err != nil || len(pending) != 1 || pending[0].Targets[0].Name != "Movie.mkv" {
		t.Fatalf("pending approvals = %+v, err=%v", pending, err)
	}
	decided, err := db.DecideDestructiveApproval(approval.ID, "operator", "approved", now)
	if err != nil || decided.Status != "approved" || decided.ApprovedBy != "operator" {
		t.Fatalf("approve = %+v, err=%v", decided, err)
	}
	claimed, err := db.ClaimDestructiveApproval(approval.ID, approval.Action, now)
	if err != nil || claimed.Status != "executing" {
		t.Fatalf("claim = %+v, err=%v", claimed, err)
	}
	if _, err := db.ClaimDestructiveApproval(approval.ID, approval.Action, now); err == nil {
		t.Fatal("approval was replayable")
	}
	if err := db.FinishDestructiveApproval(approval.ID, "executed", "", now); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestSetSettingsUpdatesDefaultAccountCredential(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	now := time.Now()
	if _, err := db.db.Exec(`
		INSERT INTO drive_accounts (id, type, name, cookie, is_default, status, created_at, updated_at)
		VALUES ('default', '', '默认账号', 'old-cookie', 1, 'active', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed legacy account: %v", err)
	}

	if err := db.SetSettingsAndDefaultAccountCookie(map[string]string{"115_cookie": "new-cookie"}, "new-cookie"); err != nil {
		t.Fatalf("update credential: %v", err)
	}
	var accountType, cookie string
	if err := db.db.QueryRow(`SELECT type, cookie FROM drive_accounts WHERE id = 'default'`).Scan(&accountType, &cookie); err != nil {
		t.Fatalf("read updated account: %v", err)
	}
	if accountType != "115" || cookie != "new-cookie" {
		t.Fatalf("default account not synchronized: type=%q cookie=%q", accountType, cookie)
	}
}

func TestOpenRepairsLegacyDefaultAccount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.SetSetting("115_cookie", "current-cookie"); err != nil {
		t.Fatalf("seed current credential: %v", err)
	}
	now := time.Now()
	if _, err := db.db.Exec(`
		INSERT INTO drive_accounts (id, type, name, cookie, token, is_default, status, created_at, updated_at)
		VALUES ('default', '', '默认账号', 'stale-cookie', '', 1, 'active', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed legacy account: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	accounts, err := reopened.ListAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("list repaired accounts: %+v, err=%v", accounts, err)
	}
	if accounts[0].Type != "115" || accounts[0].Cookie != "current-cookie" {
		t.Fatalf("legacy account not repaired: %+v", accounts[0])
	}
}

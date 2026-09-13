package storage

import (
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestRecoverInterruptedAsyncTasksRequeuesOnlyCrossDriveImports(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	legacy := &domain.AsyncTask{ID: "legacy", Type: "c115_save_share", Payload: map[string]any{}, Status: "pending", MaxAttempts: 1, CreatedAt: now}
	transfer := &domain.AsyncTask{ID: "transfer", Type: "quark_to_115_import", Payload: map[string]any{}, Status: "pending", MaxAttempts: 1, CreatedAt: now.Add(time.Millisecond)}
	if err := db.CreateAsyncTask(legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAsyncTask(transfer); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(legacy.ID); err != nil || !claimed {
		t.Fatalf("claim legacy: %v", err)
	}
	if _, claimed, err := db.BeginAsyncTask(transfer.ID); err != nil || !claimed {
		t.Fatalf("claim transfer: %v", err)
	}
	if err := db.UpdateAsyncTaskProgress(transfer.ID, 72); err != nil {
		t.Fatal(err)
	}
	state := &domain.CrossDriveImport{TaskID: transfer.ID, QuarkAccountID: "quark", QuarkTargetID: "target", C115AccountID: "c115"}
	if err := db.CreateCrossDriveImport(state); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCrossDriveImport(transfer.ID, "old-owner"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecoverInterruptedAsyncTasks(); err != nil {
		t.Fatal(err)
	}
	legacyAfter, _ := db.GetAsyncTask(legacy.ID)
	transferAfter, _ := db.GetAsyncTask(transfer.ID)
	if legacyAfter.Status != "failed" {
		t.Fatalf("legacy status = %s", legacyAfter.Status)
	}
	if transferAfter.Status != "pending" || transferAfter.MaxAttempts < 2 || transferAfter.Progress != 72 {
		t.Fatalf("transfer = %+v", transferAfter)
	}
	run, claimed, err := db.BeginAsyncTask(transfer.ID)
	if err != nil || !claimed || run.Progress != 72 {
		t.Fatalf("reclaim transfer: run=%+v claimed=%t err=%v", run, claimed, err)
	}
	if err := db.ClaimCrossDriveImport(transfer.ID, "new-owner"); err != nil {
		t.Fatalf("stale owner was not released: %v", err)
	}
}

func TestProviderDefaultsAreScopedByType(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, account := range []*domain.DriveAccount{{ID: "115-a", Type: "115", Name: "115 A", IsDefault: true}, {ID: "quark-a", Type: "quark", Name: "Quark A", IsDefault: true}, {ID: "115-b", Type: "115", Name: "115 B", IsDefault: true}} {
		if err := db.SaveAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := db.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]string{}
	for _, account := range accounts {
		if account.IsDefault {
			defaults[account.Type] = account.ID
		}
	}
	if defaults["115"] != "115-b" || defaults["quark"] != "quark-a" || len(defaults) != 2 {
		t.Fatalf("provider defaults: %+v", defaults)
	}
}

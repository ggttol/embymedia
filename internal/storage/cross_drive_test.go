package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestMigrationRedactsPersistedQuarkSignedURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signed-url.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	leaked := `Get "https://dl-pc.example/file?OSSAccessKeyId=secret&callback-var=secret": timeout`
	task := &domain.AsyncTask{ID: "leaked", Type: "quark_to_115_import", Payload: map[string]any{}, Status: "failed", Error: leaked, MaxAttempts: 1}
	if err := db.CreateAsyncTask(task); err != nil {
		t.Fatal(err)
	}
	state := &domain.CrossDriveImport{TaskID: task.ID, QuarkAccountID: "quark", QuarkTargetID: "0", C115AccountID: "115"}
	if err := db.CreateCrossDriveImport(state); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCrossDriveImport(task.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	item := &domain.CrossDriveItem{SourceFileID: "source", RelativePath: "file.txt", Name: "file.txt", Size: 1}
	if err := db.UpsertCrossDriveItem(task.ID, "owner", item); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListCrossDriveItems(task.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v %v", items, err)
	}
	if err := db.MarkCrossDriveItemFailed(items[0].ID, task.ID, "owner", leaked); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err = db.GetAsyncTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := db.GetCrossDriveImportDetail(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(task.Error, "secret") || strings.Contains(detail.Items[0].Error, "secret") {
		t.Fatalf("signed URL remained persisted: task=%q item=%q", task.Error, detail.Items[0].Error)
	}
}

func TestCrossDriveImportJSONHidesAutofillBinding(t *testing.T) {
	state := domain.CrossDriveImport{SelectedSourceIDs: []string{"source"}, AutofillLibraryName: "电视剧追更", AutofillLibraryID: "library", AutofillLibraryCID: "library-cid", AutofillSeriesID: "series", AutofillTMDBID: "42", AutofillSeriesFolder: "Show", ExpectedEpisodes: []string{"S01E02"}}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"source", "电视剧追更", "library-cid", "series", "S01E02"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("public import detail exposed internal binding %q: %s", secret, encoded)
		}
	}
}

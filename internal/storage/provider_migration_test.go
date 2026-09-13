package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestProviderDefaultsMigrateFromPreChangeDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-provider-defaults.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE drive_accounts (
			id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, cookie TEXT, token TEXT,
			is_default INTEGER DEFAULT 0, status TEXT DEFAULT 'active', quota_used INTEGER DEFAULT 0,
			quota_total INTEGER DEFAULT 0, vip_level INTEGER DEFAULT 0, vip_expires_at DATETIME,
			created_at DATETIME, updated_at DATETIME
		);
		CREATE TABLE async_tasks (
			id TEXT PRIMARY KEY, type TEXT NOT NULL, schedule_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0, result TEXT, error TEXT,
			attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		);
		INSERT INTO drive_accounts (id,type,name,cookie,token,is_default,status,created_at,updated_at) VALUES ('old-115','115','Old 115','cookie','',1,'active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
		INSERT INTO async_tasks (id,type,status,created_at,updated_at) VALUES ('old-task','emby_refresh','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tasks, err := db.ListAsyncTasks("", 0)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "old-task" {
		t.Fatalf("legacy tasks: %+v err=%v", tasks, err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "quark", Type: "quark", Name: "Quark", Cookie: "q", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "new-115", Type: "115", Name: "New 115", Cookie: "n", IsDefault: true}); err != nil {
		t.Fatal(err)
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
	if defaults["quark"] != "quark" || defaults["115"] != "new-115" || len(defaults) != 2 {
		t.Fatalf("defaults after migration: %+v", defaults)
	}
}

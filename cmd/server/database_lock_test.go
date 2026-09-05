package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestDatabaseOwnershipRejectsConcurrentAliasAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	owner, err := lockDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("owner cannot use SQLite: %v", err)
	}
	if err := db.SetSetting("ownership_probe", "written"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	alias := path + ".alias"
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, alias} {
		second, err := lockDatabase(candidate)
		if err == nil {
			second.Close()
			t.Fatal("second database owner was accepted")
		}
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockDatabase(path)
	if err != nil {
		t.Fatalf("ownership not released: %v", err)
	}
	next.Close()
}

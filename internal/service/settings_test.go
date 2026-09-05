package service

import (
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestSettingsStateRedactsAndPreservesSecrets(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	settings := NewSettingsService(db)
	if err := db.SaveAccount(&domain.DriveAccount{ID: "default", Type: "115", Name: "默认账号", Cookie: "old-cookie", IsDefault: true}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := settings.Update(map[string]string{
		"115_cookie":            "secret-cookie",
		"emby_url":              "http://emby.local",
		"emby_api_key":          "secret-key",
		"clouddrive_url":        "http://clouddrive.local",
		"clouddrive_mount_path": "/mnt/media",
		"resource_api_url":      "http://resources.local",
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	accounts, err := db.ListAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Cookie != "secret-cookie" {
		t.Fatalf("default account credential not synchronized: %+v, err=%v", accounts, err)
	}

	state, err := settings.State()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Values["115_cookie"] != "" || state.Values["emby_api_key"] != "" {
		t.Fatal("state exposed stored secrets")
	}
	for _, integration := range []string{"c115", "emby", "clouddrive", "resource"} {
		if !state.Configured[integration] {
			t.Fatalf("expected %s to be configured", integration)
		}
	}

	if err := settings.Update(map[string]string{"115_cookie": "", "emby_url": "http://emby.example"}); err != nil {
		t.Fatalf("update non-secret value: %v", err)
	}
	cookie, err := settings.Get("115_cookie")
	if err != nil {
		t.Fatalf("read stored cookie: %v", err)
	}
	if cookie != "secret-cookie" {
		t.Fatalf("empty secret update replaced stored value: %q", cookie)
	}
}

func TestSettingsStateOmitsUnknownPersistedKeys(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.SetSetting("emby_server_url", "http://legacy-emby.local"); err != nil {
		t.Fatalf("seed unknown setting: %v", err)
	}

	settings := NewSettingsService(db)
	state, err := settings.State()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if _, present := state.Values["emby_server_url"]; present {
		t.Fatal("state returned an unknown persisted setting")
	}
	state.Values["115_cookie"] = "new-cookie"
	if err := settings.Update(state.Values); err != nil {
		t.Fatalf("save returned settings: %v", err)
	}
}

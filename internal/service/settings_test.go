package service

import (
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestSettingsStateRedactsAndPreservesSecrets(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	settings := NewSettingsService(db)
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

package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSettingsValidateImmediateMediaExclusionRoots(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := NewSettingsService(db)
	if err := settings.Update(map[string]string{"media_excluded_roots": `["_待整理","_待回收"]`}); err != nil {
		t.Fatalf("save media exclusions: %v", err)
	}
	if stored, err := settings.Get("media_excluded_roots"); err != nil || stored != `["_待整理","_待回收"]` {
		t.Fatalf("unexpected media exclusions: %q, err=%v", stored, err)
	}
	if err := settings.Update(map[string]string{"media_excluded_roots": `["nested/review"]`}); err == nil {
		t.Fatal("nested media exclusion was accepted")
	}
}

func TestEmbyHealthDoesNotExposeKeyOnConnectionFailure(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "private-health-key" || r.URL.RawQuery != "" {
			t.Error("health authentication must use the Emby header, not the URL")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	if err := db.SetSetting("emby_url", upstream.URL); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("emby_api_key", "private-health-key"); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsService(db)
	if health := settings.CheckAvailability("emby"); health.Status != "ok" {
		t.Fatalf("health request failed: %+v", health)
	}
	upstream.Close()
	health := settings.CheckAvailability("emby")
	if health.Status != "error" || strings.Contains(health.Message, "private-health-key") {
		t.Fatalf("outage response leaked credentials or hid failure: %+v", health)
	}
}

func TestSettingsRejectNegativeShareSnapshotInterval(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := NewSettingsService(db)
	if err := settings.Update(map[string]string{"share_snapshot_interval_ms": "1000"}); err != nil {
		t.Fatal(err)
	}
	if err := settings.Update(map[string]string{"share_snapshot_interval_ms": "-1"}); err == nil {
		t.Fatal("negative snapshot interval was accepted")
	}
	if value, err := settings.Get("share_snapshot_interval_ms"); err != nil || value != "1000" {
		t.Fatalf("rejected interval replaced saved pacing: value=%q err=%v", value, err)
	}
}

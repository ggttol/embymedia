package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestEmbyAndCloudDriveServices(t *testing.T) {
	// Mock Emby server
	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/System/Info" {
			w.Write([]byte(`{"ServerName":"Test Emby","Version":"4.8.0.0"}`))
			return
		}
		if r.URL.Path == "/Library/VirtualFolders" {
			w.Write([]byte(`[{"Name":"Movies","CollectionType":"movies","ItemId":"lib1","Locations":["/media/movies"]}]`))
			return
		}
		if r.URL.Path == "/Sessions" {
			w.Write([]byte(`[{"Id":"sess1","UserName":"gaotao","Client":"Apple TV","DeviceName":"Living Room","PlayState":{"IsPaused":false,"PositionTicks":10000000,"PlayMethod":"DirectPlay"},"NowPlayingItem":{"Name":"Interstellar","Type":"Movie"}}]`))
			return
		}
		if r.URL.Path == "/Items" {
			w.Write([]byte(`{"Items":[{"Id":"item1","Name":"Inception","Type":"Movie","Path":"/media/movies/Inception.mkv","ImageTags":{"Primary":"tag1"}}]}`))
			return
		}
		if r.URL.Path == "/Items/item1" {
			w.Write([]byte(`{"Id":"item1","Name":"Inception","Type":"Movie","Path":"/media/movies/Inception.mkv","ImageTags":{"Primary":"tag1"},"BackdropImageTags":["backdrop"],"ProviderIds":{"Tmdb":"27205"}}`))
			return
		}
		if r.URL.Path == "/Items/RemoteSearch/Apply/item1" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	dbFile := "/tmp/test_emby_srv.db"
	_ = os.Remove(dbFile)
	defer os.Remove(dbFile)

	db, err := storage.Open(dbFile)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_ = db.SetConfig("emby_url", embyServer.URL, "test emby url")
	_ = db.SetConfig("emby_api_key", "test-key", "test api key")
	if err := db.SetSetting("clouddrive_mount_path", t.TempDir()); err != nil {
		t.Fatalf("set CloudDrive mount path: %v", err)
	}

	embySrv := NewEmbyService(db)
	info, err := embySrv.GetSystemInfo()
	if err != nil || info["ServerName"] != "Test Emby" {
		t.Fatalf("unexpected system info: %v, err: %v", info, err)
	}

	libs, err := embySrv.ListLibraries()
	if err != nil || len(libs) != 1 || libs[0].Name != "Movies" {
		t.Fatalf("unexpected libraries: %v, err: %v", libs, err)
	}

	sessions, err := embySrv.ListSessions()
	if err != nil || len(sessions) != 1 || sessions[0].ItemName != "Interstellar" {
		t.Fatalf("unexpected sessions: %v, err: %v", sessions, err)
	}
	item, err := embySrv.GetItem("item1")
	if err != nil || item.ID != "item1" || !item.HasPoster || !item.HasBackdrop || item.ProviderIDs["Tmdb"] != "27205" {
		t.Fatalf("unexpected exact item: %+v, err: %v", item, err)
	}
	if err := embySrv.MatchMedia("item1", "27205"); err != nil {
		t.Fatalf("match item: %v", err)
	}

	cdSrv := NewCloudDriveService(db)
	mounts, err := cdSrv.GetMounts(context.Background())
	if err != nil || len(mounts) == 0 {
		t.Fatalf("unexpected mounts: %v, err: %v", mounts, err)
	}
}

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestEmbyAndCloudDriveServices(t *testing.T) {
	// Mock Emby server
	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "test-key" || r.URL.Query().Has("api_key") {
			t.Errorf("Emby authentication must use the token header: %s", r.URL.String())
		}
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
			if r.URL.Query().Get("ImageTypes") == "None" {
				if r.URL.Query().Get("Fields") != "Path,ProviderIds" || r.URL.Query().Get("Limit") != "100" {
					t.Error("missing-poster query omitted bounded detail fields")
				}
				w.Write([]byte(`{"Items":[{"Id":"missing","Name":"Missing","Type":"Movie","Path":"/strm/Missing.strm","ProviderIds":{"Tmdb":"42"}}],"TotalRecordCount":2772}`))
				return
			}
			if r.URL.Query().Get("Ids") == "item1" {
				w.Write([]byte(`{"Items":[{"Id":"item1","Name":"Inception","Type":"Movie","Path":"/media/movies/Inception.mkv","ImageTags":{"Primary":"tag1"},"BackdropImageTags":["backdrop"],"ProviderIds":{"Tmdb":"27205"}}]}`))
				return
			}
			w.Write([]byte(`{"Items":[{"Id":"item1","Name":"Inception","Type":"Movie","Path":"/media/movies/Inception.mkv","ImageTags":{"Primary":"tag1"}}]}`))
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
	if err := embySrv.EnsureNoActivePlayback(context.Background()); err == nil {
		t.Fatal("active playback did not block disruptive maintenance")
	}
	items, err := embySrv.SearchMediaCtx(context.Background(), "Inception", 10)
	if err != nil || len(items) != 1 || items[0].ID != "item1" {
		t.Fatalf("search items: %+v, err=%v", items, err)
	}
	report, err := embySrv.GetMediaWithoutPosters()
	if err != nil || report.Total != 2772 || report.Returned != 1 || !report.Truncated || report.Items[0].Path != "/strm/Missing.strm" || report.Items[0].ProviderIDs["Tmdb"] != "42" {
		t.Fatalf("unexpected missing-poster report: %+v, err=%v", report, err)
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

func TestRunLibraryScanCancellationStopsStartedEmbyTask(t *testing.T) {
	polled := make(chan struct{})
	var pollOnce sync.Once
	var stopped atomic.Bool
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/ScheduledTasks":
			_, _ = response.Write([]byte(`[{"Id":"scan","Key":"RefreshLibrary","State":"Idle"}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/ScheduledTasks/Running/scan":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/ScheduledTasks/scan":
			pollOnce.Do(func() { close(polled) })
			_, _ = response.Write([]byte(`{"Id":"scan","Key":"RefreshLibrary","State":"Running","CurrentProgressPercentage":25}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/ScheduledTasks/Running/scan":
			stopped.Store(true)
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer embyServer.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSetting("emby_url", embyServer.URL); err != nil {
		t.Fatal(err)
	}
	service := NewEmbyService(db)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.RunLibraryScanCtx(ctx, func(float64, string) error { return nil })
		result <- err
	}()
	<-polled
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan returned %v", err)
	}
	if !stopped.Load() {
		t.Fatal("cancelling the local task did not stop the Emby scan")
	}
}

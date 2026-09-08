package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestFinalizeCompletedPackDeletesOldRootsOnlyAfterNewSeriesVerification(t *testing.T) {
	var effects []string
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/Items":
			_, _ = io.WriteString(response, `{"Items":[{"Id":"old","Name":"Show","Type":"Series","Path":"/strm/电视剧追更/Old","ProviderIds":{"Tmdb":"42"}},{"Id":"new","Name":"Show","Type":"Series","Path":"/strm/电视剧追更/New","ProviderIds":{"Tmdb":"42"}}],"TotalRecordCount":2}`)
		case request.Method == http.MethodGet && request.URL.Path == "/Shows/Missing":
			_, _ = io.WriteString(response, `{"Items":[],"TotalRecordCount":0}`)
		case request.Method == http.MethodGet && request.URL.Path == "/Shows/new/Episodes":
			_, _ = io.WriteString(response, `{"Items":[{"ParentIndexNumber":1,"IndexNumber":1,"PremiereDate":"2026-09-01T00:00:00Z"}]}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/Items/old":
			effects = append(effects, "emby")
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/files":
			_, _ = io.WriteString(response, `{"state":true,"count":1,"data":[{"cid":"old-cid","pid":"library-cid","n":"Old","s":"0"}]}`)
		case request.Method == http.MethodPost && request.URL.Path == "/rb/delete":
			effects = append(effects, "115")
			_, _ = io.WriteString(response, `{"state":true}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer provider.Close()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	strmRoot := t.TempDir()
	oldSTRM := filepath.Join(strmRoot, "电视剧追更", "Old")
	if err := os.MkdirAll(oldSTRM, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldSTRM, "Show.S01E01.strm"), []byte("/media/old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(map[string]string{"emby_url": provider.URL, "strm_root": strmRoot}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "UID=42_A1", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, provider.URL, "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "webapi.115.com" {
			clone := request.Clone(request.Context())
			providerURL, _ := url.Parse(provider.URL)
			clone.URL.Scheme, clone.URL.Host = providerURL.Scheme, providerURL.Host
			return http.DefaultTransport.RoundTrip(clone)
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	newID, err := queue.finalizeCompletedPack(context.Background(), &completedPackReplacement{
		LibraryID: "library", LibraryName: "电视剧追更", LibraryCID: "library-cid",
		OldSeriesID: "old", OldFolder: "Old", NewFolder: "New", TMDBID: "42",
		Expected: map[episodeKey]struct{}{{Season: 1, Episode: 1}: {}},
	})
	if err != nil || newID != "new" {
		t.Fatalf("replacement failed: id=%s err=%v", newID, err)
	}
	if !reflect.DeepEqual(effects, []string{"emby", "115"}) {
		t.Fatalf("unexpected deletion order: %v", effects)
	}
	if _, err := os.Stat(oldSTRM); !os.IsNotExist(err) {
		t.Fatalf("old STRM root remains: %v", err)
	}
}

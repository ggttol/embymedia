package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestPosterRepairDownloadsUniqueRemoteCandidate(t *testing.T) {
	var downloaded atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/Items":
			image := ""
			if downloaded.Load() {
				image = `,"ImageTags":{"Primary":"poster"}`
			}
			_, _ = io.WriteString(response, `{"Items":[{"Id":"item","Name":"暗黑","Type":"Series","Path":"/strm/电视剧/暗黑.Dark.2017.S01.1080p"`+image+`}],"TotalRecordCount":1}`)
		case request.Method == http.MethodPost && request.URL.Path == "/Items/item/Refresh":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/Items/RemoteSearch/Series":
			_, _ = io.WriteString(response, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/Items/RemoteSearch/Movie":
			var body struct {
				ItemID string `json:"ItemId"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body.ItemID != "" {
				t.Errorf("alternate type search retained incompatible item binding: %s", body.ItemID)
			}
			_, _ = io.WriteString(response, `[{"Name":"暗黑","ProductionYear":2017,"ProviderIds":{"Tmdb":"70523"},"ImageUrl":"https://image/poster.jpg","SearchProviderName":"TheMovieDb"}]`)
		case request.Method == http.MethodPost && request.URL.Path == "/Items/item/RemoteImages/Download":
			if request.URL.Query().Get("Type") != "Primary" || request.URL.Query().Get("ImageUrl") != "https://image/poster.jpg" {
				t.Errorf("unexpected image download query: %s", request.URL.RawQuery)
			}
			downloaded.Store(true)
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSetting("emby_url", server.URL); err != nil {
		t.Fatal(err)
	}
	result, err := NewEmbyService(db).RepairMissingPostersCtx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !downloaded.Load() || result.CandidateDownloaded != 1 || result.Repaired != 1 || result.Remaining != 0 {
		t.Fatalf("remote candidate did not repair poster: %+v", result)
	}
}

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestDeriveMetadataQueryRemovesReleaseAndSeasonNoise(t *testing.T) {
	name, year := deriveMetadataQuery(metadataInventoryItem{Path: "/strm/电视剧/暗黑.Dark.2017.S01-S03.1080p.NF.WEB-DL"})
	if name != "暗黑 Dark" || year != 2017 {
		t.Fatalf("unexpected derived query: %q %d", name, year)
	}
}

func TestRepairMetadataAppliesUniqueMatchAndDefersDuplicateGroup(t *testing.T) {
	var applied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/Items" && request.URL.Query().Get("Ids") == "dark":
			_, _ = io.WriteString(response, `{"Items":[{"Id":"dark","Name":"暗黑","Type":"Series","Path":"/strm/电视剧/暗黑.Dark.2017","ImageTags":{"Primary":"poster"},"ProviderIds":{"Tmdb":"70523"}}]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/Items":
			_, _ = io.WriteString(response, `{"Items":[{"Id":"dark","Name":"暗黑","Type":"Series","Path":"/strm/电视剧/暗黑.Dark.2017.S01-S03.1080p.NF.WEB-DL"},{"Id":"bb1","Name":"Breaking Bad","Type":"Series","Path":"/strm/电视剧/Breaking.Bad.2008.S01.1080p"},{"Id":"bb2","Name":"Breaking Bad","Type":"Series","Path":"/strm/电视剧/Breaking.Bad.2008.S02.1080p"}],"TotalRecordCount":3}`)
		case request.Method == http.MethodPost && request.URL.Path == "/Items/RemoteSearch/Series":
			var body struct {
				ItemID string `json:"ItemId"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body.ItemID == "dark" {
				_, _ = io.WriteString(response, `[{"Name":"暗黑","ProductionYear":2017,"ProviderIds":{"Tmdb":"70523"},"ImageUrl":"https://image/poster.jpg","SearchProviderName":"TheMovieDb"}]`)
			} else {
				_, _ = io.WriteString(response, `[{"Name":"Breaking Bad","ProductionYear":2008,"ProviderIds":{"Tmdb":"1396"},"ImageUrl":"https://image/poster.jpg","SearchProviderName":"TheMovieDb"}]`)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/Items/RemoteSearch/Apply/dark":
			applied.Store(true)
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
	result, err := NewEmbyService(db).RepairMetadataCtx(context.Background(), 10, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Load() || result.Scanned != 3 || result.MissingIdentity != 3 || result.AutoMatched != 1 || result.NeedsReview != 2 {
		t.Fatalf("unexpected metadata repair result: %+v", result)
	}
	for _, item := range result.Items {
		if strings.HasPrefix(item.ItemID, "bb") && (item.Status != "needs_review" || !strings.Contains(item.Reason, "duplicate")) {
			t.Fatalf("duplicate season root was not deferred: %+v", item)
		}
	}
}

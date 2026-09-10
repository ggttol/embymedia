package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestAutoFillIdentityRequiresIndependentExactLabels(t *testing.T) {
	series := &domain.EmbyMediaItem{Name: "交锋", Path: "/strm/电视剧追更/交锋 (2026)", ProviderIDs: map[string]string{"Tmdb": "294486"}}
	cases := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"substring advertisement", []string{"宿敌交锋 (2026) S01"}, false},
		{"matching advertisement unrelated share", []string{"交锋 (2026)", "宿敌交锋 (2026)"}, false},
		{"candidate conflicting ID", []string{"交锋 (2026) tmdb=285807", "交锋 (2026) {tmdb-294486}"}, false},
		{"root conflicting ID", []string{"交锋 (2026)", "交锋 (2026) [tmdbid=285807]"}, false},
		{"underscored conflicting ID", []string{"交锋 (2026) [tmdb_id=285807]"}, false},
		{"second conflicting ID", []string{"交锋 (2026) {tmdb-294486} {tmdb-285807}"}, false},
		{"conflicting URL ID", []string{"交锋 (2026) https://www.themoviedb.org/tv/285807"}, false},
		{"exact release title", []string{"交锋 (2026) S01 2160p WEB-DL", "交锋 (2026)", "交锋.S01E02.2160p.mkv"}, true},
		{"ID-bound alternate title", []string{"宿敌交锋 (2026) {tmdb-294486}"}, true},
		{"year conflict", []string{"交锋 (2025) {tmdb-294486}"}, false},
		{"year absent without ID", []string{"交锋.S01E02.mkv"}, false},
		{"ID supplies missing year", []string{"交锋.S01E02 {tmdb-294486}.mkv"}, true},
		{"season conflict", []string{"交锋 (2026) S01", "交锋.S02E02.mkv"}, false},
		{"generic labels cannot identify", []string{"合集", "Season 1", "S01E02.mkv"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoFillIdentityMatches(series, tc.labels...); got != tc.want {
				t.Fatalf("identity match = %t, want %t for %q", got, tc.want, tc.labels)
			}
		})
	}
	series.Path = "/strm/电视剧追更/交锋"
	if autoFillIdentityMatches(series, "交锋 (2026) S01") {
		t.Fatal("title without a canonical year identified a potentially different series")
	}
}

func TestAutoFillLeafRequiresItsOwnSeriesAncestry(t *testing.T) {
	series := &domain.EmbyMediaItem{Name: "交锋", Path: "/strm/电视剧追更/交锋 (2026)", ProviderIDs: map[string]string{"Tmdb": "294486"}}
	cases := []struct {
		name string
		leaf autoFillLeaf
		want bool
	}{
		{"untitled episode in exact root", autoFillLeaf{Name: "S01E02.mkv", Ancestors: []string{"交锋 (2026)", "Season 1"}}, true},
		{"untitled episode without root", autoFillLeaf{Name: "S01E02.mkv"}, false},
		{"unrelated sibling root", autoFillLeaf{Name: "S01E02.mkv", Ancestors: []string{"宿敌交锋 (2026)"}}, false},
		{"unrelated titled leaf", autoFillLeaf{Name: "宿敌交锋.S01E02.mkv", Ancestors: []string{"交锋 (2026)"}}, false},
		{"leaf conflicting ID", autoFillLeaf{Name: "交锋.S01E02 {tmdb-285807}.mkv", Ancestors: []string{"交锋 (2026)"}}, false},
		{"nested conflicting ID", autoFillLeaf{Name: "S01E02.mkv", Ancestors: []string{"交锋 (2026)", "Season 1 {tmdb-285807}"}}, false},
		{"implicit first season conflicts with parent", autoFillLeaf{Name: "E02.mkv", Ancestors: []string{"交锋 (2026)", "Season 2"}}, false},
		{"generic collection exact show", autoFillLeaf{Name: "S02E02.mkv", Ancestors: []string{"电视剧合集", "交锋 (2026)", "Season 2"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoFillLeafIdentityMatches(series, tc.leaf); got != tc.want {
				t.Fatalf("leaf identity match = %t, want %t for %+v", got, tc.want, tc.leaf)
			}
		})
	}
}

func TestAutoFillUsesEmbyOriginalTitleWithoutAcceptingAnotherShow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"Items":[{"Id":"series","Name":"绿灯军团","OriginalTitle":"Lanterns","Type":"Series","Path":"/strm/电视剧追更/绿灯军团 (2026) [tmdbid=95350]","PremiereDate":"2026-08-16T00:00:00Z","ProviderIds":{"Tmdb":"95350"}}],"TotalRecordCount":1}`)
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
	series, err := NewEmbyService(db).ListSeriesCtx(context.Background(), "library")
	if err != nil || len(series) != 1 {
		t.Fatalf("load canonical series: %v, %v", series, err)
	}
	leaf := autoFillLeaf{Name: "Lanterns.S01E04.The.Weenie.2160p.AMZN.WEB-DL.mkv", Ancestors: []string{"绿灯军团 (2026) [tmdbid=95350]"}}
	if !autoFillLeafIdentityMatches(&series[0], leaf) {
		t.Fatal("canonical original-language episode was rejected")
	}
	leaf.Name = "Nemesis.S01E04.2160p.NF.WEB-DL.mkv"
	if autoFillLeafIdentityMatches(&series[0], leaf) {
		t.Fatal("unrelated English-language episode inherited the containing root identity")
	}
}

func TestSeriesAutoFillTaskRetainsFailuresAndOrdinaryResourceGaps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		broken bool
		status string
	}{
		{"missing canonical identity", true, "failed"},
		{"no suitable resource", false, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/Library/VirtualFolders":
					_, _ = io.WriteString(response, `[{"Name":"电视剧追更","CollectionType":"tvshows","ItemId":"library"}]`)
				case "/Shows/Missing":
					_, _ = io.WriteString(response, `{"Items":[{"SeriesId":"series","SeriesName":"交锋","ParentIndexNumber":1,"IndexNumber":2,"PremiereDate":"2026-09-01T00:00:00Z"}],"TotalRecordCount":1}`)
				case "/Items":
					id := "294486"
					if tc.broken {
						id = ""
					}
					_, _ = io.WriteString(response, `{"Items":[{"Id":"series","Name":"交锋","Type":"Series","Path":"/strm/电视剧追更/交锋 (2026)","ProviderIds":{"Tmdb":"`+id+`"}}]}`)
				case "/files":
					_, _ = io.WriteString(response, `{"state":true,"count":1,"data":[{"cid":"series-cid","pid":"library-cid","n":"交锋 (2026)","s":"0"}]}`)
				case "/search":
					_, _ = io.WriteString(response, `{"links":[{"id":1,"title":"宿敌交锋 (2026)","url":"https://115.com/s/sharecode","health_status":"valid"}]}`)
				case "/share/snap":
					_, _ = io.WriteString(response, `{"state":true,"data":{"count":1,"shareinfo":{"share_title":"宿敌交锋 (2026)"},"list":[{"fid":"wrong-file","n":"宿敌交锋 (2026) S01E02.mkv","s":1024}]}}`)
				case "/share/receive":
					t.Error("unrelated series was transferred")
					response.WriteHeader(http.StatusInternalServerError)
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
			if err := db.SetSettings(map[string]string{"emby_url": provider.URL, "resource_api_url": provider.URL, "c115_cid_map": `{"电视剧追更":"library-cid"}`, "share_snapshot_interval_ms": "0"}); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "UID=42_A1; CID=test", IsDefault: true}); err != nil {
				t.Fatal(err)
			}
			drive := NewDriveService(db, provider.URL, "")
			drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "webapi.115.com" {
					clone := request.Clone(request.Context())
					providerURL, _ := url.Parse(provider.URL)
					clone.URL.Scheme = providerURL.Scheme
					clone.URL.Host = providerURL.Host
					return http.DefaultTransport.RoundTrip(clone)
				}
				return http.DefaultTransport.RoundTrip(request)
			})
			queue := NewTaskQueueService(db, drive, NewEmbyService(db))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := queue.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer queue.Stop()
			task, err := queue.Enqueue("series_auto_fill", map[string]any{"libraries": []string{"电视剧追更"}, "transfer": true})
			if err != nil {
				t.Fatal(err)
			}
			waitForTaskStatus(t, db, task.ID, tc.status)
			stored, err := db.GetAsyncTask(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Missing     int                     `json:"missing"`
				Transferred int                     `json:"transferred"`
				Remaining   int                     `json:"remaining"`
				Libraries   []LibraryAutoFillResult `json:"libraries"`
			}
			if err := json.Unmarshal([]byte(stored.Result), &result); err != nil {
				t.Fatalf("task lost structured diagnostics: %v; result=%s", err, stored.Result)
			}
			if result.Missing != 1 || result.Remaining != 1 || result.Transferred != 0 || len(result.Libraries) != 1 {
				t.Fatalf("task lost remaining gap: %+v", result)
			}
			if tc.broken && (len(result.Libraries[0].Issues) == 0 || !strings.Contains(strings.Join(result.Libraries[0].Issues, " "), "TMDB")) {
				t.Fatalf("identity failure diagnostics absent: %+v", result)
			}
			if !tc.broken && len(result.Libraries[0].Issues) != 0 {
				t.Fatalf("ordinary resource gap reported as failure: %+v", result)
			}
		})
	}
}

func TestSeriesAutoFillReportsDuplicateIdentityWithoutGaps(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/Library/VirtualFolders":
			_, _ = io.WriteString(response, `[{"Name":"电视剧追更","CollectionType":"tvshows","ItemId":"library"}]`)
		case "/Shows/Missing":
			_, _ = io.WriteString(response, `{"Items":[],"TotalRecordCount":0}`)
		case "/Items":
			_, _ = io.WriteString(response, `{"Items":[{"Id":"old","Name":"绿灯军团","Type":"Series","Path":"/strm/电视剧追更/绿灯军团 (2026)","ProviderIds":{"Tmdb":"95350"}},{"Id":"duplicate","Name":"绿灯军团","Type":"Series","Path":"/strm/电视剧追更/绿灯军团 亚马逊","ProviderIds":{"Tmdb":"95350"}}],"TotalRecordCount":2}`)
		default:
			t.Errorf("unexpected provider operation: %s %s", request.Method, request.URL.Path)
			http.NotFound(response, request)
		}
	}))
	defer provider.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"emby_url": provider.URL, "c115_cid_map": `{"电视剧追更":"library-cid"}`}); err != nil {
		t.Fatal(err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	task, err := queue.Enqueue("series_auto_fill", map[string]any{"libraries": []string{"电视剧追更"}, "transfer": true})
	if err != nil {
		t.Fatal(err)
	}
	queue.executeTask(context.Background(), *task)
	stored, err := db.GetAsyncTask(task.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("duplicate library reported success: %+v, %v", stored, err)
	}
	var result struct {
		Libraries []LibraryAutoFillResult `json:"libraries"`
	}
	if err := json.Unmarshal([]byte(stored.Result), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Libraries) != 1 || len(result.Libraries[0].Issues) == 0 || result.Libraries[0].MissingCount != 0 {
		t.Fatalf("duplicate identity finding was lost despite empty missing-episode response: %+v", result)
	}
}

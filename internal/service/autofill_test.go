package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestEpisodeKeysFromNameUsesExplicitEpisodeNotation(t *testing.T) {
	cases := map[string][]episodeKey{
		"Show.S02E03.2160p.mkv": {{Season: 2, Episode: 3}},
		"Show E12 1080p.mp4":    {{Season: 1, Episode: 12}},
		"节目 第7集 WEB-DL.mkv":     {{Season: 1, Episode: 7}},
		"Show.1080p.mkv":        {},
	}
	for name, expected := range cases {
		actual := episodeKeysFromName(name)
		if len(actual) != len(expected) {
			t.Fatalf("%q: got %+v, want %+v", name, actual, expected)
		}
		for index := range expected {
			if actual[index] != expected[index] {
				t.Fatalf("%q: got %+v, want %+v", name, actual, expected)
			}
		}
	}
}

func TestValidateSeriesAutoFillRejectsOtherLibraries(t *testing.T) {
	if err := ValidateTask("series_auto_fill", map[string]any{"libraries": []any{"电视剧追更"}, "transfer": true}); err != nil {
		t.Fatalf("eligible library rejected: %v", err)
	}
	if err := ValidateTask("series_auto_fill", map[string]any{"libraries": []any{"电视剧"}, "transfer": true}); err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("ineligible library was not rejected: %v", err)
	}
}

func TestCompletedPackRequiresEveryExpectedEpisode(t *testing.T) {
	expected := map[episodeKey]struct{}{{Season: 1, Episode: 1}: {}, {Season: 1, Episode: 2}: {}}
	if coversEpisodes([]autoFillLeaf{{Name: "Show.S01E01.mkv"}}, expected) {
		t.Fatal("incomplete pack was accepted")
	}
	if !coversEpisodes([]autoFillLeaf{{Name: "Show.S01E01.mkv"}, {Name: "Show.S01E02.mkv"}}, expected) {
		t.Fatal("complete pack was rejected")
	}
}

func TestCompletedPackReplacementRequiresDangerousActions(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	_, err = queue.runSeriesAutoFill(context.Background(), domain.AsyncTask{Payload: map[string]any{
		"libraries": []string{"电视剧追更"}, "transfer": true, "replace_completed_pack": true,
	}})
	if err == nil || !strings.Contains(err.Error(), "dangerous_actions_enabled") {
		t.Fatalf("automatic deletion was not gated: %v", err)
	}
}

func TestSeriesAutoFillRefusesDuplicateTMDBIdentityBeforeProviderWrites(t *testing.T) {
	canonical := &domain.EmbyMediaItem{ID: "series", Name: "Duplicate", Type: "Series", Path: "/strm/电视剧追更/Duplicate", ProviderIDs: map[string]string{"Tmdb": "42"}}
	result, paths := (&TaskQueueService{}).processAutoFillSeries(
		context.Background(), domain.EmbyLibrary{Name: "电视剧追更"}, "library-cid",
		map[episodeKey]struct{}{{Season: 1, Episode: 2}: {}}, "series", "Duplicate", canonical, 2,
		seriesAutoFillSpec{Transfer: true, CandidateLimit: 10, MaxSeries: 20},
	)
	if result.Issue == "" || !strings.Contains(result.Issue, "duplicated") || len(paths) != 0 {
		t.Fatalf("duplicate TMDB identity did not fail closed: result=%+v paths=%v", result, paths)
	}
}

func TestSeriesAutoFillTransfersExactMissingEpisodeAndVerifiesEmby(t *testing.T) {
	var scanStarted atomic.Bool
	var receiveCount atomic.Int32
	mediaRoot := filepath.Join(t.TempDir(), "media")
	strmRoot := filepath.Join(t.TempDir(), "strm")
	seriesFolder := "追更剧 (2026)"
	seriesMediaPath := filepath.Join(mediaRoot, "电视剧追更", seriesFolder)
	if err := os.MkdirAll(seriesMediaPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, ".embymedia-health-canary"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/Library/VirtualFolders":
			_, _ = io.WriteString(response, `[{"Name":"电视剧追更","Locations":["/strm/电视剧追更"],"CollectionType":"tvshows","ItemId":"library-tv"}]`)
		case "/Shows/Missing":
			if scanStarted.Load() {
				_, _ = io.WriteString(response, `{"Items":[],"TotalRecordCount":0}`)
			} else {
				_, _ = io.WriteString(response, `{"Items":[{"SeriesId":"series-1","SeriesName":"追更剧","ParentIndexNumber":1,"IndexNumber":2,"PremiereDate":"2026-09-01T00:00:00Z"}],"TotalRecordCount":1}`)
			}
		case "/Items":
			_, _ = io.WriteString(response, `{"Items":[{"Id":"series-1","Name":"追更剧","Type":"Series","Path":"/strm/电视剧追更/追更剧 (2026)","ProviderIds":{"Tmdb":"123"}}]}`)
		case "/search":
			if request.URL.Query().Get("q") != "追更剧" || request.URL.Query().Get("health_status") != "valid" {
				t.Errorf("unexpected resource query: %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(response, `{"links":[{"id":7,"title":"追更剧 (2026) S01E02","disk_type":"115","url":"https://115.com/s/sharecode","password":"abcd","health_status":"valid"}],"total":1}`)
		case "/files":
			if request.URL.Query().Get("cid") != "library-cid" {
				t.Errorf("unexpected library CID: %s", request.URL.Query().Get("cid"))
			}
			_, _ = io.WriteString(response, `{"state":true,"count":1,"data":[{"cid":"series-cid","pid":"library-cid","n":"追更剧 (2026)","s":"0"}]}`)
		case "/share/snap":
			if request.URL.Query().Get("cid") == "0" {
				_, _ = io.WriteString(response, `{"state":true,"data":{"count":1,"shareinfo":{"share_title":"追更剧 (2026)"},"list":[{"cid":"share-root","n":"追更剧 (2026)","s":0}]}}`)
			} else {
				_, _ = io.WriteString(response, `{"state":true,"data":{"count":1,"shareinfo":{"share_title":"追更剧 (2026)"},"list":[{"fid":"episode-file","cid":"share-root","n":"追更剧.S01E02.2160p.mkv","s":1024}]}}`)
			}
		case "/share/receive":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("file_id") != "episode-file" || values.Get("cid") != "series-cid" {
				t.Errorf("unexpected selective receive payload: %s", body)
			}
			receiveCount.Add(1)
			if err := os.WriteFile(filepath.Join(seriesMediaPath, "追更剧.S01E02.2160p.mkv"), []byte("video"), 0644); err != nil {
				t.Errorf("write transferred fixture: %v", err)
			}
			_, _ = io.WriteString(response, `{"state":true}`)
		case "/ScheduledTasks":
			_, _ = io.WriteString(response, `[{"Id":"scan","Key":"RefreshLibrary","State":"Idle"}]`)
		case "/ScheduledTasks/Running/scan":
			scanStarted.Store(true)
			response.WriteHeader(http.StatusNoContent)
		case "/ScheduledTasks/scan":
			_, _ = io.WriteString(response, `{"Id":"scan","Key":"RefreshLibrary","State":"Idle","LastExecutionResult":{"StartTimeUtc":"2026-09-08T00:00:00Z","EndTimeUtc":"2026-09-08T00:00:01Z","Status":"Completed"}}`)
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
	if err := db.SetSettings(map[string]string{
		"emby_url": provider.URL, "resource_api_url": provider.URL,
		"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media",
		"c115_cid_map": `{"电视剧追更":"library-cid","综艺追更":"variety-cid"}`,
	}); err != nil {
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
	task, err := queue.Enqueue("series_auto_fill", map[string]any{
		"libraries": []string{"电视剧追更"}, "transfer": true, "candidate_limit": 10, "max_series": 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, db, task.ID, "completed")
	stored, err := db.GetAsyncTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Missing     int `json:"missing"`
		Matched     int `json:"matched"`
		Transferred int `json:"transferred"`
		Remaining   int `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(stored.Result), &result); err != nil {
		t.Fatal(err)
	}
	if receiveCount.Load() != 1 || !scanStarted.Load() || result.Missing != 1 || result.Matched != 1 || result.Transferred != 1 || result.Remaining != 0 {
		t.Fatalf("automatic completion did not transfer and verify exact gap: receives=%d scan=%t result=%+v raw=%s", receiveCount.Load(), scanStarted.Load(), result, stored.Result)
	}
	if _, err := os.Stat(filepath.Join(strmRoot, "电视剧追更", seriesFolder, "追更剧.S01E02.2160p.strm")); err != nil {
		t.Fatalf("transferred episode was not projected to STRM: %v", err)
	}
}

func TestListAiredMissingEpisodesExcludesFutureAndUnnumberedItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"Items":[{"SeriesId":"aired","SeriesName":"Aired","ParentIndexNumber":1,"IndexNumber":2,"PremiereDate":"2026-09-01T00:00:00Z"},{"SeriesId":"future","SeriesName":"Future","ParentIndexNumber":1,"IndexNumber":3,"PremiereDate":"2026-10-01T00:00:00Z"},{"SeriesId":"special","SeriesName":"Special","ParentIndexNumber":0,"IndexNumber":1,"PremiereDate":"2026-09-01T00:00:00Z"}],"TotalRecordCount":3}`)
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
	missing, err := NewEmbyService(db).ListAiredMissingEpisodesCtx(context.Background(), "library", time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC))
	if err != nil || len(missing) != 1 || missing[0].SeriesID != "aired" {
		t.Fatalf("aired missing episode filter failed: %+v, err=%v", missing, err)
	}
}

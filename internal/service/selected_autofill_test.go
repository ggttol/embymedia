package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestSuppliedShareQueuesOnlyMissingEpisodesDespiteUnrelatedExpiredShare(t *testing.T) {
	searchCalls, receives := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			io.WriteString(w, `[{"Name":"电视剧追更","CollectionType":"tvshows","ItemId":"library"}]`)
		case "/Items":
			io.WriteString(w, `{"Items":[{"Id":"series","Name":"Show","Type":"Series","Path":"/strm/电视剧追更/Show (2026)","ProviderIds":{"Tmdb":"42"}},{"Id":"other","Name":"Other","Type":"Series","Path":"/strm/电视剧追更/Other (2026)","ProviderIds":{"Tmdb":"43"}}],"TotalRecordCount":2}`)
		case "/Shows/Missing":
			io.WriteString(w, `{"Items":[{"SeriesId":"series","SeriesName":"Show","ParentIndexNumber":1,"IndexNumber":17,"PremiereDate":"2020-01-01T00:00:00Z"},{"SeriesId":"series","SeriesName":"Show","ParentIndexNumber":1,"IndexNumber":18,"PremiereDate":"2020-01-01T00:00:00Z"},{"SeriesId":"other","SeriesName":"Other","ParentIndexNumber":1,"IndexNumber":5,"PremiereDate":"2020-01-01T00:00:00Z"}],"TotalRecordCount":3}`)
		case "/files":
			io.WriteString(w, `{"state":true,"count":2,"data":[{"cid":"series-cid","pid":"library-cid","n":"Show (2026)"},{"cid":"other-cid","pid":"library-cid","n":"Other (2026)"}]}`)
		case "/share/sharepage/token":
			io.WriteString(w, `{"status":200,"data":{"stoken":"secret","title":"Show (2026)"}}`)
		case "/share/sharepage/detail":
			entries := []map[string]any{}
			for _, ep := range []int{1, 4, 16, 17, 18, 19} {
				entries = append(entries, map[string]any{"fid": fmt.Sprint(ep), "pdir_fid": "0", "file_name": fmt.Sprintf("Show.2026.S01E%02d.mp4", ep), "file_size": 100, "dir": false, "revision": "r1", "share_fid_token": "token"})
			}
			json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{"list": entries}, "metadata": map[string]any{"_total": len(entries)}})
		case "/share/snap":
			io.WriteString(w, `{"state":true,"data":{"count":1,"shareinfo":{"share_title":"Other (2026)"},"list":[{"fid":"other-5","n":"Other.2026.S01E05.mkv","s":1024}]}}`)
		case "/share/receive":
			receives++
			io.WriteString(w, `{"state":false,"error":"链接已过期"}`)
		case "/search":
			searchCalls++
			http.Error(w, "index must not be needed", 500)
		default:
			if strings.HasSuffix(r.URL.Path, "/Refresh") {
				w.WriteHeader(204)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	drive, db := newSnapshotTestDrive(t, "0")
	if err := db.SaveAccount(&domain.DriveAccount{ID: "quark", Type: "quark", Name: "Quark fixture", Cookie: "fixture", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(map[string]string{"emby_url": server.URL, "emby_api_key": "fixture", "c115_cid_map": `{"电视剧追更":"library-cid"}`, "quark_autofill_target_id": "quark-staging", "media_root": t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	dest, _ := url.Parse(server.URL)
	drive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = dest.Scheme, dest.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	drive.providers["quark"].(*ProviderQuark).baseURL = server.URL
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	payload := map[string]any{"libraries": []string{"电视剧追更"}, "transfer": true, "source_shares": map[string]any{"series": map[string]any{"provider": "quark", "url": "https://pan.quark.cn/s/newshare"}, "other": map[string]any{"provider": "115", "url": "https://115.com/s/expired"}}}
	parent, err := queue.Enqueue("series_auto_fill", payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(parent.ID); err != nil || !claimed {
		t.Fatalf("claim parent: %v", err)
	}
	result, err := queue.runSeriesAutoFill(context.Background(), *parent)
	if err != nil {
		t.Fatalf("unrelated expired share aborted valid selection: %v", err)
	}
	if searchCalls != 0 || receives != 1 {
		t.Fatalf("index calls=%d receive attempts=%d", searchCalls, receives)
	}
	if result["verification_complete"] != false || result["remaining"] != 3 {
		t.Fatalf("unverified episodes reported complete: %+v", result)
	}
	tasks, err := db.ListAsyncTasks("pending", 0)
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	for _, task := range tasks {
		if task.Type != "quark_to_115_import" {
			continue
		}
		children++
		selected, err := optionalStringSlicePayload(task.Payload, "selected_source_ids")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(selected, ",") != "17,18" {
			t.Fatalf("download includes old/future episodes: %v", selected)
		}
	}
	if children != 1 {
		t.Fatalf("valid pending child count=%d", children)
	}
	// A subscription must reuse this same scoped workflow, not copy all share leaves.
	sub := domain.ShareSubscription{ID: "subscription", Name: "Show", Provider: "quark", URL: "https://pan.quark.cn/s/newshare", TargetCID: "series-cid", Active: true, CreatedAt: time.Now()}
	if err := db.CreateShareSubscription(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	request := domain.AsyncTask{ID: "poll", Payload: map[string]any{"subscription_id": sub.ID}}
	first, err := queue.runShareAutoSync(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.runShareAutoSync(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first["next_task_id"] != second["next_task_id"] || first["verification_complete"] != false {
		t.Fatalf("subscription duplicated active work: %v %v", first, second)
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type replacementFixture struct {
	queue         *TaskQueueService
	replacement   *completedPackReplacement
	cloud         map[string]domain.DriveFile
	cloudPaths    map[string]string
	states        map[string]replacementUserData
	deleted       []string
	failure       string
	stateFailed   bool
	stagingFailed bool
}

func newReplacementFixture(t *testing.T, failure string) *replacementFixture {
	t.Helper()
	base := t.TempDir()
	mediaRoot, strmRoot := filepath.Join(base, "media"), filepath.Join(base, "strm")
	mediaBase, strmBase := filepath.Join(mediaRoot, "电视剧追更"), filepath.Join(strmRoot, "电视剧追更")
	for _, path := range []string{filepath.Join(mediaBase, "Old"), filepath.Join(mediaBase, "New"), filepath.Join(strmBase, "Old"), filepath.Join(strmBase, "New")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile := func(path, contents string) {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(filepath.Join(mediaBase, "Old", "Show.S01E01.mkv"), "old-release")
	writeFixtureFile(filepath.Join(mediaBase, "New", "Show.S01E01.1080p.mkv"), "new-release-1")
	writeFixtureFile(filepath.Join(mediaBase, "New", "Show.S01E02.1080p.mkv"), "new-release-2")
	writeFixtureFile(filepath.Join(strmBase, "Old", "Show.S01E01.strm"), "/media/电视剧追更/Old/Show.S01E01.mkv\n")
	writeFixtureFile(filepath.Join(strmBase, "New", "Show.S01E01.1080p.strm"), "/media/电视剧追更/New/Show.S01E01.1080p.mkv\n")
	info, err := os.Lstat(filepath.Join(strmBase, "New"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := replacementMediaFiles(filepath.Join(mediaBase, "New"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &replacementFixture{
		replacement: &completedPackReplacement{
			LibraryID: "library", LibraryName: "电视剧追更", LibraryCID: "library-cid", AccountID: "account",
			OldSeriesID: "old", OldFolder: "Old", OldCID: "old-cid", OldPath: "/strm/电视剧追更/Old",
			NewFolder: "New", NewCID: "new-cid", TMDBID: "42", MediaBase: mediaBase, STRMBase: strmBase,
			NewSTRMInfo: info, MediaFiles: files, Expected: map[episodeKey]struct{}{{Season: 1, Episode: 1}: {}, {Season: 1, Episode: 2}: {}},
		},
		cloud: map[string]domain.DriveFile{
			"old-cid": {FileID: "old-cid", Name: "Old", ParentID: "library-cid", IsFolder: true},
			"new-cid": {FileID: "new-cid", Name: "New", ParentID: "library-cid", IsFolder: true},
		},
		cloudPaths: map[string]string{"library-cid": mediaBase, "0": filepath.Join(base, "cloud-root")},
		states:     make(map[string]replacementUserData), failure: failure,
	}
	if err := os.MkdirAll(fixture.cloudPaths["0"], 0755); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob"} {
		fixture.states[user+"/old"] = replacementUserData{IsFavorite: true}
		fixture.states[user+"/old-season"] = replacementUserData{IsFavorite: user == "alice"}
		fixture.states[user+"/old-e1"] = replacementUserData{Played: user == "alice", PlayCount: 3, PlaybackPositionTicks: 123456, IsFavorite: user == "bob"}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		respond := func(value any) { _ = json.NewEncoder(response).Encode(value) }
		canonical := false
		if _, err := os.Stat(filepath.Join(strmBase, "Old", "Show.S01E02.1080p.strm")); err == nil {
			canonical = true
		}
		switch {
		case request.Method == http.MethodDelete:
			t.Errorf("replacement attempted destructive Emby DELETE %s", request.URL.Path)
			response.WriteHeader(http.StatusInternalServerError)
		case request.URL.Path == "/Items":
			parent := request.URL.Query().Get("ParentId")
			if parent == "library" {
				tmdb := "42"
				if failure == "old-identity" || (canonical && failure == "canonical-identity") {
					tmdb = "999"
				}
				items := []map[string]any{{"Id": "old", "Type": "Series", "Name": "Show", "Path": "/strm/电视剧追更/Old", "ProviderIds": map[string]string{"Tmdb": tmdb}}}
				if _, err := os.Stat(filepath.Join(strmBase, "New")); err == nil {
					items = append(items, map[string]any{"Id": "new", "Type": "Series", "Name": "Show", "Path": "/strm/电视剧追更/New", "ProviderIds": map[string]string{"Tmdb": "42"}})
				}
				respond(map[string]any{"Items": items, "TotalRecordCount": len(items)})
				return
			}
			prefix, folder, count := "old", "Old", 1
			if parent == "new" {
				prefix, folder, count = "new", "New", 2
			} else if canonical {
				prefix, count = "canonical", 2
			}
			items := []map[string]any{{"Id": prefix + "-season", "Type": "Season", "SeriesId": parent, "IndexNumber": 1, "Path": "/strm/电视剧追更/" + folder + "/Season 01"}}
			for episode := 1; episode <= count; episode++ {
				path := fmt.Sprintf("/strm/电视剧追更/%s/Show.S01E%02d.strm", folder, episode)
				if parent == "new" && episode == 2 && failure == "virtual-coverage" {
					path = ""
				}
				items = append(items, map[string]any{"Id": fmt.Sprintf("%s-e%d", prefix, episode), "Type": "Episode", "SeriesId": parent, "ParentIndexNumber": 1, "IndexNumber": episode, "Path": path})
			}
			respond(map[string]any{"Items": items, "TotalRecordCount": len(items)})
		case request.URL.Path == "/Shows/Missing":
			respond(map[string]any{"Items": []any{}, "TotalRecordCount": 0})
		case request.URL.Path == "/Sessions":
			if failure == "playback" {
				respond([]map[string]any{{"UserName": "alice", "NowPlayingItem": map[string]string{"Name": "Show"}}})
			} else {
				respond([]any{})
			}
		case request.URL.Path == "/Users":
			respond([]map[string]string{{"Id": "alice"}, {"Id": "bob"}})
		case strings.HasPrefix(request.URL.Path, "/Users/"):
			parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
			key := parts[1] + "/" + parts[3]
			if request.Method == http.MethodPost {
				if failure == "state-write" && strings.HasPrefix(parts[3], "canonical") && !fixture.stateFailed {
					fixture.stateFailed = true
					response.WriteHeader(http.StatusInternalServerError)
					return
				}
				var state replacementUserData
				if err := json.NewDecoder(request.Body).Decode(&state); err != nil {
					t.Error(err)
				}
				if failure != "state-readback" || !strings.HasPrefix(parts[3], "canonical") {
					fixture.states[key] = state
				}
				response.WriteHeader(http.StatusNoContent)
			} else if failure == "state-read" {
				response.WriteHeader(http.StatusInternalServerError)
			} else {
				respond(map[string]any{"Id": parts[3], "UserData": fixture.states[key]})
			}
		case request.URL.Path == "/ScheduledTasks":
			respond([]map[string]string{{"Id": "scan", "Key": "RefreshLibrary", "State": "Idle"}})
		case request.URL.Path == "/ScheduledTasks/Running/scan":
			response.WriteHeader(http.StatusNoContent)
		case request.URL.Path == "/ScheduledTasks/scan":
			respond(map[string]any{"Id": "scan", "State": "Idle", "LastExecutionResult": map[string]any{"StartTimeUtc": time.Now().Add(-time.Second), "EndTimeUtc": time.Now(), "Status": "Completed"}})
		case request.URL.Path == "/files":
			entries := []map[string]any{}
			for _, file := range fixture.cloud {
				if file.ParentID == request.URL.Query().Get("cid") {
					entries = append(entries, map[string]any{"cid": file.FileID, "pid": file.ParentID, "n": file.Name, "s": "0"})
				}
			}
			respond(map[string]any{"state": true, "count": len(entries), "data": entries})
		case request.URL.Path == "/files/add":
			_ = request.ParseForm()
			cid := fmt.Sprintf("created-%d", len(fixture.cloud))
			if failure == "preexisting-mkdir" {
				respond(map[string]any{"state": true, "cid": "old-cid"})
				return
			}
			folder := domain.DriveFile{FileID: cid, Name: request.Form.Get("cname"), ParentID: request.Form.Get("pid"), IsFolder: true}
			fixture.cloud[cid] = folder
			path := filepath.Join(fixture.cloudPaths[folder.ParentID], folder.Name)
			fixture.cloudPaths[cid] = path
			if err := os.Mkdir(path, 0755); err != nil {
				t.Error(err)
			}
			respond(map[string]any{"state": true, "cid": cid})
		case request.URL.Path == "/files/move" || request.URL.Path == "/files/edit":
			_ = request.ParseForm()
			cid := request.Form.Get("fid")
			if cid == "" {
				cid = request.Form.Get("fid[0]")
			}
			file := fixture.cloud[cid]
			oldPath := filepath.Join(fixture.cloudPaths[file.ParentID], file.Name)
			if request.URL.Path == "/files/move" {
				file.ParentID = request.Form.Get("pid")
			} else {
				file.Name = request.Form.Get("file_name")
			}
			if err := os.Rename(oldPath, filepath.Join(fixture.cloudPaths[file.ParentID], file.Name)); err != nil {
				t.Error(err)
				response.WriteHeader(500)
				return
			}
			fixture.cloud[cid] = file
			respond(map[string]any{"state": true})
		case request.URL.Path == "/rb/delete":
			_ = request.ParseForm()
			cid := request.Form.Get("fid[0]")
			fixture.deleted = append(fixture.deleted, cid)
			if cid == "old-cid" {
				t.Error("old cloud source was deleted")
			}
			file := fixture.cloud[cid]
			if err := os.RemoveAll(filepath.Join(fixture.cloudPaths[file.ParentID], file.Name)); err != nil {
				t.Error(err)
			}
			delete(fixture.cloud, cid)
			respond(map[string]any{"state": true})
		case request.URL.Path == "/share/snap":
			entries := []map[string]any{{"cid": "share-root", "n": "Show (2026) [tmdbid=42]"}}
			if request.URL.Query().Get("cid") == "share-root" {
				entries = []map[string]any{{"fid": "e1", "n": "Show.S01E01.mkv"}, {"fid": "e2", "n": "Show.S01E02.mkv"}}
			}
			respond(map[string]any{"state": true, "data": map[string]any{"count": len(entries), "shareinfo": map[string]string{"share_title": "Show (2026) [tmdbid=42]"}, "list": entries}})
		case request.URL.Path == "/share/receive":
			fixture.stagingFailed = true
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(response, "receive failed")
		default:
			t.Errorf("unexpected provider request %s %s", request.Method, request.URL.String())
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(provider.Close)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSettings(map[string]string{"emby_url": provider.URL, "media_root": mediaRoot, "strm_root": strmRoot, "share_snapshot_interval_ms": "0"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "UID=42_A1", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, provider.URL, "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		providerURL, _ := url.Parse(provider.URL)
		clone.URL.Scheme, clone.URL.Host = providerURL.Scheme, providerURL.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	fixture.queue = NewTaskQueueService(db, drive, NewEmbyService(db))
	return fixture
}

func TestCompletedPackCanonicalCutoverRetainsSourcesAndUserState(t *testing.T) {
	fixture := newReplacementFixture(t, "")
	newID, err := fixture.queue.finalizeCompletedPack(context.Background(), fixture.replacement)
	if err != nil || newID != "old" {
		t.Fatalf("canonical replacement failed: id=%s err=%v", newID, err)
	}
	if len(fixture.deleted) != 0 {
		t.Fatalf("replacement deleted recoverable cloud data: %v", fixture.deleted)
	}
	if fixture.cloud["new-cid"].Name != "Old" || fixture.cloud["old-cid"].ParentID != fixture.replacement.QuarantineCID {
		t.Fatalf("canonical/quarantine locations are wrong: %v", fixture.cloud)
	}
	for _, user := range []string{"alice", "bob"} {
		for _, item := range []string{"season", "e1"} {
			if !reflect.DeepEqual(fixture.states[user+"/old-"+item], fixture.states[user+"/canonical-"+item]) {
				t.Fatalf("lost %s playback/favorite state for %s", user, item)
			}
		}
	}
	canonical := filepath.Join(fixture.replacement.STRMBase, "Old", "Show.S01E02.1080p.strm")
	contents, err := os.ReadFile(canonical)
	if err != nil || !strings.Contains(string(contents), "/Old/Show.S01E02.1080p.mkv") {
		t.Fatalf("canonical stream target is wrong: %q %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.replacement.STRMBase, "New")); !os.IsNotExist(err) {
		t.Fatalf("temporary STRM duplicate remains: %v", err)
	}
	for _, path := range []string{filepath.Join(fixture.replacement.BackupDir, "state.json"), filepath.Join(fixture.replacement.BackupDir, "strm", "Show.S01E01.strm"), filepath.Join(fixture.cloudPaths[fixture.replacement.QuarantineCID], "Old", "Show.S01E01.mkv")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("old recovery snapshot missing: %s: %v", path, err)
		}
	}
}

func TestCompletedPackFailuresRestoreCanonicalAndRemoveOnlyStaging(t *testing.T) {
	for _, failure := range []string{"old-identity", "virtual-coverage", "playback", "state-read", "state-write", "state-readback", "canonical-identity", "preexisting-mkdir"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newReplacementFixture(t, failure)
			if _, err := fixture.queue.finalizeCompletedPack(context.Background(), fixture.replacement); err == nil {
				t.Fatal("unsafe replacement succeeded")
			}
			if err := fixture.queue.rollbackCompletedPack(context.Background(), fixture.replacement); err != nil {
				t.Fatalf("completed rollback was not idempotent: %v", err)
			}
			if !reflect.DeepEqual(fixture.deleted, []string{"new-cid"}) {
				t.Fatalf("rollback did not exclusively recycle staging: %v", fixture.deleted)
			}
			if fixture.cloud["old-cid"].ParentID != "library-cid" {
				t.Fatal("old source was not restored")
			}
			contents, err := os.ReadFile(filepath.Join(fixture.replacement.STRMBase, "Old", "Show.S01E01.strm"))
			if err != nil || !strings.Contains(string(contents), "/Old/Show.S01E01.mkv") {
				t.Fatalf("old STRM was not preserved: %s %v", contents, err)
			}
			if _, err := os.Stat(filepath.Join(fixture.replacement.STRMBase, "New")); !os.IsNotExist(err) {
				t.Fatalf("failed staging duplicate remains: %v", err)
			}
			if _, err := os.Stat(filepath.Join(fixture.replacement.MediaBase, "Old", "Show.S01E01.mkv")); err != nil {
				t.Fatalf("old media was lost: %v", err)
			}
		})
	}
}

func TestCompletedPackRollbackRefusesChangedOwnership(t *testing.T) {
	fixture := newReplacementFixture(t, "old-identity")
	file := fixture.cloud["new-cid"]
	file.Name = "Preexisting"
	fixture.cloud["new-cid"] = file
	if err := fixture.queue.rollbackCompletedPack(context.Background(), fixture.replacement); err == nil {
		t.Fatal("changed staging ownership was accepted")
	}
	if len(fixture.deleted) != 0 {
		t.Fatalf("rollback deleted an unbound root: %v", fixture.deleted)
	}
}

func TestCompletedPackFailedTransferCleansCreatedCIDNotSharedSource(t *testing.T) {
	fixture := newReplacementFixture(t, "")
	series := &domain.EmbyMediaItem{ID: "old", Type: "Series", Name: "Show", Path: "/strm/电视剧追更/Old", ProviderIDs: map[string]string{"Tmdb": "42"}}
	staged, paths, issue := fixture.queue.stageCompletedPack(context.Background(), domain.EmbyLibrary{ID: "library", Name: "电视剧追更"}, "library-cid", map[episodeKey]struct{}{{Season: 1, Episode: 2}: {}}, series, []autoFillCandidate{{Title: "Show (2026) [tmdbid=42]", URL: "https://115.com/s/pack"}})
	if staged != nil || len(paths) != 0 || issue == nil || !fixture.stagingFailed {
		t.Fatalf("transfer failure was not reported: staged=%v paths=%v issue=%s received=%v", staged, paths, issue, fixture.stagingFailed)
	}
	if !reflect.DeepEqual(fixture.deleted, []string{"created-2"}) {
		t.Fatalf("rollback recycled other than the freshly created transfer CID: %v", fixture.deleted)
	}
	if len(fixture.cloud) != 2 {
		t.Fatalf("failed transfer left a duplicate root: %v", fixture.cloud)
	}
}

func TestReplacementSTRMRollbackRejectsReusedPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "staging")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	preexisting := filepath.Join(root, "keep.strm")
	if err := os.WriteFile(preexisting, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedReplacementRoot(root, owned); err == nil {
		t.Fatal("rollback accepted a different directory at the same path")
	}
	if contents, err := os.ReadFile(preexisting); err != nil || string(contents) != "keep" {
		t.Fatalf("rollback removed unrelated STRM data: %q %v", contents, err)
	}
}

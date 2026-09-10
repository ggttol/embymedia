package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type embyDeletionFixture struct {
	queue      *TaskQueueService
	mediaRoot  string
	strmRoot   string
	cloud      map[string]domain.DriveFile
	deleted    []string
	failDelete bool
	incomplete bool
}

func newEmbyDeletionFixture(t *testing.T) *embyDeletionFixture {
	t.Helper()
	base := t.TempDir()
	fixture := &embyDeletionFixture{
		mediaRoot: filepath.Join(base, "media"), strmRoot: filepath.Join(base, "strm"),
		cloud: map[string]domain.DriveFile{
			"series":  {FileID: "series", ParentID: "library-cid", Name: "Show", IsFolder: true},
			"season":  {FileID: "season", ParentID: "series", Name: "Season 1", IsFolder: true},
			"episode": {FileID: "episode", ParentID: "season", Name: "Show.S01E01.mkv", Size: 7, Sha1: strings.Repeat("a", 40)},
			"sibling": {FileID: "sibling", ParentID: "season", Name: "Show.S01E02.mkv", Size: 7, Sha1: strings.Repeat("b", 40)},
		},
	}
	for _, root := range []string{filepath.Join(fixture.mediaRoot, "TV", "Show", "Season 1"), filepath.Join(fixture.strmRoot, "TV", "Show", "Season 01")} {
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for episode := 1; episode <= 2; episode++ {
		name := fmt.Sprintf("Show.S01E%02d", episode)
		deletionWriteFixture(t, filepath.Join(fixture.mediaRoot, "TV", "Show", "Season 1", name+".mkv"), "episode")
		deletionWriteFixture(t, filepath.Join(fixture.strmRoot, "TV", "Show", "Season 01", name+".strm"), "/media/TV/Show/Season 1/"+name+".mkv\n")
	}
	deletionWriteFixture(t, filepath.Join(fixture.strmRoot, "TV", "Show", "Season 01", "season.nfo"), "metadata")
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		respond := func(value any) { _ = json.NewEncoder(response).Encode(value) }
		switch request.URL.Path {
		case "/Library/VirtualFolders":
			respond([]map[string]any{{"Name": "TV", "ItemId": "library", "CollectionType": "tvshows", "Locations": []string{"/strm-v2/TV", "/media/TV"}}})
		case "/files":
			entries := []map[string]any{}
			for _, file := range fixture.cloud {
				if file.ParentID != request.URL.Query().Get("cid") {
					continue
				}
				entry := map[string]any{"n": file.Name, "s": file.Size, "sha": file.Sha1}
				if file.IsFolder {
					entry["cid"], entry["pid"] = file.FileID, file.ParentID
				} else {
					entry["fid"], entry["cid"] = file.FileID, file.ParentID
				}
				entries = append(entries, entry)
			}
			count := len(entries)
			if fixture.incomplete {
				count++
			}
			if request.URL.Query().Get("offset") != "0" {
				entries = nil
			}
			respond(map[string]any{"state": true, "count": count, "data": entries})
		case "/rb/delete":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			id := request.Form.Get("fid[0]")
			fixture.deleted = append(fixture.deleted, id)
			if fixture.failDelete {
				respond(map[string]any{"state": false, "error": "recycle denied"})
				return
			}
			if fixture.cloud[id].IsFolder {
				t.Errorf("attempted to recycle parent directory %s", id)
			}
			delete(fixture.cloud, id)
			respond(map[string]any{"state": true})
		default:
			t.Errorf("unexpected deletion provider request %s %s", request.Method, request.URL.Path)
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(provider.Close)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSettings(map[string]string{"emby_url": provider.URL, "media_root": fixture.mediaRoot, "strm_root": fixture.strmRoot, "emby_media_prefix": "/media", "c115_cid_map": `{"TV":"library-cid"}`}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Name: "Primary", Type: "115", Cookie: "UID=test", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		endpoint, _ := url.Parse(provider.URL)
		clone.URL.Scheme, clone.URL.Host = endpoint.Scheme, endpoint.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	fixture.queue = NewTaskQueueService(db, drive, NewEmbyService(db))
	return fixture
}

func deletionWriteFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEmbyDeletionSTRMCanonicalSeasonRecyclesOnlyExactVideo(t *testing.T) {
	fixture := newEmbyDeletionFixture(t)
	path := "/strm-v2/TV/Show/Season 01/Show.S01E01.strm"
	plan, err := fixture.queue.PrepareEmbyDeletionCtx(context.Background(), []string{path, path, "/strm-v2/TV/Show/Season 01/season.nfo"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.SourcePaths, []string{"/media/TV/Show/Season 1/Show.S01E01.mkv"}) {
		t.Fatalf("wrong confirmed original paths: %v", plan.SourcePaths)
	}
	if err := os.Remove(filepath.Join(fixture.strmRoot, "TV", "Show", "Season 01", "Show.S01E01.strm")); err != nil {
		t.Fatal(err)
	}
	if err := fixture.queue.RecycleEmbyDeletionCtx(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.deleted, []string{"episode"}) {
		t.Fatalf("wrong recycle IDs: %v", fixture.deleted)
	}
	if _, exists := fixture.cloud["sibling"]; !exists {
		t.Fatal("unselected sibling was recycled")
	}
	if _, exists := fixture.cloud["season"]; !exists {
		t.Fatal("parent directory was recycled")
	}
	if !plan.Sources[0].Recycled {
		t.Fatal("successful source recycle missing from audit")
	}
}

func TestEmbyDeletionDirectoryEnumeratesSortedOriginals(t *testing.T) {
	fixture := newEmbyDeletionFixture(t)
	plan, err := fixture.queue.PrepareEmbyDeletionCtx(context.Background(), []string{"/strm-v2/TV/Show/Season 01"})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/media/TV/Show/Season 1/Show.S01E01.mkv", "/media/TV/Show/Season 1/Show.S01E02.mkv"}
	if !reflect.DeepEqual(plan.SourcePaths, expected) {
		t.Fatalf("directory confirmation omitted or reordered originals: %v", plan.SourcePaths)
	}
}

func TestEmbyDeletionRefusesUnsafePreflight(t *testing.T) {
	for _, scenario := range []string{"outside", "library-root", "symlink", "source-symlink", "multiple-targets", "target-outside", "metadata-only", "duplicate-name", "incomplete", "size", "unconfigured"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newEmbyDeletionFixture(t)
			path := "/strm-v2/TV/Show/Season 01/Show.S01E01.strm"
			strm := filepath.Join(fixture.strmRoot, "TV", "Show", "Season 01", "Show.S01E01.strm")
			switch scenario {
			case "outside":
				path = "/strm-v2/Unmanaged/Show.strm"
			case "library-root":
				path = "/strm-v2/TV"
			case "metadata-only":
				path = "/strm-v2/TV/Show/Season 01/season.nfo"
			case "symlink":
				if err := os.Remove(strm); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("Show.S01E02.strm", strm); err != nil {
					t.Fatal(err)
				}
			case "source-symlink":
				source := filepath.Join(fixture.mediaRoot, "TV", "Show", "Season 1", "Show.S01E01.mkv")
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("Show.S01E02.mkv", source); err != nil {
					t.Fatal(err)
				}
			case "multiple-targets":
				deletionWriteFixture(t, strm, "/media/TV/Show/Season 1/Show.S01E01.mkv\n/media/TV/Show/Season 1/Show.S01E02.mkv\n")
			case "target-outside":
				deletionWriteFixture(t, strm, "/media/Elsewhere/Show.mkv\n")
			case "duplicate-name":
				duplicate := fixture.cloud["episode"]
				duplicate.FileID = "duplicate"
				fixture.cloud["duplicate"] = duplicate
			case "incomplete":
				fixture.incomplete = true
			case "size":
				file := fixture.cloud["episode"]
				file.Size++
				fixture.cloud["episode"] = file
			case "unconfigured":
				if err := fixture.queue.db.SetSettings(map[string]string{"c115_cid_map": `{}`}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.queue.PrepareEmbyDeletionCtx(context.Background(), []string{path}); err == nil {
				t.Fatal("unsafe preflight was accepted")
			}
			if len(fixture.deleted) != 0 {
				t.Fatalf("preflight mutated cloud: %v", fixture.deleted)
			}
		})
	}
}

func TestEmbyDeletionNativeAbsentDoesNotRecycleReplacement(t *testing.T) {
	fixture := newEmbyDeletionFixture(t)
	plan, err := fixture.queue.PrepareEmbyDeletionCtx(context.Background(), []string{"/media/TV/Show/Season 1/Show.S01E01.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	replacement := fixture.cloud["episode"]
	replacement.FileID = "replacement"
	delete(fixture.cloud, "episode")
	fixture.cloud["replacement"] = replacement
	if err := fixture.queue.RecycleEmbyDeletionCtx(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(fixture.deleted) != 0 {
		t.Fatalf("recycled a same-named replacement: %v", fixture.deleted)
	}
	if !plan.Sources[0].AlreadyAbsent {
		t.Fatal("native deletion outcome missing from audit")
	}
}

func TestEmbyDeletionRevalidatesIdentityAndPropagatesRecycleFailure(t *testing.T) {
	for _, scenario := range []string{"name", "parent", "size", "sha", "missing-strm-source", "delete"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newEmbyDeletionFixture(t)
			plan, err := fixture.queue.PrepareEmbyDeletionCtx(context.Background(), []string{"/strm-v2/TV/Show/Season 01/Show.S01E01.strm"})
			if err != nil {
				t.Fatal(err)
			}
			file := fixture.cloud["episode"]
			switch scenario {
			case "name":
				file.Name = "changed.mkv"
			case "parent":
				file.ParentID = "elsewhere"
			case "size":
				file.Size++
			case "sha":
				file.Sha1 = strings.Repeat("c", 40)
			case "delete":
				fixture.failDelete = true
			}
			fixture.cloud["episode"] = file
			if scenario == "missing-strm-source" {
				delete(fixture.cloud, "episode")
			}
			if err := fixture.queue.RecycleEmbyDeletionCtx(context.Background(), plan); err == nil {
				t.Fatal("source failure was hidden")
			}
			if plan.Sources[0].Recycled || plan.Sources[0].AlreadyAbsent {
				t.Fatal("failed source falsely reported as deleted")
			}
			if scenario != "delete" && len(fixture.deleted) != 0 {
				t.Fatalf("changed identity was recycled: %v", fixture.deleted)
			}
		})
	}
}

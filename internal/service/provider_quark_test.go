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
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestQuarkProviderOperationsAndSignedURLRefresh(t *testing.T) {
	var signedRequests int
	savedVisible := false
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/signed" {
			if request.Header.Get("Cookie") != "quark-cookie" || request.Header.Get("Origin") != "https://pan.quark.cn" || request.Header.Get("Referer") != "https://pan.quark.cn/" {
				t.Fatalf("Quark request omitted browser headers: %s", request.URL.Path)
			}
			response.Header().Set("Content-Type", "application/json")
		}
		switch request.URL.Path {
		case "/file/sort":
			parent := request.URL.Query().Get("pdir_fid")
			if request.URL.Query().Get("_fetch_total") != "1" {
				t.Fatalf("unexpected list query: %s", request.URL.RawQuery)
			}
			if parent == "folder" {
				if savedVisible {
					_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"saved-root","pdir_fid":"folder","file_name":"shared.mkv","file_size":"4","dir":false}]},"metadata":{"_total":1}}`)
				} else {
					_, _ = io.WriteString(response, `{"status":200,"data":{"list":[]},"metadata":{"_total":0}}`)
				}
				break
			}
			if parent != "0" {
				t.Fatalf("unexpected list query: %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"folder","pdir_fid":"0","file_name":"Series","dir":true},{"fid":"file","pdir_fid":"0","file_name":"episode.mkv","file_size":"4","dir":false,"revision":"rev-1","sha1":"abcd"}]},"metadata":{"_total":2}}`)
		case "/file":
			_, _ = io.WriteString(response, `{"status":200,"data":{"fid":"created"}}`)
		case "/file/rename", "/file/move", "/file/delete":
			_, _ = io.WriteString(response, `{"status":200,"data":{}}`)
		case "/share/sharepage/token":
			_, _ = io.WriteString(response, `{"status":200,"data":{"stoken":"secret-stoken"}}`)
		case "/share/sharepage/detail":
			_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"shared","pdir_fid":"0","file_name":"shared.mkv","file_size":"4","dir":false,"revision":"share-rev","share_fid_token":"save-token"}]},"metadata":{"_total":1}}`)
		case "/share/sharepage/save":
			payload, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(payload), `"fid_token_list":["save-token"]`) || !strings.Contains(string(payload), `"share_fid_token_list":["save-token"]`) {
				t.Fatalf("save request omitted share token: %s", payload)
			}
			_, _ = io.WriteString(response, `{"status":200,"data":{"task_id":"task"}}`)
		case "/task":
			savedVisible = true
			_, _ = io.WriteString(response, `{"status":200,"data":{"status":2,"save_as":{"save_as_top_fids":["saved-root"]}}}`)
		case "/file/download":
			_, _ = io.WriteString(response, `{"status":200,"data":[{"download_url":"`+server.URL+`/signed"}]}`)
		case "/signed":
			signedRequests++
			if signedRequests == 1 {
				response.WriteHeader(http.StatusForbidden)
				return
			}
			if request.Header.Get("Range") != "bytes=2-" {
				t.Fatalf("range header = %q", request.Header.Get("Range"))
			}
			response.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(response, "ta")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	provider := NewProviderQuark(server.Client())
	provider.baseURL = server.URL
	provider.downloadBaseURL = server.URL
	provider.sleep = func(context.Context, time.Duration) error { return nil }
	account := &domain.DriveAccount{ID: "quark", Type: "quark", Cookie: "quark-cookie"}
	files, total, err := provider.ListFiles(context.Background(), account, "0", 0, 100)
	if err != nil || total != 2 || len(files) != 2 || !files[0].IsFolder || files[1].Revision != "rev-1" {
		t.Fatalf("list: total=%d files=%+v err=%v", total, files, err)
	}
	if id, err := provider.Mkdir(context.Background(), account, "0", "New"); err != nil || id != "created" {
		t.Fatalf("mkdir: %s %v", id, err)
	}
	if err := provider.Rename(context.Background(), account, "file", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Move(context.Background(), account, []string{"file"}, "folder"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Delete(context.Background(), account, []string{"file"}); err != nil {
		t.Fatal(err)
	}
	saved, err := provider.SaveShare(context.Background(), account, "https://pan.quark.cn/s/share-id?pwd=pass", "", "folder")
	if err != nil || saved.Count != 1 || len(saved.RootIDs) != 1 || saved.RootIDs[0] != "saved-root" {
		t.Fatalf("save: %+v %v", saved, err)
	}
	body, err := provider.OpenDownload(context.Background(), account, "file", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil || string(content) != "ta" || signedRequests != 2 {
		t.Fatalf("download: %q requests=%d err=%v", content, signedRequests, err)
	}
}

func TestQuarkProviderRejectsForeignParentAndLeakedShareErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/file/sort") {
			_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"foreign","pdir_fid":"other","file_name":"file","file_size":1,"dir":false}]},"metadata":{"_total":1}}`)
			return
		}
		_, _ = io.WriteString(response, `{"status":400,"message":"expired share"}`)
	}))
	defer server.Close()
	provider := NewProviderQuark(server.Client())
	provider.baseURL = server.URL
	account := &domain.DriveAccount{Cookie: "cookie"}
	if _, _, err := provider.ListFiles(context.Background(), account, "0", 0, 10); err == nil {
		t.Fatal("foreign parent accepted")
	}
	if _, err := provider.SnapshotShare(context.Background(), account, "https://pan.quark.cn/s/id", "secret-password"); err == nil || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("unsafe share error: %v", err)
	}
}

func TestResolveQuarkSavedRootsUsesNewDestinationIdentity(t *testing.T) {
	entries := []ShareEntry{{ID: "source", Name: "old.txt", Size: 42}}
	before := []domain.DriveFile{{FileID: "existing", Name: "other.txt", Size: 42}}
	after := append(before, domain.DriveFile{FileID: "destination", Name: "old.txt", Size: 42})
	ids, err := resolveQuarkSavedRoots(entries, before, after, []string{"source"})
	if err != nil || len(ids) != 1 || ids[0] != "destination" {
		t.Fatalf("resolved roots = %v, err=%v", ids, err)
	}
}

func TestSafeProviderRequestErrorDoesNotExposeSignedURL(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://download.example/file?OSSAccessKeyId=secret&Signature=secret", Err: context.DeadlineExceeded}
	safe := safeProviderRequestError("open Quark download", err)
	if strings.Contains(safe.Error(), "download.example") || strings.Contains(safe.Error(), "secret") {
		t.Fatalf("provider error exposed signed URL: %v", safe)
	}
	if !strings.Contains(safe.Error(), "context deadline exceeded") {
		t.Fatalf("provider error omitted transport cause: %v", safe)
	}
}

func TestQuarkSnapshotTreeAndSelectiveSave(t *testing.T) {
	var saveCalls int
	savedVisible := false
	var savedPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/share/sharepage/token":
			_, _ = io.WriteString(response, `{"status":200,"data":{"stoken":"secret-stoken","title":"Nested Pack"}}`)
		case "/share/sharepage/detail":
			parent := request.URL.Query().Get("pdir_fid")
			switch parent {
			case "0":
				_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"series","pdir_fid":"0","file_name":"Series","dir":true,"revision":"series-rev","share_fid_token":"series-token"},{"fid":"unrelated","pdir_fid":"0","file_name":"unrelated.mkv","file_size":"3","dir":false,"revision":"unrelated-rev","share_fid_token":"unrelated-token"}]},"metadata":{"_total":2}}`)
			case "series":
				_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"selected","pdir_fid":"series","file_name":"episode.mkv","file_size":"7","dir":false,"revision":"selected-rev","share_fid_token":"selected-token"},{"fid":"sibling","pdir_fid":"series","file_name":"sibling.mkv","file_size":"8","dir":false,"revision":"sibling-rev","share_fid_token":"sibling-token"}]},"metadata":{"_total":2}}`)
			default:
				t.Errorf("unexpected share parent %q", parent)
				_, _ = io.WriteString(response, `{"status":200,"data":{"list":[]},"metadata":{"_total":0}}`)
			}
		case "/file/sort":
			if request.URL.Query().Get("pdir_fid") != "target" {
				t.Errorf("unexpected target parent %q", request.URL.Query().Get("pdir_fid"))
			}
			if savedVisible {
				_, _ = io.WriteString(response, `{"status":200,"data":{"list":[{"fid":"saved-id","pdir_fid":"target","file_name":"episode.mkv","file_size":"7","dir":false,"revision":"saved-rev"}]},"metadata":{"_total":1}}`)
			} else {
				_, _ = io.WriteString(response, `{"status":200,"data":{"list":[]},"metadata":{"_total":0}}`)
			}
		case "/share/sharepage/save":
			if err := json.NewDecoder(request.Body).Decode(&savedPayload); err != nil {
				t.Errorf("decode save payload: %v", err)
			}
			saveCalls++
			savedVisible = true
			_, _ = io.WriteString(response, `{"status":200,"data":{"fids":["saved-id"]}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	provider := NewProviderQuark(server.Client())
	provider.baseURL = server.URL
	provider.sleep = func(context.Context, time.Duration) error { return nil }
	account := &domain.DriveAccount{ID: "quark", Type: "quark", Cookie: "cookie"}

	snapshot, err := provider.SnapshotShareTree(context.Background(), account, "https://pan.quark.cn/s/share", "")
	if err != nil {
		t.Fatalf("snapshot tree: %v", err)
	}
	if snapshot.Title != "Nested Pack" || len(snapshot.Entries) != 4 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	var selected ShareEntry
	for _, entry := range snapshot.Entries {
		if entry.ID == "selected" {
			selected = entry
		}
	}
	if selected.ParentID != "series" || selected.Revision != "selected-rev" || selected.Size != 7 || len(selected.Ancestors) != 1 || selected.Ancestors[0] != "Series" {
		t.Fatalf("selected entry metadata = %+v", selected)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "secret-stoken") || strings.Contains(string(encoded), "selected-token") {
		t.Fatalf("snapshot leaked provider token: %s", encoded)
	}

	saved, err := provider.SaveShareEntries(context.Background(), account, "https://pan.quark.cn/s/share", "", "target", []string{"selected"})
	if err != nil {
		t.Fatalf("selective save: %v", err)
	}
	if saveCalls != 1 || saved.Count != 1 || len(saved.RootIDs) != 1 || saved.RootIDs[0] != "saved-id" {
		t.Fatalf("saved = %+v calls=%d", saved, saveCalls)
	}
	ids, _ := savedPayload["fid_list"].([]any)
	if len(ids) != 1 || ids[0] != "selected" {
		t.Fatalf("save selected IDs = %#v", savedPayload["fid_list"])
	}
	if tokens, _ := savedPayload["fid_token_list"].([]any); len(tokens) != 1 || tokens[0] != "selected-token" {
		t.Fatalf("save selected tokens = %#v", savedPayload["fid_token_list"])
	}
	for _, ids := range [][]string{{"missing"}, {"selected", "selected"}} {
		if _, err := provider.SaveShareEntries(context.Background(), account, "https://pan.quark.cn/s/share", "", "target", ids); err == nil {
			t.Fatalf("invalid selection %v was accepted", ids)
		}
	}
	if saveCalls != 1 {
		t.Fatalf("invalid selections mutated target: save calls=%d", saveCalls)
	}
	if _, err := provider.SaveShareSelections(context.Background(), account, "https://pan.quark.cn/s/share", "", "target", []ShareSelection{{ID: "selected", Revision: "changed-revision", Name: "episode.mkv", Size: 7}}); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("changed preview identity was accepted: %v", err)
	}
	if saveCalls != 1 {
		t.Fatalf("identity drift mutated target: save calls=%d", saveCalls)
	}
}

func TestSelectShareEntriesRejectsInvalidSelectionsBeforeMutation(t *testing.T) {
	entries := []ShareEntry{
		{ID: "file-a", Name: "episode.mkv"},
		{ID: "file-b", Name: "episode.mkv"},
		{ID: "folder", Name: "Season 1", IsDir: true},
	}
	for _, test := range []struct {
		name string
		ids  []string
	}{
		{name: "unknown", ids: []string{"missing"}},
		{name: "duplicate id", ids: []string{"file-a", "file-a"}},
		{name: "directory", ids: []string{"folder"}},
		{name: "duplicate output name", ids: []string{"file-a", "file-b"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if selected, err := selectShareEntries(entries, test.ids); err == nil || selected != nil {
				t.Fatalf("selection accepted: selected=%+v err=%v", selected, err)
			}
		})
	}
}

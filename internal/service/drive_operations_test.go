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

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDriveProviderOperations(t *testing.T) {
	calls := make(map[string]int)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls[request.URL.Path]++
		if request.Header.Get("Cookie") != "UID=42_A1; CID=test; SEID=test" {
			t.Fatalf("provider request omitted account cookie for %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/files":
			_, _ = io.WriteString(response, `{"state":true,"count":2,"data":[{"fid":"11","cid":"0","n":"movie.mkv","s":"1024"},{"cid":"12","pid":"0","n":"Series"}]}`)
		case "/files/search":
			_, _ = io.WriteString(response, `{"state":true,"count":1,"data":[{"fid":"21","cid":"12","n":"Titanic.mkv","s":"2048","fc":1,"te":"1788367119"}]}`)
		case "/files/index_info":
			_, _ = io.WriteString(response, `{"state":true,"data":{"vip":2,"expire":4102444800,"space_info":{"all_total":{"size":1000},"all_use":{"size":250}}}}`)
		case "/files/add":
			_ = request.ParseForm()
			if request.Method != http.MethodPost || request.Form.Get("pid") != "0" || request.Form.Get("cname") != "New" {
				t.Fatalf("unexpected mkdir request: %s %v", request.Method, request.Form)
			}
			_, _ = io.WriteString(response, `{"state":true,"cid":"13"}`)
		case "/files/edit":
			_ = request.ParseForm()
			if request.Form.Get("fid") != "11" || request.Form.Get("file_name") != "renamed.mkv" {
				t.Fatalf("unexpected rename request: %v", request.Form)
			}
			_, _ = io.WriteString(response, `{"state":true}`)
		case "/files/move":
			_ = request.ParseForm()
			if request.Form.Get("pid") != "13" || request.Form.Get("fid[0]") != "11" {
				t.Fatalf("unexpected move request: %v", request.Form)
			}
			_, _ = io.WriteString(response, `{"state":true}`)
		case "/rb/delete":
			_ = request.ParseForm()
			if request.Form.Get("fid[0]") != "11" {
				t.Fatalf("unexpected delete request: %v", request.Form)
			}
			_, _ = io.WriteString(response, `{"state":true}`)
		case "/share/snap":
			_, _ = io.WriteString(response, `{"state":true,"data":{"shareinfo":{"share_title":"Example"},"list":[{"fid":"11","n":"movie.mkv","s":1024}]}}`)
		case "/share/receive":
			_, _ = io.WriteString(response, `{"state":true}`)
		case "/share/send":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), "file_ids=11") {
				t.Fatalf("share request omitted file ID: %s", body)
			}
			_, _ = io.WriteString(response, `{"state":true,"data":{"share_code":"abc123","receive_code":"p4ss"}}`)
		case "/web/lixian/":
			_, _ = io.WriteString(response, `{"state":true,"info_hash":"hash123"}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSetting("share_snapshot_interval_ms", "0"); err != nil {
		t.Fatal(err)
	}
	account := &domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "UID=42_A1; CID=test; SEID=test", IsDefault: true}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	service := NewDriveService(db, "http://127.0.0.1:8100", "")
	service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = upstreamURL.Scheme
		clone.URL.Host = upstreamURL.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	ctx := context.Background()
	files, err := service.ListFilesCtx(ctx, account.ID, "0")
	if err != nil || len(files) != 2 || files[1].FileID != "12" || !files[1].IsFolder {
		t.Fatalf("list files: %+v, err=%v", files, err)
	}
	searched, total, err := service.SearchFilesCtx(ctx, account.ID, "Titanic", 0, 20)
	if err != nil || total != 1 || len(searched) != 1 || searched[0].FileID != "21" || searched[0].ParentID != "12" {
		t.Fatalf("search files: total=%d files=%+v err=%v", total, searched, err)
	}
	if cid, err := service.MkdirCtx(ctx, account.ID, "0", "New"); err != nil || cid != "13" {
		t.Fatalf("mkdir: cid=%s err=%v", cid, err)
	}
	if err := service.RenameCtx(ctx, account.ID, "11", "renamed.mkv"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := service.MoveCtx(ctx, account.ID, []string{"11"}, "13"); err != nil {
		t.Fatalf("move: %v", err)
	}
	resolvedAccount, targets, err := service.ResolveDeleteTargetsCtx(ctx, account.ID, "0", []string{"11"})
	if err != nil || resolvedAccount != account.ID || len(targets) != 1 || targets[0].Name != "movie.mkv" || targets[0].IsFolder {
		t.Fatalf("resolve delete targets: account=%s targets=%+v err=%v", resolvedAccount, targets, err)
	}
	if err := service.DeleteCtx(ctx, account.ID, []string{"11"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count, title, err := service.SaveShareCtx(ctx, account.ID, "https://115.com/s/shared?password=code", "", "13"); err != nil || count != 1 || title != "Example" {
		t.Fatalf("save share: count=%d title=%s err=%v", count, title, err)
	}
	share, err := service.GenerateShareLink(ctx, account.ID, "11")
	if err != nil || share.URL != "https://115.com/s/abc123?password=p4ss" {
		t.Fatalf("generate share: %+v, err=%v", share, err)
	}
	if id, err := service.AddOfflineTaskCtx(ctx, account.ID, "magnet:?xt=urn:btih:test", "13"); err != nil || id != "hash123" {
		t.Fatalf("offline download: id=%s err=%v", id, err)
	}
	health, err := service.CheckAccounts(ctx)
	if err != nil || len(health) != 1 || health[0].Status != "active" || health[0].VIPLevel != 2 || health[0].QuotaTotal != 1000 || health[0].QuotaUsed != 250 {
		t.Fatalf("account health: %+v, err=%v", health, err)
	}
	for _, path := range []string{"/files", "/files/search", "/files/index_info", "/files/add", "/files/edit", "/files/move", "/rb/delete", "/share/snap", "/share/receive", "/share/send", "/web/lixian/"} {
		if calls[path] == 0 {
			t.Fatalf("provider operation %s was not called", path)
		}
	}
}

func TestSearchFilesUsesFileIDPresence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/files/search" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"state":true,"count":4,"data":[
			{"fid":"21","cid":"12","n":"Titanic.mkv"},
			{"fid":22,"cid":12,"n":"Titanic.srt"},
			{"cid":"31","pid":"0","n":"Titanic","fc":2},
			{"fid":0,"cid":32,"pid":31,"n":"Extras","fc":1}
		]}`)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	account := &domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	service := NewDriveService(db, "", "")
	service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = upstreamURL.Scheme
		clone.URL.Host = upstreamURL.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	files, total, err := service.SearchFilesCtx(context.Background(), account.ID, "Titanic", 0, 20)
	if err != nil || total != 4 || len(files) != 4 {
		t.Fatalf("search files: total=%d files=%+v err=%v", total, files, err)
	}
	expected := []struct {
		fileID, parentID string
		isFolder         bool
	}{
		{"21", "12", false},
		{"22", "12", false},
		{"31", "0", true},
		{"32", "31", true},
	}
	for index, want := range expected {
		got := files[index]
		if got.FileID != want.fileID || got.ParentID != want.parentID || got.IsFolder != want.isFolder {
			t.Errorf("search result %d: got %+v, want ID=%s parent=%s folder=%t", index, got, want.fileID, want.parentID, want.isFolder)
		}
	}
}

func TestDriveMultiAccountLifecycle(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	first := &domain.DriveAccount{ID: "first", Type: "115", Name: "First", Cookie: "cookie-1", IsDefault: true}
	second := &domain.DriveAccount{ID: "second", Type: "115", Name: "Second", Cookie: "cookie-2", IsDefault: true}
	if err := db.SaveAccount(first); err != nil {
		t.Fatalf("save first account: %v", err)
	}
	if err := db.SaveAccount(second); err != nil {
		t.Fatalf("save second account: %v", err)
	}
	accounts, err := db.ListAccounts()
	if err != nil || len(accounts) != 2 || accounts[0].ID != "second" || !accounts[0].IsDefault || accounts[1].IsDefault {
		t.Fatalf("default account transition failed: %+v, err=%v", accounts, err)
	}
	service := NewDriveService(db, "", "")
	resolved, err := service.getAccount("first")
	if err != nil || resolved.Cookie != "cookie-1" {
		t.Fatalf("selected account was not preserved: %+v, err=%v", resolved, err)
	}
	if _, err := service.getAccount("missing"); err == nil {
		t.Fatal("unknown account unexpectedly fell back to the default")
	}
	if err := db.DeleteAccount("first"); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	accounts, err = db.ListAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].ID != "second" {
		t.Fatalf("account deletion failed: %+v, err=%v", accounts, err)
	}
}

func TestSaveSharePaginatesEveryTopLevelEntry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/share/snap":
			offset := request.URL.Query().Get("offset")
			list := make([]map[string]any, 0, 1000)
			if offset == "0" {
				for index := range 1000 {
					list = append(list, map[string]any{"fid": fmt.Sprintf("id-%d", index), "n": fmt.Sprintf("file-%d", index)})
				}
			} else {
				list = append(list, map[string]any{"fid": "id-1000", "n": "file-1000"})
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"state": true, "data": map[string]any{"count": 1001, "shareinfo": map[string]any{"share_title": "Large"}, "list": list}})
		case "/share/receive":
			_ = request.ParseForm()
			if got := len(strings.Split(request.Form.Get("file_id"), ",")); got != 1001 {
				t.Fatalf("received %d share IDs", got)
			}
			_, _ = io.WriteString(response, `{"state":true}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSetting("share_snapshot_interval_ms", "0"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true, Status: "active"}); err != nil {
		t.Fatalf("save account: %v", err)
	}
	service := NewDriveService(db, "", "")
	service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = upstreamURL.Scheme
		clone.URL.Host = upstreamURL.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	count, title, err := service.SaveShareCtx(context.Background(), "account", "https://115.com/s/shared", "", "0")
	if err != nil || count != 1001 || title != "Large" {
		t.Fatalf("paginated share save: count=%d title=%s err=%v", count, title, err)
	}
}

func TestDriveListFallsBackFromMethodNotAllowed(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	var hosts []string
	service := NewDriveService(db, "", "")
	service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		status := http.StatusOK
		body := `{"state":true,"count":1,"data":[{"fid":"file","cid":"0","n":"Movie.mkv","s":"10"}]}`
		if request.URL.Host == "webapi.115.com" {
			status = http.StatusMethodNotAllowed
			body = ""
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	files, total, err := service.ListFilesPageCtx(context.Background(), "account", "0", 0, 1000)
	if err != nil || total != 1 || len(files) != 1 || files[0].FileID != "file" {
		t.Fatalf("fallback listing failed: files=%+v total=%d err=%v", files, total, err)
	}
	if len(hosts) != 2 || hosts[0] != "webapi.115.com" || hosts[1] != "aps.115.com" {
		t.Fatalf("unexpected listing endpoints: %v", hosts)
	}
}

func TestDriveListingPreservesLargeIDsAndFileParents(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	svc := NewDriveService(db, "", "")
	svc.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"state":true,"count":2,"data":[{"fid":3435279884306493457,"cid":9007199254740993,"pid":"wrong","n":"episode.mkv","s":"1024"},{"cid":3435279884306493459,"pid":9007199254740993,"n":"Season 01"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	files, _, err := svc.ListFilesPageCtx(context.Background(), "account", "9007199254740993", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].FileID != "3435279884306493457" || files[0].ParentID != "9007199254740993" || files[1].FileID != "3435279884306493459" || files[1].ParentID != "9007199254740993" {
		t.Fatalf("listing changed opaque object identity: %+v", files)
	}
}

func TestDriveListingRejectsUnverifiableDeletionEvidence(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"missing total", `{"state":true,"data":[]}`},
		{"missing entries", `{"state":true,"count":0}`},
		{"unknown size", `{"state":true,"count":1,"data":[{"fid":"file","cid":"0","n":"episode.mkv"}]}`},
		{"fractional size", `{"state":true,"count":1,"data":[{"fid":"file","cid":"0","n":"episode.mkv","s":1.5}]}`},
		{"foreign parent", `{"state":true,"count":1,"data":[{"fid":"file","cid":"other","n":"episode.mkv","s":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewDriveService(db, "", "")
			svc.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body)), Request: request}, nil
			})
			if _, _, err := svc.ListFilesPageCtx(context.Background(), "account", "0", 0, 1000); err == nil {
				t.Fatal("unverifiable listing was accepted")
			}
		})
	}
}

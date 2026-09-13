package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestQuarkProviderOperationsAndSignedURLRefresh(t *testing.T) {
	var signedRequests int
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
			if request.URL.Query().Get("_fetch_total") != "1" || request.URL.Query().Get("pdir_fid") != "0" {
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

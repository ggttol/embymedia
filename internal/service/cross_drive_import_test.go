package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestQuarkTo115ImportBuildsFixedDestinationAndRemovesSpool(t *testing.T) {
	content := "nested-content"
	digest := sha1.Sum([]byte(content))
	sha := strings.ToUpper(hex.EncodeToString(digest[:]))
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	temp := t.TempDir()
	if err := db.SetSetting("transfer_temp_dir", temp); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("transfer_min_free_bytes", "0"); err != nil {
		t.Fatal(err)
	}
	quark := &domain.DriveAccount{ID: "quark", Type: "quark", Name: "Quark", Cookie: "quark-cookie", IsDefault: true, Status: "active"}
	c115 := &domain.DriveAccount{ID: "c115", Type: "115", Name: "115", Cookie: "115-cookie", Token: "access-token", IsDefault: true, Status: "active"}
	if err := db.SaveAccount(quark); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(c115); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, "", "")
	rootListed, embyListed, targetListed, packListed := 0, 0, 0, 0
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		body := ""
		status := http.StatusOK
		switch {
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/share/sharepage/token":
			body = `{"status":200,"data":{"stoken":"stoken"}}`
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/share/sharepage/detail":
			body = `{"status":200,"data":{"list":[{"fid":"shared-folder","pdir_fid":"0","file_name":"Pack","dir":true,"share_fid_token":"save-token"}]},"metadata":{"_total":1}}`
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/share/sharepage/save":
			body = `{"status":200,"data":{"task_id":"save-task"}}`
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/task":
			body = `{"status":200,"data":{"status":2,"save_as":{"save_as_top_fids":["saved-folder"]}}}`
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/file/sort":
			parent := request.URL.Query().Get("pdir_fid")
			if parent == "quark-target" {
				body = `{"status":200,"data":{"list":[{"fid":"saved-folder","pdir_fid":"quark-target","file_name":"Pack","dir":true,"revision":"folder-rev"}]},"metadata":{"_total":1}}`
			} else if parent == "saved-folder" {
				body = `{"status":200,"data":{"list":[{"fid":"source-file","pdir_fid":"saved-folder","file_name":"movie.mkv","file_size":"14","dir":false,"revision":"file-rev","sha1":"` + sha + `"}]},"metadata":{"_total":1}}`
			} else {
				t.Fatalf("unexpected Quark parent %q", parent)
			}
		case request.URL.Host == "drive-pc.quark.cn" && request.URL.Path == "/1/clouddrive/file/download":
			body = `{"status":200,"data":[{"download_url":"https://signed.test/file"}]}`
		case request.URL.Host == "signed.test":
			body = content
		case request.URL.Host == "webapi.115.com" && request.URL.Path == "/files":
			cid := request.URL.Query().Get("cid")
			switch cid {
			case "0":
				rootListed++
				if rootListed == 1 {
					body = `{"state":true,"count":0,"data":[]}`
				} else {
					body = `{"state":true,"count":1,"data":[{"cid":"emby","pid":"0","n":"emby"}]}`
				}
			case "emby":
				embyListed++
				if embyListed == 1 {
					body = `{"state":true,"count":0,"data":[]}`
				} else {
					body = `{"state":true,"count":1,"data":[{"cid":"target","pid":"emby","n":"_待整理"}]}`
				}
			case "target":
				targetListed++
				if targetListed == 1 {
					body = `{"state":true,"count":0,"data":[]}`
				} else {
					body = `{"state":true,"count":1,"data":[{"cid":"pack","pid":"target","n":"Pack"}]}`
				}
			case "pack":
				packListed++
				if packListed == 1 {
					body = `{"state":true,"count":0,"data":[]}`
				} else {
					body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"pack","n":"movie.mkv","s":"14","sha":"` + sha + `"}]}`
				}
			default:
				t.Fatalf("unexpected 115 CID %q", cid)
			}
		case request.URL.Host == "webapi.115.com" && request.URL.Path == "/files/add":
			_ = request.ParseForm()
			if request.Form.Get("cname") == "emby" {
				body = `{"state":true,"cid":"emby"}`
			} else if request.Form.Get("cname") == "_待整理" {
				body = `{"state":true,"cid":"target"}`
			} else if request.Form.Get("cname") == "Pack" {
				body = `{"state":true,"cid":"pack"}`
			} else {
				t.Fatalf("unexpected mkdir %v", request.Form)
			}
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/init":
			body = `{"state":true,"data":{"status":2}}`
		default:
			status = http.StatusNotFound
			body = `{}`
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	task, err := queue.Enqueue("quark_to_115_import", map[string]any{"quark_account_id": "quark", "quark_target_id": "quark-target", "share_url": "https://pan.quark.cn/s/share", "share_password": "password", "c115_account_id": "c115"})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %t", err, claimed)
	}
	result, err := queue.run(context.Background(), *task)
	if err != nil {
		t.Fatal(err)
	}
	if result["destination_cid"] != "target" || result["destination_path"] != "/emby/_待整理" {
		t.Fatalf("result: %+v", result)
	}
	detail, err := db.GetCrossDriveImportDetail(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Import.Phase != "verified" || detail.Import.CompletedFiles != 1 || detail.Import.CompletedBytes != int64(len(content)) || len(detail.Items) != 1 || detail.Items[0].DestinationID != "destination" {
		t.Fatalf("detail: %+v", detail)
	}
	matches, err := filepath.Glob(filepath.Join(temp, "*.part"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("spool residue: %v %v", matches, err)
	}
}

func TestCrossDriveRetryRefusesAmbiguousShareSave(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	drive := NewDriveService(db, "", "")
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	old := &domain.AsyncTask{ID: "ambiguous", Type: "quark_to_115_import", Payload: map[string]any{"quark_account_id": "quark", "quark_target_id": "target", "share_url": "https://pan.quark.cn/s/share", "c115_account_id": "c115"}, Status: "failed", MaxAttempts: 1}
	if err := db.CreateAsyncTask(old); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateCrossDriveImport(&domain.CrossDriveImport{TaskID: old.ID, QuarkAccountID: "quark", QuarkTargetID: "target", C115AccountID: "c115"}); err != nil {
		t.Fatal(err)
	}
	retry, err := queue.Retry(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(retry.ID); err != nil || !claimed {
		t.Fatalf("claim retry: %v", err)
	}
	_, err = queue.run(context.Background(), *retry)
	if err == nil || !strings.Contains(err.Error(), "refusing to resubmit") {
		t.Fatalf("ambiguous retry result: %v", err)
	}
}

func TestAutofillQuarkImportValidatesConfiguredDestinationBeforeProviderWrite(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{
		"quark_autofill_target_id": "quark-target",
		"c115_cid_map":             `{"电视剧追更":"configured-library-cid"}`,
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []*domain.DriveAccount{
		{ID: "quark", Type: "quark", Name: "Quark", Cookie: "cookie", IsDefault: true},
		{ID: "c115", Type: "115", Name: "115", Cookie: "cookie", IsDefault: true},
	} {
		if err := db.SaveAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	var providerWrites int
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providerWrites++
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	parent := &domain.AsyncTask{ID: "parent", Type: "series_auto_fill", Payload: map[string]any{}, Status: "completed", MaxAttempts: 1, CreatedAt: time.Now()}
	if err := db.CreateAsyncTask(parent); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"quark_account_id": "quark", "quark_target_id": "quark-target", "share_url": "https://pan.quark.cn/s/share", "c115_account_id": "c115",
		"selected_source_ids": []string{"episode"}, "selected_source_manifest": []ShareSelection{{ID: "episode", Revision: "rev", Name: "Show.S01E02.mkv", Size: 1024}}, "expected_episodes": []string{"S01E02"},
		"parent_task_id":        parent.ID,
		"autofill_library_name": "电视剧追更", "autofill_library_id": "library", "autofill_library_cid": "attacker-cid",
		"autofill_series_id": "series", "autofill_tmdb_id": "42", "autofill_series_folder": "Show (2026)",
	}
	if err := ValidateTask("quark_to_115_import", payload); err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("public task validation accepted internal destination binding: %v", err)
	}
	task, err := queue.enqueueAutofillQuarkImport(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %t", err, claimed)
	}
	_, err = queue.run(context.Background(), *task)
	if err == nil || !strings.Contains(err.Error(), "configured 115 library CID changed") {
		t.Fatalf("untrusted destination was not rejected: %v", err)
	}
	if providerWrites != 0 {
		t.Fatalf("provider was contacted before destination validation: writes=%d", providerWrites)
	}
	failedParent := &domain.AsyncTask{ID: "failed-parent", Type: "series_auto_fill", Payload: map[string]any{}, Status: "failed", MaxAttempts: 1, CreatedAt: time.Now()}
	if err := db.CreateAsyncTask(failedParent); err != nil {
		t.Fatal(err)
	}
	blockedPayload := make(map[string]any, len(payload))
	for key, value := range payload {
		blockedPayload[key] = value
	}
	blockedPayload["parent_task_id"] = failedParent.ID
	blockedPayload["autofill_library_cid"] = "configured-library-cid"
	blocked, err := queue.enqueueAutofillQuarkImport(blockedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(blocked.ID); err != nil || !claimed {
		t.Fatalf("claim blocked child: %v %t", err, claimed)
	}
	_, err = queue.run(context.Background(), *blocked)
	if err == nil || !strings.Contains(err.Error(), "parent task is failed") {
		t.Fatalf("child with failed recovered parent was allowed: %v", err)
	}
	if providerWrites != 0 {
		t.Fatalf("child contacted provider after parent failure: writes=%d", providerWrites)
	}
}

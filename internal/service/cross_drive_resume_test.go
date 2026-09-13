package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestCrossDriveImportResumesDownloadAndMultipartAfterRestart(t *testing.T) {
	content := bytes.Repeat([]byte{0x5a}, (16<<20)+31)
	fullHash := sha1.Sum(content)
	fullSHA := strings.ToUpper(hex.EncodeToString(fullHash[:]))
	preHash := sha1.Sum(content[:128<<10])
	preSHA := strings.ToUpper(hex.EncodeToString(preHash[:]))
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "state.db")
	spool := filepath.Join(directory, "checkpoint.part")
	if err := os.WriteFile(spool, content[:16<<20], 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []*domain.DriveAccount{{ID: "quark", Type: "quark", Name: "Quark", Cookie: "q", IsDefault: true, Status: "active"}, {ID: "c115", Type: "115", Name: "115", Cookie: "c", Token: "access", IsDefault: true, Status: "active"}} {
		if err := db.SaveAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetSetting("transfer_temp_dir", directory); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("transfer_min_free_bytes", "0"); err != nil {
		t.Fatal(err)
	}
	task := &domain.AsyncTask{ID: "resume-task", Type: "quark_to_115_import", Payload: map[string]any{"quark_account_id": "quark", "quark_target_id": "qt", "share_url": "https://pan.quark.cn/s/secret", "share_password": "secret", "c115_account_id": "c115"}, Status: "pending", MaxAttempts: 2, CreatedAt: time.Now()}
	if err := db.CreateAsyncTask(task); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("begin: %v", err)
	}
	state := &domain.CrossDriveImport{TaskID: task.ID, Phase: "uploading", QuarkAccountID: "quark", QuarkTargetID: "qt", C115AccountID: "c115"}
	if err := db.CreateCrossDriveImport(state); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCrossDriveImport(task.ID, "crashed"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCrossDriveRoots(task.ID, "crashed", []string{"saved-folder"}); err != nil {
		t.Fatal(err)
	}
	item := &domain.CrossDriveItem{SourceFileID: "source", SourceRevision: "rev", RelativePath: "Pack/movie.mkv", Name: "movie.mkv", Size: int64(len(content)), SHA1: fullSHA}
	if err := db.UpsertCrossDriveItem(task.ID, "crashed", item); err != nil {
		t.Fatal(err)
	}
	if err := db.FinalizeCrossDriveDiscovery(task.ID, "crashed"); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListCrossDriveItems(task.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v %v", items, err)
	}
	item = &items[0]
	if err := db.UpdateCrossDriveItemDownload(item.ID, task.ID, "crashed", "downloading", spool, 16<<20); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateCrossDriveItemHashes(item.ID, task.ID, "crashed", fullSHA, preSHA); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCrossDriveUploadSession(item.ID, task.ID, "crashed", "upload", "bucket", "object"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCrossDriveUploadPart(item.ID, 1, "etag-1", 16<<20); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RecoverInterruptedAsyncTasks(); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, "", "")
	rangeSeen, partTwo, destinationVisible := false, false, false
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		body := ""
		status := http.StatusOK
		switch {
		case request.URL.Host == "drive.quark.cn" && request.URL.Path == "/1/clouddrive/file/sort":
			if request.URL.Query().Get("pdir_fid") == "qt" {
				body = `{"status":200,"data":{"list":[{"fid":"saved-folder","pdir_fid":"qt","file_name":"Pack","dir":true,"revision":"r"}]},"metadata":{"_total":1}}`
			} else {
				body = `{"status":200,"data":{"list":[{"fid":"source","pdir_fid":"saved-folder","file_name":"movie.mkv","file_size":"` + strconv.Itoa(len(content)) + `","dir":false,"revision":"rev","sha1":"` + fullSHA + `"}]},"metadata":{"_total":1}}`
			}
		case request.URL.Host == "drive-pc.quark.cn" && request.URL.Path == "/1/clouddrive/file/download":
			body = `{"status":200,"data":[{"download_url":"https://signed.test/file"}]}`
		case request.URL.Host == "signed.test":
			if request.Header.Get("Range") != "bytes=16777216-" {
				t.Fatalf("resume Range = %q", request.Header.Get("Range"))
			}
			rangeSeen = true
			status = http.StatusPartialContent
			body = string(content[16<<20:])
			header.Set("Content-Type", "application/octet-stream")
		case request.URL.Host == "webapi.115.com" && request.URL.Path == "/files":
			cid := request.URL.Query().Get("cid")
			switch cid {
			case "0":
				body = `{"state":true,"count":1,"data":[{"cid":"emby","pid":"0","n":"emby"}]}`
			case "emby":
				body = `{"state":true,"count":1,"data":[{"cid":"target","pid":"emby","n":"_待整理"}]}`
			case "target":
				body = `{"state":true,"count":1,"data":[{"cid":"pack","pid":"target","n":"Pack"}]}`
			case "pack":
				if destinationVisible {
					body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"pack","n":"movie.mkv","s":"` + strconv.Itoa(len(content)) + `","sha":"` + fullSHA + `"}]}`
				} else {
					body = `{"state":true,"count":0,"data":[]}`
				}
			}
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/init":
			body = `{"state":true,"data":{"status":1,"bucket":"new-bucket","object":"new-object","callback":{}}}`
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/get_token":
			body = `{"state":true,"data":{"endpoint":"https://oss.test","AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"token"}}`
		case request.URL.Host == "bucket.oss.test" && request.Method == http.MethodGet:
			header.Set("Content-Type", "application/xml")
			body = `<ListPartsResult><Bucket>bucket</Bucket><Key>object</Key><UploadId>upload</UploadId><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>etag-1</ETag><Size>16777216</Size></Part></ListPartsResult>`
		case request.URL.Host == "bucket.oss.test" && request.Method == http.MethodPut:
			if request.URL.Query().Get("partNumber") != "2" {
				t.Fatalf("uploaded duplicate part: %s", request.URL.RawQuery)
			}
			partTwo = true
			header.Set("ETag", "etag-2")
		case request.URL.Host == "bucket.oss.test" && request.Method == http.MethodPost:
			if !partTwo {
				t.Fatal("completed before part two")
			}
			destinationVisible = true
			header.Set("Content-Type", "application/xml")
			body = `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>object</Key><ETag>etag</ETag></CompleteMultipartUploadResult>`
		default:
			status = http.StatusNotFound
			body = fmt.Sprintf(`{"error":"unexpected %s %s"}`, request.Method, request.URL.String())
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	recovered, err := db.GetAsyncTask(task.ID)
	if err != nil || recovered.Status != "pending" {
		t.Fatalf("recovered task: %+v %v", recovered, err)
	}
	queue.executeTask(context.Background(), *recovered)
	completed, err := db.GetAsyncTask(task.ID)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("completed task: %+v %v", completed, err)
	}
	if !rangeSeen || !partTwo || !destinationVisible {
		t.Fatalf("resume evidence range=%t part2=%t destination=%t", rangeSeen, partTwo, destinationVisible)
	}
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Fatalf("verified spool remains: %v", err)
	}
	detail, err := db.GetCrossDriveImportDetail(task.ID)
	if err != nil || detail.Import.CompletedFiles != 1 || detail.Items[0].DestinationID != "destination" {
		t.Fatalf("detail: %+v %v", detail, err)
	}
}

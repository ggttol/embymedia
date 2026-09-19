package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func TestSelectedQuarkDiscoveryRejectsExtraOldEpisodeBeforeTransfer(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "only requested episodes", true: "provider returned old episode"}[extra], func(t *testing.T) {
			drive, db := newSnapshotTestDrive(t, "0")
			if err := db.SaveAccount(&domain.DriveAccount{ID: "q", Type: "quark", Name: "Quark", Cookie: "fixture"}); err != nil {
				t.Fatal(err)
			}
			task := domain.AsyncTask{ID: "selected", Type: "quark_to_115_import", Status: "running", Payload: map[string]any{}, CreatedAt: time.Now()}
			if err := db.CreateAsyncTask(&task); err != nil {
				t.Fatal(err)
			}
			if err := db.CreateCrossDriveImport(&domain.CrossDriveImport{TaskID: task.ID, QuarkAccountID: "q", QuarkTargetID: "target", C115AccountID: "account"}); err != nil {
				t.Fatal(err)
			}
			if err := db.ClaimCrossDriveImport(task.ID, "owner"); err != nil {
				t.Fatal(err)
			}
			drive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/1/clouddrive/file/sort" {
					t.Errorf("unexpected provider operation: %s", r.URL.Path)
				}
				entries := []map[string]any{{"fid": "saved17", "pdir_fid": "target", "file_name": "17.mp4", "file_size": 17, "dir": false}, {"fid": "saved18", "pdir_fid": "target", "file_name": "18.mp4", "file_size": 18, "dir": false}}
				if extra {
					entries = append(entries, map[string]any{"fid": "old04", "pdir_fid": "target", "file_name": "04.mp4", "file_size": 4, "dir": false})
				}
				data, _ := json.Marshal(map[string]any{"status": 200, "data": map[string]any{"list": entries}, "metadata": map[string]any{"_total": len(entries)}})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			roots := []string{"saved17", "saved18"}
			if extra {
				roots = append(roots, "old04")
			}
			manifest := []ShareSelection{{ID: "share17", Revision: "r17", Name: "17.mp4", Size: 17}, {ID: "share18", Revision: "r18", Name: "18.mp4", Size: 18}}
			queue := NewTaskQueueService(db, drive, NewEmbyService(db))
			err := queue.discoverQuarkImport(context.Background(), task.ID, "owner", "q", "target", roots, manifest, false)
			items, listErr := db.ListCrossDriveItems(task.ID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if extra {
				if err == nil || len(items) != 0 {
					t.Fatalf("unexpected old episode admitted for transfer: %v %+v", err, items)
				}
			} else if err != nil || len(items) != 2 {
				t.Fatalf("selected episodes not admitted: %v %+v", err, items)
			}
		})
	}
}

func TestQuarkRetryDistinguishesPreflightFromAmbiguousSave(t *testing.T) {
	for _, savedRow := range []bool{false, true} {
		t.Run(map[bool]string{false: "preflight failed", true: "save outcome unknown"}[savedRow], func(t *testing.T) {
			drive, db := newSnapshotTestDrive(t, "0")
			queue := NewTaskQueueService(db, drive, NewEmbyService(db))
			original := domain.AsyncTask{ID: "failed", Type: "quark_to_115_import", Status: "failed", Payload: map[string]any{"quark_account_id": "q", "quark_target_id": "target", "c115_account_id": "account", "share_url": "https://pan.quark.cn/s/share", "selected_source_manifest": []ShareSelection{{ID: "17", Revision: "r", Name: "17.mp4", Size: 17}}, "selected_source_ids": []string{"17"}}, CreatedAt: time.Now()}
			if err := db.CreateAsyncTask(&original); err != nil {
				t.Fatal(err)
			}
			if savedRow {
				if err := db.CreateCrossDriveImport(&domain.CrossDriveImport{TaskID: original.ID, Phase: "saving_share", QuarkAccountID: "q", QuarkTargetID: "target", C115AccountID: "account"}); err != nil {
					t.Fatal(err)
				}
			}
			retry, err := queue.Retry(original.ID)
			if savedRow {
				if err == nil {
					t.Fatalf("ambiguous save allowed replay: %+v", retry)
				}
			} else if err != nil || retry == nil {
				t.Fatalf("preflight-only failure cannot retry: %v", err)
			}
		})
	}
}

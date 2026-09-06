package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

func waitForTaskStatus(t *testing.T, db *storage.DB, id, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := db.GetAsyncTask(id)
		if err == nil && task.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := db.GetAsyncTask(id)
	t.Fatalf("task did not reach %s: %+v, err=%v", status, task, err)
}

func handleTrackedEmbyScan(response http.ResponseWriter, request *http.Request, starts, polls *atomic.Int32) bool {
	response.Header().Set("Content-Type", "application/json")
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/ScheduledTasks":
		_, _ = response.Write([]byte(`[{"Id":"scan","Key":"RefreshLibrary","State":"Idle"}]`))
	case request.Method == http.MethodPost && request.URL.Path == "/ScheduledTasks/Running/scan":
		starts.Add(1)
		polls.Store(0)
		response.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodGet && request.URL.Path == "/ScheduledTasks/scan":
		switch polls.Add(1) {
		case 1:
			_, _ = response.Write([]byte(`{"Id":"scan","Key":"RefreshLibrary","State":"Running","CurrentProgressPercentage":10}`))
		case 2:
			_, _ = response.Write([]byte(`{"Id":"scan","Key":"RefreshLibrary","State":"Running","CurrentProgressPercentage":60}`))
		default:
			_, _ = response.Write([]byte(`{"Id":"scan","Key":"RefreshLibrary","State":"Idle","LastExecutionResult":{"StartTimeUtc":"2026-09-06T00:19:56.1594483Z","EndTimeUtc":"2026-09-06T00:20:18.9853086Z","Status":"Completed"}}`))
		}
	default:
		return false
	}
	return true
}

func configureTestMedia(t *testing.T, db *storage.DB) {
	t.Helper()
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	if err := os.MkdirAll(filepath.Join(mediaRoot, "Movies"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, ".embymedia-health-canary"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "Movies", "New.mkv"), []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskQueuePersistsRealExecutionAndLogs(t *testing.T) {
	var starts, polls atomic.Int32
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !handleTrackedEmbyScan(response, request, &starts, &polls) {
			http.NotFound(response, request)
		}
	}))
	defer embyServer.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	configureTestMedia(t, db)
	if err := db.SetSetting("emby_url", embyServer.URL); err != nil {
		t.Fatalf("set Emby URL: %v", err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := queue.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	defer queue.Stop()
	task, err := queue.Enqueue("emby_refresh", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	waitForTaskStatus(t, db, task.ID, "completed")
	runs, err := db.ListTaskRuns(task.ID)
	if err != nil || len(runs) != 1 || runs[0].CompletedAt == nil || !runs[0].CompletedAt.After(runs[0].StartedAt) {
		t.Fatalf("task run timing not persisted: %+v, err=%v", runs, err)
	}
	logs := strings.Join(runs[0].Logs, "\n")
	syncCompleted := strings.Index(logs, "STRM synchronization and verification completed")
	embyAccepted := strings.Index(logs, "Emby accepted the full-library scan")
	if starts.Load() != 1 || len(runs[0].Logs) < 6 || runs[0].Logs[0] != "started emby_refresh" || runs[0].Logs[len(runs[0].Logs)-1] != "completed emby_refresh" || syncCompleted < 0 || embyAccepted < 0 || syncCompleted >= embyAccepted {
		t.Fatalf("ordered ingestion details not persisted: starts=%d runs=%+v", starts.Load(), runs)
	}
	stored, err := db.GetAsyncTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stored.Result), &result); err != nil || result["strm"] == nil || result["completion_tracked"] != true || result["emby_status"] != "Completed" {
		t.Fatalf("tracked Emby result missing: %s, err=%v", stored.Result, err)
	}
}

func TestTaskQueueCancelsRunningProviderRequest(t *testing.T) {
	started := make(chan struct{})
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer embyServer.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	configureTestMedia(t, db)
	if err := db.SetSetting("emby_url", embyServer.URL); err != nil {
		t.Fatalf("set Emby URL: %v", err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := queue.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	defer queue.Stop()
	task, err := queue.Enqueue("emby_refresh", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider request did not start")
	}
	if _, err := queue.Cancel(task.ID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	waitForTaskStatus(t, db, task.ID, "cancelled")
	cancelled, err := db.GetAsyncTask(task.ID)
	if err != nil || cancelled.Progress != 75 {
		t.Fatalf("cancelled task lost completed STRM phase progress: %+v, err=%v", cancelled, err)
	}
}

func TestTaskQueueCancelsMissingPosterRequest(t *testing.T) {
	started := make(chan struct{})
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer embyServer.Close()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSetting("emby_url", embyServer.URL); err != nil {
		t.Fatalf("set Emby URL: %v", err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := queue.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	defer queue.Stop()
	task, err := queue.Enqueue("emby_missing_posters", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("missing-poster request did not start")
	}
	if _, err := queue.Cancel(task.ID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	waitForTaskStatus(t, db, task.ID, "cancelled")
}

func TestTaskQueueFailsInterruptedWorkBeforeRetry(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	task, err := queue.Enqueue("emby_refresh", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("begin task: claimed=%v err=%v", claimed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := queue.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	defer queue.Stop()
	recovered, err := db.GetAsyncTask(task.ID)
	if err != nil || recovered.Status != "failed" {
		t.Fatalf("interrupted task was not failed safely: %+v, err=%v", recovered, err)
	}
	if err := db.FinishAsyncTask(task.ID, "completed", 100, `{}`, "", "completed"); err == nil {
		t.Fatal("stale worker overwrote recovered task state")
	}
	recovered, err = db.GetAsyncTask(task.ID)
	if err != nil || recovered.Status != "failed" {
		t.Fatalf("recovered state changed after stale completion: %+v, err=%v", recovered, err)
	}
	retry, err := queue.Retry(task.ID)
	if err != nil || retry.Status != "pending" || retry.ID == task.ID {
		t.Fatalf("reviewed retry was not created: %+v, err=%v", retry, err)
	}
}

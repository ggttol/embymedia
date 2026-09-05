package service

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestTaskQueuePersistsRealExecutionAndLogs(t *testing.T) {
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/Library/Refresh" {
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusNoContent)
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
	task, err := queue.Enqueue("emby_refresh", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	waitForTaskStatus(t, db, task.ID, "completed")
	runs, err := db.ListTaskRuns(task.ID)
	if err != nil || len(runs) != 1 || len(runs[0].Logs) != 2 {
		t.Fatalf("task run logs not persisted: %+v, err=%v", runs, err)
	}
	if runs[0].Logs[0] != "started emby_refresh" || runs[0].Logs[1] != "completed emby_refresh" {
		t.Fatalf("unexpected task logs: %+v", runs[0].Logs)
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
	if err != nil || cancelled.Progress != 10 {
		t.Fatalf("cancelled task lost progress: %+v, err=%v", cancelled, err)
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

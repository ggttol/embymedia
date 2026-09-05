package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestCronManagerQueuesRealOperation(t *testing.T) {
	var refreshes atomic.Int32
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/Library/Refresh" {
			refreshes.Add(1)
			response.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(response, request)
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
	drive := NewDriveService(db, "http://127.0.0.1:8100", "")
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := queue.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	defer queue.Stop()
	manager := NewCronManager(db, queue)
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer manager.Stop()
	task := domain.ScheduledTask{Name: "Refresh Emby", Type: "emby_refresh", CronExpr: "*/1 * * * * *", Enabled: true, Params: `{}`}
	if err := manager.ScheduleTask(&task); err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for refreshes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if refreshes.Load() == 0 {
		t.Fatal("scheduled task did not call the Emby provider")
	}
	stored, err := db.GetTask(task.ID)
	if err != nil || stored.LastRunAt == nil || stored.Result == "" {
		t.Fatalf("scheduled task did not persist its queued run: %+v, err=%v", stored, err)
	}
}

func TestCronManagerRejectsInvalidScheduleWithoutPersisting(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	manager := NewCronManager(db, queue)
	task := domain.ScheduledTask{ID: "invalid", Name: "Invalid", Type: "emby_refresh", CronExpr: "not cron", Enabled: true, Params: `{}`}
	if err := manager.ScheduleTask(&task); err == nil {
		t.Fatal("invalid cron expression was accepted")
	}
	if _, err := db.GetTask(task.ID); err == nil {
		t.Fatal("invalid enabled schedule was persisted")
	}
}

package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestCronManagerQueuesRealOperation(t *testing.T) {
	var refreshes, polls atomic.Int32
	embyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !handleTrackedEmbyScan(response, request, &refreshes, &polls) {
			http.NotFound(response, request)
		}
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
	storedBeforeRun, err := db.GetTask(task.ID)
	if err != nil || storedBeforeRun.NextRunAt == nil || storedBeforeRun.NextRunAt.IsZero() {
		t.Fatalf("schedule did not persist its initial next run: %+v, err=%v", storedBeforeRun, err)
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

func TestCronManagerRunTaskLinksDurableExecution(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	manager := NewCronManager(db, queue)
	schedule := domain.ScheduledTask{ID: "manual", Name: "Refresh Emby", Type: "emby_refresh", Enabled: false, Params: `{}`}
	if err := manager.ScheduleTask(&schedule); err != nil {
		t.Fatal(err)
	}
	queued, updated, err := manager.RunTask(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.ScheduleID != schedule.ID || updated.Result != queued.ID || updated.LastRunAt == nil || !updated.LastRunAt.Equal(queued.CreatedAt) {
		t.Fatalf("immediate run was not linked to schedule: queued=%+v schedule=%+v", queued, updated)
	}
	stored, err := db.GetTask(schedule.ID)
	if err != nil || stored.Result != queued.ID || stored.LastRunAt == nil {
		t.Fatalf("schedule launch metadata was not persisted: %+v, err=%v", stored, err)
	}
	if _, _, err := manager.RunTask("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing schedule returned %v", err)
	}
	tasks, err := db.ListAsyncTasks("", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("missing schedule created an execution: %+v, err=%v", tasks, err)
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

func TestCronManagerIgnoresObsoleteCallbacks(t *testing.T) {
	for _, change := range []string{"delete", "replace", "pause"} {
		t.Run(change, func(t *testing.T) {
			db, err := storage.Open(":memory:")
			if err != nil {
				t.Fatalf("open storage: %v", err)
			}
			defer db.Close()
			queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
			manager := NewCronManager(db, queue)
			task := domain.ScheduledTask{ID: "schedule", Name: "Original", Type: "emby_refresh", CronExpr: "0 0 1 * * *", Enabled: true, Params: `{}`}
			if err := manager.ScheduleTask(&task); err != nil {
				t.Fatalf("schedule task: %v", err)
			}
			stored, err := db.GetTask(task.ID)
			if err != nil {
				t.Fatalf("get schedule: %v", err)
			}
			if stored.NextRunAt == nil || stored.NextRunAt.IsZero() {
				t.Fatal("enabled schedule has no next run")
			}
			// Cron retains this job when dispatching, even if its entry is then removed.
			callback := manager.cron.Entry(manager.entryMap[task.ID]).Job
			var expected *domain.ScheduledTask
			if change == "delete" {
				if err := manager.DeleteTask(task.ID); err != nil {
					t.Fatalf("delete schedule: %v", err)
				}
			} else {
				stored.Name = "Updated"
				stored.CronExpr = "0 0 2 * * *"
				stored.Enabled = change != "pause"
				if err := manager.ScheduleTask(stored); err != nil {
					t.Fatalf("update schedule: %v", err)
				}
				expected, err = db.GetTask(task.ID)
				if err != nil {
					t.Fatalf("get updated schedule: %v", err)
				}
				if change == "pause" && (expected.Enabled || expected.Status != "paused" || expected.NextRunAt != nil) {
					t.Fatalf("paused schedule retains active state: %+v", expected)
				}
			}

			callback.Run()

			actual, err := db.GetTask(task.ID)
			if change == "delete" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("obsolete callback resurrected deleted schedule: %+v, err=%v", actual, err)
				}
			} else if err != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("obsolete callback changed updated schedule: got %+v, want %+v, err=%v", actual, expected, err)
			}
			queued, err := db.ListAsyncTasks("", 10)
			if err != nil {
				t.Fatalf("list queued tasks: %v", err)
			}
			if len(queued) != 0 {
				t.Fatalf("obsolete callback enqueued %d tasks", len(queued))
			}
		})
	}
}

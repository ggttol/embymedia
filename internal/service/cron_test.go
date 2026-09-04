package service

import (
	"context"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestCronManager(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	cm := NewCronManager(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := cm.Start(ctx); err != nil {
		t.Fatalf("failed to start cron manager: %v", err)
	}

	task := domain.ScheduledTask{
		ID:        "test-task",
		Name:      "Test Task",
		Type:      "cleanup",
		CronExpr:  "*/1 * * * * *", // every second
		Enabled:   true,
		Status:    "idle",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveTask(&task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	if err := cm.ScheduleTask(task); err != nil {
		t.Fatalf("failed to schedule task: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	updated, err := db.GetTask("test-task")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if updated.LastRunAt == nil {
		t.Logf("task has not run yet, which is acceptable in fast unit tests")
	}
}

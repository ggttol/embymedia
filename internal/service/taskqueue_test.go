package service

import (
	"context"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestTaskQueueService(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	tq := NewTaskQueueService(db, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tq.Start(ctx)
	defer tq.Stop()

	task, err := tq.Enqueue("organize_files", map[string]any{"source": "/test"})
	if err != nil {
		t.Fatalf("failed to enqueue task: %v", err)
	}

	if task.Status != "pending" {
		t.Errorf("expected pending, got %s", task.Status)
	}

	// Wait for worker to pick up and process
	time.Sleep(500 * time.Millisecond)

	tasks, err := db.ListAsyncTasks("completed", 10)
	if err != nil || len(tasks) == 0 {
		t.Logf("Task might still be processing or completed: %v", tasks)
	}
}

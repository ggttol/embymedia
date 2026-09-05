package service

import (
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestCloudDriveWebhookDebouncesRefresh(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSetting("clouddrive_webhook_debounce_seconds", "1"); err != nil {
		t.Fatalf("set debounce: %v", err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	webhook := NewCloudDriveWebhookService(db, queue)
	defer webhook.Close()
	if err := webhook.Notify(); err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if err := webhook.Notify(); err != nil {
		t.Fatalf("second notify: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := db.ListAsyncTasks("pending", 10)
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}
		if len(tasks) == 1 {
			if tasks[0].Type != "emby_refresh" || tasks[0].Payload["change_count"] != float64(2) && tasks[0].Payload["change_count"] != 2 {
				t.Fatalf("unexpected debounced task: %+v", tasks[0])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("debounced webhook did not enqueue one refresh task")
}

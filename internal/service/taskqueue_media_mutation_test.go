package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestMediaMutationLeavesWorkerPendingAndReleasesAfterFailure(t *testing.T) {
	var refreshes atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Items/library/Refresh" {
			http.NotFound(w, r)
			return
		}
		refreshes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSetting("emby_url", provider.URL); err != nil {
		t.Fatal(err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	task, err := queue.Enqueue("emby_refresh", map[string]any{"library_id": "library"})
	if err != nil {
		t.Fatal(err)
	}
	deleteFailure := errors.New("source recycle failed")
	if err := queue.TryMediaMutation(func() error {
		queue.executeTask(context.Background(), *task)
		stored, err := db.GetAsyncTask(task.ID)
		if err != nil || stored.Status != "pending" || refreshes.Load() != 0 {
			t.Fatalf("worker mutated media during deletion: task=%+v refreshes=%d err=%v", stored, refreshes.Load(), err)
		}
		return deleteFailure
	}); !errors.Is(err, deleteFailure) {
		t.Fatalf("deletion callback lost its error: %v", err)
	}
	queue.executeTask(context.Background(), *task)
	stored, err := db.GetAsyncTask(task.ID)
	if err != nil || stored.Status != "completed" || refreshes.Load() != 1 {
		t.Fatalf("worker did not resume after deletion failure: task=%+v refreshes=%d err=%v", stored, refreshes.Load(), err)
	}
}

func TestWorkerExcludesDeletionWithoutBlockingCancellation(t *testing.T) {
	started := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(provider.Close)
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSetting("emby_url", provider.URL); err != nil {
		t.Fatal(err)
	}
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	task, err := queue.Enqueue("emby_refresh", map[string]any{"library_id": "library"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		queue.executeTask(ctx, *task)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop after cancellation")
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach provider")
	}
	called := false
	if err := queue.TryMediaMutation(func() error { called = true; return nil }); !errors.Is(err, ErrMediaMutationBusy) || called {
		t.Fatalf("deletion entered while worker was mutating media: called=%v err=%v", called, err)
	}
	cancelled := make(chan error, 1)
	go func() {
		_, err := queue.Cancel(task.ID)
		cancelled <- err
	}()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("media exclusion blocked task cancellation")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled provider call did not release worker")
	}
	stored, err := db.GetAsyncTask(task.ID)
	if err != nil || stored.Status != "cancelled" {
		t.Fatalf("cancelled task did not settle: task=%+v err=%v", stored, err)
	}
	if err := queue.TryMediaMutation(func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("worker retained media lock after cancellation: called=%v err=%v", called, err)
	}
}

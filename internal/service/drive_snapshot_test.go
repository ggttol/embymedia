package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type snapshotResponseBody struct {
	io.Reader
	close func() error
}

func (body snapshotResponseBody) Close() error { return body.close() }

func newSnapshotTestDrive(t *testing.T, interval string) (*DriveService, *storage.DB) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := NewSettingsService(db).Update(map[string]string{"share_snapshot_interval_ms": interval}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	return NewDriveService(db, "http://resource.test", ""), db
}

func TestShareSnapshotPacesPaginationDirectoriesAndCandidates(t *testing.T) {
	const interval = 35 * time.Millisecond
	drive, db := newSnapshotTestDrive(t, "35")
	var closedAt time.Time
	var requests []string
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !closedAt.IsZero() && time.Since(closedAt) < interval-time.Millisecond {
			t.Errorf("snapshot started %s after response close, want at least %s", time.Since(closedAt), interval)
		}
		query := request.URL.Query()
		requests = append(requests, query.Get("share_code")+":"+query.Get("cid")+":"+query.Get("offset"))
		entryID := "episode-" + query.Get("share_code") + "-" + query.Get("cid") + "-" + query.Get("offset")
		entries := []map[string]any{{"fid": entryID, "n": "Show.S01E02.mkv"}}
		count := 1
		if query.Get("share_code") == "first" && query.Get("cid") == "0" {
			count = 1001
			if query.Get("offset") == "0" {
				entries = make([]map[string]any, 0, 1000)
				for index := range 999 {
					entries = append(entries, map[string]any{"fid": fmt.Sprintf("text-%d", index), "n": "readme.txt"})
				}
				entries = append(entries, map[string]any{"cid": "season", "n": "Season 1"})
			}
		}
		payload, err := json.Marshal(map[string]any{"state": true, "data": map[string]any{"count": count, "shareinfo": map[string]string{"share_title": "Show"}, "list": entries}})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: snapshotResponseBody{
			Reader: strings.NewReader(string(payload)),
			close:  func() error { closedAt = time.Now(); return nil },
		}}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	title, leaves, err := queue.scanAutoFillShare(context.Background(), autoFillCandidate{URL: "https://115.com/s/first"})
	if err != nil || title != "Show" || len(leaves) != 2 || len(leaves[0].Ancestors) != 1 || leaves[0].Ancestors[0] != "Season 1" {
		t.Fatalf("recursive paginated snapshot: title=%q leaves=%+v err=%v", title, leaves, err)
	}
	_, leaves, err = queue.scanAutoFillShare(context.Background(), autoFillCandidate{URL: "https://115.com/s/second"})
	if err != nil || len(leaves) != 1 || leaves[0].Name != "Show.S01E02.mkv" {
		t.Fatalf("next candidate: leaves=%+v err=%v", leaves, err)
	}
	if got := strings.Join(requests, ","); got != "first:0:0,first:0:1000,first:season:0,second:0:0" {
		t.Fatalf("unexpected snapshot traversal: %s", got)
	}
}

func TestShareSnapshotSerializesUntilBodyClosedAndCancelsQueuedCaller(t *testing.T) {
	drive, _ := newSnapshotTestDrive(t, "0")
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer release()
	var closed atomic.Bool
	var calls atomic.Int32
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call > 1 && !closed.Load() {
			t.Error("concurrent snapshot reached provider before previous body closed")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: snapshotResponseBody{
			Reader: strings.NewReader(`{"state":true,"data":{"count":1,"list":[{"fid":"episode","n":"Show.S01E01.mkv"}]}}`),
			close: func() error {
				if call == 1 {
					close(closeStarted)
					<-releaseClose
					closed.Store(true)
				}
				return nil
			},
		}}, nil
	})
	first := make(chan error, 1)
	go func() {
		_, _, err := drive.SnapshotShareCtx(context.Background(), "account", "https://115.com/s/first", "")
		first <- err
	}()
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("first snapshot did not consume its body")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	queued := make(chan error, 1)
	go func() {
		_, _, err := drive.SnapshotShareCtx(ctx, "account", "https://115.com/s/canceled", "")
		queued <- err
	}()
	select {
	case err := <-queued:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("queued cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled snapshot remained blocked behind response close")
	}
	if calls.Load() != 1 {
		t.Errorf("canceled queued call reached provider: %d requests", calls.Load())
	}
	release()
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first snapshot did not finish after body close")
	}
	_, entries, err := drive.SnapshotShareCtx(context.Background(), "account", "https://115.com/s/next", "")
	if err != nil || len(entries) != 1 || entries[0].ID != "episode" || calls.Load() != 2 {
		t.Fatalf("queue did not recover after cancellation: entries=%+v requests=%d err=%v", entries, calls.Load(), err)
	}
}

func TestShareSnapshotCancellationDuringIntervalSendsNoRequest(t *testing.T) {
	drive, _ := newSnapshotTestDrive(t, "2000")
	var calls atomic.Int32
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":true,"data":{"list":[]}}`))}, nil
	})
	if _, _, err := drive.SnapshotShareCtx(context.Background(), "account", "https://115.com/s/first", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := drive.SnapshotShareCtx(ctx, "account", "https://115.com/s/canceled", "")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
			t.Fatalf("interval cancellation: requests=%d err=%v", calls.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("interval wait ignored cancellation")
	}
}

func TestShareSnapshotRetainsCompletedPagesOnHTTPRejection(t *testing.T) {
	drive, _ := newSnapshotTestDrive(t, "0")
	entries := make([]map[string]any, 0, 1000)
	for index := range 1000 {
		entries = append(entries, map[string]any{"fid": fmt.Sprintf("file-%d", index), "n": fmt.Sprintf("episode-%d.mkv", index)})
	}
	page, err := json.Marshal(map[string]any{"state": true, "data": map[string]any{"count": 1001, "shareinfo": map[string]string{"share_title": "Partial pack"}, "list": entries}})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Query().Get("offset") == "0" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(page)))}, nil
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("snapshot rejected"))}, nil
	})
	title, result, err := drive.SnapshotShareCtx(context.Background(), "account", "https://115.com/s/partial", "")
	var statusError *ProviderHTTPError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("snapshot HTTP status is not recoverable: %v", err)
	}
	if requests != 2 || title != "Partial pack" || len(result) != 1000 || result[999].ID != "file-999" || result[999].Name != "episode-999.mkv" {
		t.Fatalf("rejected page discarded completed results: requests=%d title=%q entries=%d", requests, title, len(result))
	}
}

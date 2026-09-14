package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type quarkDownloadFixture struct {
	queue   *TaskQueueService
	db      *storage.DB
	taskID  string
	owner   string
	item    domain.CrossDriveItem
	spool   string
	ranges  *rangeRecorder
	content []byte
}

type rangeRecorder struct {
	mu     sync.Mutex
	ranges []string
}

func (r *rangeRecorder) record(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranges = append(r.ranges, value)
}

func (r *rangeRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ranges...)
}

func (r *rangeRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranges = nil
}

// newQuarkDownloadFixture builds a claimed cross-drive item whose only provider
// interaction is a ranged Quark download. The signed callback returns a status
// and the response body for one range request.
func newQuarkDownloadFixture(t *testing.T, content []byte, signed func(rangeHeader string) (int, io.Reader)) *quarkDownloadFixture {
	t.Helper()
	directory := t.TempDir()
	db, err := storage.Open(filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, account := range []*domain.DriveAccount{
		{ID: "quark", Type: "quark", Name: "Quark", Cookie: "q", IsDefault: true, Status: "active"},
		{ID: "c115", Type: "115", Name: "115", Cookie: "c", Token: "access", IsDefault: true, Status: "active"},
	} {
		if err := db.SaveAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{"transfer_temp_dir": directory, "transfer_min_free_bytes": "0"} {
		if err := db.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	task := &domain.AsyncTask{ID: "download-task", Type: "quark_to_115_import", Payload: map[string]any{"quark_account_id": "quark", "quark_target_id": "qt", "share_url": "https://pan.quark.cn/s/secret", "c115_account_id": "c115"}, Status: "pending", MaxAttempts: 1, CreatedAt: time.Now()}
	if err := db.CreateAsyncTask(task); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("begin: %v", err)
	}
	if err := db.CreateCrossDriveImport(&domain.CrossDriveImport{TaskID: task.ID, Phase: "downloading", QuarkAccountID: "quark", QuarkTargetID: "qt", C115AccountID: "c115"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCrossDriveImport(task.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCrossDriveRoots(task.ID, "owner", []string{"saved-folder"}); err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(content)
	if err := db.UpsertCrossDriveItem(task.ID, "owner", &domain.CrossDriveItem{
		SourceFileID: "source", SourceRevision: "rev", RelativePath: "Pack/movie.mkv", Name: "movie.mkv",
		Size: int64(len(content)), SHA1: strings.ToUpper(hex.EncodeToString(digest[:])),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.FinalizeCrossDriveDiscovery(task.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListCrossDriveItems(task.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v %v", items, err)
	}
	recorder := &rangeRecorder{}
	spool := filepath.Join(directory, "download.part")
	item := items[0]
	// Pin the spool path so the fixture and the service agree on the checkpoint key.
	item.SpoolPath = spool
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		switch {
		case request.URL.Host == "drive-pc.quark.cn" && request.URL.Path == "/1/clouddrive/file/download":
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"status":200,"data":[{"download_url":"https://signed.test/file"}]}`)), Request: request}, nil
		case request.URL.Host == "signed.test":
			rangeHeader := request.Header.Get("Range")
			recorder.record(rangeHeader)
			status, body := signed(rangeHeader)
			if status != http.StatusPartialContent {
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{"status":500,"message":"probe failure"}`)), Request: request}, nil
			}
			header.Set("Content-Type", "application/octet-stream")
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(body), Request: request}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: header, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":"unexpected %s"}`, request.URL))), Request: request}, nil
		}
	})
	return &quarkDownloadFixture{
		queue: NewTaskQueueService(db, drive, NewEmbyService(db)), db: db, taskID: task.ID, owner: "owner",
		item: item, spool: spool, ranges: recorder, content: content,
	}
}

func rangeBytes(t *testing.T, content []byte, rangeHeader string) (int, io.Reader) {
	t.Helper()
	value := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		t.Fatalf("unexpected Range %q", rangeHeader)
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("unexpected Range %q", rangeHeader)
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("unexpected Range %q", rangeHeader)
	}
	if start >= int64(len(content)) {
		return http.StatusRequestedRangeNotSatisfiable, nil
	}
	if end >= int64(len(content)) {
		end = int64(len(content)) - 1
	}
	return http.StatusPartialContent, bytes.NewReader(content[start : end+1])
}

// failingReader yields a prefix of its data and then fails, simulating a CDN
// stream error partway through one ranged response.
type failingReader struct {
	data  []byte
	after int
	read  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read >= r.after {
		return 0, fmt.Errorf("stream error: stream ID 9; INTERNAL_ERROR; received from peer")
	}
	limit := min(len(p), r.after-r.read, len(r.data)-r.read)
	if limit <= 0 {
		return 0, io.EOF
	}
	copy(p, r.data[r.read:r.read+limit])
	r.read += limit
	return limit, nil
}

func TestQuarkDownloadUsesParallelSegments(t *testing.T) {
	previous := quarkDownloadSegmentSize
	quarkDownloadSegmentSize = 1 << 20
	defer func() { quarkDownloadSegmentSize = previous }()
	content := bytes.Repeat([]byte{0x7b}, 4<<20)
	var inFlight, peak atomic.Int32
	fixture := newQuarkDownloadFixture(t, content, func(rangeHeader string) (int, io.Reader) {
		current := inFlight.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
		defer inFlight.Add(-1)
		return rangeBytes(t, content, rangeHeader)
	})
	if err := fixture.db.SetSetting("quark_download_connections", "4"); err != nil {
		t.Fatal(err)
	}
	spool, sha, _, err := fixture.queue.downloadQuarkItem(context.Background(), fixture.taskID, fixture.owner, "quark", fixture.item, fixture.item.Size, 0)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(spool)
	if err != nil || !bytes.Equal(written, content) {
		t.Fatalf("spool mismatch: %d bytes err=%v", len(written), err)
	}
	digest := sha1.Sum(content)
	if !strings.EqualFold(sha, hex.EncodeToString(digest[:])) {
		t.Fatalf("sha = %s", sha)
	}
	segments, err := fixture.db.ListCrossDriveDownloadSegments(fixture.spool)
	if err != nil || len(segments) != 4 {
		t.Fatalf("segments = %v err=%v", segments, err)
	}
	if peak.Load() != 4 {
		t.Fatalf("peak concurrency = %d, want 4", peak.Load())
	}
}

func TestQuarkDownloadReconnectsAfterMidStreamFailure(t *testing.T) {
	previous := quarkDownloadSegmentSize
	quarkDownloadSegmentSize = 1 << 20
	defer func() { quarkDownloadSegmentSize = previous }()
	content := bytes.Repeat([]byte{0x4e}, 2<<20)
	var failed atomic.Int32
	fixture := newQuarkDownloadFixture(t, content, func(rangeHeader string) (int, io.Reader) {
		if failed.Load() == 0 && strings.HasPrefix(rangeHeader, "bytes=0-") {
			// The first attempt for segment zero dies after 256 KiB; the reopened
			// range must resume from the bytes already written, not from scratch.
			failed.Add(1)
			return http.StatusPartialContent, &failingReader{data: content[:1<<20], after: 256 << 10}
		}
		return rangeBytes(t, content, rangeHeader)
	})
	spool, _, _, err := fixture.queue.downloadQuarkItem(context.Background(), fixture.taskID, fixture.owner, "quark", fixture.item, fixture.item.Size, 0)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(spool)
	if err != nil || !bytes.Equal(written, content) {
		t.Fatalf("spool mismatch after reconnect: %d bytes err=%v", len(written), err)
	}
	ranges := fixture.ranges.all()
	resumed := false
	for _, requested := range ranges {
		if requested == "bytes=262144-1048575" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("failed segment did not resume from its partial offset: %v", ranges)
	}
}

func TestQuarkProviderDownloadAbortsWhenTheCdnStalls(t *testing.T) {
	previous := quarkDownloadSegmentTimeout
	quarkDownloadSegmentTimeout = 300 * time.Millisecond
	defer func() { quarkDownloadSegmentTimeout = previous }()
	release := make(chan struct{})
	defer close(release)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/file/download":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"status":200,"data":[{"download_url":"`+server.URL+`/signed"}]}`)
		case "/signed":
			// Headers and a partial body arrive, then the connection goes silent
			// without closing: exactly the stall that would pin the single worker.
			response.Header().Set("Content-Length", "1048576")
			response.WriteHeader(http.StatusPartialContent)
			_, _ = response.Write([]byte("partial"))
			response.(http.Flusher).Flush()
			// Return when the client gives up so the test server can shut down.
			select {
			case <-release:
			case <-request.Context().Done():
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	provider := NewProviderQuark(server.Client())
	provider.baseURL = server.URL
	provider.downloadBaseURL = server.URL
	provider.sleep = func(context.Context, time.Duration) error { return nil }
	account := &domain.DriveAccount{ID: "quark", Type: "quark", Cookie: "cookie"}

	body, err := provider.OpenDownload(context.Background(), account, "file", 0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, readErr := io.ReadAll(body)
	body.Close()
	if readErr == nil {
		t.Fatal("a stalled CDN response read completed")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("a stalled CDN response blocked for %s", elapsed)
	}
}

func TestQuarkDownloadReportsPersistentSegmentFailure(t *testing.T) {
	previous := quarkDownloadSegmentSize
	quarkDownloadSegmentSize = 1 << 20
	defer func() { quarkDownloadSegmentSize = previous }()
	content := bytes.Repeat([]byte{0x11}, 1<<20)
	fixture := newQuarkDownloadFixture(t, content, func(rangeHeader string) (int, io.Reader) {
		return http.StatusInternalServerError, nil
	})
	if _, _, _, err := fixture.queue.downloadQuarkItem(context.Background(), fixture.taskID, fixture.owner, "quark", fixture.item, fixture.item.Size, 0); err == nil {
		t.Fatal("a segment that never succeeded was reported as downloaded")
	}
	segments, err := fixture.db.ListCrossDriveDownloadSegments(fixture.spool)
	if err != nil || len(segments) != 0 {
		t.Fatalf("failed segment was checkpointed: %v err=%v", segments, err)
	}
}

func TestQuarkDownloadResumesOnlyMissingSegments(t *testing.T) {
	previous := quarkDownloadSegmentSize
	quarkDownloadSegmentSize = 1 << 20
	defer func() { quarkDownloadSegmentSize = previous }()
	content := bytes.Repeat([]byte{0x2d}, 3<<20)
	var failing atomic.Bool
	failing.Store(true)
	fixture := newQuarkDownloadFixture(t, content, func(rangeHeader string) (int, io.Reader) {
		if failing.Load() && strings.HasPrefix(rangeHeader, "bytes=1048576-") {
			return http.StatusInternalServerError, nil
		}
		return rangeBytes(t, content, rangeHeader)
	})
	if _, _, _, err := fixture.queue.downloadQuarkItem(context.Background(), fixture.taskID, fixture.owner, "quark", fixture.item, fixture.item.Size, 0); err == nil {
		t.Fatal("failed segment was reported as a completed download")
	}
	checkpointed, err := fixture.db.ListCrossDriveDownloadSegments(fixture.spool)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpointed) == 0 {
		t.Fatal("no segment checkpoint survived the failed attempt")
	}
	failing.Store(false)
	fixture.ranges.reset()
	spool, _, _, err := fixture.queue.downloadQuarkItem(context.Background(), fixture.taskID, fixture.owner, "quark", fixture.item, fixture.item.Size, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index := range checkpointed {
		offset := int64(index) * quarkDownloadSegmentSize
		previous := fmt.Sprintf("bytes=%d-%d", offset, offset+quarkDownloadSegmentSize-1)
		for _, requested := range fixture.ranges.all() {
			if requested == previous {
				t.Fatalf("checkpointed segment %d was downloaded again", index)
			}
		}
	}
	written, err := os.ReadFile(spool)
	if err != nil || !bytes.Equal(written, content) {
		t.Fatalf("resumed spool mismatch: %d bytes err=%v", len(written), err)
	}
	segments, err := fixture.db.ListCrossDriveDownloadSegments(fixture.spool)
	if err != nil || len(segments) != 3 {
		t.Fatalf("segments = %v err=%v", segments, err)
	}
}

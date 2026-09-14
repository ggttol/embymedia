package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/embymedia/embymedia/internal/transfer"
)

type fakeTransferWorker struct {
	contentSHA string
	download   transfer.Request
	publish    transfer.Request
	committed  bool
	published  bool
}

func (w *fakeTransferWorker) Check(context.Context, string, string, int64) error { return nil }

func (w *fakeTransferWorker) Download(_ context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error) {
	w.download = request
	if err := progress(transfer.Event{Phase: "downloading", DownloadedBytes: request.Size, Size: request.Size}); err != nil {
		return transfer.Event{}, err
	}
	return transfer.Event{Type: "completed", Phase: "downloaded", DownloadedBytes: request.Size, Size: request.Size, SHA1: w.contentSHA, PreSHA1: w.contentSHA}, nil
}

func (w *fakeTransferWorker) Publish(_ context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error) {
	w.publish = request
	if err := progress(transfer.Event{Phase: "uploading", DownloadedBytes: request.Size, Size: request.Size, SHA1: request.ExpectedSHA1}); err != nil {
		return transfer.Event{}, err
	}
	w.published = true
	return transfer.Event{Type: "completed", Phase: "published", DownloadedBytes: request.Size, Size: request.Size, SHA1: request.ExpectedSHA1}, nil
}

func (w *fakeTransferWorker) Commit(context.Context, string, string, string) error {
	w.committed = true
	return nil
}

func TestNASTransferDownloadsBeforePublishingAndVerifies115(t *testing.T) {
	previous := destinationVerifyBaseTimeout
	destinationVerifyBaseTimeout = 10 * time.Millisecond
	defer func() { destinationVerifyBaseTimeout = previous }()
	content := []byte("remote-transfer")
	digest := sha1.Sum(content)
	sha := strings.ToUpper(hex.EncodeToString(digest[:]))
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quarkAccount := &domain.DriveAccount{ID: "quark", Type: "quark", Name: "Quark", Cookie: "secret-cookie", IsDefault: true, Status: "active"}
	c115Account := &domain.DriveAccount{ID: "c115", Type: "115", Name: "115", Cookie: "cookie", IsDefault: true, Status: "active"}
	for _, account := range []*domain.DriveAccount{quarkAccount, c115Account} {
		if err := db.SaveAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	task := &domain.AsyncTask{ID: "task", Type: "quark_to_115_import", Status: "pending", MaxAttempts: 1, CreatedAt: time.Now()}
	if err := db.CreateAsyncTask(task); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginAsyncTask(task.ID); err != nil || !claimed {
		t.Fatalf("claim task: %v", err)
	}
	state := &domain.CrossDriveImport{TaskID: task.ID, Phase: "downloading", QuarkAccountID: quarkAccount.ID, QuarkTargetID: "target", C115AccountID: c115Account.ID, DestinationCID: "parent", TotalBytes: int64(len(content))}
	if err := db.CreateCrossDriveImport(state); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCrossDriveImport(task.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertCrossDriveItem(task.ID, "owner", &domain.CrossDriveItem{SourceFileID: "source", SourceRevision: "revision", RelativePath: "Pack/movie.mkv", Name: "movie.mkv", Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if err := db.FinalizeCrossDriveDiscovery(task.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListCrossDriveItems(task.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v, %v", items, err)
	}
	worker := &fakeTransferWorker{contentSHA: sha}
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"state":true,"count":0,"data":[]}`
		if worker.published {
			body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"parent","n":"movie.mkv","s":"15","sha":"` + sha + `"}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	queue.nasWorker = worker
	result, err := queue.transferQuarkItemViaNAS(context.Background(), task.ID, "owner", &items[0], state, quarkAccount, c115Account, "parent", int64(len(content)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileID != "destination" || !worker.committed {
		t.Fatalf("result=%+v committed=%v", result, worker.committed)
	}
	if worker.download.QuarkCookie != "secret-cookie" || worker.publish.QuarkCookie != "" {
		t.Fatal("Quark credential crossed the publish boundary incorrectly")
	}
	if worker.publish.Destination != "_待整理" || worker.publish.ExpectedSHA1 != sha {
		t.Fatalf("publish request: %+v", worker.publish)
	}
}

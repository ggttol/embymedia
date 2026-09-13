package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestCloudDriveUploadPublishesAndVerifiesExactIdentity(t *testing.T) {
	content := []byte("cloud-drive-upload")
	digest := sha1.Sum(content)
	sha := strings.ToUpper(hex.EncodeToString(digest[:]))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "_待整理", "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(t.TempDir(), "spool.part")
	if err := os.WriteFile(spool, content, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	account := &domain.DriveAccount{ID: "c115", Type: "115", Name: "115", Cookie: "cookie", IsDefault: true, Status: "active"}
	if err := db.SaveAccount(account); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		finalPath := filepath.Join(root, "_待整理", "Pack", "movie.txt")
		body := `{"state":true,"count":0,"data":[]}`
		if _, err := os.Stat(finalPath); err == nil {
			body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"parent","n":"movie.txt","s":"18","sha":"` + sha + `"}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	item := domain.CrossDriveItem{SourceFileID: "source", RelativePath: "Pack/movie.txt", Name: "movie.txt", Size: int64(len(content)), SHA1: sha}
	result, err := queue.uploadViaCloudDriveAt(context.Background(), item, account, "parent", spool, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileID != "destination" || result.Transferred != int64(len(content)) {
		t.Fatalf("upload result: %+v", result)
	}
	written, err := os.ReadFile(filepath.Join(root, "_待整理", "Pack", "movie.txt"))
	if err != nil || string(written) != string(content) {
		t.Fatalf("published content %q, err=%v", written, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "_待整理", "Pack", ".embymedia-*.uploading"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging residue: %v, err=%v", matches, err)
	}
}

func TestCloudDriveUploadPublishesIntoBoundSeriesDirectory(t *testing.T) {
	content := []byte("episode")
	digest := sha1.Sum(content)
	sha := strings.ToUpper(hex.EncodeToString(digest[:]))
	root := t.TempDir()
	destination := filepath.Join(root, "电视剧追更", "Show (2026)")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(t.TempDir(), "spool.part")
	if err := os.WriteFile(spool, content, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	account := &domain.DriveAccount{ID: "c115", Type: "115", Name: "115", Cookie: "cookie", IsDefault: true}
	if err := db.SaveAccount(account); err != nil {
		t.Fatal(err)
	}
	drive := NewDriveService(db, "", "")
	drive.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"state":true,"count":0,"data":[]}`
		if _, err := os.Stat(filepath.Join(destination, "Show.S01E02.mkv")); err == nil {
			body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"series-cid","n":"Show.S01E02.mkv","s":"7","sha":"` + sha + `"}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	queue := NewTaskQueueService(db, drive, NewEmbyService(db))
	item := domain.CrossDriveItem{SourceFileID: "source", RelativePath: "Show.S01E02.mkv", Name: "Show.S01E02.mkv", Size: int64(len(content)), SHA1: sha}
	result, err := queue.uploadViaCloudDriveDirectory(context.Background(), item, account, "series-cid", spool, destination)
	if err != nil || result.FileID != "destination" {
		t.Fatalf("bound upload result=%+v err=%v", result, err)
	}
	if written, err := os.ReadFile(filepath.Join(destination, item.Name)); err != nil || string(written) != string(content) {
		t.Fatalf("bound Series content=%q err=%v", written, err)
	}
	if _, err := os.Stat(filepath.Join(root, "_待整理", item.Name)); !os.IsNotExist(err) {
		t.Fatalf("bound upload also wrote to _待整理: %v", err)
	}
}

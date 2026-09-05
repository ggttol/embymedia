package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestMediaServiceSynchronizesAndVerifiesSTRM(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	mediaFile := filepath.Join(mediaRoot, "Movies", "Example.mkv")
	if err := os.MkdirAll(filepath.Dir(mediaFile), 0755); err != nil {
		t.Fatalf("create media directory: %v", err)
	}
	if err := os.WriteFile(mediaFile, []byte("video"), 0644); err != nil {
		t.Fatalf("create media file: %v", err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatalf("configure media paths: %v", err)
	}
	service := NewMediaService(db)
	result, err := service.SyncSTRM(context.Background(), "Movies")
	if err != nil || result.Created != 1 || result.Valid != 1 || result.Missing != 0 {
		t.Fatalf("unexpected STRM sync: %+v, err=%v", result, err)
	}
	strm, err := os.ReadFile(filepath.Join(strmRoot, "Movies", "Example.strm"))
	if err != nil || strings.TrimSpace(string(strm)) != "/media/Movies/Example.mkv" {
		t.Fatalf("unexpected STRM target %q, err=%v", strm, err)
	}
	if err := os.Remove(mediaFile); err != nil {
		t.Fatalf("remove media fixture: %v", err)
	}
	verification, err := service.VerifySTRM(context.Background(), "Movies")
	if err != nil || verification.Missing != 1 || verification.Valid != 0 {
		t.Fatalf("missing target was not reported: %+v, err=%v", verification, err)
	}
}

func TestMediaServiceRejectsSymlinkedOutputDirectory(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{filepath.Join(mediaRoot, "Movies"), strmRoot, outside} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatalf("create directory: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "Movies", "Example.mkv"), []byte("video"), 0644); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(strmRoot, "Movies")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatalf("configure paths: %v", err)
	}
	if _, err := NewMediaService(db).SyncSTRM(context.Background(), "Movies"); err == nil {
		t.Fatal("sync followed a STRM output symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "Example.strm")); !os.IsNotExist(err) {
		t.Fatalf("sync wrote outside strm_root: %v", err)
	}
}

func TestMediaServiceRejectsEscapingSTRMTarget(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	if err := os.MkdirAll(filepath.Join(mediaRoot, "Movies"), 0755); err != nil {
		t.Fatalf("create media root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(strmRoot, "Movies"), 0755); err != nil {
		t.Fatalf("create STRM root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(strmRoot, "Movies", "Escape.strm"), []byte("/media/../../etc/passwd\n"), 0644); err != nil {
		t.Fatalf("create STRM fixture: %v", err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatalf("configure paths: %v", err)
	}
	result, err := NewMediaService(db).VerifySTRM(context.Background(), "Movies")
	if err != nil || result.Invalid != 1 || result.Valid != 0 {
		t.Fatalf("escaping STRM target was not rejected: %+v, err=%v", result, err)
	}
}

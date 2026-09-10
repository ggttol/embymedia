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

func TestMediaServiceSynchronizesLegacyVideoContainers(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	library := filepath.Join(mediaRoot, "Archive")
	if err := os.MkdirAll(library, 0755); err != nil {
		t.Fatal(err)
	}
	for _, extension := range []string{".avi", ".flv", ".iso", ".rm", ".rmvb", ".vob", ".wmv"} {
		name := "Legacy" + strings.TrimPrefix(extension, ".") + extension
		if err := os.WriteFile(filepath.Join(library, name), []byte(extension), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatal(err)
	}
	result, err := NewMediaService(db).SyncSTRM(context.Background(), "Archive")
	if err != nil || result.Created != 7 || result.Valid != 7 {
		t.Fatalf("legacy video containers were not synchronized: %+v, err=%v", result, err)
	}
}

func TestMediaServiceSkipsConfiguredReviewRootsDuringFullSync(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	for _, relative := range []string{"Movies/Keep.mkv", "_待整理/Review.mkv", "_待回收/Duplicate.mkv"} {
		path := filepath.Join(mediaRoot, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media", "media_excluded_roots": `["_待整理","_待回收"]`}); err != nil {
		t.Fatal(err)
	}
	result, err := NewMediaService(db).SyncSTRM(context.Background(), "")
	if err != nil || result.MediaFiles != 1 || result.Created != 1 || result.Valid != 1 {
		t.Fatalf("full synchronization did not isolate review roots: %+v, err=%v", result, err)
	}
	for _, relative := range []string{"_待整理/Review.strm", "_待回收/Duplicate.strm"} {
		if _, err := os.Stat(filepath.Join(strmRoot, relative)); !os.IsNotExist(err) {
			t.Fatalf("excluded STRM output exists: %s, err=%v", relative, err)
		}
	}
}

func TestMediaServicePrunesOnlyMissingGeneratedSTRMWithMountCanary(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	ghostDirectory := filepath.Join(strmRoot, "Movies", "Ghost")
	for _, directory := range []string{filepath.Join(mediaRoot, "Movies"), filepath.Join(strmRoot, "Movies"), ghostDirectory} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(strmRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(strmRoot, "Movies"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, ".embymedia-health-canary"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "Movies", "Current.mkv"), []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(ghostDirectory, "Stale.strm")
	manual := filepath.Join(strmRoot, "Movies", "Alternate.strm")
	if err := os.WriteFile(stale, []byte("/media/Movies/Missing.mkv\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manual, []byte("/media/Movies/Current.mkv\n"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatal(err)
	}
	progress := make([]float64, 0)
	result, err := NewMediaService(db).SyncSTRMWithProgress(context.Background(), "Movies", func(value float64, message string) error {
		if len(progress) > 0 && value < progress[len(progress)-1] {
			t.Errorf("progress moved backward: %v then %v", progress[len(progress)-1], value)
		}
		progress = append(progress, value)
		return nil
	})
	if err != nil || result.Removed != 1 || result.RemovedDirectories != 1 || result.Missing != 0 || result.Valid != 2 || result.PruneStatus != "completed" {
		t.Fatalf("stale reconciliation failed: %+v, err=%v", result, err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("STRM synchronization did not report completion: progress=%v", progress)
	}
	for _, directory := range []string{strmRoot, filepath.Join(strmRoot, "Movies")} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("read STRM directory permissions: %s: %v", directory, err)
		}
		if info.Mode().Perm()&0055 != 0055 {
			t.Fatalf("STRM directory remained unreadable by Emby: %s mode=%v", directory, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale generated STRM was retained: %v", err)
	}
	if _, err := os.Stat(ghostDirectory); !os.IsNotExist(err) {
		t.Fatalf("empty generated directory was retained: %v", err)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("valid non-generated STRM was removed: %v", err)
	}
}

func TestMediaServiceSkipsPruningWithoutMountCanary(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	for _, directory := range []string{filepath.Join(mediaRoot, "Movies"), filepath.Join(strmRoot, "Movies")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(strmRoot, "Movies", "Stale.strm")
	if err := os.WriteFile(stale, []byte("/media/Movies/Missing.mkv\n"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatal(err)
	}
	result, err := NewMediaService(db).SyncSTRM(context.Background(), "Movies")
	if err != nil || result.Removed != 0 || result.Missing != 1 || result.PruneStatus != "skipped_no_mount_canary" {
		t.Fatalf("unsafe pruning was not skipped: %+v, err=%v", result, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("STRM was removed without mount proof: %v", err)
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

func TestMediaServiceSerializesCollidingSTRMOutputs(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	movies := filepath.Join(mediaRoot, "Movies")
	if err := os.MkdirAll(movies, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Episode.mkv", "Episode.mp4"} {
		if err := os.WriteFile(filepath.Join(movies, name), []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatal(err)
	}
	result, err := NewMediaService(db).SyncSTRM(context.Background(), "Movies")
	if err != nil || result.MediaFiles != 2 || result.Created != 1 || result.Updated != 1 || result.Valid != 1 {
		t.Fatalf("colliding outputs lost source order: %+v, err=%v", result, err)
	}
	content, err := os.ReadFile(filepath.Join(strmRoot, "Movies", "Episode.strm"))
	if err != nil || strings.TrimSpace(string(content)) != "/media/Movies/Episode.mp4" {
		t.Fatalf("later source did not own colliding output: %q, err=%v", content, err)
	}
}

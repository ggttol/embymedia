package service

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/embymedia/embymedia/internal/storage"
)

func TestMediaServiceMigratesLegacySeasonSTRM(t *testing.T) {
	for _, scope := range []string{"Television", "Television/24小时/SE02"} {
		t.Run(scope, func(t *testing.T) {
			service, mediaRoot, strmRoot := newSeriesSyncFixture(t, true)
			sourceRelative := "Television/24小时/SE02/24小时.1080P.H265.SE02.01.mkv"
			oldRelative := "Television/24小时/SE02/24小时.1080P.H265.SE02.01.strm"
			canonicalRelative := "Television/24小时/Season 02/24小时.1080P.H265.S02E01.strm"
			target := "/media/" + sourceRelative + "\n"
			source := filepath.Join(mediaRoot, filepath.FromSlash(sourceRelative))
			old := filepath.Join(strmRoot, filepath.FromSlash(oldRelative))
			canonical := filepath.Join(strmRoot, filepath.FromSlash(canonicalRelative))
			writeSeriesSyncFile(t, source, "original video bytes\x00\xff")
			writeSeriesSyncFile(t, old, target)

			result, err := service.SyncSTRMWithProgress(context.Background(), scope, func(float64, string) error { return nil })
			if err != nil {
				t.Fatalf("migrate legacy season: %v", err)
			}
			if result.Valid != 1 || result.Missing != 0 || result.Invalid != 0 {
				t.Fatalf("canonical output did not verify in source scope: %+v", result)
			}
			assertSeriesSyncFile(t, canonical, target)
			assertSeriesSyncFile(t, source, "original video bytes\x00\xff")
			if _, err := os.Stat(old); !os.IsNotExist(err) {
				t.Fatalf("old generated mirror remains: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
				t.Fatalf("empty legacy output directory remains: %v", err)
			}
			wantFiles := map[string]string{canonicalRelative: target}
			if got := seriesSyncFiles(t, strmRoot); !reflect.DeepEqual(got, wantFiles) {
				t.Fatalf("unexpected migrated outputs: got %v, want %v", got, wantFiles)
			}

			repeated, err := service.SyncSTRM(context.Background(), scope)
			if err != nil {
				t.Fatalf("repeat season sync: %v", err)
			}
			if repeated.Created != 0 || repeated.Updated != 0 || repeated.Removed != 0 || repeated.RemovedDirectories != 0 {
				t.Fatalf("repeat sync changed outputs: %+v", repeated)
			}
			if got := seriesSyncFiles(t, strmRoot); !reflect.DeepEqual(got, wantFiles) {
				t.Fatalf("repeat sync changed output tree: got %v, want %v", got, wantFiles)
			}
			assertSeriesSyncFile(t, source, "original video bytes\x00\xff")
			verification, err := service.VerifySTRM(context.Background(), scope)
			if err != nil || verification.Valid != 1 || verification.Missing != 0 || verification.Invalid != 0 {
				t.Fatalf("verify canonical season through original source scope: %+v, err=%v", verification, err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatalf("remove source fixture: %v", err)
			}
			verification, err = service.VerifySTRM(context.Background(), scope)
			if err != nil || verification.Valid != 0 || verification.Missing != 1 {
				t.Fatalf("canonical output's missing original target was not reported: %+v, err=%v", verification, err)
			}
		})
	}
}

func TestMediaServicePreservesUnownedOrUnmountedLegacySeasonMirrors(t *testing.T) {
	const sourceRelative = "Series/Example/SE02/Example.SE02.01.mkv"
	const target = "/media/" + sourceRelative + "\n"
	for _, test := range []struct {
		name       string
		canary     bool
		oldContent string
	}{
		{name: "manual target", canary: true, oldContent: "/media/Series/Example/SE02/Alternate.mkv\n"},
		{name: "same target with edited whitespace", canary: true, oldContent: target + "\n"},
		{name: "mount canary absent", oldContent: target},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mediaRoot, strmRoot := newSeriesSyncFixture(t, test.canary)
			source := filepath.Join(mediaRoot, filepath.FromSlash(sourceRelative))
			old := filepath.Join(strmRoot, "Series", "Example", "SE02", "Example.SE02.01.strm")
			canonical := filepath.Join(strmRoot, "Series", "Example", "Season 02", "Example.S02E01.strm")
			writeSeriesSyncFile(t, source, "source video")
			writeSeriesSyncFile(t, old, test.oldContent)
			result, err := service.SyncSTRM(context.Background(), "Series")
			if err != nil {
				t.Fatalf("sync protected legacy mirror: %v", err)
			}
			assertSeriesSyncFile(t, canonical, target)
			assertSeriesSyncFile(t, old, test.oldContent)
			assertSeriesSyncFile(t, source, "source video")
			if result.Removed != 0 || result.RemovedDirectories != 0 {
				t.Fatalf("sync removed protected legacy output: %+v", result)
			}
		})
	}
}

func TestMediaServicePreservesConflictingCanonicalSeasonOutput(t *testing.T) {
	service, mediaRoot, strmRoot := newSeriesSyncFixture(t, true)
	sourceRelative := "Series/Example/SE02/Example.SE02.01.mkv"
	target := "/media/" + sourceRelative + "\n"
	source := filepath.Join(mediaRoot, filepath.FromSlash(sourceRelative))
	old := filepath.Join(strmRoot, "Series", "Example", "SE02", "Example.SE02.01.strm")
	canonical := filepath.Join(strmRoot, "Series", "Example", "Season 02", "Example.S02E01.strm")
	otherTarget := "/media/Series/Example/SE02/Other.mkv\n"
	writeSeriesSyncFile(t, source, "source video")
	writeSeriesSyncFile(t, old, target)
	writeSeriesSyncFile(t, canonical, otherTarget)

	if _, err := service.SyncSTRM(context.Background(), "Series"); err == nil {
		t.Fatal("sync accepted a canonical output owned by a different target")
	}
	assertSeriesSyncFile(t, canonical, otherTarget)
	assertSeriesSyncFile(t, old, target)
	assertSeriesSyncFile(t, source, "source video")
}

func TestMediaServiceRejectsCollidingCanonicalSeasonSources(t *testing.T) {
	service, mediaRoot, strmRoot := newSeriesSyncFixture(t, true)
	sources := []string{
		"Series/Example/SE02/Example.SE02.01.mkv",
		"Series/Example/SE02/Example.S02E01.mkv",
	}
	for _, relative := range sources {
		writeSeriesSyncFile(t, filepath.Join(mediaRoot, filepath.FromSlash(relative)), "video for "+relative)
		mirror := relative[:len(relative)-len(filepath.Ext(relative))] + ".strm"
		writeSeriesSyncFile(t, filepath.Join(strmRoot, filepath.FromSlash(mirror)), "/media/"+relative+"\n")
	}
	if _, err := service.SyncSTRM(context.Background(), "Series"); err == nil {
		t.Fatal("different source videos were accepted for one canonical episode output")
	}
	for _, relative := range sources {
		assertSeriesSyncFile(t, filepath.Join(mediaRoot, filepath.FromSlash(relative)), "video for "+relative)
		mirror := relative[:len(relative)-len(filepath.Ext(relative))] + ".strm"
		assertSeriesSyncFile(t, filepath.Join(strmRoot, filepath.FromSlash(mirror)), "/media/"+relative+"\n")
	}
}

func newSeriesSyncFixture(t *testing.T, canary bool) (*MediaService, string, string) {
	t.Helper()
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	strmRoot := filepath.Join(root, "strm")
	for _, directory := range []string{mediaRoot, strmRoot} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatalf("create media roots: %v", err)
		}
	}
	if canary {
		writeSeriesSyncFile(t, filepath.Join(mediaRoot, ".embymedia-health-canary"), "")
	}
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.SetSettings(map[string]string{"media_root": mediaRoot, "strm_root": strmRoot, "emby_media_prefix": "/media"}); err != nil {
		t.Fatalf("configure media paths: %v", err)
	}
	return NewMediaService(db), mediaRoot, strmRoot
}

func writeSeriesSyncFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func assertSeriesSyncFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("unexpected contents at %s: got %q, want %q, err=%v", path, got, want, err)
	}
}

func seriesSyncFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect output tree: %v", err)
	}
	return files
}

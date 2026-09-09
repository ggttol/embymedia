package service

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/embymedia/embymedia/internal/storage"
)

var videoExtensions = map[string]struct{}{
	".m2ts": {}, ".m4v": {}, ".mkv": {}, ".mov": {}, ".mp4": {}, ".mpeg": {}, ".mpg": {}, ".ts": {}, ".webm": {},
}

// STRMResult reports observable synchronization and validation counts.
type STRMResult struct {
	MediaFiles         int      `json:"media_files"`
	Created            int      `json:"created"`
	Updated            int      `json:"updated"`
	Unchanged          int      `json:"unchanged"`
	Removed            int      `json:"removed"`
	RemovedDirectories int      `json:"removed_directories"`
	Valid              int      `json:"valid"`
	Missing            int      `json:"missing"`
	Invalid            int      `json:"invalid"`
	Examples           []string `json:"examples,omitempty"`
	PruneStatus        string   `json:"prune_status,omitempty"`
}

// MediaService synchronizes STRM files from the configured CloudDrive media tree.
type MediaService struct {
	db *storage.DB
}

// NewMediaService creates a filesystem media service.
func NewMediaService(db *storage.DB) *MediaService {
	return &MediaService{db: db}
}

func cleanRelative(value, label string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(value))
	if clean == "." {
		return "", nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must stay below its configured root", label)
	}
	return clean, nil
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensureReadableDirectory(path string, info fs.FileInfo) error {
	permissions := info.Mode().Perm()
	if permissions&0055 == 0055 {
		return nil
	}
	return os.Chmod(path, permissions|0055)
}

func canonicalRoot(path string, create bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(absolute, 0755); err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	if create {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if err := ensureReadableDirectory(resolved, info); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func (s *MediaService) paths(subpath string) (string, string, string, string, error) {
	mediaSetting, _ := s.db.GetSetting("media_root")
	strmSetting, _ := s.db.GetSetting("strm_root")
	embyPrefix, _ := s.db.GetSetting("emby_media_prefix")
	if mediaSetting == "" || strmSetting == "" {
		return "", "", "", "", fmt.Errorf("media_root and strm_root must be configured")
	}
	if embyPrefix == "" {
		embyPrefix = "/media"
	}
	library, err := cleanRelative(subpath, "library")
	if err != nil {
		return "", "", "", "", err
	}
	mediaRoot, err := canonicalRoot(mediaSetting, false)
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve media_root: %w", err)
	}
	mediaBase, err := filepath.EvalSymlinks(filepath.Join(mediaRoot, library))
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve media library: %w", err)
	}
	if !inside(mediaRoot, mediaBase) {
		return "", "", "", "", fmt.Errorf("media library resolves outside media_root")
	}
	strmRoot, err := canonicalRoot(strmSetting, true)
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve strm_root: %w", err)
	}
	strmBase := filepath.Join(strmRoot, canonicalSTRMDirectory(library))
	if resolved, err := filepath.EvalSymlinks(strmBase); err == nil {
		if !inside(strmRoot, resolved) {
			return "", "", "", "", fmt.Errorf("STRM library resolves outside strm_root")
		}
		strmBase = resolved
	} else if !os.IsNotExist(err) {
		return "", "", "", "", fmt.Errorf("resolve STRM library: %w", err)
	}
	return mediaRoot, mediaBase, strmBase, filepath.ToSlash(strings.TrimRight(embyPrefix, "/")), nil
}

func addExample(result *STRMResult, value string) {
	if len(result.Examples) < 20 {
		result.Examples = append(result.Examples, value)
	}
}

func safeOutputPath(root, relative string) (string, error) {
	clean, err := cleanRelative(relative, "STRM output")
	if err != nil || clean == "" {
		return "", fmt.Errorf("invalid STRM output path")
	}
	parentRelative := filepath.Dir(clean)
	current := root
	if parentRelative != "." {
		for _, segment := range strings.Split(parentRelative, string(filepath.Separator)) {
			current = filepath.Join(current, segment)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				if err := os.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
					return "", err
				}
				info, err = os.Lstat(current)
			}
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("STRM output parent %s is not a real directory", current)
			}
			if err := ensureReadableDirectory(current, info); err != nil {
				return "", err
			}
		}
	}
	output := filepath.Join(root, clean)
	if info, err := os.Lstat(output); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("STRM output %s is a symbolic link", output)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return output, nil
}

func writeAtomic(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".strm-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Chmod(0644); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func pruneStaleSTRM(ctx context.Context, mediaRoot, strmBase, embyPrefix string, expected map[string]struct{}, relocations map[string]strmRelocation, result *STRMResult) error {
	canary, err := os.Stat(filepath.Join(mediaRoot, ".embymedia-health-canary"))
	if err != nil || !canary.Mode().IsRegular() {
		result.PruneStatus = "skipped_no_mount_canary"
		return nil
	}
	result.PruneStatus = "completed"
	emptied := make(map[string]struct{})
	if err := filepath.WalkDir(strmBase, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == strmBase && os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".strm" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, exists := expected[path]; exists {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relocation, moving := relocations[path]
		if moving {
			if !bytes.Equal(content, relocation.contents) {
				return nil
			}
			info, err := os.Lstat(relocation.output)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("canonical STRM %s is not a regular file", relocation.output)
			}
			replacement, err := os.ReadFile(relocation.output)
			if err != nil {
				return err
			}
			if !bytes.Equal(content, replacement) {
				return fmt.Errorf("canonical STRM %s no longer references the original source", relocation.output)
			}
		} else {
			target := strings.TrimSpace(string(content))
			prefix := embyPrefix + "/"
			if !strings.HasPrefix(target, prefix) {
				return nil
			}
			relative, err := cleanRelative(filepath.FromSlash(strings.TrimPrefix(target, prefix)), "STRM target")
			if err != nil || relative == "" {
				return nil
			}
			candidate := filepath.Join(mediaRoot, relative)
			if _, err := filepath.EvalSymlinks(candidate); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		result.Removed++
		for directory := filepath.Dir(path); directory != strmBase && inside(strmBase, directory); directory = filepath.Dir(directory) {
			emptied[directory] = struct{}{}
		}
		if moving && filepath.Dir(path) == strmBase && filepath.Dir(relocation.output) != strmBase {
			emptied[strmBase] = struct{}{}
		}
		return nil
	}); err != nil {
		return err
	}
	directories := make([]string, 0, len(emptied))
	for directory := range emptied {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			continue
		}
		if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
			return err
		}
		result.RemovedDirectories++
	}
	return nil
}

func reportMediaProgress(update func(float64, string) error, progress float64, message string) error {
	if update == nil {
		return nil
	}
	return update(progress, message)
}

const strmWorkerCount = 2

func processConcurrently[T any](ctx context.Context, items []T, process func(context.Context, T) error) error {
	workerCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	jobs := make(chan T, strmWorkerCount)
	var workers sync.WaitGroup
	for range strmWorkerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					if err := process(workerCtx, item); err != nil {
						cancel(err)
						return
					}
				}
			}
		}()
	}
send:
	for _, item := range items {
		select {
		case <-workerCtx.Done():
			break send
		case jobs <- item:
		}
	}
	close(jobs)
	workers.Wait()
	return context.Cause(workerCtx)
}

type strmSyncJob struct {
	outputRelative string
	contents       [][]byte
	normalized     bool
}

type strmRelocation struct {
	output   string
	contents []byte
}

type strmVerifyJob struct {
	path   string
	linked bool
}

type strmTargetState uint8

const (
	strmTargetValid strmTargetState = iota
	strmTargetMissing
	strmTargetInvalid
)

type resolvedDirectory struct {
	path string
	err  error
}

func inspectSTRMTarget(mediaRoot, relative string, directories *sync.Map) (strmTargetState, error) {
	candidate := filepath.Join(mediaRoot, relative)
	parent := filepath.Dir(candidate)
	value, ok := directories.Load(parent)
	if !ok {
		resolved, err := filepath.EvalSymlinks(parent)
		value, _ = directories.LoadOrStore(parent, resolvedDirectory{path: resolved, err: err})
	}
	directory := value.(resolvedDirectory)
	if os.IsNotExist(directory.err) {
		return strmTargetMissing, nil
	}
	if directory.err != nil {
		return strmTargetInvalid, directory.err
	}
	if !inside(mediaRoot, directory.path) {
		return strmTargetInvalid, nil
	}
	leaf := filepath.Join(directory.path, filepath.Base(candidate))
	info, err := os.Lstat(leaf)
	if os.IsNotExist(err) {
		return strmTargetMissing, nil
	}
	if err != nil {
		return strmTargetInvalid, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return strmTargetValid, nil
	}
	resolved, err := filepath.EvalSymlinks(leaf)
	if os.IsNotExist(err) {
		return strmTargetMissing, nil
	}
	if err != nil {
		return strmTargetInvalid, err
	}
	if !inside(mediaRoot, resolved) {
		return strmTargetInvalid, nil
	}
	return strmTargetValid, nil
}

func addConcurrentExample(mu *sync.Mutex, examples *[]string, value string) {
	mu.Lock()
	defer mu.Unlock()
	*examples = append(*examples, value)
	sort.Strings(*examples)
	if len(*examples) > 20 {
		*examples = (*examples)[:20]
	}
}

// SyncSTRM synchronizes a source-relative library, canonicalizes legacy season names, and verifies original media targets.
func (s *MediaService) SyncSTRM(ctx context.Context, library string) (STRMResult, error) {
	return s.SyncSTRMWithProgress(ctx, library, nil)
}

// SyncSTRMWithProgress reports durable progress while synchronizing a source-relative library into canonical STRM paths.
func (s *MediaService) SyncSTRMWithProgress(ctx context.Context, library string, update func(float64, string) error) (STRMResult, error) {
	mediaRoot, mediaBase, strmBase, embyPrefix, err := s.paths(library)
	if err != nil {
		return STRMResult{}, err
	}
	if err := reportMediaProgress(update, 5, "STRM source scan started"); err != nil {
		return STRMResult{}, err
	}
	strmRoot, _ := s.db.GetSetting("strm_root")
	strmRoot, err = canonicalRoot(strmRoot, true)
	if err != nil {
		return STRMResult{}, err
	}
	originalBase := filepath.Join(strmRoot, filepath.Clean(library))
	if !inside(strmRoot, strmBase) || !inside(strmRoot, originalBase) {
		return STRMResult{}, fmt.Errorf("STRM library escapes strm_root")
	}
	result := STRMResult{}
	expected := make(map[string]struct{})
	jobs := make([]strmSyncJob, 0, 1024)
	jobIndex := make(map[string]int)
	var relocations map[string]strmRelocation
	err = filepath.WalkDir(mediaBase, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("media file %s is a symbolic link", path)
		}
		if _, supported := videoExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !supported {
			return nil
		}
		result.MediaFiles++
		if result.MediaFiles%1000 == 0 {
			if err := reportMediaProgress(update, 10, fmt.Sprintf("STRM source scan processed %d media files", result.MediaFiles)); err != nil {
				return err
			}
		}
		relative, err := filepath.Rel(mediaBase, path)
		if err != nil {
			return err
		}
		targetRelative := relative
		if strings.TrimSpace(library) != "" {
			targetRelative = filepath.Join(filepath.Clean(library), relative)
		}
		outputRelative, err := canonicalSTRMPath(targetRelative)
		if err != nil {
			return err
		}
		cleanOutput, err := cleanRelative(outputRelative, "STRM output")
		if err != nil || cleanOutput == "" {
			return fmt.Errorf("invalid STRM output path")
		}
		expected[filepath.Join(strmRoot, cleanOutput)] = struct{}{}
		content := []byte(embyPrefix + "/" + filepath.ToSlash(targetRelative) + "\n")
		originalRelative := strings.TrimSuffix(targetRelative, filepath.Ext(targetRelative)) + ".strm"
		normalized := cleanOutput != originalRelative
		if index, exists := jobIndex[cleanOutput]; exists {
			if normalized || jobs[index].normalized {
				return fmt.Errorf("different media sources map to canonical STRM %s", cleanOutput)
			}
			jobs[index].contents = append(jobs[index].contents, content)
		} else {
			jobIndex[cleanOutput] = len(jobs)
			jobs = append(jobs, strmSyncJob{outputRelative: cleanOutput, contents: [][]byte{content}, normalized: normalized})
		}
		if normalized {
			if relocations == nil {
				relocations = make(map[string]strmRelocation)
			}
			relocations[filepath.Join(strmRoot, originalRelative)] = strmRelocation{output: filepath.Join(strmRoot, cleanOutput), contents: content}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err := reportMediaProgress(update, 30, fmt.Sprintf("STRM source scan found %d media files", result.MediaFiles)); err != nil {
		return result, err
	}
	var created, updated, unchanged, completed atomic.Int64
	var progressMu sync.Mutex
	nextReport := int64(1000)
	err = processConcurrently(ctx, jobs, func(_ context.Context, job strmSyncJob) error {
		output, err := safeOutputPath(strmRoot, job.outputRelative)
		if err != nil {
			return err
		}
		for _, content := range job.contents {
			existing, readErr := os.ReadFile(output)
			if readErr == nil && bytes.Equal(existing, content) {
				unchanged.Add(1)
			} else {
				if readErr != nil && !os.IsNotExist(readErr) {
					return readErr
				}
				if job.normalized && readErr == nil {
					return fmt.Errorf("canonical STRM %s already references a different source", output)
				}
				if err := writeAtomic(output, content); err != nil {
					return err
				}
				if os.IsNotExist(readErr) {
					created.Add(1)
				} else {
					updated.Add(1)
				}
			}
			count := completed.Add(1)
			progressMu.Lock()
			var progressErr error
			if count >= nextReport {
				nextReport = count/1000*1000 + 1000
				progress := 30.0
				if result.MediaFiles > 0 {
					progress += 30 * float64(count) / float64(result.MediaFiles)
				}
				progressErr = reportMediaProgress(update, progress, fmt.Sprintf("STRM output reconciliation processed %d files", count))
			}
			progressMu.Unlock()
			if progressErr != nil {
				return progressErr
			}
		}
		return nil
	})
	result.Created = int(created.Load())
	result.Updated = int(updated.Load())
	result.Unchanged = int(unchanged.Load())
	if err != nil {
		return result, err
	}
	if err := reportMediaProgress(update, 60, fmt.Sprintf("STRM source scan completed with %d media files", result.MediaFiles)); err != nil {
		return result, err
	}
	if err := pruneStaleSTRM(ctx, mediaRoot, originalBase, embyPrefix, expected, relocations, &result); err != nil {
		return result, err
	}
	if originalBase != strmBase {
		if err := pruneStaleSTRM(ctx, mediaRoot, strmBase, embyPrefix, expected, relocations, &result); err != nil {
			return result, err
		}
	}
	if err := reportMediaProgress(update, 70, fmt.Sprintf("STRM stale reconciliation removed %d files and %d empty directories with status %s", result.Removed, result.RemovedDirectories, result.PruneStatus)); err != nil {

		return result, err
	}
	verification, err := s.VerifySTRMWithProgress(ctx, library, func(progress float64, message string) error {
		return reportMediaProgress(update, 75+progress*0.24, message)
	})
	if err != nil {
		return result, err
	}
	result.Valid = verification.Valid
	result.Missing = verification.Missing
	result.Invalid = verification.Invalid
	result.Examples = verification.Examples
	if err := reportMediaProgress(update, 100, "STRM synchronization and verification completed"); err != nil {
		return result, err
	}
	return result, nil
}

// VerifySTRM validates generated targets for a source-relative library without changing files.
func (s *MediaService) VerifySTRM(ctx context.Context, library string) (STRMResult, error) {
	return s.VerifySTRMWithProgress(ctx, library, nil)
}

// VerifySTRMWithProgress reports processed-item updates while validating the generated targets of a source-relative library.
func (s *MediaService) VerifySTRMWithProgress(ctx context.Context, library string, update func(float64, string) error) (STRMResult, error) {
	mediaRoot, _, strmBase, embyPrefix, err := s.paths(library)
	if err != nil {
		return STRMResult{}, err
	}
	if err := reportMediaProgress(update, 0, "STRM verification started"); err != nil {
		return STRMResult{}, err
	}
	jobs := make([]strmVerifyJob, 0, 1024)
	err = filepath.WalkDir(strmBase, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".strm" {
			return nil
		}
		jobs = append(jobs, strmVerifyJob{path: path, linked: entry.Type()&os.ModeSymlink != 0})
		return nil
	})
	if err != nil {
		return STRMResult{}, err
	}
	if err := reportMediaProgress(update, 5, fmt.Sprintf("STRM verification found %d files", len(jobs))); err != nil {
		return STRMResult{}, err
	}
	groups := make([][]strmVerifyJob, 0, 256)
	groupIndex := make(map[string]int)
	for _, job := range jobs {
		directory := filepath.Dir(job.path)
		if index, exists := groupIndex[directory]; exists {
			groups[index] = append(groups[index], job)
		} else {
			groupIndex[directory] = len(groups)
			groups = append(groups, []strmVerifyJob{job})
		}
	}
	var valid, missing, invalid, completed atomic.Int64
	var examplesMu, progressMu sync.Mutex
	examples := make([]string, 0, 20)
	var directories sync.Map
	nextReport := int64(2000)
	err = processConcurrently(ctx, groups, func(_ context.Context, group []strmVerifyJob) error {
		for _, job := range group {
			if job.linked {
				invalid.Add(1)
				addConcurrentExample(&examplesMu, &examples, job.path+": symbolic link is not allowed")
			} else {
				content, err := os.ReadFile(job.path)
				if err != nil {
					return err
				}
				target := strings.TrimSpace(string(content))
				prefix := embyPrefix + "/"
				if !strings.HasPrefix(target, prefix) {
					invalid.Add(1)
					addConcurrentExample(&examplesMu, &examples, job.path+": invalid target")
				} else {
					relative, err := cleanRelative(filepath.FromSlash(strings.TrimPrefix(target, prefix)), "STRM target")
					if err != nil || relative == "" {
						invalid.Add(1)
						addConcurrentExample(&examplesMu, &examples, job.path+": target escapes media_root")
					} else {
						state, err := inspectSTRMTarget(mediaRoot, relative, &directories)
						switch {
						case err != nil:
							return err
						case state == strmTargetMissing:
							missing.Add(1)
							addConcurrentExample(&examplesMu, &examples, job.path+": missing "+target)
						case state == strmTargetInvalid:
							invalid.Add(1)
							addConcurrentExample(&examplesMu, &examples, job.path+": target resolves outside media_root")
						default:
							valid.Add(1)
						}
					}
				}
			}
			count := completed.Add(1)
			progressMu.Lock()
			var progressErr error
			if count >= nextReport {
				nextReport = count/2000*2000 + 2000
				progress := 5.0
				if len(jobs) > 0 {
					progress += 94 * float64(count) / float64(len(jobs))
				}
				progressErr = reportMediaProgress(update, progress, fmt.Sprintf("STRM verification checked %d files", count))
			}
			progressMu.Unlock()
			if progressErr != nil {
				return progressErr
			}
		}
		return nil
	})
	result := STRMResult{Valid: int(valid.Load()), Missing: int(missing.Load()), Invalid: int(invalid.Load()), Examples: examples}
	if err != nil {
		return result, err
	}
	err = reportMediaProgress(update, 100, fmt.Sprintf("STRM verification completed after %d files", len(jobs)))
	return result, err
}

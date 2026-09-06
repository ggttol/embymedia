package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/embymedia/embymedia/internal/storage"
)

var videoExtensions = map[string]struct{}{
	".m2ts": {}, ".m4v": {}, ".mkv": {}, ".mov": {}, ".mp4": {}, ".mpeg": {}, ".mpg": {}, ".ts": {}, ".webm": {},
}

// STRMResult reports observable synchronization and validation counts.
type STRMResult struct {
	MediaFiles  int      `json:"media_files"`
	Created     int      `json:"created"`
	Updated     int      `json:"updated"`
	Unchanged   int      `json:"unchanged"`
	Removed     int      `json:"removed"`
	Valid       int      `json:"valid"`
	Missing     int      `json:"missing"`
	Invalid     int      `json:"invalid"`
	Examples    []string `json:"examples,omitempty"`
	PruneStatus string   `json:"prune_status,omitempty"`
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
	strmBase := filepath.Join(strmRoot, library)
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

func pruneStaleSTRM(ctx context.Context, mediaRoot, strmBase, embyPrefix string, expected map[string]struct{}, result *STRMResult) error {
	canary, err := os.Stat(filepath.Join(mediaRoot, ".embymedia-health-canary"))
	if err != nil || !canary.Mode().IsRegular() {
		result.PruneStatus = "skipped_no_mount_canary"
		return nil
	}
	result.PruneStatus = "completed"
	return filepath.WalkDir(strmBase, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
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
		if err := os.Remove(path); err != nil {
			return err
		}
		result.Removed++
		return nil
	})
}

func reportMediaProgress(update func(float64, string) error, progress float64, message string) error {
	if update == nil {
		return nil
	}
	return update(progress, message)
}

// SyncSTRM creates or updates one STRM file for each supported video file and then verifies the result.
func (s *MediaService) SyncSTRM(ctx context.Context, library string) (STRMResult, error) {
	return s.SyncSTRMWithProgress(ctx, library, nil)
}

// SyncSTRMWithProgress reports durable phase and processed-item updates while synchronizing.
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
	libraryRelative, err := filepath.Rel(strmRoot, strmBase)
	if err != nil || !inside(strmRoot, strmBase) {
		return STRMResult{}, fmt.Errorf("STRM library escapes strm_root")
	}
	result := STRMResult{}
	expected := make(map[string]struct{})
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
		outputRelative := filepath.Join(libraryRelative, strings.TrimSuffix(relative, filepath.Ext(relative))+".strm")
		output, err := safeOutputPath(strmRoot, outputRelative)
		if err != nil {
			return err
		}
		expected[output] = struct{}{}
		targetRelative := relative
		if strings.TrimSpace(library) != "" {
			targetRelative = filepath.Join(filepath.Clean(library), relative)
		}
		content := []byte(embyPrefix + "/" + filepath.ToSlash(targetRelative) + "\n")
		existing, readErr := os.ReadFile(output)
		if readErr == nil && string(existing) == string(content) {
			result.Unchanged++
			return nil
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if err := writeAtomic(output, content); err != nil {
			return err
		}
		if os.IsNotExist(readErr) {
			result.Created++
		} else {
			result.Updated++
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err := reportMediaProgress(update, 60, fmt.Sprintf("STRM source scan completed with %d media files", result.MediaFiles)); err != nil {
		return result, err
	}
	if err := pruneStaleSTRM(ctx, mediaRoot, strmBase, embyPrefix, expected, &result); err != nil {
		return result, err
	}
	if err := reportMediaProgress(update, 70, fmt.Sprintf("STRM stale reconciliation removed %d files with status %s", result.Removed, result.PruneStatus)); err != nil {
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

// VerifySTRM validates STRM targets against the configured media tree without changing files.
func (s *MediaService) VerifySTRM(ctx context.Context, library string) (STRMResult, error) {
	return s.VerifySTRMWithProgress(ctx, library, nil)
}

// VerifySTRMWithProgress reports processed-item updates while validating every STRM target.
func (s *MediaService) VerifySTRMWithProgress(ctx context.Context, library string, update func(float64, string) error) (STRMResult, error) {
	mediaRoot, _, strmBase, embyPrefix, err := s.paths(library)
	if err != nil {
		return STRMResult{}, err
	}
	if err := reportMediaProgress(update, 0, "STRM verification started"); err != nil {
		return STRMResult{}, err
	}
	result := STRMResult{}
	checked := 0
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
		checked++
		if checked%2000 == 0 {
			if err := reportMediaProgress(update, 0, fmt.Sprintf("STRM verification checked %d files", checked)); err != nil {
				return err
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			result.Invalid++
			addExample(&result, path+": symbolic link is not allowed")
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := strings.TrimSpace(string(content))
		prefix := embyPrefix + "/"
		if !strings.HasPrefix(target, prefix) {
			result.Invalid++
			addExample(&result, path+": invalid target")
			return nil
		}
		relative, err := cleanRelative(filepath.FromSlash(strings.TrimPrefix(target, prefix)), "STRM target")
		if err != nil || relative == "" {
			result.Invalid++
			addExample(&result, path+": target escapes media_root")
			return nil
		}
		candidate := filepath.Join(mediaRoot, relative)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				result.Missing++
				addExample(&result, path+": missing "+target)
				return nil
			}
			return err
		}
		if !inside(mediaRoot, resolved) {
			result.Invalid++
			addExample(&result, path+": target resolves outside media_root")
			return nil
		}
		result.Valid++
		return nil
	})
	if err == nil {
		err = reportMediaProgress(update, 100, fmt.Sprintf("STRM verification completed after %d files", checked))
	}
	return result, err
}

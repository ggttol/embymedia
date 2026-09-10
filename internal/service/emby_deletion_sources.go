package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/embymedia/embymedia/internal/domain"
)

// EmbyDeletionSource contains non-secret coordinates and the observed recycle outcome.
type EmbyDeletionSource struct {
	Path          string `json:"path"`
	FileID        string `json:"file_id"`
	ParentID      string `json:"parent_id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	SHA1          string `json:"sha1,omitempty"`
	Recycled      bool   `json:"recycled"`
	AlreadyAbsent bool   `json:"already_absent"`
}

// EmbyDeletionPlan is a fully preflighted snapshot. SourcePaths are Emby-visible
// originals for confirmation; Sources are audit details, not deletion inputs.
type EmbyDeletionPlan struct {
	SourcePaths []string             `json:"source_paths"`
	AccountID   string               `json:"account_id"`
	Sources     []EmbyDeletionSource `json:"sources"`
	owner       *TaskQueueService
	accountID   string
	targets     []embyDeletionTarget
	attempted   bool
}

type embyDeletionTarget struct {
	file   domain.DriveFile
	path   string
	native bool
}

type embyDeletionLibrary struct {
	visible   string
	host      string
	mediaRoot string
	mediaBase string
	prefix    string
	cid       string
	native    bool
}

// PrepareEmbyDeletionCtx resolves only exact native DeleteInfo paths. It refuses
// unmanaged paths, symbolic links, ambiguous identities and incomplete listings.
// The caller must serialize prepare, native deletion and recycle with media mutations.
func (s *TaskQueueService) PrepareEmbyDeletionCtx(ctx context.Context, paths []string) (*EmbyDeletionPlan, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("Emby deletion has no filesystem paths")
	}
	libraries, err := s.embyDeletionLibraries(ctx)
	if err != nil {
		return nil, err
	}
	account, err := s.driveSvc.GetDefaultAccount()
	if err != nil {
		return nil, err
	}
	if account.ID == "" || account.Type != "115" || strings.TrimSpace(account.Cookie) == "" {
		return nil, fmt.Errorf("Emby source deletion requires a configured default 115 account")
	}
	plan := &EmbyDeletionPlan{owner: s, accountID: account.ID, AccountID: account.ID}
	inventories := make(map[string][]domain.DriveFile)
	byPath := make(map[string]int)
	byID := make(map[string]string)
	for _, input := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		visible, err := embyDeletionAbsolute(input)
		if err != nil {
			return nil, err
		}
		var selected *embyDeletionLibrary
		for index := range libraries {
			library := &libraries[index]
			if visible == library.visible {
				return nil, fmt.Errorf("refusing Emby library-root deletion: %s", visible)
			}
			if inside(library.visible, visible) {
				if selected != nil {
					return nil, fmt.Errorf("ambiguous managed library for %s", visible)
				}
				selected = library
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("Emby deletion path is outside managed libraries: %s", visible)
		}
		relative, err := filepath.Rel(selected.visible, visible)
		if err != nil {
			return nil, err
		}
		host := filepath.Join(selected.host, relative)
		if _, err := embyDeletionLstat(selected.host, host); err != nil {
			return nil, err
		}
		err = filepath.WalkDir(host, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("Emby deletion refuses symbolic link %s", path)
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Emby deletion refuses non-regular file %s", path)
			}
			extension := strings.ToLower(filepath.Ext(path))
			_, video := videoExtensions[extension]
			if extension != ".strm" && !video {
				return nil
			}
			var mounted, sourcePath string
			native := selected.native && video
			if extension == ".strm" {
				if selected.native {
					return fmt.Errorf("STRM pointer in native media library is unsupported: %s", path)
				}
				sourcePath, err = embyDeletionSTRM(path)
				if err != nil {
					return err
				}
				if !inside(selected.prefix, sourcePath) || sourcePath == selected.prefix {
					return fmt.Errorf("STRM target is outside emby_media_prefix: %s", path)
				}
				relative, err := filepath.Rel(selected.prefix, sourcePath)
				if err != nil {
					return err
				}
				mounted = filepath.Join(selected.mediaRoot, relative)
				if !inside(selected.mediaBase, mounted) || mounted == selected.mediaBase {
					return fmt.Errorf("STRM target is outside its managed source library: %s", path)
				}
				if _, video := videoExtensions[strings.ToLower(filepath.Ext(mounted))]; !video {
					return fmt.Errorf("STRM target is not a managed video: %s", path)
				}
			} else {
				if !selected.native {
					return fmt.Errorf("native video in STRM library is unsupported: %s", path)
				}
				mounted = path
				relative, err := filepath.Rel(selected.mediaRoot, mounted)
				if err != nil {
					return err
				}
				sourcePath = filepath.Join(selected.prefix, relative)
			}
			if index, exists := byPath[sourcePath]; exists {
				plan.targets[index].native = plan.targets[index].native || native
				return nil
			}
			mountedInfo, err := embyDeletionLstat(selected.mediaRoot, mounted)
			if err != nil {
				return err
			}
			if !mountedInfo.Mode().IsRegular() {
				return fmt.Errorf("original source is not a regular file: %s", sourcePath)
			}
			file, err := s.resolveEmbyDeletionSource(ctx, account.ID, *selected, mounted, inventories)
			if err != nil {
				return err
			}
			if file.Size != mountedInfo.Size() {
				return fmt.Errorf("mounted and 115 sizes differ for %s (file ID %s)", sourcePath, file.FileID)
			}
			if previous, duplicate := byID[file.FileID]; duplicate && previous != sourcePath {
				return fmt.Errorf("115 file ID %s resolves to multiple source paths", file.FileID)
			}
			byID[file.FileID] = sourcePath
			byPath[sourcePath] = len(plan.targets)
			plan.targets = append(plan.targets, embyDeletionTarget{file: file, path: sourcePath, native: native})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(plan.targets) == 0 {
		return nil, fmt.Errorf("Emby deletion contains no managed original videos")
	}
	sort.Slice(plan.targets, func(i, j int) bool { return plan.targets[i].path < plan.targets[j].path })
	for _, target := range plan.targets {
		plan.SourcePaths = append(plan.SourcePaths, target.path)
		plan.Sources = append(plan.Sources, EmbyDeletionSource{Path: target.path, FileID: target.file.FileID, ParentID: target.file.ParentID, Name: target.file.Name, Size: target.file.Size, SHA1: target.file.Sha1})
	}
	return plan, nil
}

func (s *TaskQueueService) embyDeletionLibraries(ctx context.Context) ([]embyDeletionLibrary, error) {
	for _, key := range []string{"media_root", "strm_root"} {
		root, err := s.db.GetSetting(key)
		if err != nil {
			return nil, err
		}
		if _, err := embyDeletionAbsolute(root); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", key, err)
		}
		info, err := os.Lstat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s must be a real directory, not a symbolic link", key)
		}
	}
	raw, err := s.db.GetSetting("c115_cid_map")
	if err != nil {
		return nil, err
	}
	var cidMap map[string]string
	if err := json.Unmarshal([]byte(raw), &cidMap); err != nil {
		return nil, fmt.Errorf("invalid c115_cid_map: %w", err)
	}
	libraries, err := s.embySvc.ListLibrariesCtx(ctx)
	if err != nil {
		return nil, err
	}
	var result []embyDeletionLibrary
	for _, library := range libraries {
		cid := strings.TrimSpace(cidMap[library.Name])
		if cid == "" {
			continue
		}
		name, err := cleanRelative(library.Name, "managed library")
		if err != nil || name == "" || name != library.Name {
			return nil, fmt.Errorf("invalid managed library name %q", library.Name)
		}
		mediaRoot, mediaBase, strmBase, prefix, err := s.mediaSvc.paths(name)
		if err != nil {
			return nil, err
		}
		if _, err := embyDeletionLstat(mediaRoot, filepath.Join(mediaRoot, name)); err != nil {
			return nil, err
		}
		strmSetting, err := s.db.GetSetting("strm_root")
		if err != nil {
			return nil, err
		}
		strmRoot, err := canonicalRoot(strmSetting, false)
		if err != nil {
			return nil, err
		}
		if _, err := embyDeletionAbsolute(prefix); err != nil {
			return nil, fmt.Errorf("invalid emby_media_prefix: %w", err)
		}
		for _, location := range library.Locations {
			visible, err := embyDeletionAbsolute(location)
			if err != nil {
				return nil, err
			}
			mapping := embyDeletionLibrary{visible: visible, mediaRoot: mediaRoot, mediaBase: mediaBase, prefix: prefix, cid: cid}
			switch visible {
			case filepath.Join(prefix, name), mediaBase:
				mapping.host, mapping.native = mediaBase, true
			case filepath.Join("/strm-v2", canonicalSTRMDirectory(name)), strmBase:
				if _, err := embyDeletionLstat(strmRoot, filepath.Join(strmRoot, canonicalSTRMDirectory(name))); err != nil {
					return nil, err
				}
				mapping.host = strmBase
			default:
				return nil, fmt.Errorf("managed Emby library location has no exact media/STRM mapping: %s", visible)
			}
			for _, existing := range result {
				if inside(existing.visible, mapping.visible) || inside(mapping.visible, existing.visible) {
					return nil, fmt.Errorf("managed Emby library locations overlap: %s and %s", existing.visible, mapping.visible)
				}
			}
			result = append(result, mapping)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no Emby libraries have configured 115 source mappings")
	}
	return result, nil
}

func embyDeletionAbsolute(value string) (string, error) {
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("Emby deletion requires a canonical absolute path")
	}
	return value, nil
}

func embyDeletionLstat(root, path string) (os.FileInfo, error) {
	if !inside(root, path) {
		return nil, fmt.Errorf("deletion path is outside managed root: %s", path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	current := root
	info, err := os.Lstat(current)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("deletion refuses symbolic link %s", current)
	}
	if relative == "." {
		return info, nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("deletion refuses symbolic link %s", current)
		}
	}
	return info, nil
}

func embyDeletionSTRM(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("STRM pointer is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, opened) {
		return "", fmt.Errorf("STRM pointer changed during preflight: %s", path)
	}
	body, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return "", err
	}
	if len(body) > 64*1024 {
		return "", fmt.Errorf("STRM pointer is too large: %s", path)
	}
	var target string
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if target != "" && target != line {
			return "", fmt.Errorf("STRM contains ambiguous multiple targets: %s", path)
		}
		target = line
	}
	canonical, err := embyDeletionAbsolute(target)
	if err != nil {
		return "", fmt.Errorf("STRM must contain one absolute managed video path: %s", path)
	}
	return canonical, nil
}

func (s *TaskQueueService) embyDeletionInventory(ctx context.Context, accountID, cid string) ([]domain.DriveFile, error) {
	var files []domain.DriveFile
	seenIDs, seenNames := make(map[string]struct{}), make(map[string]struct{})
	var expected int64 = -1
	for offset := 0; ; {
		page, total, err := s.driveSvc.ListFilesPageCtx(ctx, accountID, cid, offset, 1000)
		if err != nil {
			return nil, err
		}
		if total < 0 || (expected >= 0 && total != expected) || total < int64(offset+len(page)) || (len(page) == 0 && int64(offset) < total) {
			return nil, fmt.Errorf("incomplete or changing 115 directory inventory for CID %s", cid)
		}
		expected = total
		for _, file := range page {
			_, duplicateID := seenIDs[file.FileID]
			_, duplicateName := seenNames[file.Name]
			if file.FileID == "" || file.FileID == "0" || file.FileID == "<nil>" || file.ParentID != cid || file.Name == "" || file.Name == "." || file.Name == ".." || strings.ContainsAny(file.Name, "/\\\x00") || file.Size < 0 || duplicateID || duplicateName {
				return nil, fmt.Errorf("ambiguous or invalid 115 identity in parent CID %s", cid)
			}
			seenIDs[file.FileID], seenNames[file.Name] = struct{}{}, struct{}{}
			files = append(files, file)
		}
		offset += len(page)
		if int64(offset) == total {
			return files, nil
		}
	}
}

func (s *TaskQueueService) resolveEmbyDeletionSource(ctx context.Context, accountID string, library embyDeletionLibrary, mounted string, inventories map[string][]domain.DriveFile) (domain.DriveFile, error) {
	relative, err := filepath.Rel(library.mediaBase, mounted)
	if err != nil {
		return domain.DriveFile{}, err
	}
	parts := strings.Split(relative, string(filepath.Separator))
	cid := library.cid
	for index, part := range parts {
		files, cached := inventories[cid]
		if !cached {
			files, err = s.embyDeletionInventory(ctx, accountID, cid)
			if err != nil {
				return domain.DriveFile{}, err
			}
			inventories[cid] = files
		}
		var match *domain.DriveFile
		for i := range files {
			if files[i].Name == part {
				match = &files[i]
				break
			}
		}
		if match == nil {
			return domain.DriveFile{}, fmt.Errorf("115 source component %q is absent from parent CID %s", part, cid)
		}
		last := index == len(parts)-1
		if match.IsFolder == last {
			return domain.DriveFile{}, fmt.Errorf("115 source component has the wrong file type: %q", part)
		}
		if last {
			return *match, nil
		}
		cid = match.FileID
	}
	return domain.DriveFile{}, fmt.Errorf("original source path has no video component")
}

// RecycleEmbyDeletionCtx must run only after successful native Emby deletion.
// It revalidates recorded IDs immediately before recycling; it never substitutes
// same-named replacements. A failure may follow native deletion or earlier recycles.
func (s *TaskQueueService) RecycleEmbyDeletionCtx(ctx context.Context, plan *EmbyDeletionPlan) error {
	if plan == nil || plan.owner != s || plan.attempted || len(plan.targets) == 0 {
		return fmt.Errorf("invalid or already attempted Emby deletion plan")
	}
	plan.attempted = true
	for index, target := range plan.targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		files, err := s.embyDeletionInventory(ctx, plan.accountID, target.file.ParentID)
		if err != nil {
			return fmt.Errorf("revalidate source %s (file ID %s): %w", target.path, target.file.FileID, err)
		}
		var current *domain.DriveFile
		for i := range files {
			if files[i].FileID == target.file.FileID {
				current = &files[i]
				break
			}
		}
		if current == nil {
			if !target.native {
				return fmt.Errorf("preflighted source ID %s disappeared before recycling %s", target.file.FileID, target.path)
			}
			plan.Sources[index].AlreadyAbsent = true
			continue
		}
		if current.IsFolder || current.Name != target.file.Name || current.ParentID != target.file.ParentID || current.Size != target.file.Size || !strings.EqualFold(current.Sha1, target.file.Sha1) {
			return fmt.Errorf("source identity changed for %s (file ID %s)", target.path, target.file.FileID)
		}
		if err := s.driveSvc.DeleteCtx(ctx, plan.accountID, []string{target.file.FileID}); err != nil {
			return fmt.Errorf("recycle source %s (parent CID %s, file ID %s): %w", target.path, target.file.ParentID, target.file.FileID, err)
		}
		remaining, err := s.embyDeletionInventory(ctx, plan.accountID, target.file.ParentID)
		if err != nil {
			return fmt.Errorf("verify recycled source ID %s: %w", target.file.FileID, err)
		}
		for _, file := range remaining {
			if file.FileID == target.file.FileID {
				return fmt.Errorf("115 still lists source ID %s after recycling", target.file.FileID)
			}
		}
		plan.Sources[index].Recycled = true
	}
	return nil
}

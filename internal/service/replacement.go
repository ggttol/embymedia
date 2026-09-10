package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/google/uuid"
)

type completedPackReplacement struct {
	LibraryID         string
	LibraryName       string
	LibraryCID        string
	AccountID         string
	OldSeriesID       string
	OldFolder         string
	OldCID            string
	OldPath           string
	NewFolder         string
	NewCID            string
	TMDBID            string
	Expected          map[episodeKey]struct{} `json:"-"`
	MediaFiles        map[string]int64
	MediaBase         string
	STRMBase          string
	NewSTRMInfo       os.FileInfo `json:"-"`
	CanonicalSTRMInfo os.FileInfo `json:"-"`
	BackupDir         string
	QuarantineCID     string
	States            []replacementUserState
	DestinationStates []replacementUserState
	CutoverStarted    bool
	Finalized         bool
	RolledBack        bool
	RollbackScanned   bool
}

func episodeSet(episodes []EmbyMissingEpisode) map[episodeKey]struct{} {
	result := make(map[episodeKey]struct{}, len(episodes))
	for _, episode := range episodes {
		result[episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}] = struct{}{}
	}
	return result
}

func coversEpisodes(leaves []autoFillLeaf, expected map[episodeKey]struct{}) bool {
	covered := make(map[episodeKey]struct{}, len(expected))
	for _, leaf := range leaves {
		for _, key := range episodeKeysFromName(leaf.Name) {
			if _, needed := expected[key]; needed {
				covered[key] = struct{}{}
			}
		}
	}
	return len(covered) == len(expected)
}

func waitForAutoFillCoverage(ctx context.Context, root string, expected map[episodeKey]struct{}) error {
	deadline := time.Now().Add(autoFillMountWait)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		covered := make(map[episodeKey]struct{}, len(expected))
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("completed-pack path %s is a symbolic link", path)
			}
			if entry.IsDir() {
				return nil
			}
			if _, video := videoExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !video {
				return nil
			}
			for _, key := range episodeKeysFromName(entry.Name()) {
				if _, needed := expected[key]; needed {
					covered[key] = struct{}{}
				}
			}
			return nil
		})
		if walkErr == nil && len(covered) == len(expected) {
			return nil
		}
		if walkErr != nil && !os.IsNotExist(walkErr) {
			return walkErr
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("completed pack did not become fully visible through CloudDrive within %s", autoFillMountWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *TaskQueueService) replacementFolders(ctx context.Context, accountID, parentCID string) (map[string]domain.DriveFile, error) {
	files := make(map[string]domain.DriveFile)
	for offset := 0; ; {
		page, total, err := s.driveSvc.ListFilesPageCtx(ctx, accountID, parentCID, offset, 1000)
		if err != nil {
			return nil, err
		}
		if total < int64(offset+len(page)) || (int64(offset) < total && len(page) == 0) {
			return nil, fmt.Errorf("incomplete 115 replacement directory inventory")
		}
		for _, file := range page {
			if file.FileID == "" {
				return nil, fmt.Errorf("115 replacement inventory has an empty object ID")
			}
			if _, duplicate := files[file.FileID]; duplicate || file.ParentID != parentCID {
				return nil, fmt.Errorf("115 replacement inventory has duplicate or foreign-parent objects")
			}
			files[file.FileID] = file
		}
		offset += len(page)
		if int64(offset) == total {
			return files, nil
		}
	}
}

func (s *TaskQueueService) createReplacementFolder(ctx context.Context, accountID, parentCID, name string) (string, error) {
	before, err := s.replacementFolders(ctx, accountID, parentCID)
	if err != nil {
		return "", err
	}
	for _, file := range before {
		if file.Name == name {
			return "", fmt.Errorf("replacement destination %q already exists", name)
		}
	}
	cid, err := s.driveSvc.MkdirCtx(ctx, accountID, parentCID, name)
	if err != nil {
		return "", err
	}
	if _, existed := before[cid]; existed {
		return "", fmt.Errorf("115 mkdir returned a preexisting object; refusing ownership")
	}
	after, err := s.replacementFolders(ctx, accountID, parentCID)
	if err != nil {
		return "", fmt.Errorf("new 115 replacement CID %s requires ownership verification: %w", cid, err)
	}
	if !replacementFolderIs(after, cid, name) {
		return "", fmt.Errorf("new 115 replacement CID %s is not bound to destination %q", cid, name)
	}
	return cid, nil
}

func (s *TaskQueueService) stageCompletedPack(ctx context.Context, library domain.EmbyLibrary, libraryCID string, gaps map[episodeKey]struct{}, series *domain.EmbyMediaItem, candidates []autoFillCandidate) (*completedPackReplacement, []string, error) {
	if series == nil || series.Type != "Series" || strings.TrimSpace(series.ProviderIDs["Tmdb"]) == "" {
		return nil, nil, fmt.Errorf("completed-pack replacement requires an exact TMDB Series")
	}
	mediaRoot, mediaBase, strmBase, _, err := s.mediaSvc.paths(library.Name)
	if err != nil {
		return nil, nil, err
	}
	oldFolder := filepath.Base(filepath.Clean(series.Path))
	if oldFolder == "." || oldFolder == ".." || filepath.Base(filepath.Dir(series.Path)) != library.Name {
		return nil, nil, fmt.Errorf("completed-pack Series is not a direct library child")
	}
	oldCID, err := s.resolveAutoFillFolder(ctx, libraryCID, oldFolder)
	if err != nil {
		return nil, nil, err
	}
	_, expected, err := s.embySvc.replacementItems(ctx, series.ID, series.Path)
	if err != nil {
		return nil, nil, err
	}
	for key := range gaps {
		expected[key] = struct{}{}
	}
	if len(expected) == 0 {
		return nil, nil, fmt.Errorf("completed-pack replacement has no episode inventory")
	}
	account, err := s.driveSvc.getAccount("")
	if err != nil {
		return nil, nil, err
	}
	var lastInspectionErr error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		shareCode, receiveCode, err := ParseShareCode(candidate.URL, candidate.Password)
		if err != nil {
			continue
		}
		shareTitle, roots, err := s.driveSvc.snapshotShareEntries(ctx, account, shareCode, receiveCode, "0")
		if err != nil {
			if stopAutoFillShareProbes(err) {
				return nil, nil, err
			}
			lastInspectionErr = err
			continue
		}
		if len(roots) != 1 || !roots[0].IsDir {
			continue
		}
		root := roots[0]
		if !autoFillIdentityMatches(series, candidate.Title, shareTitle, root.Name) || !autoFillIdentityMatches(series, root.Name) {
			continue
		}
		_, leaves, err := s.scanAutoFillShare(ctx, candidate)
		if err != nil {
			if stopAutoFillShareProbes(err) {
				return nil, nil, err
			}
			lastInspectionErr = err
			continue
		}
		if !coversEpisodes(leaves, expected) {
			continue
		}
		valid := true
		for _, leaf := range leaves {
			if !autoFillLeafIdentityMatches(series, leaf) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		_, contents, err := s.driveSvc.snapshotShareEntries(ctx, account, shareCode, receiveCode, root.ID)
		if err != nil {
			if stopAutoFillShareProbes(err) {
				return nil, nil, err
			}
			lastInspectionErr = err
			continue
		}
		if len(contents) == 0 {
			continue
		}
		ids := make([]string, 0, len(contents))
		for _, entry := range contents {
			ids = append(ids, entry.ID)
		}
		newFolder := oldFolder + " [replacement-" + uuid.NewString() + "]"
		newSTRM := filepath.Join(strmBase, newFolder)
		if err := os.Mkdir(newSTRM, 0775); err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(newSTRM)
		if err != nil {
			return nil, nil, err
		}
		replacement := &completedPackReplacement{
			LibraryID: library.ID, LibraryName: library.Name, LibraryCID: libraryCID, AccountID: account.ID,
			OldSeriesID: series.ID, OldFolder: oldFolder, OldCID: oldCID, OldPath: series.Path,
			NewFolder: newFolder, TMDBID: strings.TrimSpace(series.ProviderIDs["Tmdb"]), Expected: expected,
			MediaBase: mediaBase, STRMBase: strmBase, NewSTRMInfo: info,
		}
		fail := func(cause error) (*completedPackReplacement, []string, error) {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			return nil, nil, errors.Join(cause, s.rollbackCompletedPack(cleanupCtx, replacement))
		}
		newPath := filepath.Join(mediaBase, newFolder)
		if !inside(mediaRoot, newPath) {
			return fail(fmt.Errorf("completed-pack destination escapes media_root"))
		}
		if _, err := os.Lstat(newPath); err == nil || !os.IsNotExist(err) {
			return fail(fmt.Errorf("completed-pack media destination already exists or is unreadable"))
		}
		replacement.NewCID, err = s.createReplacementFolder(ctx, account.ID, libraryCID, newFolder)
		if err != nil {
			return fail(err)
		}
		if _, _, err := s.driveSvc.receiveShareEntriesCtx(ctx, account, shareCode, receiveCode, ids, replacement.NewCID, shareTitle); err != nil {
			return fail(err)
		}
		if err := waitForAutoFillCoverage(ctx, newPath, expected); err != nil {
			return fail(err)
		}
		replacement.MediaFiles, err = replacementMediaFiles(newPath)
		if err != nil {
			return fail(err)
		}
		return replacement, []string{newPath}, nil
	}
	return nil, nil, lastInspectionErr
}

func removeOwnedReplacementRoot(path string, owned os.FileInfo) error {
	if owned == nil {
		return nil
	}
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(owned, current) {
		return fmt.Errorf("replacement STRM root %s no longer belongs to this operation", path)
	}
	return os.RemoveAll(path)
}

func (s *TaskQueueService) verifyReplacementSeries(ctx context.Context, replacement *completedPackReplacement, staged bool) (string, map[string]string, error) {
	inventory, err := s.embySvc.ListSeriesCtx(ctx, replacement.LibraryID)
	if err != nil {
		return "", nil, err
	}
	expectedPath := replacement.OldPath
	expectedCount := 1
	if staged {
		expectedPath = filepath.Join(filepath.Dir(replacement.OldPath), replacement.NewFolder)
		expectedCount = 2
	}
	matches := 0
	oldBound := false
	var found *domain.EmbyMediaItem
	for index := range inventory {
		series := &inventory[index]
		if series.ID == replacement.OldSeriesID {
			oldBound = series.Type == "Series" && series.Path == replacement.OldPath && strings.TrimSpace(series.ProviderIDs["Tmdb"]) == replacement.TMDBID
		}
		if strings.TrimSpace(series.ProviderIDs["Tmdb"]) != replacement.TMDBID {
			continue
		}
		matches++
		if series.Type == "Series" && series.Path == expectedPath {
			found = series
		}
	}
	if found == nil || matches != expectedCount || (staged && (!oldBound || found.ID == replacement.OldSeriesID)) {
		return "", nil, fmt.Errorf("completed-pack old/new Series paths or exact TMDB identity changed")
	}
	items, episodes, err := s.embySvc.replacementItems(ctx, found.ID, found.Path)
	if err != nil {
		return "", nil, err
	}
	if !coversEpisodesFromSet(episodes, replacement.Expected) {
		return "", nil, fmt.Errorf("completed-pack Series does not own every expected physical episode")
	}
	missing, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, replacement.LibraryID, time.Now())
	if err != nil {
		return "", nil, err
	}
	for _, episode := range missing {
		if episode.SeriesID == found.ID {
			return "", nil, fmt.Errorf("completed-pack Series still has aired gaps")
		}
	}
	return found.ID, items, nil
}

// Old cloud data and STRM/state snapshots remain in operation-specific quarantine
// outside the active library. Emby's DELETE endpoint also deletes media files.
func (s *TaskQueueService) finalizeCompletedPack(ctx context.Context, replacement *completedPackReplacement) (newID string, resultErr error) {
	defer func() {
		if resultErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			resultErr = errors.Join(resultErr, s.rollbackCompletedPack(cleanupCtx, replacement))
		}
	}()
	if replacement.Finalized {
		return "", fmt.Errorf("completed pack is already finalized")
	}
	_, newItems, err := s.verifyReplacementSeries(ctx, replacement, true)
	if err != nil {
		return "", err
	}
	oldItems, oldEpisodes, err := s.embySvc.replacementItems(ctx, replacement.OldSeriesID, replacement.OldPath)
	if err != nil {
		return "", err
	}
	for key := range oldItems {
		if newItems[key] == "" {
			return "", fmt.Errorf("completed pack cannot preserve existing item %s", key)
		}
	}
	for key := range oldEpisodes {
		replacement.Expected[key] = struct{}{}
	}
	if err := s.embySvc.EnsureNoActivePlayback(ctx); err != nil {
		return "", err
	}
	replacement.States, err = s.embySvc.snapshotReplacementState(ctx, oldItems)
	if err != nil {
		return "", err
	}
	extraItems := make(map[string]string)
	for key, id := range newItems {
		if oldItems[key] == "" {
			extraItems[key] = id
		}
	}
	replacement.DestinationStates = append([]replacementUserState(nil), replacement.States...)
	if len(extraItems) > 0 {
		extraStates, err := s.embySvc.snapshotReplacementState(ctx, extraItems)
		if err != nil {
			return "", err
		}
		replacement.DestinationStates = append(replacement.DestinationStates, extraStates...)
	}
	folders, err := s.replacementFolders(ctx, replacement.AccountID, replacement.LibraryCID)
	if err != nil {
		return "", err
	}
	if !replacementFolderIs(folders, replacement.OldCID, replacement.OldFolder) || !replacementFolderIs(folders, replacement.NewCID, replacement.NewFolder) {
		return "", fmt.Errorf("old or staged 115 root changed before cutover")
	}
	strmRoot, err := s.db.GetSetting("strm_root")
	if err != nil || strings.TrimSpace(strmRoot) == "" {
		return "", fmt.Errorf("strm_root is required for replacement backup")
	}
	strmRoot, err = canonicalRoot(strmRoot, false)
	if err != nil {
		return "", err
	}
	replacement.BackupDir, err = os.MkdirTemp(filepath.Dir(strmRoot), ".embymedia-replacement-")
	if err != nil {
		return "", err
	}
	replacement.QuarantineCID, err = s.createReplacementFolder(ctx, replacement.AccountID, "0", "embymedia-replacement-"+uuid.NewString())
	if err != nil {
		return "", err
	}
	if err := persistReplacementSnapshot(replacement); err != nil {
		return "", err
	}
	if _, _, err := s.verifyReplacementSeries(ctx, replacement, true); err != nil {
		return "", err
	}
	if err := s.embySvc.EnsureNoActivePlayback(ctx); err != nil {
		return "", err
	}
	oldSTRM := filepath.Join(replacement.STRMBase, replacement.OldFolder)
	info, err := os.Lstat(oldSTRM)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("canonical STRM root is not a real directory")
	}
	replacement.CutoverStarted = true
	if err := s.driveSvc.MoveCtx(ctx, replacement.AccountID, []string{replacement.OldCID}, replacement.QuarantineCID); err != nil {
		return "", err
	}
	if err := s.driveSvc.RenameCtx(ctx, replacement.AccountID, replacement.NewCID, replacement.OldFolder); err != nil {
		return "", err
	}
	if err := waitForReplacementMount(ctx, filepath.Join(replacement.MediaBase, replacement.OldFolder), replacement.MediaFiles); err != nil {
		return "", err
	}
	if err := os.Rename(oldSTRM, filepath.Join(replacement.BackupDir, "strm")); err != nil {
		return "", err
	}
	if err := os.Mkdir(oldSTRM, 0775); err != nil {
		return "", err
	}
	replacement.CanonicalSTRMInfo, err = os.Lstat(oldSTRM)
	if err != nil {
		return "", err
	}
	if err := removeOwnedReplacementRoot(filepath.Join(replacement.STRMBase, replacement.NewFolder), replacement.NewSTRMInfo); err != nil {
		return "", err
	}
	if _, err := s.mediaSvc.SyncSTRMWithProgress(ctx, filepath.Join(replacement.LibraryName, replacement.OldFolder), nil); err != nil {
		return "", err
	}
	if _, err := s.embySvc.RunLibraryScanCtx(ctx, func(float64, string) error { return nil }); err != nil {
		return "", err
	}
	newID, newItems, err = s.verifyReplacementSeries(ctx, replacement, false)
	if err != nil {
		return "", err
	}
	if err := s.embySvc.restoreReplacementState(ctx, replacement.DestinationStates, newItems); err != nil {
		return "", err
	}
	replacement.Finalized = true
	if err := persistReplacementSnapshot(replacement); err != nil {
		replacement.Finalized = false
		return "", err
	}
	return newID, nil
}

func replacementFolderIs(files map[string]domain.DriveFile, cid, name string) bool {
	file, exists := files[cid]
	if !exists || !file.IsFolder || file.Name != name {
		return false
	}
	for id, other := range files {
		if id != cid && other.Name == name {
			return false
		}
	}
	return true
}

func persistReplacementSnapshot(replacement *completedPackReplacement) error {
	encoded, err := json.MarshalIndent(replacement, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(replacement.BackupDir, ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(encoded)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(replacement.BackupDir, "state.json")); err != nil {
		return err
	}
	directory, err := os.Open(replacement.BackupDir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// Rollback deletes only the freshly created staging CID and inode. An uncertain
// cloud move keeps both data copies and the snapshot for explicit recovery.
func (s *TaskQueueService) rollbackCompletedPack(ctx context.Context, replacement *completedPackReplacement) error {
	if replacement == nil || replacement.Finalized || replacement.RolledBack {
		return nil
	}
	if replacement.NewCID != "" && replacement.NewCID == replacement.OldCID {
		return fmt.Errorf("rollback staging CID aliases the original source; refusing deletion")
	}
	if replacement.CutoverStarted {
		library, err := s.replacementFolders(ctx, replacement.AccountID, replacement.LibraryCID)
		if err != nil {
			return fmt.Errorf("replacement recovery requires library inventory: %w", err)
		}
		quarantine, err := s.replacementFolders(ctx, replacement.AccountID, replacement.QuarantineCID)
		if err != nil {
			return fmt.Errorf("replacement recovery requires quarantine inventory: %w", err)
		}
		if !replacementFolderIs(library, replacement.OldCID, replacement.OldFolder) && !replacementFolderIs(quarantine, replacement.OldCID, replacement.OldFolder) {
			return fmt.Errorf("old 115 root location is uncertain; keeping staged copy and snapshot %s", replacement.BackupDir)
		}
		if replacementFolderIs(library, replacement.NewCID, replacement.OldFolder) {
			if err := s.driveSvc.RenameCtx(ctx, replacement.AccountID, replacement.NewCID, replacement.NewFolder); err != nil {
				return fmt.Errorf("restore staging name: %w", err)
			}
		} else if replacement.NewCID != "" && !replacementFolderIs(library, replacement.NewCID, replacement.NewFolder) {
			return fmt.Errorf("staged 115 root location changed; keeping snapshot %s", replacement.BackupDir)
		}
		if replacementFolderIs(quarantine, replacement.OldCID, replacement.OldFolder) {
			if err := s.driveSvc.MoveCtx(ctx, replacement.AccountID, []string{replacement.OldCID}, replacement.LibraryCID); err != nil {
				return fmt.Errorf("restore old 115 root: %w", err)
			}
		}
		backup := filepath.Join(replacement.BackupDir, "strm")
		if _, err := os.Lstat(backup); err == nil {
			canonical := filepath.Join(replacement.STRMBase, replacement.OldFolder)
			if err := removeOwnedReplacementRoot(canonical, replacement.CanonicalSTRMInfo); err != nil {
				return err
			}
			if err := os.Rename(backup, canonical); err != nil {
				return fmt.Errorf("restore old STRM snapshot: %w", err)
			}
			replacement.CanonicalSTRMInfo = nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if replacement.NewCID != "" {
		files, err := s.replacementFolders(ctx, replacement.AccountID, replacement.LibraryCID)
		if err != nil {
			return err
		}
		if replacement.CutoverStarted && !replacementFolderIs(files, replacement.OldCID, replacement.OldFolder) {
			return fmt.Errorf("old canonical 115 root was not restored; keeping staged copy")
		}
		if _, exists := files[replacement.NewCID]; exists {
			if !replacementFolderIs(files, replacement.NewCID, replacement.NewFolder) {
				return fmt.Errorf("staged 115 root changed; refusing rollback deletion")
			}
			if err := s.driveSvc.DeleteCtx(ctx, replacement.AccountID, []string{replacement.NewCID}); err != nil {
				return fmt.Errorf("recycle operation-owned staging root: %w", err)
			}
		}
		replacement.NewCID = ""
	}
	if err := removeOwnedReplacementRoot(filepath.Join(replacement.STRMBase, replacement.NewFolder), replacement.NewSTRMInfo); err != nil {
		return err
	}
	if replacement.CutoverStarted {
		if _, err := s.embySvc.RunLibraryScanCtx(ctx, func(float64, string) error { return nil }); err != nil {
			return fmt.Errorf("scan restored canonical root: %w", err)
		}
		inventory, err := s.embySvc.ListSeriesCtx(ctx, replacement.LibraryID)
		if err != nil {
			return err
		}
		for _, series := range inventory {
			if series.Path == replacement.OldPath && strings.TrimSpace(series.ProviderIDs["Tmdb"]) == replacement.TMDBID {
				items, _, err := s.embySvc.replacementItems(ctx, series.ID, series.Path)
				if err != nil {
					return err
				}
				if err := s.embySvc.restoreReplacementState(ctx, replacement.States, items); err != nil {
					return err
				}
				replacement.CutoverStarted = false
				replacement.RolledBack = true
				replacement.RollbackScanned = true
				return nil
			}
		}
		return fmt.Errorf("restored canonical Series was not found; user-state snapshot remains at %s", replacement.BackupDir)
	}
	replacement.RolledBack = true
	return nil
}

func coversEpisodesFromSet(actual, expected map[episodeKey]struct{}) bool {
	for key := range expected {
		if _, exists := actual[key]; !exists {
			return false
		}
	}
	return true
}

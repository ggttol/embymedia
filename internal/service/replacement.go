package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

type completedPackReplacement struct {
	LibraryID   string
	LibraryName string
	LibraryCID  string
	OldSeriesID string
	OldFolder   string
	OldCID      string
	NewFolder   string
	TMDBID      string
	Expected    map[episodeKey]struct{}
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
		covered := make(map[episodeKey]struct{}, len(expected))
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
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

func (s *TaskQueueService) stageCompletedPack(ctx context.Context, library domain.EmbyLibrary, libraryCID string, gaps map[episodeKey]struct{}, series *domain.EmbyMediaItem, candidates []autoFillCandidate) (*completedPackReplacement, []string, string) {
	present, err := s.embySvc.ListAiredSeriesEpisodesCtx(ctx, series.ID, time.Now())

	if err != nil {
		return nil, nil, err.Error()
	}
	expected := episodeSet(present)
	for key := range gaps {
		expected[key] = struct{}{}
	}
	if len(expected) == 0 {
		return nil, nil, "completed-pack replacement has no aired episode inventory"
	}
	account, err := s.driveSvc.getAccount("")
	if err != nil {
		return nil, nil, err.Error()
	}
	mediaRoot, _ := s.db.GetSetting("media_root")
	oldFolder := filepath.Base(filepath.Clean(series.Path))
	identity := normalizeMediaTitle(series.Name)
	lastError := ""
	for _, candidate := range candidates {
		if !strings.Contains(normalizeMediaTitle(candidate.Title), identity) {
			continue
		}
		shareCode, receiveCode, err := ParseShareCode(candidate.URL, candidate.Password)
		if err != nil {
			continue
		}
		shareTitle, roots, err := s.driveSvc.snapshotShareEntries(ctx, account, shareCode, receiveCode, "0")
		if err != nil || len(roots) != 1 || !roots[0].IsDir {
			continue
		}
		root := roots[0]
		if root.Name == oldFolder || !strings.Contains(normalizeMediaTitle(candidate.Title+shareTitle+root.Name), identity) {
			continue
		}
		newPath := filepath.Join(mediaRoot, library.Name, root.Name)
		if _, err := os.Stat(newPath); err == nil || !os.IsNotExist(err) {
			continue
		}
		_, leaves, err := s.scanAutoFillShare(ctx, candidate)
		if err != nil || !coversEpisodes(leaves, expected) {
			continue
		}
		if _, _, err := s.driveSvc.receiveShareEntriesCtx(ctx, account, shareCode, receiveCode, []string{root.ID}, libraryCID, shareTitle); err != nil {
			lastError = err.Error()
			continue
		}
		if err := waitForAutoFillCoverage(ctx, newPath, expected); err != nil {
			return nil, nil, err.Error()
		}
		return &completedPackReplacement{
			LibraryID: library.ID, LibraryName: library.Name, LibraryCID: libraryCID,
			OldSeriesID: series.ID, OldFolder: oldFolder, NewFolder: root.Name,
			TMDBID: strings.TrimSpace(series.ProviderIDs["Tmdb"]), Expected: expected,
		}, []string{newPath}, ""
	}
	if lastError != "" {
		return nil, nil, "verified completed pack could not be transferred: " + lastError
	}
	return nil, nil, ""
}

func (s *TaskQueueService) removeOldSTRMRoot(libraryName, folder string) error {
	strmSetting, err := s.db.GetSetting("strm_root")
	if err != nil {
		return err
	}
	root, err := canonicalRoot(strmSetting, false)
	if err != nil {
		return err
	}
	target := filepath.Join(root, libraryName, folder)
	if !inside(root, target) {
		return fmt.Errorf("old STRM root escapes configured root")
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("old STRM root is a symbolic link")
	}
	return os.RemoveAll(target)
}

func (s *TaskQueueService) finalizeCompletedPack(ctx context.Context, replacement *completedPackReplacement) (string, error) {
	inventory, err := s.embySvc.ListSeriesCtx(ctx, replacement.LibraryID)
	if err != nil {
		return "", err
	}
	var newSeries *domain.EmbyMediaItem
	tmdbMatches := 0
	for index := range inventory {
		series := &inventory[index]
		if strings.TrimSpace(series.ProviderIDs["Tmdb"]) == replacement.TMDBID {
			tmdbMatches++
			if filepath.Base(filepath.Clean(series.Path)) == replacement.NewFolder {
				newSeries = series
			}
		}
	}
	if newSeries == nil || tmdbMatches != 2 {
		return "", fmt.Errorf("new completed Series was not uniquely bound beside the old TMDB Series")
	}
	missing, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, replacement.LibraryID, time.Now())
	if err != nil {
		return "", err
	}
	for _, episode := range missing {
		if episode.SeriesID == newSeries.ID {
			return "", fmt.Errorf("new completed Series still has aired gaps")
		}
	}
	present, err := s.embySvc.ListAiredSeriesEpisodesCtx(ctx, newSeries.ID, time.Now())
	if err != nil || !coversEpisodesFromSet(episodeSet(present), replacement.Expected) {
		return "", fmt.Errorf("new completed Series does not own every expected episode")
	}
	oldCID, err := s.resolveAutoFillFolder(ctx, replacement.LibraryCID, replacement.OldFolder)
	if err != nil {
		return "", err
	}
	if err := s.embySvc.DeleteItemCtx(ctx, replacement.OldSeriesID); err != nil {
		return "", err
	}
	if err := s.driveSvc.DeleteCtx(ctx, "", []string{oldCID}); err != nil {
		return "", fmt.Errorf("old Emby item was deleted but old 115 root could not be recycled: %w", err)
	}
	if err := s.removeOldSTRMRoot(replacement.LibraryName, replacement.OldFolder); err != nil {
		return "", fmt.Errorf("old 115 root was recycled but old STRM root could not be removed: %w", err)
	}
	return newSeries.ID, nil
}

func coversEpisodesFromSet(actual, expected map[episodeKey]struct{}) bool {
	for key := range expected {
		if _, exists := actual[key]; !exists {
			return false
		}
	}
	return true
}

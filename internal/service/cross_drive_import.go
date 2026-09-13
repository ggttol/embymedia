package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	defaultTransferTempDir      = "/srv/embymedia/data/transfers"
	defaultTransferMinFreeBytes = int64(10 << 30)
)

func shareSelectionsPayload(payload map[string]any, key string) ([]ShareSelection, error) {
	raw, present := payload[key]
	if !present || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an array of source identities", key)
	}
	var selections []ShareSelection
	if err := json.Unmarshal(encoded, &selections); err != nil {
		return nil, fmt.Errorf("%s must be an array of source identities", key)
	}
	seen := make(map[string]struct{}, len(selections))
	for index := range selections {
		selections[index].ID = strings.TrimSpace(selections[index].ID)
		if selections[index].ID == "" || strings.TrimSpace(selections[index].Revision) == "" || strings.TrimSpace(selections[index].Name) == "" || selections[index].Size < 0 {
			return nil, fmt.Errorf("%s contains an invalid or revisionless source identity", key)
		}
		if _, duplicate := seen[selections[index].ID]; duplicate {
			return nil, fmt.Errorf("%s must not contain duplicate source IDs", key)
		}
		seen[selections[index].ID] = struct{}{}
	}
	return selections, nil
}

func (s *TaskQueueService) runQuarkTo115Import(ctx context.Context, task domain.AsyncTask) (result map[string]any, runErr error) {
	quarkAccountID, _ := stringPayload(task.Payload, "quark_account_id", true)
	quarkTargetID, _ := stringPayload(task.Payload, "quark_target_id", true)
	rawURL, _ := stringPayload(task.Payload, "share_url", true)
	password, _ := stringPayload(task.Payload, "share_password", false)
	c115AccountID, _ := stringPayload(task.Payload, "c115_account_id", true)
	priorTaskID, _ := stringPayload(task.Payload, "prior_import_task_id", false)
	parentTaskID, err := stringPayload(task.Payload, "parent_task_id", false)
	if err != nil {
		return nil, err
	}
	selectedIDs, err := optionalStringSlicePayload(task.Payload, "selected_source_ids")
	if err != nil {
		return nil, err
	}
	selections, err := shareSelectionsPayload(task.Payload, "selected_source_manifest")
	if err != nil {
		return nil, err
	}
	state := &domain.CrossDriveImport{
		TaskID: task.ID, PriorTaskID: priorTaskID, Phase: "saving_share",
		QuarkAccountID: quarkAccountID, QuarkTargetID: quarkTargetID, C115AccountID: c115AccountID,
		SelectedSourceIDs: selectedIDs,
	}
	for key, destination := range map[string]*string{
		"autofill_library_name": &state.AutofillLibraryName, "autofill_library_id": &state.AutofillLibraryID,
		"autofill_library_cid": &state.AutofillLibraryCID, "autofill_series_id": &state.AutofillSeriesID,
		"autofill_tmdb_id": &state.AutofillTMDBID, "autofill_series_folder": &state.AutofillSeriesFolder,
	} {
		*destination, _ = stringPayload(task.Payload, key, false)
	}
	state.ExpectedEpisodes, err = optionalStringSlicePayload(task.Payload, "expected_episodes")
	if err != nil {
		return nil, err
	}
	bound := state.AutofillSeriesID != ""
	if bound {
		target, settingErr := s.db.GetSetting("quark_autofill_target_id")
		if settingErr != nil {
			return nil, settingErr
		}
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("quark_autofill_target_id must be configured for autofill transfer")
		}
		if strings.TrimSpace(target) != quarkTargetID {
			return nil, fmt.Errorf("quark_to_115_import target does not match quark_autofill_target_id")
		}
	}
	if bound {
		parent, parentErr := s.db.GetAsyncTask(parentTaskID)
		if parentErr != nil {
			return nil, fmt.Errorf("read autofill parent task: %w", parentErr)
		}
		if parent.Type != "series_auto_fill" || parent.Status != "completed" {
			return nil, fmt.Errorf("autofill parent task is %s; refusing child provider writes", parent.Status)
		}
	}
	prevalidatedDestination := ""
	boundCloudDriveDestination := ""
	if bound {
		prevalidatedDestination, err = s.resolveAutofillDestination(ctx, state)
		if err != nil {
			return nil, fmt.Errorf("validate autofill destination before Quark save: %w", err)
		}
		state.DestinationCID = prevalidatedDestination
		account, accountErr := s.driveSvc.getAccountForProvider("115", c115AccountID)
		if accountErr != nil {
			return nil, accountErr
		}
		if strings.TrimSpace(account.Token) == "" {
			mountRoot, mountErr := s.cloudDrive115Mount(ctx, account.ID)
			if mountErr != nil {
				return nil, fmt.Errorf("validate CloudDrive upload before Quark save: %w", mountErr)
			}
			boundCloudDriveDestination = filepath.Join(mountRoot, state.AutofillLibraryName, state.AutofillSeriesFolder)
			if err := waitForLocalDirectory(ctx, boundCloudDriveDestination); err != nil {
				return nil, fmt.Errorf("validate CloudDrive Series directory before Quark save: %w", err)
			}
			if err := unix.Access(boundCloudDriveDestination, unix.W_OK); err != nil {
				return nil, fmt.Errorf("CloudDrive Series directory is not writable: %w", err)
			}
		}
	}
	if err := s.db.CreateCrossDriveImport(state); err != nil {
		return nil, err
	}
	owner := uuid.NewString()
	if err := s.db.ClaimCrossDriveImport(task.ID, owner); err != nil {
		return nil, err
	}
	defer func() { _ = s.db.ReleaseCrossDriveImport(task.ID, owner) }()
	state, err = s.db.GetCrossDriveImport(task.ID)
	if err != nil {
		return nil, err
	}
	if state.CancelRequested {
		return nil, context.Canceled
	}
	if priorTaskID != "" && len(state.SavedRootIDs) == 0 {
		prior, err := s.db.GetCrossDriveImport(priorTaskID)
		if err != nil {
			return nil, fmt.Errorf("read prior import checkpoints: %w", err)
		}
		if !sameCrossDriveBinding(prior, state) {
			return nil, fmt.Errorf("retry checkpoint source or destination binding changed")
		}
		if len(prior.SavedRootIDs) == 0 {
			return nil, fmt.Errorf("prior import has no reconciled Quark saved-root checkpoint; refusing to resubmit the share")
		}
		if err := s.db.SaveCrossDriveRoots(task.ID, owner, prior.SavedRootIDs); err != nil {
			return nil, err
		}
		state.SavedRootIDs = prior.SavedRootIDs
	}
	if len(state.SavedRootIDs) == 0 {
		if task.Attempts > 0 {
			return nil, fmt.Errorf("Quark share save may have completed before restart; refusing to submit it again")
		}
		if err := s.db.UpdateCrossDriveImport(task.ID, owner, "saving_share", "", "", ""); err != nil {
			return nil, err
		}
		var saved SavedShare
		if len(state.SelectedSourceIDs) > 0 {
			if len(selections) != len(state.SelectedSourceIDs) {
				return nil, fmt.Errorf("selected_source_manifest does not match selected_source_ids")
			}
			for index := range selections {
				if selections[index].ID != state.SelectedSourceIDs[index] {
					return nil, fmt.Errorf("selected_source_manifest order does not match selected_source_ids")
				}
			}
			saved, err = s.driveSvc.SaveProviderShareSelections(ctx, "quark", quarkAccountID, rawURL, password, quarkTargetID, selections)
		} else {
			saved, err = s.driveSvc.SaveProviderShare(ctx, "quark", quarkAccountID, rawURL, password, quarkTargetID)
		}
		if err != nil {
			return nil, err
		}
		if err := s.db.SaveCrossDriveRoots(task.ID, owner, saved.RootIDs); err != nil {
			return nil, fmt.Errorf("persist Quark saved roots: %w", err)
		}
		state.SavedRootIDs = saved.RootIDs
		if err := s.db.UpdateAsyncTaskProgress(task.ID, 12); err != nil {
			return nil, err
		}
	}
	if err := s.db.UpdateCrossDriveImport(task.ID, owner, "discovering", "", "", ""); err != nil {
		return nil, err
	}
	if err := s.discoverQuarkImport(ctx, task.ID, owner, quarkAccountID, quarkTargetID, state.SavedRootIDs); err != nil {
		return nil, err
	}
	if err := s.db.FinalizeCrossDriveDiscovery(task.ID, owner); err != nil {
		return nil, err
	}
	if err := s.db.UpdateAsyncTaskProgress(task.ID, 20); err != nil {
		return nil, err
	}
	bound = state.AutofillSeriesID != ""
	destinationCID := state.DestinationCID
	if bound {
		revalidated, validateErr := s.resolveAutofillDestination(ctx, state)
		if validateErr != nil {
			return nil, validateErr
		}
		if destinationCID == "" || destinationCID != revalidated {
			return nil, fmt.Errorf("persisted autofill destination CID changed")
		}
		destinationCID = revalidated
	} else {
		destinationCID, err = s.resolveImportDestination(ctx, c115AccountID)
	}
	if err != nil {
		return nil, err
	}
	if state.DestinationCID != "" && state.DestinationCID != destinationCID {
		return nil, fmt.Errorf("persisted destination CID changed from %s to %s", state.DestinationCID, destinationCID)
	}
	if err := s.db.UpdateCrossDriveImport(task.ID, owner, "downloading", destinationCID, "", ""); err != nil {
		return nil, err
	}
	items, err := s.db.ListCrossDriveItems(task.ID)
	if err != nil {
		return nil, err
	}
	state, err = s.db.GetCrossDriveImport(task.ID)
	if err != nil {
		return nil, err
	}
	totalBytes := state.TotalBytes
	verifiedBytes := state.CompletedBytes
	for index := range items {
		item := &items[index]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := s.db.GetCrossDriveImport(task.ID)
		if err != nil {
			return nil, err
		}
		if state.CancelRequested {
			return nil, context.Canceled
		}
		if item.State == "verified" {
			if err := s.reconcileVerifiedItem(ctx, c115AccountID, *item); err != nil {
				return nil, fmt.Errorf("reconcile verified %s: %w", item.RelativePath, err)
			}
			continue
		}
		if err := s.db.UpdateCrossDriveImport(task.ID, owner, "downloading", destinationCID, item.RelativePath, ""); err != nil {
			return nil, err
		}
		spool, sha, pre, err := s.downloadQuarkItem(ctx, task.ID, owner, quarkAccountID, *item, totalBytes, verifiedBytes)
		if err != nil {
			_ = s.db.MarkCrossDriveItemFailed(item.ID, task.ID, owner, err.Error())
			return nil, err
		}
		parent := destinationCID
		if !bound {
			parent, err = s.resolveRelativeDestination(ctx, c115AccountID, destinationCID, filepath.Dir(item.RelativePath))
			if err != nil {
				return nil, err
			}
		}
		if err := s.db.UpdateCrossDriveImport(task.ID, owner, "uploading", destinationCID, item.RelativePath, ""); err != nil {
			return nil, err
		}
		account, err := s.driveSvc.getAccountForProvider("115", c115AccountID)
		if err != nil {
			return nil, err
		}
		provider := s.driveSvc.providers["115"].(*Provider115)
		uploadSource := UploadSource{Path: spool, Name: item.Name, Size: item.Size, SHA1: sha, PreSHA1: pre, UploadID: item.UploadID, Bucket: item.UploadBucket, Object: item.UploadObject}
		uploadSource.OnSession = func(uploadID, bucket, object string) error {
			uploadSource.UploadID, uploadSource.Bucket, uploadSource.Object = uploadID, bucket, object
			return s.db.SaveCrossDriveUploadSession(item.ID, task.ID, owner, uploadID, bucket, object)
		}
		uploadSource.OnPart = func(number int, etag string, size int64) error {
			return s.db.SaveCrossDriveUploadPart(item.ID, number, etag, size)
		}
		var upload UploadResult
		if strings.TrimSpace(account.Token) == "" {
			if bound {
				upload, err = s.uploadViaCloudDriveDirectory(ctx, *item, account, parent, spool, boundCloudDriveDestination)
			} else {
				upload, err = s.uploadViaCloudDrive(ctx, task.ID, *item, account, parent, spool)
			}
		} else {
			upload, err = provider.UploadFile(ctx, account, parent, uploadSource)
		}
		if err != nil {
			if ctx.Err() != nil && uploadSource.UploadID != "" {
				abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				abortErr := provider.AbortUpload(abortCtx, account, uploadSource)
				cancel()
				if abortErr == nil {
					_ = s.db.ResetCrossDriveUpload(item.ID, task.ID, owner)
				} else {
					return nil, fmt.Errorf("%w; abort multipart safely: %v", err, abortErr)
				}
				return nil, err
			}
			_ = s.db.MarkCrossDriveItemFailed(item.ID, task.ID, owner, err.Error())
			return nil, err
		}
		if err := s.db.UpdateCrossDriveImport(task.ID, owner, "verifying", destinationCID, item.RelativePath, ""); err != nil {
			return nil, err
		}
		if err := s.db.MarkCrossDriveItemVerified(item.ID, task.ID, owner, parent, upload.FileID); err != nil {
			return nil, err
		}
		if err := os.Remove(spool); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove verified spool file: %w", err)
		}
		verifiedBytes += item.Size
		progress := 20.0 + 79.0*float64(index+1)/float64(max(1, len(items)))
		if totalBytes > 0 {
			progress = 20 + 79*float64(verifiedBytes)/float64(totalBytes)
		}
		if err := s.db.UpdateAsyncTaskProgress(task.ID, min(99, progress)); err != nil {
			return nil, err
		}
	}
	if err := s.db.UpdateCrossDriveImport(task.ID, owner, "verified", destinationCID, "", ""); err != nil {
		return nil, err
	}
	destinationPath := "/emby/_待整理"
	if bound {
		if err := s.finalizeAutofillImport(ctx, c115AccountID, state); err != nil {
			return nil, err
		}
		destinationPath = "/" + filepath.ToSlash(filepath.Join(state.AutofillLibraryName, state.AutofillSeriesFolder))
	}
	return map[string]any{"destination_cid": destinationCID, "destination_path": destinationPath, "files_completed": len(items), "bytes_completed": totalBytes}, nil
}

func sameCrossDriveBinding(a, b *domain.CrossDriveImport) bool {
	if a == nil || b == nil {
		return false
	}
	if a.QuarkAccountID != b.QuarkAccountID || a.QuarkTargetID != b.QuarkTargetID || a.C115AccountID != b.C115AccountID ||
		a.AutofillLibraryName != b.AutofillLibraryName || a.AutofillLibraryID != b.AutofillLibraryID ||
		a.AutofillLibraryCID != b.AutofillLibraryCID || a.AutofillSeriesID != b.AutofillSeriesID ||
		a.AutofillTMDBID != b.AutofillTMDBID || a.AutofillSeriesFolder != b.AutofillSeriesFolder {
		return false
	}
	if strings.Join(a.SelectedSourceIDs, "\x00") != strings.Join(b.SelectedSourceIDs, "\x00") ||
		strings.Join(a.ExpectedEpisodes, "\x00") != strings.Join(b.ExpectedEpisodes, "\x00") {
		return false
	}
	return true
}

func (s *TaskQueueService) resolveAutofillDestination(ctx context.Context, state *domain.CrossDriveImport) (string, error) {
	if state == nil || state.AutofillLibraryName == "" || state.AutofillLibraryID == "" || state.AutofillLibraryCID == "" ||
		state.AutofillSeriesID == "" || state.AutofillTMDBID == "" || state.AutofillSeriesFolder == "" {
		return "", fmt.Errorf("autofill destination binding is incomplete")
	}
	rawCIDMap, err := s.db.GetSetting("c115_cid_map")
	if err != nil {
		return "", err
	}
	cidMap, err := loadAutoFillCIDMap(rawCIDMap, []string{state.AutofillLibraryName})
	if err != nil {
		return "", err
	}
	if cidMap[state.AutofillLibraryName] != state.AutofillLibraryCID {
		return "", fmt.Errorf("configured 115 library CID changed")
	}
	libraries, err := s.embySvc.ListLibrariesCtx(ctx)
	if err != nil {
		return "", err
	}
	var library *domain.EmbyLibrary
	for i := range libraries {
		if libraries[i].ID == state.AutofillLibraryID && libraries[i].Name == state.AutofillLibraryName {
			library = &libraries[i]
			break
		}
	}
	if library == nil || library.Collection != "tvshows" {
		return "", fmt.Errorf("autofill Emby library identity changed")
	}
	series, err := s.embySvc.GetItemCtx(ctx, state.AutofillSeriesID)
	if err != nil {
		return "", err
	}
	if series.Type != "Series" || strings.TrimSpace(series.ProviderIDs["Tmdb"]) != state.AutofillTMDBID {
		return "", fmt.Errorf("autofill canonical Emby Series identity changed")
	}
	cleanSeriesPath := filepath.Clean(series.Path)
	if filepath.Base(cleanSeriesPath) != state.AutofillSeriesFolder || filepath.Base(filepath.Dir(cleanSeriesPath)) != state.AutofillLibraryName {
		return "", fmt.Errorf("autofill canonical Series path changed")
	}
	files, err := s.listAllProviderFiles(ctx, "115", state.C115AccountID, state.AutofillLibraryCID)
	if err != nil {
		return "", err
	}
	var found string
	for _, file := range files {
		if file.ParentID == state.AutofillLibraryCID && file.Name == state.AutofillSeriesFolder && file.IsFolder {
			if found != "" && found != file.FileID {
				return "", fmt.Errorf("115 autofill Series destination is ambiguous")
			}
			found = file.FileID
		}
	}
	if found == "" {
		return "", fmt.Errorf("115 autofill Series folder %q was not found under library CID %s", state.AutofillSeriesFolder, state.AutofillLibraryCID)
	}
	return found, nil
}

func (s *TaskQueueService) finalizeAutofillImport(ctx context.Context, accountID string, state *domain.CrossDriveImport) error {
	if configured, err := s.db.GetSetting("clouddrive_c115_account_id"); err != nil {
		return err
	} else if strings.TrimSpace(configured) != "" {
		if _, err := s.cloudDrive115Mount(ctx, accountID); err != nil {
			return fmt.Errorf("verify CloudDrive mount visibility: %w", err)
		}
	}
	if _, err := s.mediaSvc.SyncSTRMWithProgress(ctx, state.AutofillLibraryName, nil); err != nil {
		return fmt.Errorf("sync STRM after Quark autofill: %w", err)
	}
	if _, err := s.embySvc.RunLibraryScanCtx(ctx, func(float64, string) error { return nil }); err != nil {
		return fmt.Errorf("scan Emby after Quark autofill: %w", err)
	}
	if _, err := s.resolveAutofillDestination(ctx, state); err != nil {
		return fmt.Errorf("canonical Series changed after Emby scan: %w", err)
	}
	if len(state.ExpectedEpisodes) == 0 {
		return nil
	}
	missing, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, state.AutofillLibraryID, time.Now())
	if err != nil {
		return err
	}
	expected := make(map[string]struct{}, len(state.ExpectedEpisodes))
	for _, label := range state.ExpectedEpisodes {
		keys := episodeKeysFromName(label)
		if len(keys) == 0 {
			expected[strings.TrimSpace(label)] = struct{}{}
			continue
		}
		for _, key := range keys {
			expected[key.String()] = struct{}{}
		}
	}
	for _, episode := range missing {
		if episode.SeriesID != state.AutofillSeriesID {
			continue
		}
		if _, remains := expected[(episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}).String()]; remains {
			return fmt.Errorf("Emby still reports expected episode %s as missing", (episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}).String())
		}
	}
	return nil
}

func (s *TaskQueueService) discoverQuarkImport(ctx context.Context, taskID, owner, accountID, targetID string, rootIDs []string) error {
	wanted := make(map[string]struct{}, len(rootIDs))
	for _, id := range rootIDs {
		wanted[id] = struct{}{}
	}
	roots, err := s.listAllProviderFiles(ctx, "quark", accountID, targetID)
	if err != nil {
		return err
	}
	found := 0
	visited := make(map[string]struct{})
	entries := 0
	var walk func(domain.DriveFile, string, int) error
	walk = func(file domain.DriveFile, relative string, depth int) error {
		if depth > quarkMaxDepth {
			return fmt.Errorf("Quark saved share exceeds %d directory levels", quarkMaxDepth)
		}
		if _, duplicate := visited[file.FileID]; duplicate {
			return fmt.Errorf("Quark saved tree repeats object %s", file.FileID)
		}
		visited[file.FileID] = struct{}{}
		entries++
		if entries > quarkMaxEntries {
			return fmt.Errorf("Quark saved share exceeds %d entries", quarkMaxEntries)
		}
		if !file.IsFolder {
			return s.db.UpsertCrossDriveItem(taskID, owner, &domain.CrossDriveItem{SourceFileID: file.FileID, SourceRevision: file.Revision, RelativePath: relative, Name: file.Name, Size: file.Size, SHA1: file.Sha1})
		}
		children, err := s.listAllProviderFiles(ctx, "quark", accountID, file.FileID)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := walk(child, filepath.Join(relative, child.Name), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if _, ok := wanted[root.FileID]; ok {
			found++
			if err := walk(root, root.Name, 0); err != nil {
				return err
			}
		}
	}
	if found != len(wanted) {
		return fmt.Errorf("Quark saved roots are not all visible in target directory")
	}
	return nil
}

func (s *TaskQueueService) listAllProviderFiles(ctx context.Context, provider, accountID, parent string) ([]domain.DriveFile, error) {
	result := make([]domain.DriveFile, 0)
	seen := map[string]struct{}{}
	for offset := 0; offset < quarkMaxEntries; offset += 200 {
		files, total, err := s.driveSvc.ListProviderFiles(ctx, provider, accountID, parent, offset, 200)
		if err != nil {
			return nil, err
		}
		before := len(result)
		for _, file := range files {
			if _, duplicate := seen[file.FileID]; !duplicate {
				seen[file.FileID] = struct{}{}
				result = append(result, file)
			}
		}
		if len(result) == before && len(files) > 0 {
			return nil, fmt.Errorf("%s listing made no progress", provider)
		}
		if len(files) == 0 || int64(offset+len(files)) >= total {
			return result, nil
		}
	}
	return nil, fmt.Errorf("%s directory exceeds %d entries", provider, quarkMaxEntries)
}

func (s *TaskQueueService) resolveImportDestination(ctx context.Context, accountID string) (string, error) {
	return s.resolveRelativeDestination(ctx, accountID, "0", filepath.Join("emby", "_待整理"))
}
func (s *TaskQueueService) resolveRelativeDestination(ctx context.Context, accountID, base, relative string) (string, error) {
	if relative == "." || relative == "" {
		return base, nil
	}
	current := base
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == "" || segment == "." {
			continue
		}
		if segment == ".." || strings.ContainsAny(segment, "\x00/") {
			return "", fmt.Errorf("invalid destination path segment")
		}
		files, err := s.listAllProviderFiles(ctx, "115", accountID, current)
		if err != nil {
			return "", err
		}
		next := ""
		for _, file := range files {
			if file.Name == segment {
				if !file.IsFolder {
					return "", fmt.Errorf("115 destination path conflicts with file %s", segment)
				}
				next = file.FileID
				break
			}
		}
		if next == "" {
			created, err := s.driveSvc.MkdirProvider(ctx, "115", accountID, current, segment)
			if err != nil {
				return "", err
			}
			files, err = s.listAllProviderFiles(ctx, "115", accountID, current)
			if err != nil {
				return "", err
			}
			for _, file := range files {
				if file.FileID == created && file.Name == segment && file.IsFolder && file.ParentID == current {
					next = created
					break
				}
			}
			if next == "" {
				return "", fmt.Errorf("115 mkdir result for %s could not be verified in parent %s", segment, current)
			}
		}
		current = next
	}
	return current, nil
}

func transferSettings(db interface{ GetSetting(string) (string, error) }) (string, int64, error) {
	directory, err := db.GetSetting("transfer_temp_dir")
	if err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(directory) == "" {
		directory = defaultTransferTempDir
	}
	if !filepath.IsAbs(directory) {
		return "", 0, fmt.Errorf("transfer_temp_dir must be absolute")
	}
	minimumText, err := db.GetSetting("transfer_min_free_bytes")
	if err != nil {
		return "", 0, err
	}
	minimum := defaultTransferMinFreeBytes
	if strings.TrimSpace(minimumText) != "" {
		if _, err := fmt.Sscan(minimumText, &minimum); err != nil || minimum < 0 {
			return "", 0, fmt.Errorf("transfer_min_free_bytes is invalid")
		}
	}
	return directory, minimum, nil
}
func requireTransferSpace(directory string, required int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(directory, &stat); err != nil {
		return err
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free < required {
		return fmt.Errorf("transfer spool has %d free bytes; %d required", free, required)
	}
	return nil
}

func (s *TaskQueueService) downloadQuarkItem(ctx context.Context, taskID, owner, accountID string, item domain.CrossDriveItem, totalBytes, verifiedBytes int64) (string, string, string, error) {
	directory, minimum, err := transferSettings(s.db)
	if err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", "", err
	}
	if err := requireTransferSpace(directory, item.Size+minimum); err != nil {
		return "", "", "", err
	}
	spool := item.SpoolPath
	if spool == "" {
		key := sha1.Sum([]byte(taskID + "\x00" + item.SourceFileID))
		spool = filepath.Join(directory, hex.EncodeToString(key[:])+".part")
	}
	file, err := os.OpenFile(spool, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", "", "", err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", "", "", err
	}
	offset := stat.Size()
	if offset > item.Size {
		return "", "", "", fmt.Errorf("spool file exceeds source size")
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", "", "", err
	}
	if offset < item.Size {
		body, err := s.driveSvc.OpenProviderDownload(ctx, "quark", accountID, item.SourceFileID, offset)
		if err != nil {
			return "", "", "", err
		}
		defer body.Close()
		buffer := make([]byte, 1<<20)
		downloaded := offset
		for downloaded < item.Size {
			count, readErr := body.Read(buffer)
			if count > 0 {
				remaining := item.Size - downloaded
				if int64(count) > remaining {
					return "", "", "", fmt.Errorf("Quark download exceeded declared size")
				}
				if _, err := file.Write(buffer[:count]); err != nil {
					return "", "", "", err
				}
				downloaded += int64(count)
				if err := s.db.UpdateCrossDriveItemDownload(item.ID, taskID, owner, "downloading", spool, downloaded); err != nil {
					return "", "", "", err
				}
				progress := 20.0
				if totalBytes > 0 {
					progress += 39 * float64(verifiedBytes+downloaded) / float64(totalBytes)
				}
				if err := s.db.UpdateAsyncTaskProgress(taskID, min(59, progress)); err != nil {
					return "", "", "", err
				}
			}
			if readErr != nil {
				if readErr == io.EOF && downloaded == item.Size {
					break
				}
				return "", "", "", readErr
			}
		}
	}
	if err := file.Sync(); err != nil {
		return "", "", "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", "", err
	}
	full := sha1.New()
	prefix := sha1.New()
	if _, err := io.Copy(full, file); err != nil {
		return "", "", "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", "", err
	}
	if _, err := io.CopyN(prefix, file, min(item.Size, int64(128<<10))); err != nil && err != io.EOF {
		return "", "", "", err
	}
	sha := strings.ToUpper(hex.EncodeToString(full.Sum(nil)))
	pre := strings.ToUpper(hex.EncodeToString(prefix.Sum(nil)))
	if item.SHA1 != "" && !strings.EqualFold(item.SHA1, sha) {
		return "", "", "", fmt.Errorf("Quark source SHA-1 changed for %s", item.RelativePath)
	}
	if err := s.db.UpdateCrossDriveItemHashes(item.ID, taskID, owner, sha, pre); err != nil {
		return "", "", "", err
	}
	return spool, sha, pre, nil
}

func (s *TaskQueueService) reconcileVerifiedItem(ctx context.Context, accountID string, item domain.CrossDriveItem) error {
	provider := s.driveSvc.providers["115"].(*Provider115)
	account, err := s.driveSvc.getAccountForProvider("115", accountID)
	if err != nil {
		return err
	}
	file, err := provider.findDestinationFile(ctx, account, item.DestinationParent, item.Name)
	if err != nil {
		return err
	}
	if file == nil || file.FileID != item.DestinationID || file.Size != item.Size || !strings.EqualFold(file.Sha1, item.SHA1) {
		return fmt.Errorf("provider state is ambiguous; destination checkpoint no longer matches")
	}
	return nil
}

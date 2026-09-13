package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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

func (s *TaskQueueService) runQuarkTo115Import(ctx context.Context, task domain.AsyncTask) (result map[string]any, runErr error) {
	quarkAccountID, _ := stringPayload(task.Payload, "quark_account_id", true)
	quarkTargetID, _ := stringPayload(task.Payload, "quark_target_id", true)
	rawURL, _ := stringPayload(task.Payload, "share_url", true)
	password, _ := stringPayload(task.Payload, "share_password", false)
	c115AccountID, _ := stringPayload(task.Payload, "c115_account_id", true)
	priorTaskID, _ := stringPayload(task.Payload, "prior_import_task_id", false)
	state := &domain.CrossDriveImport{TaskID: task.ID, PriorTaskID: priorTaskID, Phase: "saving_share", QuarkAccountID: quarkAccountID, QuarkTargetID: quarkTargetID, C115AccountID: c115AccountID}
	if err := s.db.CreateCrossDriveImport(state); err != nil {
		return nil, err
	}
	owner := uuid.NewString()
	if err := s.db.ClaimCrossDriveImport(task.ID, owner); err != nil {
		return nil, err
	}
	defer func() { _ = s.db.ReleaseCrossDriveImport(task.ID, owner) }()
	state, err := s.db.GetCrossDriveImport(task.ID)
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
		saved, err := s.driveSvc.SaveProviderShare(ctx, "quark", quarkAccountID, rawURL, password, quarkTargetID)
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
	destinationCID, err := s.resolveImportDestination(ctx, c115AccountID)
	if err != nil {
		return nil, err
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
		item.SpoolPath, item.SHA1, item.PreSHA1, item.DownloadedBytes, item.State = spool, sha, pre, item.Size, "downloaded"
		parent, err := s.resolveRelativeDestination(ctx, c115AccountID, destinationCID, filepath.Dir(item.RelativePath))
		if err != nil {
			return nil, err
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
			upload, err = s.uploadViaCloudDrive(ctx, task.ID, *item, account, parent, spool)
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
	return map[string]any{"destination_cid": destinationCID, "destination_path": "/emby/_待整理", "files_completed": len(items), "bytes_completed": totalBytes}, nil
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

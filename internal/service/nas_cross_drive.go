package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/transfer"
)

func nasDestination(state *domain.CrossDriveImport) string {
	if state != nil && state.AutofillSeriesID != "" {
		return strings.TrimSpace(state.AutofillLibraryName) + "/" + strings.TrimSpace(state.AutofillSeriesFolder)
	}
	return "_待整理"
}

func (s *TaskQueueService) checkNASDestination(ctx context.Context, taskID string, state *domain.CrossDriveImport) error {
	if s.nasWorker == nil {
		return nil
	}
	account, err := s.driveSvc.getAccountForProvider("115", state.C115AccountID)
	if err != nil {
		return err
	}
	canaryParent, err := s.resolveImportDestination(ctx, account.ID)
	if err != nil {
		return fmt.Errorf("resolve NAS worker canary parent: %w", err)
	}
	files, err := s.listAllProviderFiles(ctx, "115", account.ID, canaryParent)
	if err != nil {
		return fmt.Errorf("read NAS worker canary: %w", err)
	}
	matches := 0
	for _, file := range files {
		if file.ParentID == canaryParent && file.Name == transfer.WorkerCanaryName && !file.IsFolder && file.Size == 0 && strings.EqualFold(file.Sha1, transfer.EmptySHA1) {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("NAS worker canary does not identify the configured 115 account")
	}
	if err := s.nasWorker.Check(ctx, taskID+":preflight", nasDestination(state), 0); err != nil {
		return fmt.Errorf("validate NAS transfer worker before Quark save: %w", err)
	}
	return nil
}

func (s *TaskQueueService) transferQuarkItemViaNAS(ctx context.Context, taskID, owner string, item *domain.CrossDriveItem, state *domain.CrossDriveImport, quarkAccount, c115Account *domain.DriveAccount, parentCID string, totalBytes, verifiedBytes int64) (UploadResult, error) {
	jobID := taskID + ":" + item.SourceFileID
	destination := nasDestination(state)
	downloaded, err := s.nasWorker.Download(ctx, transfer.Request{
		JobID: jobID, AccountID: quarkAccount.ID, SourceFileID: item.SourceFileID, SourceRevision: item.SourceRevision,
		Name: item.Name, Size: item.Size, ExpectedSHA1: item.SHA1, QuarkCookie: quarkAccount.Cookie,
		Connections: s.quarkDownloadConnections(),
	}, func(event transfer.Event) error {
		if event.Phase != "downloading" || event.Size != item.Size || event.DownloadedBytes < 0 || event.DownloadedBytes > item.Size {
			return fmt.Errorf("NAS worker returned invalid download progress")
		}
		if err := s.db.UpdateCrossDriveItemDownload(item.ID, taskID, owner, "downloading", "", event.DownloadedBytes); err != nil {
			return err
		}
		progress := 20.0
		if totalBytes > 0 {
			progress += 39 * float64(verifiedBytes+event.DownloadedBytes) / float64(totalBytes)
		}
		return s.db.UpdateAsyncTaskProgress(taskID, min(59, progress))
	})
	if err != nil {
		return UploadResult{}, err
	}
	if downloaded.Phase != "downloaded" || downloaded.Size != item.Size || downloaded.DownloadedBytes != item.Size || !transfer.ValidSHA1(downloaded.SHA1) || !transfer.ValidSHA1(downloaded.PreSHA1) {
		return UploadResult{}, fmt.Errorf("NAS worker returned an invalid completed download")
	}
	if item.SHA1 != "" && !strings.EqualFold(item.SHA1, downloaded.SHA1) {
		return UploadResult{}, fmt.Errorf("NAS worker source SHA-1 changed for %s", item.RelativePath)
	}
	item.SHA1, item.PreSHA1 = downloaded.SHA1, downloaded.PreSHA1
	if err := s.db.UpdateCrossDriveItemHashes(item.ID, taskID, owner, item.SHA1, item.PreSHA1); err != nil {
		return UploadResult{}, err
	}
	provider := s.driveSvc.providers["115"].(*Provider115)
	source := UploadSource{Name: item.Name, Size: item.Size, SHA1: item.SHA1, PreSHA1: item.PreSHA1}
	existing, err := provider.findDestinationFile(ctx, c115Account, parentCID, item.Name)
	if err != nil {
		return UploadResult{}, err
	}
	if existing != nil {
		switch classifyDestination(existing, source, parentCID) {
		case destinationVerified:
			if err := s.nasWorker.Commit(ctx, jobID, quarkAccount.ID, item.SourceFileID); err != nil {
				return UploadResult{}, err
			}
			return UploadResult{FileID: existing.FileID, Rapid: true}, nil
		case destinationForeign:
			return UploadResult{}, fmt.Errorf("same-name destination conflict: 115 destination identity verification failed for %s", source.Name)
		default:
			result, err := provider.waitForVerifiedDestination(ctx, c115Account, parentCID, source, false, item.Size)
			if err != nil {
				return UploadResult{}, err
			}
			if err := s.nasWorker.Commit(ctx, jobID, quarkAccount.ID, item.SourceFileID); err != nil {
				return UploadResult{}, err
			}
			return result, nil
		}
	}
	if err := s.db.UpdateCrossDriveImport(taskID, owner, "uploading", state.DestinationCID, item.RelativePath, ""); err != nil {
		return UploadResult{}, err
	}
	// 115 lists an in-progress upload as "<name>**..uploading". A placeholder with
	// no settled object means an earlier publication never finished, and the
	// mount still reports that stale file at its full size, so the bytes are
	// rewritten instead of skipped.
	replace, err := provider.cloudDriveUploadPlaceholder(ctx, c115Account, parentCID, item.Name)
	if err != nil {
		return UploadResult{}, err
	}
	published, err := s.nasWorker.Publish(ctx, transfer.Request{
		JobID: jobID, AccountID: quarkAccount.ID, SourceFileID: item.SourceFileID, SourceRevision: item.SourceRevision,
		Name: item.Name, Size: item.Size, ExpectedSHA1: item.SHA1, Destination: destination, Replace: replace,
	}, func(event transfer.Event) error {
		if event.Phase != "uploading" || event.Size != item.Size || !strings.EqualFold(event.SHA1, item.SHA1) {
			return fmt.Errorf("NAS worker returned invalid upload progress")
		}
		return nil
	})
	if err != nil {
		return UploadResult{}, err
	}
	if published.Phase != "published" || published.Size != item.Size || !strings.EqualFold(published.SHA1, item.SHA1) {
		return UploadResult{}, fmt.Errorf("NAS worker returned an invalid publish result")
	}
	if err := s.db.UpdateCrossDriveImport(taskID, owner, "verifying", state.DestinationCID, item.RelativePath, ""); err != nil {
		return UploadResult{}, err
	}
	result, err := provider.waitForVerifiedDestination(ctx, c115Account, parentCID, source, false, item.Size)
	if err != nil {
		return UploadResult{}, err
	}
	if err := s.nasWorker.Commit(ctx, jobID, quarkAccount.ID, item.SourceFileID); err != nil {
		return UploadResult{}, err
	}
	return result, nil
}

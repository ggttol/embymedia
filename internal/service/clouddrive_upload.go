package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

const cloudDriveUploadWait = 2 * time.Minute

func hashPathSHA1(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha1.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

func waitForLocalDirectory(ctx context.Context, directory string) error {
	deadline := time.Now().Add(cloudDriveUploadWait)
	for {
		info, err := os.Stat(directory)
		if err == nil && info.IsDir() {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("CloudDrive2 destination directory did not appear: %s", directory)
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (s *TaskQueueService) cloudDrive115Mount(ctx context.Context, accountID string) (string, error) {
	boundAccount, err := s.db.GetSetting("clouddrive_c115_account_id")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(boundAccount) == "" || strings.TrimSpace(boundAccount) != accountID {
		return "", fmt.Errorf("clouddrive_c115_account_id must bind this mount to 115 account %s", accountID)
	}
	mounts, err := NewCloudDriveService(s.db).GetMounts(ctx)
	if err != nil {
		return "", err
	}
	for _, mount := range mounts {
		readOnly := mount.ReadOnly == nil || *mount.ReadOnly
		if mount.Status != "mounted" || readOnly || path.Clean(mount.RemotePath) != "/115open/emby" {
			continue
		}
		return filepath.Clean(mount.MountPath), nil
	}
	return "", fmt.Errorf("no writable mounted CloudDrive2 path is bound to /115open/emby")
}

func (s *TaskQueueService) uploadViaCloudDrive(ctx context.Context, _ string, item domain.CrossDriveItem, account *domain.DriveAccount, parentCID, spoolPath string) (UploadResult, error) {
	provider := s.driveSvc.providers["115"].(*Provider115)
	source := UploadSource{Path: spoolPath, Name: item.Name, Size: item.Size, SHA1: item.SHA1, PreSHA1: item.PreSHA1}
	existing, err := provider.findDestinationFile(ctx, account, parentCID, item.Name)
	if err != nil {
		return UploadResult{}, err
	}
	if existing != nil {
		if err := verifyDestinationIdentity(existing, source, parentCID); err != nil {
			return UploadResult{}, fmt.Errorf("same-name destination conflict: %w", err)
		}
		return UploadResult{FileID: existing.FileID, Rapid: true}, nil
	}
	mountRoot, err := s.cloudDrive115Mount(ctx, account.ID)
	if err != nil {
		return UploadResult{}, err
	}
	return s.uploadViaCloudDriveAt(ctx, item, account, parentCID, spoolPath, mountRoot)
}

func (s *TaskQueueService) uploadViaCloudDriveAt(ctx context.Context, item domain.CrossDriveItem, account *domain.DriveAccount, parentCID, spoolPath, mountRoot string) (UploadResult, error) {
	relativeDirectory := filepath.Dir(item.RelativePath)
	if relativeDirectory == "." {
		relativeDirectory = ""
	}
	cleanRelative, err := cleanRelative(relativeDirectory, "CloudDrive2 upload directory")
	if err != nil {
		return UploadResult{}, err
	}
	return s.uploadViaCloudDriveDirectory(ctx, item, account, parentCID, spoolPath, filepath.Join(mountRoot, "_待整理", cleanRelative))
}

func (s *TaskQueueService) uploadViaCloudDriveDirectory(ctx context.Context, item domain.CrossDriveItem, account *domain.DriveAccount, parentCID, spoolPath, destinationDirectory string) (UploadResult, error) {
	provider := s.driveSvc.providers["115"].(*Provider115)
	source := UploadSource{Path: spoolPath, Name: item.Name, Size: item.Size, SHA1: item.SHA1, PreSHA1: item.PreSHA1}
	if err := waitForLocalDirectory(ctx, destinationDirectory); err != nil {
		return UploadResult{}, err
	}
	key := sha1.Sum([]byte(item.SourceFileID))
	stagingPath := filepath.Join(destinationDirectory, ".embymedia-quark-"+hex.EncodeToString(key[:])+".uploading")
	finalPath := filepath.Join(destinationDirectory, item.Name)
	if info, err := os.Lstat(finalPath); err == nil {
		return UploadResult{}, fmt.Errorf("CloudDrive2 destination %s already exists with unverified identity", finalPath)
	} else if !os.IsNotExist(err) {
		return UploadResult{}, err
	} else if info != nil {
		return UploadResult{}, fmt.Errorf("CloudDrive2 destination is not absent")
	}
	if info, err := os.Stat(stagingPath); err == nil {
		if !info.Mode().IsRegular() || info.Size() != item.Size {
			return UploadResult{}, fmt.Errorf("CloudDrive2 staging file has ambiguous size")
		}
		sha, err := hashPathSHA1(stagingPath)
		if err != nil {
			return UploadResult{}, err
		}
		if !strings.EqualFold(sha, item.SHA1) {
			return UploadResult{}, fmt.Errorf("CloudDrive2 staging file has ambiguous content")
		}
	} else if os.IsNotExist(err) {
		sourceFile, err := os.Open(spoolPath)
		if err != nil {
			return UploadResult{}, err
		}
		destinationFile, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			sourceFile.Close()
			return UploadResult{}, err
		}
		written, copyErr := io.CopyBuffer(destinationFile, sourceFile, make([]byte, 1<<20))
		syncErr := destinationFile.Sync()
		closeErr := destinationFile.Close()
		sourceFile.Close()
		if copyErr != nil {
			return UploadResult{}, copyErr
		}
		if written != item.Size {
			return UploadResult{}, fmt.Errorf("CloudDrive2 wrote %d of %d bytes", written, item.Size)
		}
		if syncErr != nil {
			return UploadResult{}, syncErr
		}
		if closeErr != nil {
			return UploadResult{}, closeErr
		}
	} else {
		return UploadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}
	if err := os.Rename(stagingPath, finalPath); err != nil {
		return UploadResult{}, fmt.Errorf("publish CloudDrive2 upload: %w", err)
	}
	return provider.waitForVerifiedDestination(ctx, account, parentCID, source, false, item.Size)
}

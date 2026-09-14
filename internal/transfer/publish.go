package transfer

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errFileIdentityMismatch = errors.New("file identity mismatch")

func CheckDestination(mountRoot, destination string, requiredFreeBytes int64) error {
	root, err := filepath.Abs(mountRoot)
	if err != nil {
		return err
	}
	clean, err := CleanDestination(destination)
	if err != nil {
		return err
	}
	canary := filepath.Join(root, ".embymedia-health-canary")
	if info, err := os.Stat(canary); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("canary is not a regular file")
		}
		return fmt.Errorf("CloudDrive2 mount canary is unavailable: %w", err)
	}
	workerCanary := filepath.Join(root, "_待整理", WorkerCanaryName)
	if exists, err := matchingRegularFile(workerCanary, 0, EmptySHA1); err != nil {
		return fmt.Errorf("NAS worker canary identity is invalid: %w", err)
	} else if !exists {
		return fmt.Errorf("NAS worker canary is unavailable")
	}
	directory, err := confinedPath(root, clean)
	if err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("CloudDrive2 destination is not a plain directory")
	}
	if err := unix.Access(directory, unix.W_OK); err != nil {
		return fmt.Errorf("CloudDrive2 destination is not writable: %w", err)
	}
	if requiredFreeBytes > 0 {
		var stat unix.Statfs_t
		if err := unix.Statfs(directory, &stat); err != nil {
			return err
		}
		free := int64(stat.Bavail) * int64(stat.Bsize)
		if free < requiredFreeBytes {
			return fmt.Errorf("CloudDrive2 destination has %d free bytes; %d required", free, requiredFreeBytes)
		}
	}
	return nil
}

func Publish(ctx context.Context, spoolPath, mountRoot, destination, sourceKey, name string, size int64, expectedSHA1 string) error {
	cleanDestination, err := CleanDestination(destination)
	if err != nil {
		return err
	}
	cleanName, err := CleanFileName(name)
	if err != nil {
		return err
	}
	if err := CheckDestination(mountRoot, cleanDestination, 0); err != nil {
		return err
	}
	root, err := filepath.Abs(mountRoot)
	if err != nil {
		return err
	}
	directory, err := confinedPath(root, cleanDestination)
	if err != nil {
		return err
	}
	finalPath := filepath.Join(directory, cleanName)
	digest := sha1.Sum([]byte(sourceKey))
	stagingPath := filepath.Join(directory, ".embymedia-quark-"+hex.EncodeToString(digest[:])+".uploading")
	if exists, err := matchingRegularFile(finalPath, size, expectedSHA1); err != nil {
		return fmt.Errorf("same-name destination conflict: %w", err)
	} else if exists {
		return nil
	}
	if exists, err := matchingRegularFile(stagingPath, size, expectedSHA1); err != nil {
		if !errors.Is(err, errFileIdentityMismatch) {
			return err
		}
		if removeErr := os.Remove(stagingPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove invalid transfer staging file: %w", removeErr)
		}
	} else if exists {
		return os.Rename(stagingPath, finalPath)
	}
	source, err := os.Open(spoolPath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := copyContext(ctx, target, source, make([]byte, 1<<20))
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil || written != size || syncErr != nil || closeErr != nil {
		_ = os.Remove(stagingPath)
		switch {
		case copyErr != nil:
			return copyErr
		case written != size:
			return fmt.Errorf("CloudDrive2 wrote %d of %d bytes", written, size)
		case syncErr != nil:
			return syncErr
		default:
			return closeErr
		}
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(stagingPath)
		return err
	}
	if actual, err := HashPath(stagingPath); err != nil {
		_ = os.Remove(stagingPath)
		return err
	} else if !strings.EqualFold(actual, expectedSHA1) {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("CloudDrive2 staging content identity changed")
	}
	return os.Rename(stagingPath, finalPath)
}

func matchingRegularFile(path string, size int64, expectedSHA1 string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return false, fmt.Errorf("%w: %s has an unexpected type or size", errFileIdentityMismatch, path)
	}
	actual, err := HashPath(path)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(actual, expectedSHA1) {
		return false, fmt.Errorf("%w: %s has unexpected content", errFileIdentityMismatch, path)
	}
	return true, nil
}

func confinedPath(root, relative string) (string, error) {
	joined := filepath.Join(root, relative)
	inside, err := filepath.Rel(root, joined)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("destination escapes the mount root")
	}
	return joined, nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader, buffer []byte) (int64, error) {
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			output, writeErr := destination.Write(buffer[:count])
			written += int64(output)
			if writeErr != nil {
				return written, writeErr
			}
			if output != count {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

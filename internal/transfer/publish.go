package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

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
	if exists, err := regularFileOfSize(workerCanary, 0); err != nil {
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

// Publish writes the spool straight to its final destination name. CloudDrive2
// uploads a closed FUSE file in the background and 115 lists "<name>**..uploading"
// until that upload finishes; renaming the file while its upload is still in
// flight leaves that in-progress object behind permanently, so the final name is
// written directly and the mount is never asked to rename it. 115 remains the
// authority on identity: the caller verifies the exact parent, name, size and
// SHA-1 through the 115 API after this returns.
func Publish(ctx context.Context, spoolPath, mountRoot, destination, name string, size int64, expectedSHA1 string, replace bool) error {
	cleanDestination, err := CleanDestination(destination)
	if err != nil {
		return err
	}
	cleanName, err := CleanFileName(name)
	if err != nil {
		return err
	}
	if err := CheckDestination(mountRoot, cleanDestination, size); err != nil {
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
	if replace {
		// The caller established that 115 has no verified object for this name,
		// for example because an earlier publication left a
		// "<name>**..uploading" placeholder behind. The mount still reports that
		// stale file at its full size, so it must be removed before the bytes are
		// written again.
		if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale destination %s: %w", cleanName, err)
		}
	} else {
		switch exists, err := regularFileOfSize(finalPath, size); {
		case err != nil:
			return fmt.Errorf("same-name destination conflict: %w", err)
		case exists:
			// A previous publication already reached the destination name. 115
			// owns the verdict on whether those bytes are the expected object.
			return nil
		}
	}
	source, err := os.Open(spoolPath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(finalPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := copyContext(ctx, target, source, make([]byte, 1<<20))
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil || written != size || syncErr != nil || closeErr != nil {
		_ = os.Remove(finalPath)
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
	info, err := os.Stat(finalPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		_ = os.Remove(finalPath)
		return fmt.Errorf("CloudDrive2 reported %d of %d bytes for %s", info.Size(), size, cleanName)
	}
	return nil
}

// regularFileOfSize reports whether path is a regular file of exactly size
// bytes. Content identity is deliberately left to the 115 API, so a published
// file is never read back through the FUSE mount.
func regularFileOfSize(path string, size int64) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return false, fmt.Errorf("%s has an unexpected type or size", path)
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

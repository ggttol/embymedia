package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func replacementMediaFiles(root string) (map[string]int64, error) {
	files := make(map[string]int64)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("replacement media path %s is a symbolic link", path)
		}
		if entry.IsDir() {
			return nil
		}
		if _, video := videoExtensions[strings.ToLower(filepath.Ext(path))]; !video {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = info.Size()
		return nil
	})
	return files, err
}

// CloudDrive can briefly serve the previous directory after a cloud rename.
// A canonical destination must expose the staged filenames and sizes, not just
// episode labels that may also exist in the previous release.
func waitForReplacementMount(ctx context.Context, root string, expected map[string]int64) error {
	if len(expected) == 0 {
		return fmt.Errorf("completed-pack mounted file inventory is empty")
	}
	deadline := time.Now().Add(autoFillMountWait)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		actual, err := replacementMediaFiles(root)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		matches := err == nil && len(actual) == len(expected)
		if matches {
			for path, size := range expected {
				if actualSize, exists := actual[path]; !exists || actualSize != size {
					matches = false
					break
				}
			}
		}
		if matches {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("canonical completed-pack files did not become visible within %s", autoFillMountWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

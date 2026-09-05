package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDatabase holds exclusive process ownership until the returned file closes.
// SQLite connections within the owner retain their own transactional locking.
func lockDatabase(path string) (*os.File, error) {
	if path == ":memory:" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		if parentErr != nil {
			return nil, parentErr
		}
		canonical = filepath.Join(parent, filepath.Base(path))
	} else if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(canonical+".owner.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open database ownership lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("database already owned or cannot be locked; use the running server's HTTP MCP endpoint instead of starting another process: %w", err)
	}
	return file, nil
}

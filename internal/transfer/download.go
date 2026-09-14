package transfer

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Checkpoint interface {
	Load() (map[int]int64, error)
	Save(index int, size int64) error
}

type OpenRange func(ctx context.Context, offset, length int64) (io.ReadCloser, error)
type ReportProgress func(downloaded int64) error

type DownloadOptions struct {
	Path         string
	Name         string
	Size         int64
	ExpectedSHA1 string
	SegmentSize  int64
	Connections  int
	Attempts     int
	BufferSize   int
	ReportEvery  time.Duration
}

type DownloadResult struct {
	Path       string
	SHA1       string
	PreSHA1    string
	Downloaded int64
}

func Download(ctx context.Context, options DownloadOptions, checkpoint Checkpoint, openRange OpenRange, report ReportProgress) (DownloadResult, error) {
	if options.Path == "" || options.Size < 0 || options.SegmentSize <= 0 || options.Connections < 1 || options.Attempts < 1 || options.BufferSize < 1 {
		return DownloadResult{}, fmt.Errorf("invalid segmented download configuration")
	}
	segments, err := checkpoint.Load()
	if err != nil {
		return DownloadResult{}, err
	}
	spoolExisted := true
	if info, statErr := os.Stat(options.Path); statErr != nil {
		if !os.IsNotExist(statErr) {
			return DownloadResult{}, statErr
		}
		spoolExisted = false
	} else if !info.Mode().IsRegular() {
		return DownloadResult{}, fmt.Errorf("transfer spool is not a regular file")
	}
	if !spoolExisted {
		segments = map[int]int64{}
	}
	file, err := os.OpenFile(options.Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return DownloadResult{}, err
	}
	defer file.Close()
	if err := file.Truncate(options.Size); err != nil {
		return DownloadResult{}, err
	}
	totalSegments := int((options.Size + options.SegmentSize - 1) / options.SegmentSize)
	downloaded := int64(0)
	pending := make([]int, 0, totalSegments)
	for index := range totalSegments {
		expected := min(options.SegmentSize, options.Size-int64(index)*options.SegmentSize)
		if size, ok := segments[index]; ok {
			if size != expected {
				return DownloadResult{}, fmt.Errorf("segment %d checkpoint size is ambiguous", index)
			}
			downloaded += size
			continue
		}
		pending = append(pending, index)
	}
	for index := range segments {
		if index < 0 || index >= totalSegments {
			return DownloadResult{}, fmt.Errorf("segment %d checkpoint is outside the source", index)
		}
	}
	if report != nil {
		if err := report(downloaded); err != nil {
			return DownloadResult{}, err
		}
	}
	if len(pending) > 0 {
		workerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		work := make(chan int)
		var workers sync.WaitGroup
		var mu sync.Mutex
		var failure error
		lastReport := time.Time{}
		for range options.Connections {
			workers.Add(1)
			go func() {
				defer workers.Done()
				buffer := make([]byte, options.BufferSize)
				for index := range work {
					if workerCtx.Err() != nil {
						return
					}
					start := int64(index) * options.SegmentSize
					length := min(options.SegmentSize, options.Size-start)
					err := downloadSegment(workerCtx, file, index, start, length, options.Attempts, buffer, openRange, func(count int64) error {
						mu.Lock()
						defer mu.Unlock()
						downloaded = min(options.Size, downloaded+count)
						if report == nil || time.Since(lastReport) < options.ReportEvery {
							return nil
						}
						lastReport = time.Now()
						return report(downloaded)
					})
					if err == nil {
						err = checkpoint.Save(index, length)
					}
					if err != nil {
						mu.Lock()
						if failure == nil {
							failure = fmt.Errorf("segment %d of %s failed after %d attempts: %w", index, options.Name, options.Attempts, err)
							cancel()
						}
						mu.Unlock()
						return
					}
				}
			}()
		}
		for _, index := range pending {
			select {
			case work <- index:
			case <-workerCtx.Done():
				break
			}
			if workerCtx.Err() != nil {
				break
			}
		}
		close(work)
		workers.Wait()
		if failure != nil {
			return DownloadResult{}, failure
		}
		if err := ctx.Err(); err != nil {
			return DownloadResult{}, err
		}
		if report != nil {
			if err := report(downloaded); err != nil {
				return DownloadResult{}, err
			}
		}
	}
	if err := file.Sync(); err != nil {
		return DownloadResult{}, err
	}
	sha, pre, err := Hashes(file, options.Size)
	if err != nil {
		return DownloadResult{}, err
	}
	if options.ExpectedSHA1 != "" && !equalFoldASCII(options.ExpectedSHA1, sha) {
		return DownloadResult{}, fmt.Errorf("source SHA-1 changed for %s", options.Name)
	}
	return DownloadResult{Path: options.Path, SHA1: sha, PreSHA1: pre, Downloaded: options.Size}, nil
}

func downloadSegment(ctx context.Context, file *os.File, index int, start, length int64, attempts int, buffer []byte, openRange OpenRange, report func(int64) error) error {
	written := int64(0)
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		body, err := openRange(ctx, start+written, length-written)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		lastErr = consumeRange(ctx, body, file, buffer, start, length, &written, report)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func consumeRange(ctx context.Context, body io.ReadCloser, file *os.File, buffer []byte, start, length int64, written *int64, report func(int64) error) error {
	closed := false
	defer func() {
		if !closed {
			_ = body.Close()
		}
	}()
	for *written < length {
		if err := ctx.Err(); err != nil {
			return err
		}
		wanted := int(min(int64(len(buffer)), length-*written))
		count, readErr := io.ReadFull(body, buffer[:wanted])
		if count > 0 {
			if _, err := file.WriteAt(buffer[:count], start+*written); err != nil {
				return err
			}
			*written += int64(count)
			if report != nil {
				if err := report(int64(count)); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				if *written == length {
					break
				}
				return fmt.Errorf("ranged response ended at %d of %d bytes", *written, length)
			}
			return readErr
		}
	}
	closeErr := body.Close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	return file.Sync()
}

func Hashes(file *os.File, size int64) (string, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	full := sha1.New()
	if _, err := io.Copy(full, file); err != nil {
		return "", "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	prefix := sha1.New()
	if _, err := io.CopyN(prefix, file, min(size, int64(128<<10))); err != nil && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	return stringsUpperHex(full.Sum(nil)), stringsUpperHex(prefix.Sum(nil)), nil
}

func HashPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha1.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return stringsUpperHex(hash.Sum(nil)), nil
}

func stringsUpperHex(value []byte) string {
	return strings.ToUpper(hex.EncodeToString(value))
}

func equalFoldASCII(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range len(left) {
		a, b := left[index], right[index]
		if a >= 'a' && a <= 'z' {
			a -= 'a' - 'A'
		}
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

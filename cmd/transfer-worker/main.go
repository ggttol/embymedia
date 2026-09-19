package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/transfer"
	"golang.org/x/sys/unix"
)

const (
	defaultSegmentSize = int64(10 << 20)
	defaultMinFree     = int64(10 << 30)
)

func main() {
	spoolDir := flag.String("spool-dir", envOr("EMBYMEDIA_WORKER_SPOOL_DIR", ""), "persistent local spool directory")
	mountRoot := flag.String("mount-root", envOr("EMBYMEDIA_WORKER_MOUNT_ROOT", ""), "CloudDrive2 mount of the 115 /emby directory")
	minimumFree := flag.Int64("min-free-bytes", envInt64("EMBYMEDIA_WORKER_MIN_FREE_BYTES", defaultMinFree), "free bytes retained in the local spool")
	accessHelper := flag.String("access-helper", envOr("EMBYMEDIA_WORKER_ACCESS_HELPER", ""), "root-owned helper allowed to grant one destination")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()
	encoder := json.NewEncoder(os.Stdout)
	if err := run(ctx, encoder, *spoolDir, *mountRoot, *accessHelper, *minimumFree, os.Stdin); err != nil {
		_ = encoder.Encode(transfer.Event{Version: transfer.ProtocolVersion, Type: "error", Error: err.Error()})
		os.Exit(1)
	}
}

func run(ctx context.Context, encoder *json.Encoder, spoolDir, mountRoot, accessHelper string, minimumFree int64, input io.Reader) error {
	if !filepath.IsAbs(spoolDir) || !filepath.IsAbs(mountRoot) || (accessHelper != "" && !filepath.IsAbs(accessHelper)) || minimumFree < 0 {
		return fmt.Errorf("worker spool, mount root, access helper, and minimum free space are invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(input, 1<<20))
	decoder.DisallowUnknownFields()
	var request transfer.Request
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("decode transfer request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("transfer request must contain one JSON object")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	emit := func(event transfer.Event) error {
		event.Version = transfer.ProtocolVersion
		return encoder.Encode(event)
	}
	if request.Operation == transfer.OperationCheck {
		if err := checkSpool(spoolDir, request.RequiredFreeBytes); err != nil {
			return err
		}
		if err := ensureAccess(ctx, accessHelper, request.Destination); err != nil {
			return err
		}
		if err := transfer.CheckDestination(mountRoot, request.Destination, request.RequiredFreeBytes); err != nil {
			return err
		}
		return emit(transfer.Event{Type: "completed", Phase: "checked"})
	}
	key := transfer.SourceKey(request.AccountID, request.SourceFileID)
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(spoolDir, key+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("source transfer is already running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	spoolPath := filepath.Join(spoolDir, key+".part")
	checkpoint := transfer.NewFileCheckpoint(filepath.Join(spoolDir, key+".json"), key, request.SourceRevision, request.Size, defaultSegmentSize)
	if request.Operation == transfer.OperationCommit {
		if err := os.Remove(spoolPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := checkpoint.Remove(); err != nil {
			return err
		}
		return emit(transfer.Event{Type: "completed", Phase: "committed"})
	}
	if request.Operation == transfer.OperationPublish {
		if err := ensureAccess(ctx, accessHelper, request.Destination); err != nil {
			return err
		}
		if err := transfer.CheckDestination(mountRoot, request.Destination, request.Size); err != nil {
			return err
		}
		actual, err := transfer.HashPath(spoolPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, request.ExpectedSHA1) {
			return fmt.Errorf("worker spool identity changed before publish")
		}
		if err := emit(transfer.Event{Type: "progress", Phase: "uploading", DownloadedBytes: request.Size, Size: request.Size, SHA1: request.ExpectedSHA1}); err != nil {
			return err
		}
		if err := transfer.Publish(ctx, spoolPath, mountRoot, request.Destination, request.Name, request.Size, request.ExpectedSHA1, request.Replace); err != nil {
			return err
		}
		return emit(transfer.Event{Type: "completed", Phase: "published", DownloadedBytes: request.Size, Size: request.Size, SHA1: request.ExpectedSHA1})
	}
	if err := checkSpool(spoolDir, request.Size+minimumFree); err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 10 * time.Minute}
	provider := service.NewProviderQuark(client)
	account := domain.DriveAccount{ID: request.AccountID, Type: "quark", Cookie: request.QuarkCookie, Status: "active"}
	result, err := transfer.Download(ctx, transfer.DownloadOptions{
		Path: spoolPath, Name: request.Name, Size: request.Size, ExpectedSHA1: request.ExpectedSHA1,
		SegmentSize: defaultSegmentSize, Connections: request.Connections, Attempts: 4, BufferSize: 1 << 20, ReportEvery: time.Second,
	}, checkpoint, quarkRangeDownloader(provider, account, request.SourceFileID), func(downloaded int64) error {
		return emit(transfer.Event{Type: "progress", Phase: "downloading", DownloadedBytes: downloaded, Size: request.Size})
	})
	if err != nil {
		return err
	}
	return emit(transfer.Event{Type: "completed", Phase: "downloaded", DownloadedBytes: request.Size, Size: request.Size, SHA1: result.SHA1, PreSHA1: result.PreSHA1})
}

// Each worker invocation owns its cookie; parallel ranges share a single refresh.
func quarkRangeDownloader(provider *service.ProviderQuark, account domain.DriveAccount, fileID string) transfer.OpenRange {
	var mu sync.Mutex
	return func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
		mu.Lock()
		snapshot := account
		mu.Unlock()
		body, err := provider.OpenDownload(ctx, &snapshot, fileID, offset, length)
		var providerErr *service.ProviderHTTPError
		if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusPreconditionFailed {
			return body, err
		}
		mu.Lock()
		if account.Cookie == snapshot.Cookie {
			err = provider.RefreshDownloadCookie(ctx, &account)
		} else {
			err = nil
		}
		snapshot = account
		mu.Unlock()
		if err != nil {
			return nil, err
		}
		return provider.OpenDownload(ctx, &snapshot, fileID, offset, length)
	}
}

func ensureAccess(ctx context.Context, helper, destination string) error {
	if helper == "" {
		return nil
	}
	command := exec.CommandContext(ctx, "sudo", "-n", helper)
	command.Stdin = strings.NewReader(destination + "\n")
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("grant NAS transfer destination access: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

type limitedOutput struct {
	data []byte
}

func (o *limitedOutput) Write(value []byte) (int, error) {
	const limit = 8 << 10
	accepted := min(len(value), max(0, limit-len(o.data)))
	o.data = append(o.data, value[:accepted]...)
	return len(value), nil
}

func (o *limitedOutput) String() string {
	return string(o.data)
}

func checkSpool(directory string, required int64) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(directory, &stat); err != nil {
		return err
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free < required {
		return fmt.Errorf("worker spool has %d free bytes; %d required", free, required)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt64(name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	var parsed int64
	if _, err := fmt.Sscan(value, &parsed); err != nil {
		return -1
	}
	return parsed
}

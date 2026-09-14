package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/embymedia/embymedia/internal/transfer"
)

var sshTargetPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+$`)

type transferWorker interface {
	Check(ctx context.Context, jobID, destination string, requiredFreeBytes int64) error
	Download(ctx context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error)
	Publish(ctx context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error)
	Commit(ctx context.Context, jobID, accountID, sourceFileID string) error
}

type NASWorkerClient struct {
	target         string
	port           int
	identityFile   string
	knownHostsFile string
}

func NASWorkerFromEnvironment() (*NASWorkerClient, error) {
	target := strings.TrimSpace(os.Getenv("EMBYMEDIA_NAS_WORKER_SSH_TARGET"))
	if target == "" {
		return nil, nil
	}
	port := 22
	if raw := strings.TrimSpace(os.Getenv("EMBYMEDIA_NAS_WORKER_SSH_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return nil, fmt.Errorf("EMBYMEDIA_NAS_WORKER_SSH_PORT must be between 1 and 65535")
		}
		port = parsed
	}
	client := &NASWorkerClient{
		target:         target,
		port:           port,
		identityFile:   strings.TrimSpace(os.Getenv("EMBYMEDIA_NAS_WORKER_SSH_IDENTITY_FILE")),
		knownHostsFile: strings.TrimSpace(os.Getenv("EMBYMEDIA_NAS_WORKER_SSH_KNOWN_HOSTS_FILE")),
	}
	if !sshTargetPattern.MatchString(client.target) {
		return nil, fmt.Errorf("EMBYMEDIA_NAS_WORKER_SSH_TARGET must be user@host")
	}
	if err := validateWorkerFile(client.identityFile, true); err != nil {
		return nil, fmt.Errorf("NAS worker identity file: %w", err)
	}
	if err := validateWorkerFile(client.knownHostsFile, false); err != nil {
		return nil, fmt.Errorf("NAS worker known-hosts file: %w", err)
	}
	return client, nil
}

func validateWorkerFile(path string, private bool) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("private key permissions must not grant group or other access")
	}
	return nil
}

func (c *NASWorkerClient) Check(ctx context.Context, jobID, destination string, requiredFreeBytes int64) error {
	_, err := c.run(ctx, transfer.Request{
		Version: transfer.ProtocolVersion, Operation: transfer.OperationCheck, JobID: jobID,
		Destination: destination, RequiredFreeBytes: requiredFreeBytes,
	}, nil)
	return err
}

func (c *NASWorkerClient) Download(ctx context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error) {
	request.Version = transfer.ProtocolVersion
	request.Operation = transfer.OperationDownload
	return c.run(ctx, request, progress)
}

func (c *NASWorkerClient) Publish(ctx context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error) {
	request.Version = transfer.ProtocolVersion
	request.Operation = transfer.OperationPublish
	return c.run(ctx, request, progress)
}

func (c *NASWorkerClient) Commit(ctx context.Context, jobID, accountID, sourceFileID string) error {
	_, err := c.run(ctx, transfer.Request{
		Version: transfer.ProtocolVersion, Operation: transfer.OperationCommit, JobID: jobID,
		AccountID: accountID, SourceFileID: sourceFileID,
	}, nil)
	return err
}

func (c *NASWorkerClient) run(ctx context.Context, request transfer.Request, progress func(transfer.Event) error) (transfer.Event, error) {
	if err := request.Validate(); err != nil {
		return transfer.Event{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return transfer.Event{}, err
	}
	encoded = append(encoded, '\n')
	arguments := []string{
		"-T", "-p", strconv.Itoa(c.port), "-i", c.identityFile,
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + c.knownHostsFile, "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
		c.target, "transfer",
	}
	command := exec.CommandContext(ctx, "/usr/bin/ssh", arguments...)
	command.Stdin = bytes.NewReader(encoded)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return transfer.Event{}, err
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return transfer.Event{}, fmt.Errorf("start NAS transfer worker: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var completed transfer.Event
	var workerError string
	for scanner.Scan() {
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		var event transfer.Event
		if err := decoder.Decode(&event); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return transfer.Event{}, fmt.Errorf("decode NAS worker event: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			_ = command.Process.Kill()
			_ = command.Wait()
			return transfer.Event{}, fmt.Errorf("NAS worker event must contain one JSON object")
		}
		if event.Version != transfer.ProtocolVersion {
			_ = command.Process.Kill()
			_ = command.Wait()
			return transfer.Event{}, fmt.Errorf("NAS worker returned protocol version %d", event.Version)
		}
		switch event.Type {
		case "progress":
			if progress != nil {
				if err := progress(event); err != nil {
					_ = command.Process.Kill()
					_ = command.Wait()
					return transfer.Event{}, err
				}
			}
		case "completed":
			if completed.Type != "" {
				_ = command.Process.Kill()
				_ = command.Wait()
				return transfer.Event{}, fmt.Errorf("NAS worker returned multiple completion events")
			}
			completed = event
		case "error":
			workerError = strings.TrimSpace(event.Error)
		default:
			_ = command.Process.Kill()
			_ = command.Wait()
			return transfer.Event{}, fmt.Errorf("NAS worker returned unknown event type %q", event.Type)
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return transfer.Event{}, err
	}
	if scanErr != nil {
		return transfer.Event{}, fmt.Errorf("read NAS worker output: %w", scanErr)
	}
	if workerError != "" {
		return transfer.Event{}, fmt.Errorf("NAS worker: %s", workerError)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return transfer.Event{}, fmt.Errorf("NAS worker exited: %w", waitErr)
		}
		return transfer.Event{}, fmt.Errorf("NAS worker exited: %w: %s", waitErr, message)
	}
	if completed.Type != "completed" {
		return transfer.Event{}, fmt.Errorf("NAS worker exited without a completion event")
	}
	return completed, nil
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	const limit = 64 << 10
	accepted := min(len(value), max(0, limit-len(b.data)))
	b.data = append(b.data, value[:accepted]...)
	return len(value), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

var _ io.Writer = (*limitedBuffer)(nil)

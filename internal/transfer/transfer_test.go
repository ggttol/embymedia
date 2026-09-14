package transfer

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadResumesOnlyUnfinishedSegments(t *testing.T) {
	directory := t.TempDir()
	content := []byte("abcdefghijkl")
	spool := filepath.Join(directory, "source.part")
	checkpoint := NewFileCheckpoint(filepath.Join(directory, "source.json"), "source", "revision", int64(len(content)), 4)
	firstRanges := make([]int64, 0)
	_, err := Download(context.Background(), DownloadOptions{
		Path: spool, Name: "source.bin", Size: int64(len(content)), SegmentSize: 4, Connections: 1, Attempts: 1, BufferSize: 2, ReportEvery: time.Millisecond,
	}, checkpoint, func(_ context.Context, offset, length int64) (io.ReadCloser, error) {
		firstRanges = append(firstRanges, offset)
		if offset >= 4 {
			return nil, fmt.Errorf("temporary network failure")
		}
		return io.NopCloser(bytes.NewReader(content[offset : offset+length])), nil
	}, nil)
	if err == nil {
		t.Fatal("interrupted download unexpectedly succeeded")
	}
	if len(firstRanges) != 2 || firstRanges[0] != 0 || firstRanges[1] != 4 {
		t.Fatalf("unexpected first ranges: %v", firstRanges)
	}
	secondRanges := make([]int64, 0)
	result, err := Download(context.Background(), DownloadOptions{
		Path: spool, Name: "source.bin", Size: int64(len(content)), SegmentSize: 4, Connections: 1, Attempts: 1, BufferSize: 2, ReportEvery: time.Millisecond,
	}, checkpoint, func(_ context.Context, offset, length int64) (io.ReadCloser, error) {
		secondRanges = append(secondRanges, offset)
		return io.NopCloser(bytes.NewReader(content[offset : offset+length])), nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondRanges) != 2 || secondRanges[0] != 4 || secondRanges[1] != 8 {
		t.Fatalf("completed segment was downloaded again: %v", secondRanges)
	}
	written, err := os.ReadFile(spool)
	if err != nil || !bytes.Equal(written, content) {
		t.Fatalf("spool mismatch: %q, %v", written, err)
	}
	digest := sha1.Sum(content)
	if result.SHA1 != strings.ToUpper(hex.EncodeToString(digest[:])) {
		t.Fatalf("unexpected SHA-1 %s", result.SHA1)
	}
}

func TestPublishIsIdempotentAndRejectsChangedDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "_待整理")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".embymedia-health-canary"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, WorkerCanaryName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	content := []byte("verified content")
	spool := filepath.Join(t.TempDir(), "source.part")
	if err := os.WriteFile(spool, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(content)
	sha := strings.ToUpper(hex.EncodeToString(digest[:]))
	if err := Publish(context.Background(), spool, root, "_待整理", "movie.mkv", int64(len(content)), sha, false); err != nil {
		t.Fatal(err)
	}
	if err := Publish(context.Background(), spool, root, "_待整理", "movie.mkv", int64(len(content)), sha, false); err != nil {
		t.Fatalf("idempotent publish failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "movie.mkv"), bytes.Repeat([]byte{'x'}, len(content)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Publish(context.Background(), spool, root, "_待整理", "movie.mkv", int64(len(content)), sha, false); err == nil || !strings.Contains(err.Error(), "same-name destination conflict") {
		t.Fatalf("changed destination was accepted: %v", err)
	}
	if err := Publish(context.Background(), spool, root, "_待整理", "movie.mkv", int64(len(content)), sha, true); err != nil {
		t.Fatalf("replace publish failed: %v", err)
	}
	if written, err := os.ReadFile(filepath.Join(destination, "movie.mkv")); err != nil || !bytes.Equal(written, content) {
		t.Fatalf("replace did not rewrite the destination: %q, %v", written, err)
	}
}

func TestRequestRejectsDestinationTraversal(t *testing.T) {
	request := Request{Version: ProtocolVersion, Operation: OperationCheck, JobID: "job", Destination: "../outside"}
	if err := request.Validate(); err == nil {
		t.Fatal("destination traversal was accepted")
	}
}

package transfer

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	WorkerCanaryName = ".embymedia-nas-worker-health-canary"
	EmptySHA1        = "DA39A3EE5E6B4B0D3255BFEF95601890AFD80709"
)
const ProtocolVersion = 1

const (
	OperationCheck    = "check"
	OperationDownload = "download"
	OperationPublish  = "publish"
	OperationCommit   = "commit"
)

type Request struct {
	Version           int    `json:"version"`
	Operation         string `json:"operation"`
	JobID             string `json:"job_id"`
	AccountID         string `json:"account_id,omitempty"`
	SourceFileID      string `json:"source_file_id,omitempty"`
	SourceRevision    string `json:"source_revision,omitempty"`
	Name              string `json:"name,omitempty"`
	Size              int64  `json:"size,omitempty"`
	ExpectedSHA1      string `json:"expected_sha1,omitempty"`
	QuarkCookie       string `json:"quark_cookie,omitempty"`
	Destination       string `json:"destination,omitempty"`
	Connections       int    `json:"connections,omitempty"`
	RequiredFreeBytes int64  `json:"required_free_bytes,omitempty"`
}

type Event struct {
	Version         int    `json:"version"`
	Type            string `json:"type"`
	Phase           string `json:"phase,omitempty"`
	DownloadedBytes int64  `json:"downloaded_bytes,omitempty"`
	Size            int64  `json:"size,omitempty"`
	SHA1            string `json:"sha1,omitempty"`
	PreSHA1         string `json:"pre_sha1,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (r Request) Validate() error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("unsupported transfer protocol version %d", r.Version)
	}
	if strings.TrimSpace(r.JobID) == "" || len(r.JobID) > 200 {
		return fmt.Errorf("job_id is required and must not exceed 200 bytes")
	}
	if r.RequiredFreeBytes < 0 {
		return fmt.Errorf("required_free_bytes must not be negative")
	}
	switch r.Operation {
	case OperationCheck:
		_, err := CleanDestination(r.Destination)
		return err
	case OperationCommit:
		if strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.SourceFileID) == "" {
			return fmt.Errorf("account_id and source_file_id are required for commit")
		}
		return nil
	case OperationDownload:
		if strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.SourceFileID) == "" || strings.TrimSpace(r.QuarkCookie) == "" {
			return fmt.Errorf("account_id, source_file_id, and quark_cookie are required for download")
		}
		if r.Size < 0 {
			return fmt.Errorf("size must not be negative")
		}
		if r.Connections < 1 || r.Connections > 8 {
			return fmt.Errorf("connections must be between 1 and 8")
		}
		_, err := CleanFileName(r.Name)
		return err
	case OperationPublish:
		if strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.SourceFileID) == "" || r.Size < 0 || !ValidSHA1(r.ExpectedSHA1) {
			return fmt.Errorf("account_id, source_file_id, size, and expected_sha1 are required for publish")
		}
		if _, err := CleanFileName(r.Name); err != nil {
			return err
		}
		_, err := CleanDestination(r.Destination)
		return err
	default:
		return fmt.Errorf("operation must be check, download, publish, or commit")
	}
}

func CleanDestination(value string) (string, error) {
	if strings.ContainsRune(value, 0) || filepath.IsAbs(value) {
		return "", fmt.Errorf("destination must be a relative path")
	}
	clean := filepath.Clean(strings.TrimSpace(value))
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("destination must stay below the mount root")
	}
	return clean, nil
}

func CleanFileName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\x00/\\") || filepath.Base(name) != name {
		return "", fmt.Errorf("name must be one filesystem component")
	}
	return name, nil
}

func ValidSHA1(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

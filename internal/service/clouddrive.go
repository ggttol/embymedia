package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type CloudDriveService struct {
	db     *storage.DB
	client *http.Client
}

func NewCloudDriveService(db *storage.DB) *CloudDriveService {
	return &CloudDriveService{
		db: db,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (s *CloudDriveService) getBaseURL() string {
	val, err := s.db.GetConfig("clouddrive_url")
	if err != nil || val == "" {
		val = "http://127.0.0.1:19798"
	}
	return strings.TrimRight(val, "/")
}

// GetMounts checks local / mounted filesystem paths or queries CD2
func (s *CloudDriveService) GetMounts() ([]domain.CloudDriveMount, error) {
	mountPath, err := s.db.GetConfig("mount_path")
	if err != nil || mountPath == "" {
		mountPath = "/Volumes/CloudNAS"
	}

	status := "offline"
	if fi, err := os.Stat(mountPath); err == nil && fi.IsDir() {
		status = "mounted"
	}

	res := []domain.CloudDriveMount{
		{
			MountPath:  mountPath,
			RemotePath: "115://",
			Status:     status,
			TotalSpace: 1024 * 1024 * 1024 * 1024 * 10, // 10TB
			FreeSpace:  1024 * 1024 * 1024 * 1024 * 5,
			UpdatedAt:  time.Now(),
		},
	}

	// Also try querying CloudDrive2 API if reachable
	reqURL := fmt.Sprintf("%s/api/system/status", s.getBaseURL())
	if resp, err := s.client.Get(reqURL); err == nil {
		defer resp.Body.Close()
		var data map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&data)
	}

	return res, nil
}

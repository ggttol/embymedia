package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/clouddrivepb"
	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

const cloudDriveTimeout = 10 * time.Second

// CloudDriveHealth is the live server state returned by CloudDrive2.
type CloudDriveHealth struct {
	SystemReady    bool   `json:"system_ready"`
	LoggedIn       bool   `json:"logged_in"`
	UserName       string `json:"user_name,omitempty"`
	Message        string `json:"message,omitempty"`
	ProductName    string `json:"product_name,omitempty"`
	ProductVersion string `json:"product_version,omitempty"`
	APIVersion     string `json:"api_version,omitempty"`
}

// CloudDriveService talks to the CloudDrive2 gRPC API and verifies mounted filesystems locally.
type CloudDriveService struct {
	db *storage.DB
}

// NewCloudDriveService creates a CloudDrive2 service backed by persisted settings.
func NewCloudDriveService(db *storage.DB) *CloudDriveService {
	return &CloudDriveService{db: db}
}

func (s *CloudDriveService) setting(key string) string {
	value, _ := s.db.GetSetting(key)
	if value == "" {
		value, _ = s.db.GetConfig(key)
	}
	return strings.TrimSpace(value)
}

func (s *CloudDriveService) endpoint() (*url.URL, error) {
	raw := s.setting("clouddrive_url")
	if raw == "" {
		raw = "http://127.0.0.1:19798"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid CloudDrive2 URL %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("CloudDrive2 URL must use http or https")
	}
	return parsed, nil
}

func (s *CloudDriveService) client() (clouddrivepb.CloudDriveFileSrvClient, *grpc.ClientConn, error) {
	endpoint, err := s.endpoint()
	if err != nil {
		return nil, nil, err
	}
	var transport credentials.TransportCredentials
	if endpoint.Scheme == "https" {
		transport = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: endpoint.Hostname()})
	} else {
		transport = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(endpoint.Host, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, nil, fmt.Errorf("create CloudDrive2 gRPC client: %w", err)
	}
	return clouddrivepb.NewCloudDriveFileSrvClient(conn), conn, nil
}

func (s *CloudDriveService) authorizedContext(ctx context.Context) context.Context {
	if existing, ok := metadata.FromOutgoingContext(ctx); ok && len(existing.Get("authorization")) > 0 {
		return ctx
	}
	if token := s.setting("clouddrive_api_token"); token != "" {
		return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	return ctx
}

// Health reads CloudDrive2's public system state and authorized runtime version when configured.
func (s *CloudDriveService) Health(ctx context.Context) (CloudDriveHealth, error) {
	client, conn, err := s.client()
	if err != nil {
		return CloudDriveHealth{}, err
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(ctx, cloudDriveTimeout)
	defer cancel()
	info, err := client.GetSystemInfo(callCtx, &emptypb.Empty{})
	if err != nil {
		return CloudDriveHealth{}, fmt.Errorf("read CloudDrive2 system state: %w", err)
	}
	health := CloudDriveHealth{
		SystemReady: info.GetSystemReady(),
		LoggedIn:    info.GetIsLogin(),
		UserName:    info.GetUserName(),
		Message:     info.GetSystemMessage(),
	}
	if s.setting("clouddrive_api_token") == "" {
		return health, nil
	}
	runtime, err := client.GetRuntimeInfo(s.authorizedContext(callCtx), &emptypb.Empty{})
	if err != nil {
		return CloudDriveHealth{}, fmt.Errorf("read CloudDrive2 runtime state: %w", err)
	}
	health.ProductName = runtime.GetProductName()
	health.ProductVersion = runtime.GetProductVersion()
	health.APIVersion = runtime.GetCloudApiVersion()
	return health, nil
}

func filesystemCapacity(path string) (int64, int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize), nil
}

func mountedFilesystem(path string) bool {
	clean := filepath.Clean(path)
	parent := filepath.Dir(clean)
	if parent == clean {
		return true
	}
	var current, parentStat unix.Stat_t
	return unix.Stat(clean, &current) == nil && unix.Stat(parent, &parentStat) == nil && current.Dev != parentStat.Dev
}

func localMount(path, source, name string, readOnly, autoMount, reportedMounted, providerKnown bool, failReason string) domain.CloudDriveMount {
	status := "unmounted"
	mounted := mountedFilesystem(path)
	var total, free int64
	if mounted {
		status = "mounted"
		if observedTotal, observedFree, err := filesystemCapacity(path); err == nil {
			total, free = observedTotal, observedFree
		} else {
			status = "error"
			failReason = err.Error()
		}
	} else if reportedMounted {
		status = "error"
		if failReason == "" {
			failReason = "CloudDrive2 reports mounted but the filesystem is not mounted"
		}
	}
	result := domain.CloudDriveMount{Name: name, MountPath: path, RemotePath: source, Status: status, TotalSpace: total, FreeSpace: free, Error: failReason, UpdatedAt: time.Now()}
	if providerKnown {
		result.ReadOnly = &readOnly
		result.AutoMount = &autoMount
	}
	return result
}

// GetMounts returns CloudDrive2's configured mount points and verifies each local mount.
func (s *CloudDriveService) GetMounts(ctx context.Context) ([]domain.CloudDriveMount, error) {
	if s.setting("clouddrive_api_token") == "" {
		path := s.setting("clouddrive_mount_path")
		if path == "" {
			return nil, fmt.Errorf("CloudDrive2 API token and mount path are not configured")
		}
		return []domain.CloudDriveMount{localMount(path, s.setting("clouddrive_source_path"), "", false, false, false, false, "")}, nil
	}
	client, conn, err := s.client()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(s.authorizedContext(ctx), cloudDriveTimeout)
	defer cancel()
	result, err := client.GetMountPoints(callCtx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("list CloudDrive2 mounts: %w", err)
	}
	mounts := make([]domain.CloudDriveMount, 0, len(result.GetMountPoints()))
	for _, mount := range result.GetMountPoints() {
		mounts = append(mounts, localMount(
			mount.GetMountPoint(), mount.GetSourceDir(), mount.GetName(), mount.GetReadOnly(), mount.GetAutoMount(), mount.GetIsMounted(), true, mount.GetFailReason(),
		))
	}
	return mounts, nil
}

// RemountStep records the provider effects applied to one mount point.
type RemountStep struct {
	MountPath  string `json:"mount_path"`
	WasMounted bool   `json:"was_mounted"`
	Unmounted  bool   `json:"unmounted"`
	Mounted    bool   `json:"mounted"`
	Error      string `json:"error,omitempty"`
}

// RemountReport accounts for completed and failed remount effects.
type RemountReport struct {
	Steps  []RemountStep            `json:"steps"`
	Mounts []domain.CloudDriveMount `json:"mounts,omitempty"`
}

// Remount unmounts and mounts configured CloudDrive2 points and returns partial effects on failure.
func (s *CloudDriveService) Remount(ctx context.Context) (RemountReport, error) {
	report := RemountReport{Steps: make([]RemountStep, 0)}
	if s.setting("clouddrive_api_token") == "" {
		return report, fmt.Errorf("CloudDrive2 API token is required for remount")
	}
	client, conn, err := s.client()
	if err != nil {
		return report, err
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(s.authorizedContext(ctx), 40*time.Second)
	defer cancel()
	configured, err := client.GetMountPoints(callCtx, &emptypb.Empty{})
	if err != nil {
		return report, fmt.Errorf("list CloudDrive2 mounts before remount: %w", err)
	}
	if len(configured.GetMountPoints()) == 0 {
		return report, fmt.Errorf("CloudDrive2 has no configured mount points")
	}
	for _, mount := range configured.GetMountPoints() {
		step := RemountStep{MountPath: mount.GetMountPoint(), WasMounted: mount.GetIsMounted()}
		request := &clouddrivepb.MountPointRequest{MountPoint: mount.GetMountPoint()}
		if step.WasMounted {
			result, err := client.Unmount(callCtx, request)
			if err != nil {
				step.Error = err.Error()
				report.Steps = append(report.Steps, step)
				return report, fmt.Errorf("unmount %s: %w", step.MountPath, err)
			}
			if !result.GetSuccess() {
				step.Error = result.GetFailReason()
				report.Steps = append(report.Steps, step)
				return report, fmt.Errorf("unmount %s: %s", step.MountPath, step.Error)
			}
			step.Unmounted = true
		}
		result, err := client.Mount(callCtx, request)
		if err != nil {
			step.Error = err.Error()
			report.Steps = append(report.Steps, step)
			return report, fmt.Errorf("mount %s: %w", step.MountPath, err)
		}
		if !result.GetSuccess() {
			step.Error = result.GetFailReason()
			report.Steps = append(report.Steps, step)
			return report, fmt.Errorf("mount %s: %s", step.MountPath, step.Error)
		}
		step.Mounted = true
		report.Steps = append(report.Steps, step)
	}
	mounts, err := s.GetMounts(callCtx)
	if err != nil {
		return report, fmt.Errorf("verify CloudDrive2 mounts: %w", err)
	}
	report.Mounts = mounts
	for _, mount := range mounts {
		if mount.Status != "mounted" {
			return report, fmt.Errorf("verify CloudDrive2 mount %s: %s", mount.MountPath, mount.Status)
		}
	}
	return report, nil
}

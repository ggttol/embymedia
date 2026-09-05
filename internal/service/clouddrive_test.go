package service

import (
	"context"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/embymedia/embymedia/internal/clouddrivepb"
	"github.com/embymedia/embymedia/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeCloudDriveServer struct {
	clouddrivepb.UnimplementedCloudDriveFileSrvServer
	mountPath       string
	secondMountPath string
	failMountPath   string
	mu              sync.Mutex
	calls           []string
}

func requireCloudDriveToken(ctx context.Context) error {
	metadataValues, _ := metadata.FromIncomingContext(ctx)
	if values := metadataValues.Get("authorization"); len(values) != 1 || values[0] != "Bearer test-token" {
		return context.Canceled
	}
	return nil
}

func (s *fakeCloudDriveServer) GetSystemInfo(context.Context, *emptypb.Empty) (*clouddrivepb.CloudDriveSystemInfo, error) {
	return &clouddrivepb.CloudDriveSystemInfo{IsLogin: true, UserName: "operator", SystemReady: true}, nil
}

func (s *fakeCloudDriveServer) GetRuntimeInfo(ctx context.Context, _ *emptypb.Empty) (*clouddrivepb.RuntimeInfo, error) {
	if err := requireCloudDriveToken(ctx); err != nil {
		return nil, err
	}
	return &clouddrivepb.RuntimeInfo{ProductName: "CloudDrive2", ProductVersion: "1.0.14", CloudApiVersion: "1.0.14"}, nil
}

func (s *fakeCloudDriveServer) GetMountPoints(ctx context.Context, _ *emptypb.Empty) (*clouddrivepb.GetMountPointsResult, error) {
	if err := requireCloudDriveToken(ctx); err != nil {
		return nil, err
	}
	mounts := []*clouddrivepb.MountPoint{{MountPoint: s.mountPath, SourceDir: "115://Media", IsMounted: true, AutoMount: true}}
	if s.secondMountPath != "" {
		mounts = append(mounts, &clouddrivepb.MountPoint{MountPoint: s.secondMountPath, SourceDir: "115://Archive", IsMounted: true})
	}
	return &clouddrivepb.GetMountPointsResult{MountPoints: mounts}, nil
}

func (s *fakeCloudDriveServer) Mount(ctx context.Context, request *clouddrivepb.MountPointRequest) (*clouddrivepb.MountPointResult, error) {
	if err := requireCloudDriveToken(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls = append(s.calls, "mount:"+request.GetMountPoint())
	s.mu.Unlock()
	if request.GetMountPoint() == s.failMountPath {
		return &clouddrivepb.MountPointResult{Success: false, FailReason: "injected mount failure"}, nil
	}
	return &clouddrivepb.MountPointResult{Success: true}, nil
}

func (s *fakeCloudDriveServer) Unmount(ctx context.Context, request *clouddrivepb.MountPointRequest) (*clouddrivepb.MountPointResult, error) {
	if err := requireCloudDriveToken(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls = append(s.calls, "unmount:"+request.GetMountPoint())
	s.mu.Unlock()
	return &clouddrivepb.MountPointResult{Success: true}, nil
}

func TestLocalMountPathUsesConfiguredHostPathForProviderMount(t *testing.T) {
	configured := filepath.Join(string(filepath.Separator), "srv", "embymedia", "CloudNAS", "CloudDrive")
	if got := localMountPath("/CloudNAS/CloudDrive", configured); got != configured {
		t.Fatalf("local mount path = %q, want %q", got, configured)
	}
	if got := localMountPath("/CloudNAS/Archive", configured); got != "/CloudNAS/Archive" {
		t.Fatalf("unmatched provider mount path changed to %q", got)
	}
}

func TestLocalMountPathDistinguishesSameBasenameMounts(t *testing.T) {
	configured := "/srv/embymedia/data/clouddrive/CloudNAS/B/Media"
	if got := localMountPath("/CloudNAS/A/Media", configured); got != "/CloudNAS/A/Media" {
		t.Fatalf("mount A uses mount B's local path: %q", got)
	}
	if got := localMountPath("/CloudNAS/B/Media", configured); got != configured {
		t.Fatalf("mount B local path = %q, want %q", got, configured)
	}
}

func TestLocalMountPathBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, provider, configured, expected string
	}{
		{"unconfigured", "/CloudNAS/Media", "", "/CloudNAS/Media"},
		{"identical", "/CloudNAS/Media", "/CloudNAS/Media", "/CloudNAS/Media"},
		{"trailing separators", "/CloudNAS/Media/", "/srv/CloudNAS/Media/", "/srv/CloudNAS/Media/"},
		{"component boundary", "/CloudNAS/Media", "/srv/NotCloudNAS/Media", "/CloudNAS/Media"},
		{"relative provider", "CloudNAS/Media", "/srv/CloudNAS/Media", "CloudNAS/Media"},
		{"provider root", "/", "/srv/CloudNAS/Media", "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := localMountPath(test.provider, test.configured); got != test.expected {
				t.Fatalf("local mount path = %q, want %q", got, test.expected)
			}
		})
	}
}

func TestCloudDriveGRPCHealthAndRemount(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	provider := &fakeCloudDriveServer{mountPath: string(filepath.Separator)}
	clouddrivepb.RegisterCloudDriveFileSrvServer(server, provider)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"clouddrive_url": "http://" + listener.Addr().String(), "clouddrive_api_token": "test-token"}); err != nil {
		t.Fatalf("configure CloudDrive: %v", err)
	}
	service := NewCloudDriveService(db)
	health, err := service.Health(context.Background())
	if err != nil || !health.SystemReady || health.ProductVersion != "1.0.14" {
		t.Fatalf("unexpected CloudDrive health: %+v, err=%v", health, err)
	}
	report, err := service.Remount(context.Background())
	if err != nil || len(report.Mounts) != 1 || report.Mounts[0].RemotePath != "115://Media" || len(report.Steps) != 1 || !report.Steps[0].Mounted {
		t.Fatalf("unexpected remount report: %+v, err=%v", report, err)
	}
	provider.mu.Lock()
	calls := append([]string(nil), provider.calls...)
	provider.mu.Unlock()
	expected := []string{"unmount:" + provider.mountPath, "mount:" + provider.mountPath}
	if !slices.Equal(calls, expected) {
		t.Fatalf("unexpected remount calls: %v", calls)
	}
}

func TestCloudDriveRemountReportsPartialFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	provider := &fakeCloudDriveServer{mountPath: string(filepath.Separator), secondMountPath: "/second", failMountPath: "/second"}
	server := grpc.NewServer()
	clouddrivepb.RegisterCloudDriveFileSrvServer(server, provider)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	if err := db.SetSettings(map[string]string{"clouddrive_url": "http://" + listener.Addr().String(), "clouddrive_api_token": "test-token"}); err != nil {
		t.Fatalf("configure CloudDrive: %v", err)
	}
	report, err := NewCloudDriveService(db).Remount(context.Background())
	if err == nil || len(report.Steps) != 2 || report.Steps[1].Mounted || report.Steps[1].Error != "injected mount failure" {
		t.Fatalf("partial failure was not reported: %+v, err=%v", report, err)
	}
}

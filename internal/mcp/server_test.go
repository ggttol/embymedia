package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestAll18MCPToolsRegistered(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	cd := service.NewCloudDriveService(db)
	tq := service.NewTaskQueueService(db, drive, emby)

	mcpServer := NewMCPServer(db, drive, emby, cd, tq)
	if mcpServer == nil {
		t.Fatal("mcpServer is nil")
	}

	// Test calling a tool
	req := mcp.CallToolRequest{}
	req.Params.Name = "system_health"
	res, err := mcpServer.handleSystemHealth(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSystemHealth failed: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected non-empty tool result")
	}
}

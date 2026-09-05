package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
)

func newTestMCPServer(t *testing.T) *MCPServer {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close storage: %v", err)
		}
	})
	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	cd := service.NewCloudDriveService(db)
	tq := service.NewTaskQueueService(db, drive, emby)
	return NewMCPServer(db, drive, emby, cd, tq)
}

func TestAll18MCPToolsRegistered(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	if mcpServer == nil {
		t.Fatal("mcpServer is nil")
	}

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

func TestUnimplementedAgentActionsFailLoud(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_id": "123"}

	shareResult, err := mcpServer.handleC115GetShareLink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleC115GetShareLink failed: %v", err)
	}
	if !shareResult.IsError {
		t.Fatal("share-link action must report an MCP error")
	}
	text := shareResult.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "https://") {
		t.Fatalf("share-link action returned a fabricated URL: %s", text)
	}

	remountResult, err := mcpServer.handleCD2Remount(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleCD2Remount failed: %v", err)
	}
	if !remountResult.IsError {
		t.Fatal("remount action must report an MCP error")
	}
}

func TestTaskSubmitRejectsUnsupportedInput(t *testing.T) {
	mcpServer := newTestMCPServer(t)

	unsupported := mcp.CallToolRequest{}
	unsupported.Params.Arguments = map[string]any{"task_type": "organize_files", "payload": "{}"}
	result, err := mcpServer.handleTaskSubmit(context.Background(), unsupported)
	if err != nil {
		t.Fatalf("handleTaskSubmit failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("unsupported task type must report an MCP error")
	}

	invalidJSON := mcp.CallToolRequest{}
	invalidJSON.Params.Arguments = map[string]any{"task_type": "sync_library", "payload": "{"}
	result, err = mcpServer.handleTaskSubmit(context.Background(), invalidJSON)
	if err != nil {
		t.Fatalf("handleTaskSubmit failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("invalid task payload must report an MCP error")
	}
}

func TestTaskLogsReturnPersistedState(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	task, err := mcpServer.taskQueue.Enqueue("sync_library", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"task_id": task.ID}
	result, err := mcpServer.handleTaskGetLogs(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTaskGetLogs failed: %v", err)
	}
	if result.IsError {
		t.Fatal("persisted task state returned an MCP error")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"status": "pending"`) {
		t.Fatalf("task state did not contain persisted status: %s", text)
	}
	if strings.Contains(text, "Started") {
		t.Fatalf("task state contained fabricated execution logs: %s", text)
	}
}

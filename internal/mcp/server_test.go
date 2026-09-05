package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/security"
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
	return NewMCPServer(db, drive, emby, cd, tq, security.NewAgentAuthorizer(db), false)
}

func TestAll18MCPToolsRegistered(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	registered := mcpServer.Server().ListTools()
	expected := []string{
		"c115_list_files", "c115_search", "c115_save_share", "c115_move", "c115_rename", "c115_mkdir", "c115_get_share_link",
		"cd2_mount_status", "cd2_remount", "emby_refresh_library", "emby_get_libraries", "emby_inspect_item",
		"task_submit", "task_query", "task_cancel", "task_get_logs", "system_get_config", "system_health",
	}
	if len(registered) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(registered))
	}
	for _, name := range expected {
		if registered[name] == nil {
			t.Errorf("MCP tool %s is not registered", name)
		}
	}
}

func TestSystemHealthDoesNotCallUnconfiguredServicesHealthy(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	result, err := mcpServer.handleSystemHealth(context.Background(), mcp.CallToolRequest{})
	if err != nil || result.IsError {
		t.Fatalf("system health failed: result=%+v err=%v", result, err)
	}
	var health map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health["status"] == "healthy" {
		t.Fatalf("unconfigured system reported healthy: %+v", health)
	}
}

func TestTaskCancelCancelsPendingWork(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	task, err := mcpServer.taskQueue.Enqueue("emby_refresh", map[string]any{})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"task_id": task.ID}
	result, err := mcpServer.handleTaskCancel(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTaskCancel failed: %v", err)
	}
	if result.IsError {
		t.Fatal("pending task cancellation returned an MCP error")
	}
	stored, err := mcpServer.db.GetAsyncTask(task.ID)
	if err != nil || stored.Status != "cancelled" {
		t.Fatalf("task was not cancelled: %+v, err=%v", stored, err)
	}
}

func TestTaskSubmitRejectsUnsupportedInput(t *testing.T) {
	mcpServer := newTestMCPServer(t)

	unsupported := mcp.CallToolRequest{}
	unsupported.Params.Arguments = map[string]any{"task_type": "organize_files", "payload": map[string]any{}}
	result, err := mcpServer.handleTaskSubmit(context.Background(), unsupported)
	if err != nil {
		t.Fatalf("handleTaskSubmit failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("unsupported task type must report an MCP error")
	}

	invalidPayload := mcp.CallToolRequest{}
	invalidPayload.Params.Arguments = map[string]any{"task_type": "emby_refresh", "payload": "not an object"}
	result, err = mcpServer.handleTaskSubmit(context.Background(), invalidPayload)
	if err != nil {
		t.Fatalf("handleTaskSubmit failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("invalid task payload must report an MCP error")
	}
	for _, arguments := range []map[string]any{
		{"task_type": "c115_save_share", "payload": map[string]any{"url": "not-a-share"}},
		{"task_type": "c115_offline_download", "payload": map[string]any{"urls": []any{"garbage"}}},
	} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = arguments
		result, err := mcpServer.handleTaskSubmit(context.Background(), request)
		if err != nil || !result.IsError {
			t.Fatalf("invalid provider input was accepted: result=%+v err=%v", result, err)
		}
	}
	tasks, err := mcpServer.db.ListAsyncTasks("", 10)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("invalid task input was persisted: %+v, err=%v", tasks, err)
	}
}

func TestTaskLogsReturnPersistedState(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	task, err := mcpServer.taskQueue.Enqueue("emby_refresh", map[string]any{})
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
	if !strings.Contains(text, `"runs": []`) {
		t.Fatalf("pending task unexpectedly contained execution runs: %s", text)
	}
}

func TestSystemConfigRedactsSecrets(t *testing.T) {
	mcpServer := newTestMCPServer(t)
	if err := mcpServer.db.SetSettings(map[string]string{
		"115_cookie": "secret-cookie", "emby_api_key": "secret-key", "resource_api_token": "resource-secret",
		"clouddrive_api_token": "cloud-secret", "clouddrive_webhook_secret": "webhook-secret",
		"emby_url": "http://emby", "c115_cid_map": `{"电影":"123"}`,
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if err := mcpServer.db.SetConfig("provider_access_token", "legacy-secret", "legacy"); err != nil {
		t.Fatalf("save legacy config: %v", err)
	}
	result, err := mcpServer.handleSystemGetConfig(context.Background(), mcp.CallToolRequest{})
	if err != nil || result.IsError {
		t.Fatalf("get config: result=%+v err=%v", result, err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	for _, secret := range []string{"secret-cookie", "secret-key", "resource-secret", "cloud-secret", "webhook-secret", "legacy-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("system config exposed %q: %s", secret, text)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("decode config result: %v", err)
	}
	if cidMap, ok := decoded["c115_cid_map"].(map[string]any); !ok || cidMap["电影"] != "123" {
		t.Fatalf("CID map was not structured: %+v", decoded["c115_cid_map"])
	}
}

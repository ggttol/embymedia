package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MCPServer struct {
	server     *server.MCPServer
	db         *storage.DB
	drive      *service.DriveService
	emby       *service.EmbyService
	cloudDrive *service.CloudDriveService
	taskQueue  *service.TaskQueueService
}

func NewMCPServer(db *storage.DB, drive *service.DriveService, emby *service.EmbyService, cd *service.CloudDriveService, tq *service.TaskQueueService) *MCPServer {
	s := server.NewMCPServer(
		"EmbyMedia MCP Server",
		"2.0.0",
		server.WithToolCapabilities(true),
	)

	ms := &MCPServer{
		server:     s,
		db:         db,
		drive:      drive,
		emby:       emby,
		cloudDrive: cd,
		taskQueue:  tq,
	}

	ms.registerAll18Tools()
	return ms
}

func (m *MCPServer) Server() *server.MCPServer {
	return m.server
}

func (m *MCPServer) registerAll18Tools() {
	// 1. c115_list_files: 列出指定 CID 下文件
	m.server.AddTool(mcp.NewTool("c115_list_files",
		mcp.WithDescription("List files and folders in 115 Cloud Drive under a given CID"),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
		mcp.WithString("cid", mcp.Description("Category/Directory ID (0 for root)")),
	), m.handleC115ListFiles)

	// 2. c115_search: 搜索 115 文件
	m.server.AddTool(mcp.NewTool("c115_search",
		mcp.WithDescription("Search files inside 115 Cloud Drive by keyword"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Keyword to search for")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115Search)

	// 3. c115_save_share: 转存分享链接到指定 CID
	m.server.AddTool(mcp.NewTool("c115_save_share",
		mcp.WithDescription("Save a 115 share code/URL or offline magnet link to a target directory"),
		mcp.WithString("url", mcp.Required(), mcp.Description("115 share link, code, or magnet URL")),
		mcp.WithString("target_cid", mcp.Description("Target CID in 115")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115SaveShare)

	// 4. c115_move: 移动文件
	m.server.AddTool(mcp.NewTool("c115_move",
		mcp.WithDescription("Move files or folders to a target CID in 115"),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File or folder ID to move")),
		mcp.WithString("target_cid", mcp.Required(), mcp.Description("Destination directory CID")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115Move)

	// 5. c115_rename: 重命名文件
	m.server.AddTool(mcp.NewTool("c115_rename",
		mcp.WithDescription("Rename a file or folder in 115"),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File or folder ID")),
		mcp.WithString("new_name", mcp.Required(), mcp.Description("New file or folder name")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115Rename)

	// 6. c115_mkdir: 新建目录
	m.server.AddTool(mcp.NewTool("c115_mkdir",
		mcp.WithDescription("Create a new directory under parent CID in 115"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Directory name")),
		mcp.WithString("parent_cid", mcp.Description("Parent directory CID (0 for root)")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115Mkdir)

	// 7. c115_get_share_link: 报告当前提供方不支持分享链接
	m.server.AddTool(mcp.NewTool("c115_get_share_link",
		mcp.WithDescription("Report that 115 share-link generation is unavailable until a provider is configured"),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File or folder ID")),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
	), m.handleC115GetShareLink)

	// 8. cd2_mount_status: 查看挂载盘健康状态
	m.server.AddTool(mcp.NewTool("cd2_mount_status",
		mcp.WithDescription("Check CloudDrive2 virtual mount health, path and storage statistics"),
	), m.handleCD2MountStatus)

	// 9. cd2_remount: 报告当前提供方不支持重新挂载
	m.server.AddTool(mcp.NewTool("cd2_remount",
		mcp.WithDescription("Report that CloudDrive2 remount is unavailable until a provider is configured"),
	), m.handleCD2Remount)

	// 10. emby_refresh_library: 触发 Emby 媒体库刷新
	m.server.AddTool(mcp.NewTool("emby_refresh_library",
		mcp.WithDescription("Trigger media library scan and metadata refresh in Emby"),
		mcp.WithString("library_id", mcp.Description("Specific library ID (scans all if omitted)")),
	), m.handleEmbyRefreshLibrary)

	// 11. emby_get_libraries: 获取全部 Emby 媒体库信息
	m.server.AddTool(mcp.NewTool("emby_get_libraries",
		mcp.WithDescription("Get list of all Emby media libraries and their paths"),
	), m.handleEmbyGetLibraries)

	// 12. emby_inspect_item: 检查指定条目元数据与海报
	m.server.AddTool(mcp.NewTool("emby_inspect_item",
		mcp.WithDescription("Inspect Emby item metadata, artwork and playback info"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Emby item ID")),
	), m.handleEmbyInspectItem)

	// 13. task_submit: 提交异步后台流水线任务
	m.server.AddTool(mcp.NewTool("task_submit",
		mcp.WithDescription("Submit a supported asynchronous background task"),
		mcp.WithString("task_type", mcp.Required(), mcp.Description("Supported task type: sync_library")),
		mcp.WithString("payload", mcp.Description("JSON object with task parameters")),
	), m.handleTaskSubmit)

	// 14. task_query: 查询任务执行状态与完成百分比
	m.server.AddTool(mcp.NewTool("task_query",
		mcp.WithDescription("Query background task status, progress and results"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), m.handleTaskQuery)

	// 15. task_cancel: 报告任务队列不支持运行时取消
	m.server.AddTool(mcp.NewTool("task_cancel",
		mcp.WithDescription("Report that running task cancellation is unavailable"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), m.handleTaskCancel)

	// 16. task_get_logs: 读取持久化的任务状态和错误
	m.server.AddTool(mcp.NewTool("task_get_logs",
		mcp.WithDescription("Read persisted task status, progress and error details"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), m.handleTaskGetLogs)

	// 17. system_get_config: 读取系统配置与分类映射
	m.server.AddTool(mcp.NewTool("system_get_config",
		mcp.WithDescription("Get system settings, mount paths and directory category mappings"),
	), m.handleSystemGetConfig)

	// 18. system_health: 查看本地存储与挂载配置状态
	m.server.AddTool(mcp.NewTool("system_health",
		mcp.WithDescription("Inspect local storage and configured CloudDrive2 mount inventory"),
	), m.handleSystemHealth)
}

// Implement handlers for all 18 tools

func (m *MCPServer) handleC115ListFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	accID := req.GetString("account_id", "")
	cid := req.GetString("cid", "0")
	files, err := m.drive.ListFilesCtx(ctx, accID, cid)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list files failed: %v", err)), nil
	}
	b, _ := json.MarshalIndent(files, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleC115Search(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}
	accID := req.GetString("account_id", "")
	params := url.Values{"q": {q}, "disk_type": {"115"}}
	_ = accID
	resp, err := m.drive.SearchResources(params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}
	b, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleC115SaveShare(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	u, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("url is required"), nil
	}
	accID := req.GetString("account_id", "")
	targetCID := req.GetString("target_cid", "0")
	password := req.GetString("password", "")
	count, title, err := m.drive.SaveShare(accID, u, password, targetCID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save share failed: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Saved %d items from share %q into cid %s", count, title, targetCID)), nil
}

func (m *MCPServer) handleC115Move(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fileID, err := req.RequireString("file_id")
	if err != nil {
		return mcp.NewToolResultError("file_id is required"), nil
	}
	targetCID, err := req.RequireString("target_cid")
	if err != nil {
		return mcp.NewToolResultError("target_cid is required"), nil
	}
	accID := req.GetString("account_id", "")
	if err := m.drive.MoveCtx(ctx, accID, []string{fileID}, targetCID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("move failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Moved successfully"), nil
}

func (m *MCPServer) handleC115Rename(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fileID, err := req.RequireString("file_id")
	if err != nil {
		return mcp.NewToolResultError("file_id is required"), nil
	}
	newName, err := req.RequireString("new_name")
	if err != nil {
		return mcp.NewToolResultError("new_name is required"), nil
	}
	accID := req.GetString("account_id", "")
	if err := m.drive.RenameCtx(ctx, accID, fileID, newName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rename failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Renamed successfully"), nil
}

func (m *MCPServer) handleC115Mkdir(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	parentCID := req.GetString("parent_cid", "0")
	accID := req.GetString("account_id", "")
	cid, err := m.drive.MkdirCtx(ctx, accID, parentCID, name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("mkdir failed: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Directory created with CID: %s", cid)), nil
}

func (m *MCPServer) handleC115GetShareLink(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := req.RequireString("file_id"); err != nil {
		return mcp.NewToolResultError("file_id is required"), nil
	}
	return mcp.NewToolResultError("115 share-link generation is not implemented by the configured provider"), nil
}

func (m *MCPServer) handleCD2MountStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mounts, err := m.cloudDrive.GetMounts()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get mount status failed: %v", err)), nil
	}
	b, _ := json.MarshalIndent(mounts, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleCD2Remount(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError("CloudDrive2 remount is not implemented by the configured provider"), nil
}

func (m *MCPServer) handleEmbyRefreshLibrary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	libID := req.GetString("library_id", "")
	if err := m.emby.RefreshLibrary(libID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("emby refresh failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Emby library scan triggered"), nil
}

func (m *MCPServer) handleEmbyGetLibraries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	libs, err := m.emby.GetLibraries()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get libraries failed: %v", err)), nil
	}
	b, _ := json.MarshalIndent(libs, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleEmbyInspectItem(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	itemID, err := req.RequireString("item_id")
	if err != nil {
		return mcp.NewToolResultError("item_id is required"), nil
	}
	items, err := m.emby.SearchMedia(itemID, 1)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("inspect item failed: %v", err)), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("Emby item %s was not found", itemID)), nil
	}
	b, _ := json.MarshalIndent(items[0], "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleTaskSubmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskType, err := req.RequireString("task_type")
	if err != nil {
		return mcp.NewToolResultError("task_type is required"), nil
	}
	if taskType != "sync_library" {
		return mcp.NewToolResultError(fmt.Sprintf("unsupported task_type %q; supported type: sync_library", taskType)), nil
	}
	payloadStr := req.GetString("payload", "{}")
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("payload must be a JSON object: %v", err)), nil
	}

	asyncTask, err := m.taskQueue.Enqueue(taskType, payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("submit task failed: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"task_id": "%s", "status": "pending"}`, asyncTask.ID)), nil
}

func (m *MCPServer) handleTaskQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	task, err := m.db.GetAsyncTask(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("task not found: %v", err)), nil
	}
	b, _ := json.MarshalIndent(task, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleTaskCancel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	if _, err := m.db.GetAsyncTask(taskID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("task not found: %v", err)), nil
	}
	return mcp.NewToolResultError("running task cancellation is not implemented by the task queue"), nil
}

func (m *MCPServer) handleTaskGetLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	task, err := m.db.GetAsyncTask(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("task not found: %v", err)), nil
	}
	b, _ := json.MarshalIndent(task, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleSystemGetConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	settings, err := m.db.GetAllSettings()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get config failed: %v", err)), nil
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (m *MCPServer) handleSystemHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status := "healthy"
	health := map[string]any{
		"status":    status,
		"timestamp": time.Now(),
	}
	accs, err := m.db.ListAccounts()
	if err != nil {
		status = "degraded"
		health["database"] = err.Error()
	} else {
		health["database"] = "connected"
		health["configured_115_accounts"] = len(accs)
	}
	mounts, err := m.cloudDrive.GetMounts()
	if err != nil {
		status = "degraded"
		health["clouddrive2"] = err.Error()
	} else {
		health["clouddrive2"] = "connected"
		health["configured_mounts"] = len(mounts)
	}
	health["status"] = status
	b, _ := json.MarshalIndent(health, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

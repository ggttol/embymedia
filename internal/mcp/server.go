package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MCPServer struct {
	server         *server.MCPServer
	db             *storage.DB
	drive          *service.DriveService
	emby           *service.EmbyService
	cloudDrive     *service.CloudDriveService
	taskQueue      *service.TaskQueueService
	settings       *service.SettingsService
	authorizer     *security.AgentAuthorizer
	trustTokenless bool
}

func NewMCPServer(db *storage.DB, drive *service.DriveService, emby *service.EmbyService, cd *service.CloudDriveService, tq *service.TaskQueueService, authorizer *security.AgentAuthorizer, trustTokenless bool) *MCPServer {
	s := server.NewMCPServer(
		"EmbyMedia MCP Server",
		"2.0.0",
		server.WithToolCapabilities(true),
	)

	ms := &MCPServer{
		server: s, db: db, drive: drive, emby: emby, cloudDrive: cd, taskQueue: tq, settings: service.NewSettingsService(db), authorizer: authorizer, trustTokenless: trustTokenless,
	}
	s.Use(ms.auditToolCalls)

	ms.registerAll18Tools()
	return ms
}

func (m *MCPServer) Server() *server.MCPServer {
	return m.server
}

func toolRequiresWrite(name string) bool {
	switch name {
	case "c115_save_share", "c115_move", "c115_rename", "c115_mkdir", "c115_get_share_link", "cd2_remount", "emby_refresh_library", "task_submit", "task_cancel":
		return true
	default:
		return false
	}
}

func auditIP(header http.Header) string {
	if value := strings.TrimSpace(header.Get("X-Real-Ip")); value != "" {
		return value
	}
	value := strings.Split(header.Get("X-Forwarded-For"), ",")[0]
	return strings.TrimSpace(value)
}

func (m *MCPServer) auditToolCalls(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		caller := "ui"
		if m.trustTokenless {
			caller = "mcp-stdio"
		}
		tokenID := ""
		agentName := ""
		secret := security.BearerToken(request.Header)
		if secret == "" && !m.trustTokenless && !security.TrustedWithoutToken(request.Header) {
			message := "agent token is required"
			m.recordToolAudit(request, "", "", "denied", security.Summary(map[string]any{"error": message}, 1024), started)
			return mcp.NewToolResultError(message), nil
		}
		if secret != "" {
			token, err := m.authorizer.Authorize(secret, toolRequiresWrite(request.Params.Name), started)
			if token != nil {
				tokenID = token.ID
				agentName = token.Name
			}
			if err != nil {
				message := err.Error()
				if !errors.Is(err, security.ErrInvalidToken) && !errors.Is(err, security.ErrDisabledToken) && !errors.Is(err, security.ErrRateLimited) && !errors.Is(err, security.ErrWriteScope) && !errors.Is(err, security.ErrReadScope) {
					message = "authorize agent token"
				}
				m.recordToolAudit(request, tokenID, agentName, "denied", security.Summary(map[string]any{"error": message}, 1024), started)
				return mcp.NewToolResultError(message), nil
			}
			caller = "agent"
		}
		result, err := next(ctx, request)
		status := "success"
		output := map[string]any{"handler_error": err != nil}
		if result != nil {
			output["is_error"] = result.IsError
			output["content_items"] = len(result.Content)
			previews := make([]string, 0, min(len(result.Content), 3))
			for _, content := range result.Content {
				text, ok := content.(mcp.TextContent)
				if ok && len(previews) < 3 {
					previews = append(previews, security.TextSummary(text.Text, 512))
				}
			}
			output["previews"] = previews
		}
		if err != nil || result == nil || result.IsError {
			status = "error"
		}
		entry := &domain.AuditLog{
			Caller: caller, TokenID: tokenID, AgentName: agentName, Action: request.Params.Name,
			Target: "mcp/tools/call", Input: security.Summary(request.GetArguments(), 4096), Output: security.Summary(output, 2048),
			Status: status, LatencyMS: time.Since(started).Milliseconds(), IP: auditIP(request.Header), CreatedAt: time.Now(),
		}
		if persistErr := m.db.AddAuditLog(entry); persistErr != nil {
			log.Printf("persist MCP audit: %v", persistErr)
		}
		return result, err
	}
}

func (m *MCPServer) recordToolAudit(request mcp.CallToolRequest, tokenID, agentName, status, output string, started time.Time) {
	entry := &domain.AuditLog{
		Caller: "agent", TokenID: tokenID, AgentName: agentName, Action: request.Params.Name,
		Target: "mcp/tools/call", Input: security.Summary(request.GetArguments(), 4096), Output: output,
		Status: status, LatencyMS: time.Since(started).Milliseconds(), IP: auditIP(request.Header), CreatedAt: time.Now(),
	}
	if err := m.db.AddAuditLog(entry); err != nil {
		log.Printf("persist MCP audit: %v", err)
	}
}

func (m *MCPServer) registerAll18Tools() {
	// 1. c115_list_files: 列出指定 CID 下文件
	m.server.AddTool(mcp.NewTool("c115_list_files",
		mcp.WithDescription("List one bounded page of files and folders in a 115 directory"),
		mcp.WithString("account_id", mcp.Description("Optional account ID")),
		mcp.WithString("cid", mcp.Description("Directory CID; root when omitted")),
		mcp.WithInteger("offset", mcp.Min(0), mcp.Description("Zero-based page offset")),
		mcp.WithInteger("limit", mcp.Min(1), mcp.Max(1000), mcp.Description("Page size; default 200")),
	), m.handleC115ListFiles)

	// 2. c115_search: 搜索 115 分享资源
	m.server.AddTool(mcp.NewTool("c115_search",
		mcp.WithDescription("Search the configured resource index for 115 share links by keyword"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Keyword to search for")),
	), m.handleC115Search)

	// 3. c115_save_share: 转存分享链接到指定 CID
	m.server.AddTool(mcp.NewTool("c115_save_share",
		mcp.WithDescription("Save all top-level entries from a 115 share into a target directory"),
		mcp.WithString("url", mcp.Required(), mcp.Description("115 share link or share code")),
		mcp.WithString("password", mcp.Description("Optional extraction code")),
		mcp.WithString("target_cid", mcp.Description("Target CID in 115; root when omitted")),
		mcp.WithString("account_id", mcp.Description("Optional managed 115 account ID")),
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

	// 7. c115_get_share_link: 生成 115 分享链接
	m.server.AddTool(mcp.NewTool("c115_get_share_link",
		mcp.WithDescription("Create a 115 share link for a file or directory"),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File or directory ID")),
		mcp.WithString("account_id", mcp.Description("Optional managed 115 account ID")),
	), m.handleC115GetShareLink)

	// 8. cd2_mount_status: 查看挂载盘健康状态
	m.server.AddTool(mcp.NewTool("cd2_mount_status",
		mcp.WithDescription("Check CloudDrive2 virtual mount health, path and storage statistics"),
	), m.handleCD2MountStatus)

	// 9. cd2_remount: 重新挂载 CloudDrive2 挂载点
	m.server.AddTool(mcp.NewTool("cd2_remount",
		mcp.WithDescription("Remount configured CloudDrive2 points after playback is stopped; returns partial effects on failure"),
		mcp.WithBoolean("confirm_playback_stopped", mcp.Required(), mcp.Description("Must be true after stopping active playback")),
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

	// 12. emby_inspect_item: 精确检查条目元数据与图片
	m.server.AddTool(mcp.NewTool("emby_inspect_item",
		mcp.WithDescription("Read one exact Emby item with its path, provider IDs and artwork state"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Exact Emby item ID")),
	), m.handleEmbyInspectItem)

	// 13. task_submit: 提交异步后台任务
	m.server.AddTool(mcp.NewTool("task_submit",
		mcp.WithDescription("Submit a real provider operation to the persistent background queue"),
		mcp.WithString("task_type", mcp.Required(), mcp.Enum(service.SupportedTaskTypes()...), mcp.Description("Supported background task type")),
		mcp.WithObject("payload", mcp.Description("Task parameters; fields depend on task_type")),
	), m.handleTaskSubmit)

	// 14. task_query: 查询任务执行状态与完成百分比
	m.server.AddTool(mcp.NewTool("task_query",
		mcp.WithDescription("Query background task status, progress and results"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), m.handleTaskQuery)

	// 15. task_cancel: 取消等待中或执行中的任务
	m.server.AddTool(mcp.NewTool("task_cancel",
		mcp.WithDescription("Cancel a pending task or signal its running provider request"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), m.handleTaskCancel)

	// 16. task_get_logs: 读取持久化任务执行记录
	m.server.AddTool(mcp.NewTool("task_get_logs",
		mcp.WithDescription("Read persisted execution attempts, progress, errors and logs"),
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
	accountID := req.GetString("account_id", "")
	cid := req.GetString("cid", "0")
	offset := req.GetInt("offset", 0)
	limit := req.GetInt("limit", 200)
	files, total, err := m.drive.ListFilesPageCtx(ctx, accountID, cid, offset, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list files failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(map[string]any{"files": files, "total": total, "offset": offset, "limit": limit}, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleC115Search(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return mcp.NewToolResultError("query must not be empty"), nil
	}
	result, err := m.drive.SearchResourcesCtx(ctx, url.Values{"q": {query}, "disk_type": {"115"}})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleC115SaveShare(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURL, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("url is required"), nil
	}
	accountID := req.GetString("account_id", "")
	targetCID := req.GetString("target_cid", "0")
	password := req.GetString("password", "")
	count, title, err := m.drive.SaveShareCtx(ctx, accountID, rawURL, password, targetCID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save share failed: %v", err)), nil
	}
	encoded, _ := json.Marshal(map[string]any{"count": count, "title": title, "target_cid": targetCID})
	return mcp.NewToolResultText(string(encoded)), nil
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
	fileID, err := req.RequireString("file_id")
	if err != nil {
		return mcp.NewToolResultError("file_id is required"), nil
	}
	link, err := m.drive.GenerateShareLink(ctx, req.GetString("account_id", ""), fileID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create share link failed: %v", err)), nil
	}
	encoded, _ := json.Marshal(link)
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleCD2MountStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mounts, err := m.cloudDrive.GetMounts(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get mount status failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(mounts, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleCD2Remount(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !req.GetBool("confirm_playback_stopped", false) {
		return mcp.NewToolResultError("confirm_playback_stopped must be true"), nil
	}
	report, err := m.cloudDrive.Remount(ctx)
	if err != nil {
		encoded, _ := json.MarshalIndent(map[string]any{"error": err.Error(), "report": report}, "", "  ")
		return mcp.NewToolResultError(string(encoded)), nil
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}
func (m *MCPServer) handleEmbyRefreshLibrary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	libraryID := req.GetString("library_id", "")
	if err := m.emby.RefreshLibraryCtx(ctx, libraryID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Emby refresh failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Emby library scan triggered"), nil
}

func (m *MCPServer) handleEmbyGetLibraries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	libs, err := m.emby.GetLibrariesCtx(ctx)
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
	item, err := m.emby.GetItemCtx(ctx, itemID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("inspect item failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(item, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleTaskSubmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskType, err := req.RequireString("task_type")
	if err != nil {
		return mcp.NewToolResultError("task_type is required"), nil
	}
	payload := map[string]any{}
	if rawPayload, present := req.GetArguments()["payload"]; present {
		var ok bool
		payload, ok = rawPayload.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("payload must be an object"), nil
		}
	}
	task, err := m.taskQueue.Enqueue(taskType, payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("submit task failed: %v", err)), nil
	}
	encoded, _ := json.Marshal(map[string]any{"task_id": task.ID, "status": task.Status})
	return mcp.NewToolResultText(string(encoded)), nil
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
	task, err := m.taskQueue.Cancel(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("cancel task failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(task, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
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
	runs, err := m.db.ListTaskRuns(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read task logs failed: %v", err)), nil
	}
	encoded, _ := json.MarshalIndent(map[string]any{"task": task, "runs": runs}, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleSystemGetConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	legacy, err := m.db.ListConfigs()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get legacy config failed: %v", err)), nil
	}
	settings, err := m.db.GetAllSettings()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get settings failed: %v", err)), nil
	}
	values := make(map[string]any, len(legacy)+len(settings))
	for key, value := range legacy {
		values[key] = value
	}
	for key, value := range settings {
		values[key] = value
	}
	if encoded, ok := values["c115_cid_map"].(string); ok && encoded != "" {
		var cidMap map[string]string
		if json.Unmarshal([]byte(encoded), &cidMap) == nil {
			values["c115_cid_map"] = cidMap
		}
	}
	encoded, _ := json.MarshalIndent(security.RedactValue(values), "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func (m *MCPServer) handleSystemHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status := "healthy"
	health := map[string]any{"timestamp": time.Now(), "database": "connected"}
	accounts, err := m.drive.CheckAccounts(ctx)
	if err != nil {
		status = "unhealthy"
		health["database"] = err.Error()
	} else {
		health["configured_115_accounts"] = len(accounts)
		health["115_accounts"] = accounts
	}
	components := map[string]service.ServiceHealth{
		"emby": m.settings.CheckAvailability("emby"), "clouddrive": m.settings.CheckAvailability("clouddrive"), "resource": m.settings.CheckAvailability("resource"),
	}
	c115Health := service.ServiceHealth{Status: "unconfigured", Message: "未配置 115 账号"}
	if len(accounts) > 0 {
		active := 0
		for _, account := range accounts {
			if account.Status == "active" {
				active++
			}
		}
		c115Health = service.ServiceHealth{Status: "error", Message: "所有 115 账号均不可用"}
		if active > 0 {
			c115Health = service.ServiceHealth{Status: "ok", Message: fmt.Sprintf("115 账号可用 %d / %d", active, len(accounts))}
		}
	}
	components["c115"] = c115Health
	for _, component := range components {
		if component.Status != "ok" && status == "healthy" {
			status = "degraded"
		}
	}
	health["components"] = components
	mounts, err := m.cloudDrive.GetMounts(ctx)
	if err != nil {
		if status == "healthy" {
			status = "degraded"
		}
		health["mounts_error"] = err.Error()
	} else {
		for _, mount := range mounts {
			if mount.Status != "mounted" && status == "healthy" {
				status = "degraded"
			}
		}
		health["mounts"] = mounts
	}
	health["status"] = status
	encoded, _ := json.MarshalIndent(health, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

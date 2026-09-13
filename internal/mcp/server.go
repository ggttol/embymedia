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
	"github.com/embymedia/embymedia/internal/product"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
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
	cron           *service.CronManager
	settings       *service.SettingsService
	authorizer     *security.AgentAuthorizer
	trustTokenless bool
}

func NewMCPServer(db *storage.DB, drive *service.DriveService, emby *service.EmbyService, cd *service.CloudDriveService, tq *service.TaskQueueService, cron *service.CronManager, authorizer *security.AgentAuthorizer, trustTokenless bool) *MCPServer {
	s := server.NewMCPServer(
		"EmbyMedia MCP Server",
		product.Version,
		server.WithToolCapabilities(true),
	)

	ms := &MCPServer{
		server: s, db: db, drive: drive, emby: emby, cloudDrive: cd, taskQueue: tq, cron: cron, settings: service.NewSettingsService(db), authorizer: authorizer, trustTokenless: trustTokenless,
	}
	s.Use(ms.auditToolCalls)

	ms.registerTools()
	return ms
}

func (m *MCPServer) Server() *server.MCPServer {
	return m.server
}

func toolRequiresWrite(name string) bool {
	switch name {
	case "c115_save_share", "c115_move", "c115_rename", "c115_mkdir", "c115_get_share_link", "c115_request_delete", "c115_execute_delete", "quark_import_share_to_115", "cd2_remount", "emby_refresh_library", "task_submit", "task_cancel", "task_retry", "schedule_upsert", "schedule_run", "schedule_delete", "system_update_config":
		return true
	default:
		return false
	}
}

type requesterContextKey struct{}

func requesterFromContext(ctx context.Context) string {
	requester, _ := ctx.Value(requesterContextKey{}).(string)
	if requester == "" {
		return "unknown MCP caller"
	}
	return requester
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
		requester := agentName
		if requester == "" {
			requester = caller
		}
		ctx = context.WithValue(ctx, requesterContextKey{}, requester)
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

func (m *MCPServer) registerTools() {
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
		mcp.WithDescription("Remount configured CloudDrive2 points when live Emby sessions show no active playback; returns partial effects on failure"),
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

	m.server.AddTool(mcp.NewTool("c115_list_accounts", mcp.WithDescription("List managed 115 accounts, status and capacity without credentials")), m.handleC115ListAccounts)
	m.server.AddTool(mcp.NewTool("c115_search_files", mcp.WithDescription("Search files and directories already stored in a managed 115 account"), mcp.WithString("query", mcp.Required()), mcp.WithString("account_id"), mcp.WithInteger("offset", mcp.Min(0)), mcp.WithInteger("limit", mcp.Min(1), mcp.Max(200))), m.handleC115SearchFiles)
	m.server.AddTool(mcp.NewTool("c115_snapshot_share", mcp.WithDescription("Inspect a 115 share before saving it"), mcp.WithString("url", mcp.Required()), mcp.WithString("password"), mcp.WithString("account_id")), m.handleC115SnapshotShare)
	m.server.AddTool(mcp.NewTool("c115_list_offline", mcp.WithDescription("List current 115 offline download tasks"), mcp.WithString("account_id")), m.handleC115ListOffline)
	m.server.AddTool(mcp.NewTool("emby_search_items", mcp.WithDescription("Search Emby items and return exact IDs for later operations"), mcp.WithString("query", mcp.Required()), mcp.WithInteger("limit", mcp.Min(1), mcp.Max(100))), m.handleEmbySearchItems)
	m.server.AddTool(mcp.NewTool("emby_list_sessions", mcp.WithDescription("List active Emby playback sessions before disruptive maintenance")), m.handleEmbyListSessions)
	m.server.AddTool(mcp.NewTool("emby_missing_posters", mcp.WithDescription("Return the complete missing-poster count and up to 100 Emby movies or series with paths and provider IDs")), m.handleEmbyMissingPosters)
	m.server.AddTool(mcp.NewTool("task_list", mcp.WithDescription("List recent persistent tasks with optional status filter"), mcp.WithString("status"), mcp.WithInteger("limit", mcp.Min(1), mcp.Max(100))), m.handleTaskList)
	m.server.AddTool(mcp.NewTool("task_retry", mcp.WithDescription("Create a reviewed retry from a failed or cancelled task"), mcp.WithString("task_id", mcp.Required())), m.handleTaskRetry)
	m.server.AddTool(mcp.NewTool("schedule_list", mcp.WithDescription("List persisted automatic operation schedules")), m.handleScheduleList)
	m.server.AddTool(mcp.NewTool("schedule_upsert", mcp.WithDescription("Create, update, pause or enable an automatic operation schedule"), mcp.WithString("schedule_id"), mcp.WithString("name", mcp.Required()), mcp.WithString("task_type", mcp.Required(), mcp.Enum(service.SupportedTaskTypes()...)), mcp.WithString("cron_expr"), mcp.WithBoolean("enabled", mcp.Required()), mcp.WithObject("payload")), m.handleScheduleUpsert)
	m.server.AddTool(mcp.NewTool("schedule_run", mcp.WithDescription("Run one persisted schedule immediately"), mcp.WithString("schedule_id", mcp.Required())), m.handleScheduleRun)
	m.server.AddTool(mcp.NewTool("schedule_delete", mcp.WithDescription("Delete an automatic schedule; does not delete media or task history"), mcp.WithString("schedule_id", mcp.Required())), m.handleScheduleDelete)
	m.server.AddTool(mcp.NewTool("c115_request_delete", mcp.WithDescription("Request browser confirmation to recycle exact 115 objects"), mcp.WithString("account_id"), mcp.WithString("parent_cid", mcp.Required()), mcp.WithArray("file_ids", mcp.Required(), mcp.WithStringItems())), m.handleC115RequestDelete)
	m.server.AddTool(mcp.NewTool("c115_execute_delete", mcp.WithDescription("Execute one unexpired target-bound deletion after browser approval"), mcp.WithString("approval_id", mcp.Required())), m.handleC115ExecuteDelete)
	m.server.AddTool(mcp.NewTool("system_update_config", mcp.WithDescription("Update validated system settings; cannot change the browser deletion switch"), mcp.WithObject("settings", mcp.Required())), m.handleSystemUpdateConfig)
	m.server.AddTool(mcp.NewTool("quark_import_share_to_115",
		mcp.WithDescription("Save a Quark share into a selected Quark directory, then transfer and verify it under /emby/_待整理 in the default 115 account"),
		mcp.WithString("quark_account_id", mcp.Required()),
		mcp.WithString("quark_target_id", mcp.Required()),
		mcp.WithString("share_url", mcp.Required()),
		mcp.WithString("share_password"),
		mcp.WithString("c115_account_id"),
	), m.handleQuarkImportShare)
}

// Tool handlers share the same provider and persistence services as REST.

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

func (m *MCPServer) handleC115SearchFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}
	files, total, err := m.drive.SearchFilesCtx(ctx, req.GetString("account_id", ""), query, req.GetInt("offset", 0), req.GetInt("limit", 50))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search 115 files failed: %v", err)), nil
	}
	return jsonToolResult(map[string]any{"files": files, "total": total, "offset": req.GetInt("offset", 0)}), nil
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

func (m *MCPServer) handleQuarkImportShare(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	quarkAccountID, err := req.RequireString("quark_account_id")
	if err != nil {
		return mcp.NewToolResultError("quark_account_id is required"), nil
	}
	quarkTargetID, err := req.RequireString("quark_target_id")
	if err != nil {
		return mcp.NewToolResultError("quark_target_id is required"), nil
	}
	shareURL, err := req.RequireString("share_url")
	if err != nil {
		return mcp.NewToolResultError("share_url is required"), nil
	}
	if _, err := m.drive.GetAccount("quark", quarkAccountID); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c115AccountID := strings.TrimSpace(req.GetString("c115_account_id", ""))
	if c115AccountID == "" {
		account, err := m.drive.GetDefaultAccount("115")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		c115AccountID = account.ID
	} else if _, err := m.drive.GetAccount("115", c115AccountID); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := m.taskQueue.Enqueue("quark_to_115_import", map[string]any{
		"quark_account_id": quarkAccountID,
		"quark_target_id":  quarkTargetID,
		"share_url":        strings.TrimSpace(shareURL),
		"share_password":   strings.TrimSpace(req.GetString("share_password", "")),
		"c115_account_id":  c115AccountID,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("submit Quark import: %v", err)), nil
	}
	return jsonToolResult(map[string]any{"task_id": task.ID, "status": task.Status, "destination_path": "/emby/_待整理"}), nil
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

func (m *MCPServer) handleCD2Remount(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := m.emby.EnsureNoActivePlayback(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

func safeTask(task *domain.AsyncTask) *domain.AsyncTask {
	if task == nil || task.Type != "quark_to_115_import" {
		return task
	}
	copy := *task
	copy.Payload = make(map[string]any, len(task.Payload))
	for key, value := range task.Payload {
		if key != "share_url" && key != "share_password" {
			copy.Payload[key] = value
		}
	}
	return &copy
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
	b, _ := json.MarshalIndent(safeTask(task), "", "  ")
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
	encoded, _ := json.MarshalIndent(safeTask(task), "", "  ")
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
	encoded, _ := json.MarshalIndent(map[string]any{"task": safeTask(task), "runs": runs}, "", "  ")
	return mcp.NewToolResultText(string(encoded)), nil
}

func jsonToolResult(value any) *mcp.CallToolResult {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return mcp.NewToolResultText(string(encoded))
}

func (m *MCPServer) handleC115ListAccounts(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	accounts, err := m.drive.CheckAccounts(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list accounts failed: %v", err)), nil
	}
	return jsonToolResult(accounts), nil
}

func (m *MCPServer) handleC115SnapshotShare(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURL, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("url is required"), nil
	}
	title, entries, err := m.drive.SnapshotShareCtx(ctx, req.GetString("account_id", ""), rawURL, req.GetString("password", ""))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("snapshot share failed: %v", err)), nil
	}
	return jsonToolResult(map[string]any{"title": title, "entries": entries, "count": len(entries)}), nil
}

func (m *MCPServer) handleC115ListOffline(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks, err := m.drive.ListOfflineTasks(req.GetString("account_id", ""))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list offline tasks failed: %v", err)), nil
	}
	return jsonToolResult(tasks), nil
}

func (m *MCPServer) handleEmbySearchItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}
	items, err := m.emby.SearchMediaCtx(ctx, query, req.GetInt("limit", 20))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search Emby items failed: %v", err)), nil
	}
	return jsonToolResult(items), nil
}

func (m *MCPServer) handleEmbyListSessions(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sessions, err := m.emby.ListSessionsCtx(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list Emby sessions failed: %v", err)), nil
	}
	return jsonToolResult(sessions), nil
}

func (m *MCPServer) handleEmbyMissingPosters(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	report, err := m.emby.GetMediaWithoutPostersCtx(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list missing posters failed: %v", err)), nil
	}
	return jsonToolResult(report), nil
}

func (m *MCPServer) handleTaskList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks, err := m.db.ListAsyncTasks(strings.TrimSpace(req.GetString("status", "")), req.GetInt("limit", 50))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list tasks failed: %v", err)), nil
	}
	for index := range tasks {
		tasks[index] = *safeTask(&tasks[index])
	}
	return jsonToolResult(tasks), nil
}

func (m *MCPServer) handleTaskRetry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	task, err := m.taskQueue.Retry(id)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("retry task failed: %v", err)), nil
	}
	return jsonToolResult(safeTask(task)), nil
}

func (m *MCPServer) handleScheduleList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks, err := m.db.ListTasks()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list schedules failed: %v", err)), nil
	}
	return jsonToolResult(tasks), nil
}

func schedulePayload(req mcp.CallToolRequest) (map[string]any, error) {
	payload := map[string]any{}
	if raw, present := req.GetArguments()["payload"]; present {
		var ok bool
		payload, ok = raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("payload must be an object")
		}
	}
	return payload, nil
}

func (m *MCPServer) handleScheduleUpsert(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	taskType, err := req.RequireString("task_type")
	if err != nil {
		return mcp.NewToolResultError("task_type is required"), nil
	}
	payload, err := schedulePayload(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	params, _ := json.Marshal(payload)
	task := domain.ScheduledTask{ID: strings.TrimSpace(req.GetString("schedule_id", "")), Name: strings.TrimSpace(name), Type: strings.TrimSpace(taskType), CronExpr: strings.TrimSpace(req.GetString("cron_expr", "")), Enabled: req.GetBool("enabled", false), Params: string(params)}
	if err := m.cron.ScheduleTask(&task); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save schedule failed: %v", err)), nil
	}
	return jsonToolResult(task), nil
}

func (m *MCPServer) handleScheduleRun(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("schedule_id")
	if err != nil {
		return mcp.NewToolResultError("schedule_id is required"), nil
	}
	schedule, err := m.db.GetTask(id)
	if err != nil {
		return mcp.NewToolResultError("scheduled task not found"), nil
	}
	payload := map[string]any{}
	if schedule.Params != "" && json.Unmarshal([]byte(schedule.Params), &payload) != nil {
		return mcp.NewToolResultError("scheduled task payload is invalid"), nil
	}
	task, err := m.taskQueue.Enqueue(schedule.Type, payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("run schedule failed: %v", err)), nil
	}
	return jsonToolResult(map[string]any{"schedule_id": id, "task_id": task.ID, "status": task.Status}), nil
}

func (m *MCPServer) handleScheduleDelete(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("schedule_id")
	if err != nil {
		return mcp.NewToolResultError("schedule_id is required"), nil
	}
	if err := m.cron.DeleteTask(id); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete schedule failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Schedule deleted"), nil
}

func (m *MCPServer) handleC115RequestDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fileIDs, err := req.RequireStringSlice("file_ids")
	if err != nil {
		return mcp.NewToolResultError("file_ids is required"), nil
	}
	parentCID, err := req.RequireString("parent_cid")
	if err != nil {
		return mcp.NewToolResultError("parent_cid is required"), nil
	}
	accountID, targets, err := m.drive.ResolveDeleteTargetsCtx(ctx, req.GetString("account_id", ""), parentCID, fileIDs)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve delete targets failed: %v", err)), nil
	}
	now := time.Now()
	approval := &domain.DestructiveApproval{ID: uuid.NewString(), Action: "drive.115.delete", AccountID: accountID, ParentCID: parentCID, Targets: targets, Status: "pending", RequestedBy: requesterFromContext(ctx), ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now}
	if err := m.db.CreateDestructiveApproval(approval); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("request delete approval failed: %v", err)), nil
	}
	return jsonToolResult(approval), nil
}

func (m *MCPServer) handleC115ExecuteDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("approval_id")
	if err != nil {
		return mcp.NewToolResultError("approval_id is required"), nil
	}
	now := time.Now()
	approval, err := m.db.ClaimDestructiveApproval(id, "drive.115.delete", now)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete approval rejected: %v", err)), nil
	}
	fileIDs := make([]string, len(approval.Targets))
	for index, target := range approval.Targets {
		fileIDs[index] = target.FileID
	}
	if err := m.drive.DeleteProvider(ctx, "115", approval.AccountID, fileIDs); err != nil {
		_ = m.db.FinishDestructiveApproval(id, "failed", err.Error(), time.Now())
		return mcp.NewToolResultError(fmt.Sprintf("delete failed after approval: %v", err)), nil
	}
	if err := m.db.FinishDestructiveApproval(id, "executed", "", time.Now()); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("record delete result failed: %v", err)), nil
	}
	approval.Status = "executed"
	executed := time.Now()
	approval.ExecutedAt = &executed
	return jsonToolResult(approval), nil
}

func (m *MCPServer) handleSystemUpdateConfig(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, present := req.GetArguments()["settings"]
	settings, ok := raw.(map[string]any)
	if !present || !ok {
		return mcp.NewToolResultError("settings must be an object"), nil
	}
	values := make(map[string]string, len(settings))
	for key, value := range settings {
		text, ok := value.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("setting %s must be a string", key)), nil
		}
		if key == "dangerous_actions_enabled" {
			return mcp.NewToolResultError("Agent tools cannot change the browser deletion safety switch"), nil
		}
		values[key] = text
	}
	if err := m.settings.Update(values); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("update settings failed: %v", err)), nil
	}
	state, err := m.settings.State()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read updated settings failed: %v", err)), nil
	}
	return jsonToolResult(state), nil
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

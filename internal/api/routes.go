package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Server struct {
	echo       *echo.Echo
	db         *storage.DB
	drive      *service.DriveService
	emby       *service.EmbyService
	cloudDrive *service.CloudDriveService
	taskQueue  *service.TaskQueueService
	cron       *service.CronManager
	webhook    *service.CloudDriveWebhookService
	settings   *service.SettingsService
	authorizer *security.AgentAuthorizer
}

func NewServer(
	e *echo.Echo,
	db *storage.DB,
	drive *service.DriveService,
	emby *service.EmbyService,
	cloudDrive *service.CloudDriveService,
	taskQueue *service.TaskQueueService,
	cron *service.CronManager,
	settings *service.SettingsService,
	authorizer *security.AgentAuthorizer,
) *Server {
	s := &Server{
		echo: e, db: db, drive: drive, emby: emby, cloudDrive: cloudDrive,
		taskQueue: taskQueue, cron: cron, settings: settings, authorizer: authorizer,
		webhook: service.NewCloudDriveWebhookService(db, taskQueue),
	}
	s.registerRoutes()
	return s
}

func (s *Server) Start(addr string) error {
	return s.echo.Start(addr)
}

// Close stops delayed background work owned by the API server.
func (s *Server) Close() {
	s.webhook.Close()
}

func (s *Server) registerRoutes() {
	s.echo.Any("/internal/emby-delete/*", s.handleEmbyDeletion)

	// OpenAPI 3.1 schema
	s.echo.GET("/openapi.json", s.handleOpenAPI)
	s.echo.GET("/api/v1/openapi.json", s.handleOpenAPI)

	// API v1 group
	v1 := s.echo.Group("/api/v1")

	// Audit Logging Middleware
	v1.Use(s.auditLogMiddleware)
	v1.GET("/home/summary", s.handleHomeSummary)
	v1.GET("/sources", s.handleSources)
	v1.GET("/trends", s.handleTrends)
	v1.GET("/search", s.handleSearch)
	s.echo.GET("/search", s.handleSearch) // also directly on /search
	v1.GET("/links/:id", s.handleGetLink)
	s.echo.POST("/hooks/clouddrive2", s.handleCloudDriveWebhook)
	v1.POST("/links/:id/save", s.handleSaveLink)
	v1.GET("/cid-map", s.handleCidMap)
	// Provider-neutral drive management. Every request identifies its provider.
	v1.GET("/drive/accounts", s.handleListAccounts)
	v1.POST("/drive/accounts", s.handleAddAccount)
	v1.DELETE("/drive/accounts/:id", s.handleDeleteAccount)
	v1.GET("/drive/files", s.handleListFiles)
	v1.POST("/drive/files/mkdir", s.handleMkdir)
	v1.POST("/drive/files/rename", s.handleRename)
	v1.POST("/drive/files/move", s.handleMove)
	v1.POST("/drive/files/delete", s.handleDelete)
	v1.POST("/drive/share-snapshot", s.handleSnapshotShare)
	v1.POST("/drive/share-save", s.handleSaveShare)
	v1.POST("/offline/download", s.handleAddOffline)

	// Emby & Media
	v1.GET("/emby/libraries", s.handleEmbyLibraries)
	v1.POST("/emby/refresh", s.handleEmbyRefresh)
	v1.POST("/emby/match", s.handleEmbyMatch)
	v1.GET("/emby/items/:id", s.handleEmbyItem)

	// CloudDrive2 Mounts
	v1.GET("/mounts", s.handleListMounts)
	v1.POST("/mounts/remount", s.handleRemount)

	// Tasks & Cron
	v1.GET("/tasks", s.handleListTasks)
	v1.POST("/tasks", s.handleCreateTask)
	v1.POST("/tasks/:id/run", s.handleRunTask)
	v1.POST("/tasks/batch", s.handleBatchTasks)
	v1.GET("/async-tasks", s.handleListAsyncTasks)
	v1.GET("/async-tasks/:id", s.handleGetAsyncTask)
	v1.POST("/async-tasks/:id/cancel", s.handleCancelAsyncTask)
	v1.POST("/async-tasks/:id/retry", s.handleRetryAsyncTask)
	v1.GET("/async-tasks/:id/runs", s.handleListTaskRuns)
	v1.POST("/quark/share-imports", s.handleCreateQuarkShareImport)
	v1.GET("/quark/share-imports/:task_id", s.handleGetQuarkShareImport)

	// Settings & Agent Tokens
	v1.GET("/settings", s.handleGetSettings)
	v1.POST("/settings", s.handleUpdateSettings)
	v1.POST("/settings/check", s.handleCheckSettings)
	v1.GET("/tokens", s.handleListTokens)
	v1.POST("/tokens", s.handleCreateToken)
	v1.DELETE("/tokens/:id", s.handleDeleteToken)
	v1.GET("/audit-logs", s.handleListAuditLogs)
	v1.GET("/destructive-approvals", s.handleListDestructiveApprovals)
	v1.POST("/destructive-approvals/:id/approve", s.handleApproveDestructiveApproval)
	v1.POST("/destructive-approvals/:id/reject", s.handleRejectDestructiveApproval)
}

func (s *Server) handleCloudDriveWebhook(c echo.Context) error {
	configured, err := s.settings.Get("clouddrive_webhook_secret")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	provided := c.Request().Header.Get("X-Webhook-Secret")
	if configured == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{"error": "CloudDrive webhook secret is not configured"})
	}
	if subtle.ConstantTimeCompare([]byte(configured), []byte(provided)) != 1 {
		return c.JSON(http.StatusUnauthorized, map[string]any{"error": "invalid webhook secret"})
	}
	var event map[string]any
	if err := c.Bind(&event); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.webhook.Notify(); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
	}
	s.writeAudit(&domain.AuditLog{
		Caller: "clouddrive-webhook", Action: "filesystem.change", Target: "/hooks/clouddrive2",
		Input: security.Summary(event, 4096), Status: "accepted", IP: c.RealIP(), CreatedAt: time.Now(),
	})
	return c.JSON(http.StatusAccepted, map[string]any{"status": "debounced"})
}
func (s *Server) handleHomeSummary(c echo.Context) error {
	summary, err := s.drive.GetHomeSummary()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error(), "upstream_available": false})
	}
	return c.JSON(http.StatusOK, map[string]any{"summary": summary, "upstream_available": true})
}
func (s *Server) handleSources(c echo.Context) error {
	sources, err := s.drive.GetSources()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"sources": sources})
}
func (s *Server) handleTrends(c echo.Context) error {
	trends, err := s.drive.GetTrends()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"code": 0, "message": "success", "data": map[string]any{"trends": trends}})
}
func (s *Server) handleSearch(c echo.Context) error {
	response, err := s.drive.SearchResourcesCtx(c.Request().Context(), c.QueryParams())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, response)
}
func (s *Server) handleGetLink(c echo.Context) error {
	_, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": 400, "message": "invalid link id"})
	}
	res, err := s.drive.GetLink(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"code": 404, "message": "link not found"})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"code":    0,
		"message": "success",
		"data":    res,
	})
}

func (s *Server) handleCidMap(c echo.Context) error {
	raw, err := s.settings.Get("c115_cid_map")
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"map": map[string]string{}})
	}
	var cidMap map[string]string
	if err := json.Unmarshal([]byte(raw), &cidMap); err != nil {
		return c.JSON(http.StatusOK, map[string]any{"map": map[string]string{}})
	}
	return c.JSON(http.StatusOK, map[string]any{"map": cidMap})
}

func (s *Server) handleSaveShare(c echo.Context) error {
	var req struct {
		Provider  string `json:"provider"`
		URL       string `json:"url"`
		Password  string `json:"password"`
		TargetCID string `json:"target_cid"`
		AccountID string `json:"account_id"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if req.Provider == "" || strings.TrimSpace(req.URL) == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "provider and url are required"})
	}
	saved, err := s.drive.SaveProviderShare(c.Request().Context(), req.Provider, req.AccountID, req.URL, req.Password, req.TargetCID)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "count": saved.Count, "title": saved.Title, "root_ids": saved.RootIDs, "target_cid": req.TargetCID})
}

func (s *Server) handleSnapshotShare(c echo.Context) error {
	var req struct {
		Provider  string `json:"provider"`
		AccountID string `json:"account_id"`
		URL       string `json:"url"`
		Password  string `json:"password"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if req.Provider == "" || strings.TrimSpace(req.URL) == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "provider and url are required"})
	}
	snapshot, err := s.drive.SnapshotProviderShare(c.Request().Context(), req.Provider, req.AccountID, req.URL, req.Password)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, snapshot)
}

func (s *Server) handleSaveLink(c echo.Context) error {
	id := c.Param("id")
	var req struct {
		TargetCID string `json:"target_cid"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	link, err := s.drive.GetLink(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "link not found"})
	}
	data, ok := link["data"].(map[string]any)
	if !ok {
		data = link
	}
	rawURL, _ := data["url"].(string)
	if rawURL == "" {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "link has no url"})
	}
	password, _ := data["password"].(string)
	diskType, _ := data["disk_type"].(string)

	if diskType == "115" || strings.Contains(rawURL, "115.com/s/") || strings.Contains(rawURL, "115cdn.com/s/") {
		count, title, err := s.drive.SaveShare("", rawURL, password, req.TargetCID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"success": true, "method": "share_save", "count": count, "title": title, "target_cid": req.TargetCID})
	}
	taskIDs, err := s.drive.AddOfflineTasks(c.Request().Context(), "", []string{rawURL}, req.TargetCID)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error(), "task_ids": taskIDs})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "method": "offline", "task_ids": taskIDs, "target_cid": req.TargetCID})
}
func publicAccount(account domain.DriveAccount) map[string]any {
	return map[string]any{
		"id": account.ID, "type": account.Type, "name": account.Name, "is_default": account.IsDefault,
		"status": account.Status, "quota_used": account.QuotaUsed, "quota_total": account.QuotaTotal,
		"vip_level": account.VIPLevel, "vip_expires_at": account.VIPExpiresAt,
		"created_at": account.CreatedAt, "updated_at": account.UpdatedAt,
	}
}

func (s *Server) handleListAccounts(c echo.Context) error {
	provider := strings.TrimSpace(c.QueryParam("provider"))
	if provider != "115" && provider != "quark" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "provider must be 115 or quark"})
	}
	var accounts []domain.DriveAccount
	var err error
	if c.QueryParam("refresh") == "true" {
		accounts, err = s.drive.CheckAccounts(c.Request().Context(), provider)
	} else {
		all, listErr := s.db.ListAccounts()
		err = listErr
		for _, account := range all {
			if account.Type == provider {
				accounts = append(accounts, account)
			}
		}
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if len(accounts) == 0 {
		if fallback, fallbackErr := s.drive.GetDefaultAccount(provider); fallbackErr == nil {
			accounts = append(accounts, *fallback)
		}
	}
	result := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, publicAccount(account))
	}
	capabilities, _ := s.drive.ProviderCapabilities(provider)
	return c.JSON(http.StatusOK, map[string]any{"accounts": result, "capabilities": capabilities})
}

func (s *Server) handleAddAccount(c echo.Context) error {
	var request struct {
		domain.DriveAccount
		Cookie string `json:"cookie"`
		Token  string `json:"token"`
	}
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	account := request.DriveAccount
	account.Cookie = strings.TrimSpace(request.Cookie)
	account.Token = strings.TrimSpace(request.Token)
	account.Name = strings.TrimSpace(account.Name)
	account.Type = strings.ToLower(strings.TrimSpace(account.Type))
	if account.Name == "" || account.Cookie == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "type, name, and cookie are required"})
	}
	if account.Type != "115" && account.Type != "quark" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "type must be 115 or quark"})
	}
	if account.ID == "" {
		account.ID = uuid.NewString()
	}
	if account.Status == "" {
		account.Status = "active"
	}
	if err := s.db.SaveAccount(&account); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, publicAccount(account))
}

func (s *Server) handleDeleteAccount(c echo.Context) error {
	if err := s.db.DeleteAccount(c.Param("id")); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleListFiles(c echo.Context) error {
	provider := c.QueryParam("provider")
	if provider == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "provider is required"})
	}
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit == 0 {
		limit = 200
	}
	files, total, err := s.drive.ListProviderFiles(c.Request().Context(), provider, c.QueryParam("account_id"), c.QueryParam("parent_id"), offset, limit)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"files": files, "total": total, "offset": offset, "limit": limit})
}

func (s *Server) handleMkdir(c echo.Context) error {
	var req struct {
		Provider  string `json:"provider"`
		AccountID string `json:"account_id"`
		ParentID  string `json:"parent_id"`
		Name      string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	id, err := s.drive.MkdirProvider(c.Request().Context(), req.Provider, req.AccountID, req.ParentID, req.Name)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"id": id, "name": req.Name})
}

func (s *Server) handleRename(c echo.Context) error {
	var req struct {
		Provider  string `json:"provider"`
		AccountID string `json:"account_id"`
		FileID    string `json:"file_id"`
		NewName   string `json:"new_name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.drive.RenameProvider(c.Request().Context(), req.Provider, req.AccountID, req.FileID, req.NewName); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleMove(c echo.Context) error {
	var req struct {
		Provider  string   `json:"provider"`
		AccountID string   `json:"account_id"`
		FileIDs   []string `json:"file_ids"`
		TargetID  string   `json:"target_id"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.drive.MoveProvider(c.Request().Context(), req.Provider, req.AccountID, req.FileIDs, req.TargetID); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func agentAuthenticated(c echo.Context) bool {
	authenticated, _ := c.Get("agent-authenticated").(bool)
	return authenticated
}
func (s *Server) handleDelete(c echo.Context) error {
	if agentAuthenticated(c) {
		return c.JSON(http.StatusForbidden, map[string]any{"error": "Agent deletion requires a target-bound browser approval"})
	}
	enabled, err := s.settings.Get("dangerous_actions_enabled")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if enabled != "true" {
		return c.JSON(http.StatusForbidden, map[string]any{"error": "destructive actions are disabled in system settings"})
	}
	var req struct {
		Provider  string   `json:"provider"`
		AccountID string   `json:"account_id"`
		ParentID  string   `json:"parent_id"`
		FileIDs   []string `json:"file_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if len(req.FileIDs) == 0 || req.Provider == "" || req.ParentID == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "provider, parent_id, and file_ids are required"})
	}
	if err := s.drive.DeleteProvider(c.Request().Context(), req.Provider, req.AccountID, req.FileIDs); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func publicAsyncTask(task domain.AsyncTask) domain.AsyncTask {
	if task.Type != "quark_to_115_import" {
		return task
	}
	payload := make(map[string]any, len(task.Payload))
	for key, value := range task.Payload {
		if key == "share_password" || key == "share_url" {
			continue
		}
		payload[key] = value
	}
	task.Payload = payload
	return task
}

func (s *Server) handleListAsyncTasks(c echo.Context) error {
	status := c.QueryParam("status")
	tasks, err := s.db.ListAsyncTasks(status, 50)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if tasks == nil {
		tasks = []domain.AsyncTask{}
	}
	for index := range tasks {
		tasks[index] = publicAsyncTask(tasks[index])
	}
	return c.JSON(http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleAddOffline(c echo.Context) error {
	var req struct {
		AccountID string   `json:"account_id"`
		URLs      []string `json:"urls"`
		TargetCID string   `json:"target_cid"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	taskIDs, err := s.drive.AddOfflineTasks(c.Request().Context(), req.AccountID, req.URLs, req.TargetCID)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error(), "task_ids": taskIDs})
	}
	return c.JSON(http.StatusOK, map[string]any{"task_ids": taskIDs})
}

func (s *Server) handleEmbyLibraries(c echo.Context) error {
	libs, err := s.emby.GetLibrariesCtx(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error(), "libraries": []any{}})
	}
	return c.JSON(http.StatusOK, map[string]any{"libraries": libs})
}

func (s *Server) handleEmbyRefresh(c echo.Context) error {
	if err := s.emby.RefreshLibraryCtx(c.Request().Context(), c.QueryParam("library_id")); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleEmbyMatch(c echo.Context) error {
	var req struct {
		ItemID string `json:"item_id"`
		TMDBID string `json:"tmdb_id"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.emby.MatchMediaCtx(c.Request().Context(), req.ItemID, req.TMDBID); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleEmbyItem(c echo.Context) error {
	item, err := s.emby.GetItemCtx(c.Request().Context(), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, item)
}

func (s *Server) handleListMounts(c echo.Context) error {
	mounts, err := s.cloudDrive.GetMounts(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"mounts": mounts})
}

func (s *Server) handleRemount(c echo.Context) error {
	if err := s.emby.EnsureNoActivePlayback(c.Request().Context()); err != nil {
		return c.JSON(http.StatusConflict, map[string]any{"error": err.Error()})
	}
	report, err := s.cloudDrive.Remount(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"error": err.Error(), "report": report})
	}
	return c.JSON(http.StatusOK, report)
}
func (s *Server) handleListTasks(c echo.Context) error {
	tasks, err := s.db.ListTasks()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleCreateTask(c echo.Context) error {
	var task domain.ScheduledTask
	if err := c.Bind(&task); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.cron.ScheduleTask(&task); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, task)
}

func (s *Server) handleRunTask(c echo.Context) error {
	queued, schedule, err := s.cron.RunTask(c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "scheduled task not found"})
		}
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusAccepted, map[string]any{"task": queued, "schedule": schedule})
}

func (s *Server) handleBatchTasks(c echo.Context) error {
	var req struct {
		Operations []struct {
			Action  string         `json:"action"`
			Payload map[string]any `json:"payload"`
		} `json:"operations"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if len(req.Operations) == 0 || len(req.Operations) > 100 {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "operations must contain 1 to 100 tasks"})
	}
	for _, operation := range req.Operations {
		if err := service.ValidateTask(operation.Action, operation.Payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
		}
	}
	ids := make([]string, 0, len(req.Operations))
	for _, operation := range req.Operations {
		queued, err := s.taskQueue.Enqueue(operation.Action, operation.Payload)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error(), "queued_task_ids": ids})
		}
		ids = append(ids, queued.ID)
	}
	return c.JSON(http.StatusAccepted, map[string]any{"async_task_ids": ids, "status": "pending"})
}
func (s *Server) handleGetAsyncTask(c echo.Context) error {
	task, err := s.db.GetAsyncTask(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "async task not found"})
	}
	return c.JSON(http.StatusOK, publicAsyncTask(*task))
}

func (s *Server) handleCancelAsyncTask(c echo.Context) error {
	task, err := s.taskQueue.Cancel(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusConflict, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusAccepted, publicAsyncTask(*task))
}

func (s *Server) handleRetryAsyncTask(c echo.Context) error {
	task, err := s.taskQueue.Retry(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusConflict, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusAccepted, publicAsyncTask(*task))
}
func (s *Server) handleCreateQuarkShareImport(c echo.Context) error {
	var request struct {
		QuarkAccountID string `json:"quark_account_id"`
		QuarkTargetID  string `json:"quark_target_id"`
		ShareURL       string `json:"share_url"`
		SharePassword  string `json:"share_password"`
		C115AccountID  string `json:"c115_account_id"`
	}
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if strings.TrimSpace(request.QuarkAccountID) == "" || strings.TrimSpace(request.QuarkTargetID) == "" || strings.TrimSpace(request.ShareURL) == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "quark_account_id, quark_target_id, and share_url are required"})
	}
	if _, err := s.drive.GetAccount("quark", request.QuarkAccountID); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if request.C115AccountID == "" {
		account, err := s.drive.GetDefaultAccount("115")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
		}
		request.C115AccountID = account.ID
	} else if _, err := s.drive.GetAccount("115", request.C115AccountID); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	task, err := s.taskQueue.Enqueue("quark_to_115_import", map[string]any{
		"quark_account_id": request.QuarkAccountID,
		"quark_target_id":  request.QuarkTargetID,
		"share_url":        strings.TrimSpace(request.ShareURL),
		"share_password":   strings.TrimSpace(request.SharePassword),
		"c115_account_id":  request.C115AccountID,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusAccepted, map[string]any{"task_id": task.ID, "status": task.Status})
}

func (s *Server) handleGetQuarkShareImport(c echo.Context) error {
	task, err := s.db.GetAsyncTask(c.Param("task_id"))
	if err != nil || task.Type != "quark_to_115_import" {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "Quark share import not found"})
	}
	detail, detailErr := s.db.GetCrossDriveImportDetail(task.ID)
	if detailErr != nil && !errors.Is(detailErr, sql.ErrNoRows) {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": detailErr.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"task": publicAsyncTask(*task), "detail": detail})
}

func (s *Server) handleListTaskRuns(c echo.Context) error {
	if _, err := s.db.GetAsyncTask(c.Param("id")); err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "async task not found"})
	}
	runs, err := s.db.ListTaskRuns(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetSettings(c echo.Context) error {
	state, err := s.settings.State()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, state)
}

func (s *Server) handleUpdateSettings(c echo.Context) error {
	var body map[string]string
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if agentAuthenticated(c) {
		if _, changesDeletionPolicy := body["dangerous_actions_enabled"]; changesDeletionPolicy {
			return c.JSON(http.StatusForbidden, map[string]any{"error": "Agent requests cannot change the browser deletion safety switch"})
		}
	}
	if err := s.settings.Update(body); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	state, err := s.settings.State()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"success":    true,
		"settings":   state.Values,
		"configured": state.Configured,
		"health":     state.Health,
	})
}

func (s *Server) handleCheckSettings(c echo.Context) error {
	var req struct {
		Component string `json:"component"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if req.Component != "" {
		health := s.settings.CheckAvailability(req.Component)
		return c.JSON(http.StatusOK, map[string]any{
			"component": req.Component,
			"health":    health,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"health": s.settings.CheckAll(),
	})
}

func (s *Server) handleListTokens(c echo.Context) error {
	tokens, err := s.db.ListTokens()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Server) requireBrowserDecision(c echo.Context, status string) error {
	if agentAuthenticated(c) {
		return c.JSON(http.StatusForbidden, map[string]any{"error": "destructive approvals require an authenticated browser user"})
	}
	user := strings.TrimSpace(c.Request().Header.Get("Remote-User"))
	if user == "" {
		return c.JSON(http.StatusUnauthorized, map[string]any{"error": "authenticated browser user is required"})
	}
	approval, err := s.db.DecideDestructiveApproval(c.Param("id"), user, status, time.Now())
	if err != nil {
		return c.JSON(http.StatusConflict, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, approval)
}

func (s *Server) handleListDestructiveApprovals(c echo.Context) error {
	if agentAuthenticated(c) {
		return c.JSON(http.StatusForbidden, map[string]any{"error": "destructive approvals are visible only to authenticated browser users"})
	}
	approvals, err := s.db.ListPendingDestructiveApprovals(time.Now())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"approvals": approvals})
}

func (s *Server) handleApproveDestructiveApproval(c echo.Context) error {
	return s.requireBrowserDecision(c, "approved")
}
func (s *Server) handleRejectDestructiveApproval(c echo.Context) error {
	return s.requireBrowserDecision(c, "rejected")
}

func (s *Server) handleCreateToken(c echo.Context) error {
	var req struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
		RateLimit   int      `json:"rate_limit"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "name is required"})
	}
	readAllowed := false
	writeAllowed := false
	for _, permission := range req.Permissions {
		switch permission {
		case "read":
			readAllowed = true
		case "write":
			writeAllowed = true
		default:
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "permissions must be read or write"})
		}
	}
	if !readAllowed && !writeAllowed {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "at least one permission is required"})
	}
	scopes := []string{"read"}
	role := "readonly"
	if writeAllowed {
		scopes = append(scopes, "write")
		role = "full-access"
	}
	if req.RateLimit == 0 {
		req.RateLimit = 120
	}
	if req.RateLimit < 1 || req.RateLimit > 600 {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "rate_limit must be between 1 and 600 requests per minute"})
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": "generate token"})
	}
	secret := "embymedia_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	digest := sha256.Sum256([]byte(secret))
	token := &domain.AgentToken{
		ID:        uuid.NewString(),
		Token:     hex.EncodeToString(digest[:]),
		Name:      req.Name,
		Role:      role,
		Scopes:    scopes,
		RateLimit: req.RateLimit,
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	if err := s.db.SaveToken(token); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, map[string]any{
		"id": token.ID, "token": secret, "name": token.Name, "role": token.Role,
		"scopes": token.Scopes, "rate_limit": token.RateLimit, "created_at": token.CreatedAt,
	})
}

func (s *Server) handleDeleteToken(c echo.Context) error {
	deleted, err := s.db.DeleteToken(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if !deleted {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "agent token not found"})
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleListAuditLogs(c echo.Context) error {
	return s.queryAuditLogs(c)
}

func (s *Server) handleOpenAPI(c echo.Context) error {
	return c.JSON(http.StatusOK, openAPISchema())
}

func (s *Server) writeAudit(entry *domain.AuditLog) {
	if err := s.db.AddAuditLog(entry); err != nil {
		log.Printf("persist Agent audit: %v", err)
	}
}

func (s *Server) auditLogMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		started := time.Now()
		request := c.Request()
		input := map[string]any{"query": request.URL.Query()}
		if len(c.ParamNames()) > 0 {
			params := make(map[string]string, len(c.ParamNames()))
			for index, name := range c.ParamNames() {
				params[name] = c.ParamValues()[index]
			}
			input["path"] = params
		}
		if request.Body != nil && request.Method != http.MethodGet && request.Method != http.MethodHead {
			body, err := io.ReadAll(io.LimitReader(request.Body, (64<<10)+1))
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]any{"error": "read request body"})
			}
			if len(body) > 64<<10 {
				return c.JSON(http.StatusRequestEntityTooLarge, map[string]any{"error": "request body exceeds 64 KiB"})
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			if len(body) > 0 {
				var value any
				if json.Unmarshal(body, &value) == nil {
					input["body"] = value
				}
			}
		}

		caller := "ui"
		tokenID := ""
		agentName := ""
		secret := security.BearerToken(request.Header)
		recordDenied := func(code int, message string) error {
			s.writeAudit(&domain.AuditLog{
				Caller: "agent", TokenID: tokenID, AgentName: agentName, Action: request.Method + " " + c.Path(), Target: request.URL.Path,
				Input: security.Summary(input, 4096), Output: security.Summary(map[string]any{"error": message}, 1024),
				Status: "denied", LatencyMS: time.Since(started).Milliseconds(), IP: c.RealIP(), CreatedAt: time.Now(),
			})
			return c.JSON(code, map[string]any{"error": message})
		}
		if secret == "" && !security.TrustedWithoutToken(request.Header) {
			return recordDenied(http.StatusUnauthorized, "agent token is required")
		}
		if secret != "" {
			write := request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions
			token, err := s.authorizer.Authorize(secret, write, time.Now())
			if token != nil {
				agentName = token.Name
				tokenID = token.ID
			}
			if err != nil {
				switch {
				case errors.Is(err, security.ErrRateLimited):
					return recordDenied(http.StatusTooManyRequests, err.Error())
				case errors.Is(err, security.ErrWriteScope), errors.Is(err, security.ErrReadScope):
					return recordDenied(http.StatusForbidden, err.Error())
				case errors.Is(err, security.ErrInvalidToken), errors.Is(err, security.ErrDisabledToken):
					return recordDenied(http.StatusUnauthorized, err.Error())
				default:
					return recordDenied(http.StatusInternalServerError, "authorize agent token")
				}
			}
			caller = "agent"
		}

		c.Set("agent-authenticated", secret != "")
		err := next(c)
		statusCode := c.Response().Status
		if statusCode == 0 {
			if err != nil {
				statusCode = http.StatusInternalServerError
			} else {
				statusCode = http.StatusOK
			}
		}
		if caller == "agent" || (request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions) {
			status := "success"
			if err != nil || statusCode >= 400 {
				status = "error"
			}
			s.writeAudit(&domain.AuditLog{
				Caller: caller, TokenID: tokenID, AgentName: agentName, Action: request.Method + " " + c.Path(), Target: request.URL.Path,
				Input: security.Summary(input, 4096), Output: security.Summary(map[string]any{"http_status": statusCode}, 1024),
				Status: status, LatencyMS: time.Since(started).Milliseconds(), IP: c.RealIP(), CreatedAt: time.Now(),
			})
		}
		return err
	}
}

package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
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
	settings   *service.SettingsService
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
) *Server {
	s := &Server{
		echo:       e,
		db:         db,
		drive:      drive,
		emby:       emby,
		cloudDrive: cloudDrive,
		taskQueue:  taskQueue,
		cron:       cron,
		settings:   settings,
	}
	s.registerRoutes()
	return s
}

func (s *Server) Start(addr string) error {
	return s.echo.Start(addr)
}

func (s *Server) registerRoutes() {
	// OpenAPI 3.1 schema
	s.echo.GET("/openapi.json", s.handleOpenAPI)

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

	// 115 Accounts & Cloud Drive
	v1.GET("/accounts", s.handleListAccounts)
	v1.POST("/accounts", s.handleAddAccount)
	v1.GET("/files", s.handleListFiles)
	v1.POST("/files/mkdir", s.handleMkdir)
	v1.POST("/files/rename", s.handleRename)
	v1.POST("/files/move", s.handleMove)
	v1.POST("/offline/download", s.handleAddOffline)

	// Emby & Media
	v1.GET("/emby/libraries", s.handleEmbyLibraries)
	v1.POST("/emby/refresh", s.handleEmbyRefresh)
	v1.POST("/emby/match", s.handleEmbyMatch)

	// CloudDrive2 Mounts
	v1.GET("/mounts", s.handleListMounts)

	// Tasks & Cron
	v1.GET("/tasks", s.handleListTasks)
	v1.POST("/tasks", s.handleCreateTask)
	v1.POST("/tasks/:id/run", s.handleRunTask)
	v1.POST("/tasks/batch", s.handleBatchTasks)
	v1.GET("/async-tasks/:id", s.handleGetAsyncTask)

	// Settings & Agent Tokens
	v1.GET("/settings", s.handleGetSettings)
	v1.POST("/settings", s.handleUpdateSettings)
	v1.POST("/settings/check", s.handleCheckSettings)
	v1.GET("/tokens", s.handleListTokens)
	v1.POST("/tokens", s.handleCreateToken)
	v1.DELETE("/tokens/:id", s.handleDeleteToken)
}

func (s *Server) handleHomeSummary(c echo.Context) error {
	summary, err := s.drive.GetHomeSummary()
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"summary":            nil,
			"upstream_available": false,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"summary":            summary,
		"upstream_available": true,
	})
}

func (s *Server) handleSources(c echo.Context) error {
	sources, err := s.drive.GetSources()
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"sources": []any{}})
	}
	return c.JSON(http.StatusOK, map[string]any{"sources": sources})
}

func (s *Server) handleTrends(c echo.Context) error {
	trends, err := s.drive.GetTrends()
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"code":    0,
			"message": "success",
			"data":    map[string]any{"trends": []any{}},
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"code":    0,
		"message": "success",
		"data":    map[string]any{"trends": trends},
	})
}

func (s *Server) handleSearch(c echo.Context) error {
	// Forward query params directly

	resp, err := s.drive.SearchResources(c.QueryParams())
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"total":      0,
			"links":      []any{},
			"disk_types": []any{},
			"has_more":   false,
		})
	}
	return c.JSON(http.StatusOK, resp)
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

func (s *Server) handleListAccounts(c echo.Context) error {
	accounts, err := s.db.ListAccounts()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if len(accounts) == 0 {
		def, err := s.drive.GetDefaultAccount()
		if err == nil && def != nil {
			accounts = []domain.DriveAccount{*def}
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) handleAddAccount(c echo.Context) error {
	var acc domain.DriveAccount
	if err := c.Bind(&acc); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.db.SaveAccount(&acc); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, acc)
}

func (s *Server) handleListFiles(c echo.Context) error {
	cid := c.QueryParam("cid")
	accountID := c.QueryParam("account_id")
	files, err := s.drive.ListFilesCtx(c.Request().Context(), accountID, cid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleMkdir(c echo.Context) error {
	var req struct {
		AccountID string `json:"account_id"`
		ParentCID string `json:"parent_cid"`
		Name      string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	cid, err := s.drive.MkdirCtx(c.Request().Context(), req.AccountID, req.ParentCID, req.Name)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"cid": cid, "name": req.Name})
}

func (s *Server) handleRename(c echo.Context) error {
	var req struct {
		AccountID string `json:"account_id"`
		FileID    string `json:"file_id"`
		NewName   string `json:"new_name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.drive.RenameCtx(c.Request().Context(), req.AccountID, req.FileID, req.NewName); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleMove(c echo.Context) error {
	var req struct {
		AccountID string   `json:"account_id"`
		FileIDs   []string `json:"file_ids"`
		TargetCID string   `json:"target_cid"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if err := s.drive.MoveCtx(c.Request().Context(), req.AccountID, req.FileIDs, req.TargetCID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
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
	taskID, err := s.drive.AddOfflineTasks(c.Request().Context(), req.AccountID, req.URLs, req.TargetCID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"task_id": taskID})
}

func (s *Server) handleEmbyLibraries(c echo.Context) error {
	libs, err := s.emby.GetLibraries()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error(), "libraries": []any{}})
	}
	return c.JSON(http.StatusOK, map[string]any{"libraries": libs})
}

func (s *Server) handleEmbyRefresh(c echo.Context) error {
	libID := c.QueryParam("library_id")
	if err := s.emby.RefreshLibrary(libID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
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
	if err := s.emby.MatchMedia(req.ItemID, req.TMDBID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleListMounts(c echo.Context) error {
	mounts, err := s.cloudDrive.GetMounts()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"mounts": mounts})
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
	if err := s.cron.ScheduleTask(task); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, task)
}

func (s *Server) handleRunTask(c echo.Context) error {
	id := c.Param("id")
	asyncTaskID, err := s.taskQueue.Enqueue("manual_task", map[string]any{"task_id": id})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"async_task_id": asyncTaskID, "status": "queued"})
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
	taskID, err := s.taskQueue.Enqueue("batch_operations", map[string]any{"operations": req.Operations})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"async_task_id": taskID, "status": "queued"})
}

func (s *Server) handleGetAsyncTask(c echo.Context) error {
	id := c.Param("id")
	task, err := s.db.GetAsyncTask(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "async task not found"})
	}
	return c.JSON(http.StatusOK, task)
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
	_ = c.Bind(&req)
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

func (s *Server) handleCreateToken(c echo.Context) error {
	var req struct {
		Name        string   `json:"name"`
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	token := &domain.AgentToken{
		Name:      req.Name,
		Role:      "agent",
		Scopes:    req.Permissions,
		RateLimit: 60,
		CreatedAt: time.Now(),
	}
	if err := s.db.SaveToken(token); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, token)
}

func (s *Server) handleDeleteToken(c echo.Context) error {
	id := c.Param("id")
	if err := s.db.DeleteToken(id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleOpenAPI(c echo.Context) error {
	schema := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "EmbyMedia V2 API",
			"version":     "2.0.0",
			"description": "Unified 115 Drive, Emby, CloudDrive2 & Resource Indexing API for AI Agents & Web UI",
		},
		"paths": map[string]any{
			"/api/v1/home/summary":     map[string]any{"get": map[string]any{"summary": "Home summary metrics"}},
			"/api/v1/sources":          map[string]any{"get": map[string]any{"summary": "Indexed resource sources"}},
			"/api/v1/trends":           map[string]any{"get": map[string]any{"summary": "Trending searches"}},
			"/api/v1/search":           map[string]any{"get": map[string]any{"summary": "Search indexed netdisk resources"}},
			"/api/v1/links/{id}":       map[string]any{"get": map[string]any{"summary": "Get resource link details"}},
			"/api/v1/accounts":         map[string]any{"get": map[string]any{"summary": "List 115 Drive accounts"}},
			"/api/v1/files":            map[string]any{"get": map[string]any{"summary": "Browse files in 115 Drive"}},
			"/api/v1/files/mkdir":      map[string]any{"post": map[string]any{"summary": "Create directory in 115 Drive"}},
			"/api/v1/files/rename":     map[string]any{"post": map[string]any{"summary": "Rename file in 115 Drive"}},
			"/api/v1/files/move":       map[string]any{"post": map[string]any{"summary": "Move files in 115 Drive"}},
			"/api/v1/offline/download": map[string]any{"post": map[string]any{"summary": "Add offline magnet/sha1 download"}},
			"/api/v1/emby/libraries":   map[string]any{"get": map[string]any{"summary": "List Emby libraries"}},
			"/api/v1/emby/refresh":     map[string]any{"post": map[string]any{"summary": "Trigger Emby library scan"}},
			"/api/v1/mounts":           map[string]any{"get": map[string]any{"summary": "List CloudDrive2 mount status"}},
			"/api/v1/tasks":            map[string]any{"get": map[string]any{"summary": "List scheduled tasks"}},
			"/api/v1/tasks/batch":      map[string]any{"post": map[string]any{"summary": "Execute batch operations"}},
			"/api/v1/tokens":           map[string]any{"get": map[string]any{"summary": "Manage agent tokens"}},
		},
	}
	return c.JSON(http.StatusOK, schema)
}

func (s *Server) auditLogMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		caller := "ui"
		tokenID := ""
		authHeader := c.Request().Header.Get("Authorization")
		if authHeader != "" {
			caller = "agent"
			tokenID = authHeader
		}

		err := next(c)

		// Record write operations or sensitive operations
		method := c.Request().Method
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
			log := &domain.AuditLog{
				Caller:    caller,
				TokenID:   tokenID,
				Action:    c.Request().Method + " " + c.Path(),
				Target:    c.Request().RequestURI,
				IP:        c.RealIP(),
				CreatedAt: time.Now(),
			}
			_ = s.db.AddAuditLog(log)
		}
		return err
	}
}

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
)

func TestAPIRoutes(t *testing.T) {
	e := echo.New()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	cloudDrive := service.NewCloudDriveService(db)
	taskQueue := service.NewTaskQueueService(db, drive, emby)
	cron := service.NewCronManager(db)
	settings := service.NewSettingsService(db)

	_ = NewServer(e, db, drive, emby, cloudDrive, taskQueue, cron, settings)

	// Test OpenAPI endpoint
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /openapi.json, got %d", rec.Code)
	}

	// Test /api/v1/home/summary fallback
	req = httptest.NewRequest(http.MethodGet, "/api/v1/home/summary", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/home/summary, got %d", rec.Code)
	}

	// Test /search fallback
	req = httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /search, got %d", rec.Code)
	}
}

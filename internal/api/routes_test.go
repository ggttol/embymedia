package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	req = httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/openapi.json, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"Hermes","permissions":["read"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating token, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created token: %v", err)
	}
	if !strings.HasPrefix(created.Token, "embymedia_") {
		t.Fatalf("expected one-time token secret, got %q", created.Token)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("X-Agent-Token", created.Token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected valid Agent token to authorize, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Agent-Token", created.Token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected read-only Agent token to reject writes, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("X-Agent-Token", "invalid")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid Agent token to be rejected, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Fatal("token listing exposed the one-time secret")
	}
}

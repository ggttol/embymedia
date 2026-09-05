package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
)

func TestAPIRoutes(t *testing.T) {
	e := echo.New()
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Request().Header.Set("Remote-User", "test-admin")
			return next(c)
		}
	})
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	resourceServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		switch request.URL.Path {
		case "/api/v1/home/summary":
			_, _ = response.Write([]byte(`{"summary":{"links":1}}`))
		case "/search":
			if request.URL.Query().Get("q") == "error" {
				_, _ = response.Write([]byte(`{"code":503,"message":"index unavailable"}`))
			} else {
				_, _ = response.Write([]byte(`{"total":0,"links":[],"has_more":false}`))
			}
		default:
			_, _ = response.Write([]byte(`{}`))
		}
	}))
	defer resourceServer.Close()
	drive := service.NewDriveService(db, resourceServer.URL, "")
	emby := service.NewEmbyService(db)
	cloudDrive := service.NewCloudDriveService(db)
	taskQueue := service.NewTaskQueueService(db, drive, emby)
	cron := service.NewCronManager(db, taskQueue)
	settings := service.NewSettingsService(db)
	authorizer := security.NewAgentAuthorizer(db)
	server := NewServer(e, db, drive, emby, cloudDrive, taskQueue, cron, settings, authorizer)
	defer server.Close()

	// Test OpenAPI endpoint
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /openapi.json, got %d", rec.Code)
	}

	// Test resource summary proxy
	req = httptest.NewRequest(http.MethodGet, "/api/v1/home/summary", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/home/summary, got %d", rec.Code)
	}

	// Test resource search proxy
	req = httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /search, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/search?q=error", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected resource error envelope to return 502, got %d", rec.Code)
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

	if err := db.SetSetting("clouddrive_webhook_secret", "webhook-secret"); err != nil {
		t.Fatalf("set webhook secret: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/hooks/clouddrive2", strings.NewReader(`{"path":"/media/example.mkv"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Webhook-Secret", "wrong")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid webhook secret to be rejected, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/hooks/clouddrive2", strings.NewReader(`{"path":"/media/example.mkv"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Webhook-Secret", "webhook-secret")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected valid webhook event to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPICoversEveryAPIRoute(t *testing.T) {
	e := echo.New()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	queue := service.NewTaskQueueService(db, drive, emby)
	NewServer(e, db, drive, emby, service.NewCloudDriveService(db), queue, service.NewCronManager(db, queue), service.NewSettingsService(db), security.NewAgentAuthorizer(db))
	paths := openAPISchema()["paths"].(map[string]any)
	parameter := regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)
	for _, route := range e.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/") || route.Path == "/api/v1/openapi.json" || strings.Contains(route.Method, "echo_route_not_found") {
			continue
		}
		path := parameter.ReplaceAllString(route.Path, `{$1}`)
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("OpenAPI is missing route %s %s", route.Method, path)
			continue
		}
		operation, ok := pathItem[strings.ToLower(route.Method)].(map[string]any)
		if !ok || operation["operationId"] == "" {
			t.Errorf("OpenAPI is missing operation %s %s", route.Method, path)
		}
	}
}

func TestProxiedRESTRequiresAgentToken(t *testing.T) {
	e := echo.New()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	queue := service.NewTaskQueueService(db, drive, emby)
	server := NewServer(e, db, drive, emby, service.NewCloudDriveService(db), queue, service.NewCronManager(db, queue), service.NewSettingsService(db), security.NewAgentAuthorizer(db))
	defer server.Close()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	request.Header.Set("X-Forwarded-For", "203.0.113.10")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("proxied tokenless request returned %d", response.Code)
	}
}

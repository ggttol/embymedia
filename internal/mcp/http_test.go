package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func saveTestToken(t *testing.T, db *storage.DB, secret string, scopes []string, rate int) {
	t.Helper()
	digest := sha256.Sum256([]byte(secret))
	if err := db.SaveToken(&domain.AgentToken{ID: secret, Token: hex.EncodeToString(digest[:]), Name: secret, Role: "agent", Scopes: scopes, RateLimit: rate, Enabled: true}); err != nil {
		t.Fatalf("save token: %v", err)
	}
}

func postMCP(t *testing.T, url, sessionID, token string, payload map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create MCP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	if token != "" {
		request.Header.Set("X-Agent-Token", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send MCP request: %v", err)
	}
	defer response.Body.Close()
	var result map[string]any
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatalf("decode MCP response: %v", err)
		}
	}
	return response, result
}

func TestStreamableMCPEnforcesSharedAgentPolicyAndAudits(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	readSecret := "embymedia_read"
	saveTestToken(t, db, readSecret, []string{"read"}, 1)
	drive := service.NewDriveService(db, "http://127.0.0.1:8100", "")
	emby := service.NewEmbyService(db)
	queue := service.NewTaskQueueService(db, drive, emby)
	authorizer := security.NewAgentAuthorizer(db)
	mcpServer := NewMCPServer(db, drive, emby, service.NewCloudDriveService(db), queue, authorizer, false)
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpServer.Server()))
	defer httpServer.Close()

	initialize := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}}
	response, _ := postMCP(t, httpServer.URL, "", readSecret, initialize)
	sessionID := response.Header.Get("Mcp-Session-Id")
	if response.StatusCode != http.StatusOK || sessionID == "" {
		t.Fatalf("MCP initialize failed: status=%d session=%q", response.StatusCode, sessionID)
	}
	postMCP(t, httpServer.URL, sessionID, readSecret, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	_, denied := postMCP(t, httpServer.URL, sessionID, readSecret, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "c115_mkdir", "arguments": map[string]any{"name": "blocked"}}})
	toolResult := denied["result"].(map[string]any)
	if toolResult["isError"] != true {
		t.Fatalf("read-only token executed a write tool: %v", denied)
	}

	_, firstRead := postMCP(t, httpServer.URL, sessionID, readSecret, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "system_get_config", "arguments": map[string]any{}}})
	if firstRead["result"].(map[string]any)["isError"] == true {
		t.Fatalf("first read unexpectedly failed: %v", firstRead)
	}
	_, limited := postMCP(t, httpServer.URL, sessionID, readSecret, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "system_get_config", "arguments": map[string]any{}}})
	if limited["result"].(map[string]any)["isError"] != true {
		t.Fatalf("rate limit did not reject the second read: %v", limited)
	}
	audits, err := db.ListAuditLogs(10)
	if err != nil || len(audits) != 3 {
		t.Fatalf("MCP calls were not fully audited: %+v, err=%v", audits, err)
	}
	if audits[0].LatencyMS < 0 || audits[0].CreatedAt.After(time.Now()) {
		t.Fatalf("invalid MCP audit timing: %+v", audits[0])
	}
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestMCPHTTPAuthentication(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	secret := "embymedia_http"
	digest := sha256.Sum256([]byte(secret))
	if err := db.SaveToken(&domain.AgentToken{ID: "http", Token: hex.EncodeToString(digest[:]), Name: "HTTP", Scopes: []string{"read"}, RateLimit: 20, Enabled: true}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	handler := authenticateMCPHTTP(security.NewAgentAuthorizer(db), http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token returned %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("X-Agent-Token", secret)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid token returned %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Remote-User", "administrator")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated browser session returned %d", response.Code)
	}
}

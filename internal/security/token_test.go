package security

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestAgentAuthorizerScopesRateAndEnabledState(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	secret := "embymedia_test"
	digest := sha256.Sum256([]byte(secret))
	token := &domain.AgentToken{ID: "token", Token: hex.EncodeToString(digest[:]), Name: "test", Role: "readonly", Scopes: []string{"read"}, RateLimit: 1, Enabled: true}
	if err := db.SaveToken(token); err != nil {
		t.Fatalf("save token: %v", err)
	}
	authorizer := NewAgentAuthorizer(db)
	now := time.Now()
	if _, err := authorizer.Authorize(secret, false, now); err != nil {
		t.Fatalf("authorize read: %v", err)
	}
	if _, err := authorizer.Authorize(secret, false, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	authorizer = NewAgentAuthorizer(db)
	if _, err := authorizer.Authorize(secret, true, now); !errors.Is(err, ErrWriteScope) {
		t.Fatalf("expected write-scope rejection, got %v", err)
	}
	token.Enabled = false
	if err := db.SaveToken(token); err != nil {
		t.Fatalf("disable token: %v", err)
	}
	if _, err := NewAgentAuthorizer(db).Authorize(secret, false, now); !errors.Is(err, ErrDisabledToken) {
		t.Fatalf("expected disabled-token rejection, got %v", err)
	}
}

func TestAuditSummaryRedactsNestedSecretsAndURLs(t *testing.T) {
	summary := Summary(map[string]any{
		"password": "visible",
		"nested":   map[string]any{"api_token": "visible", "url": "https://115.com/s/code?password=visible"},
	}, 4096)
	if strings.Contains(summary, "visible") {
		t.Fatalf("audit summary exposed a secret: %s", summary)
	}
}

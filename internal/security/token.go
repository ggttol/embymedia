package security

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

var (
	ErrInvalidToken  = errors.New("invalid agent token")
	ErrDisabledToken = errors.New("agent token is disabled")
	ErrRateLimited   = errors.New("agent token rate limit exceeded")
	ErrWriteScope    = errors.New("agent token lacks write permission")
	ErrReadScope     = errors.New("agent token lacks read permission")
)

type rateBucket struct {
	windowStart time.Time
	count       int
}

// AgentAuthorizer authenticates and throttles tokens across REST and MCP ingress.
type AgentAuthorizer struct {
	db      *storage.DB
	mu      sync.Mutex
	buckets map[string]rateBucket
}

// NewAgentAuthorizer creates a process-wide token authorizer.
func NewAgentAuthorizer(db *storage.DB) *AgentAuthorizer {
	return &AgentAuthorizer{db: db, buckets: make(map[string]rateBucket)}
}

// BearerToken reads the supported Agent authentication headers.
func BearerToken(header http.Header) string {
	if secret := strings.TrimSpace(header.Get("X-Agent-Token")); secret != "" {
		return secret
	}
	authorization := strings.TrimSpace(header.Get("Authorization"))
	if strings.HasPrefix(authorization, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	}
	return ""
}

// TrustedWithoutToken reports whether an authenticated browser session initiated the request.
func TrustedWithoutToken(header http.Header) bool {
	return len(header.Values("X-Agent-Token")) == 0 && len(header.Values("Authorization")) == 0 && strings.TrimSpace(header.Get("Remote-User")) != ""
}

func containsScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

func (a *AgentAuthorizer) take(token *domain.AgentToken, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	bucket := a.buckets[token.ID]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= time.Minute {
		a.buckets[token.ID] = rateBucket{windowStart: now, count: 1}
		return true
	}
	if bucket.count >= token.RateLimit {
		return false
	}
	bucket.count++
	a.buckets[token.ID] = bucket
	return true
}

// Authenticate validates a token without consuming its tool-call rate budget.
func (a *AgentAuthorizer) Authenticate(secret string) (*domain.AgentToken, error) {
	digest := sha256.Sum256([]byte(secret))
	token, err := a.db.GetTokenByDigest(hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, ErrInvalidToken
	}
	if !token.Enabled {
		return token, ErrDisabledToken
	}
	return token, nil
}

// Authorize authenticates one token use, enforces scope and rate, and records use time.
func (a *AgentAuthorizer) Authorize(secret string, write bool, now time.Time) (*domain.AgentToken, error) {
	digest := sha256.Sum256([]byte(secret))
	token, err := a.db.GetTokenByDigest(hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, ErrInvalidToken
	}
	if !token.Enabled {
		return token, ErrDisabledToken
	}
	if write && !containsScope(token.Scopes, "write") {
		return token, ErrWriteScope
	}
	if !write && !containsScope(token.Scopes, "read") && !containsScope(token.Scopes, "write") {
		return token, ErrReadScope
	}
	if !a.take(token, now) {
		return token, ErrRateLimited
	}
	if err := a.db.TouchToken(token.ID, now); err != nil {
		return token, err
	}
	return token, nil
}

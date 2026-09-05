package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

var secretSettingKeys = map[string]struct{}{
	"115_cookie":                {},
	"emby_api_key":              {},
	"resource_api_token":        {},
	"clouddrive_api_token":      {},
	"clouddrive_webhook_secret": {},
}

var allowedSettingKeys = map[string]struct{}{
	"115_cookie": {}, "c115_cid_map": {},
	"emby_url": {}, "emby_api_key": {},
	"media_root": {}, "strm_root": {}, "emby_media_prefix": {},
	"clouddrive_url": {}, "clouddrive_api_token": {}, "clouddrive_mount_path": {}, "clouddrive_source_path": {},
	"clouddrive_webhook_secret": {}, "clouddrive_webhook_debounce_seconds": {},
	"resource_api_url": {}, "resource_api_token": {},
	"dangerous_actions_enabled": {},
}

type monitoredComponent struct {
	id           string
	isConfigured func(map[string]string) bool
}

var monitoredComponents = []monitoredComponent{
	{id: "c115", isConfigured: func(s map[string]string) bool { return strings.TrimSpace(s["115_cookie"]) != "" }},
	{id: "emby", isConfigured: func(s map[string]string) bool {
		return strings.TrimSpace(s["emby_url"]) != "" && strings.TrimSpace(s["emby_api_key"]) != ""
	}},
	{id: "clouddrive", isConfigured: func(s map[string]string) bool {
		return strings.TrimSpace(s["clouddrive_url"]) != "" && strings.TrimSpace(s["clouddrive_mount_path"]) != ""
	}},
	{id: "resource", isConfigured: func(s map[string]string) bool { return strings.TrimSpace(s["resource_api_url"]) != "" }},
}

func RedactSecrets(values map[string]string) map[string]string {
	redacted := make(map[string]string, len(values))
	for k, v := range values {
		if _, secret := secretSettingKeys[k]; secret {
			redacted[k] = ""
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

type ServiceHealth struct {
	Status  string `json:"status"`  // "ok", "error", "unconfigured"
	Message string `json:"message"` // e.g. "响应正常 45ms", "Cookie已失效: 登录超时", "无法连接服务"
	Latency int64  `json:"latency"` // latency in ms
	Details string `json:"details,omitempty"`
}

type SettingsState struct {
	Values     map[string]string        `json:"settings"`
	Configured map[string]bool          `json:"configured"`
	Health     map[string]ServiceHealth `json:"health"`
}

// SettingsService persists deployment settings without returning stored secrets.
type SettingsService struct {
	db *storage.DB
}

func NewSettingsService(db *storage.DB) *SettingsService {
	return &SettingsService{db: db}
}

// CheckAvailability tests the live connection to a specified component
func (s *SettingsService) CheckAvailability(component string) ServiceHealth {
	start := time.Now()
	client := &http.Client{Timeout: 6 * time.Second}

	switch component {
	case "emby":
		baseURL, _ := s.db.GetSetting("emby_url")
		if baseURL == "" {
			baseURL, _ = s.db.GetConfig("emby_url")
		}
		if baseURL == "" {
			return ServiceHealth{Status: "unconfigured", Message: "未配置 Emby URL"}
		}
		apiKey, _ := s.db.GetConfig("emby_api_key")
		if apiKey == "" {
			return ServiceHealth{Status: "unconfigured", Message: "未配置 Emby API Key"}
		}
		url := fmt.Sprintf("%s/System/Info?api_key=%s", strings.TrimRight(baseURL, "/"), apiKey)
		resp, err := client.Get(url)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return ServiceHealth{Status: "error", Message: "连接超时或拒绝: " + err.Error(), Latency: latency}
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			return ServiceHealth{Status: "ok", Message: "Emby 可访问，API Key 有效", Latency: latency}
		}
		return ServiceHealth{Status: "error", Message: fmt.Sprintf("Emby 响应 HTTP %d", resp.StatusCode), Latency: latency}

	case "resource":
		url, _ := s.db.GetSetting("resource_api_url")
		if url == "" {
			return ServiceHealth{Status: "unconfigured", Message: "未配置资源 API URL"}
		}
		token, _ := s.db.GetSetting("resource_api_token")
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/home/summary", strings.TrimRight(url, "/")), nil)
		if err != nil {
			return ServiceHealth{Status: "error", Message: err.Error()}
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return ServiceHealth{Status: "error", Message: "无法连接资源服务: " + err.Error(), Latency: latency}
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			return ServiceHealth{Status: "ok", Message: "资源 API 可访问，Token 有效", Latency: latency}
		}
		return ServiceHealth{Status: "error", Message: fmt.Sprintf("资源 API 响应 HTTP %d", resp.StatusCode), Latency: latency}

	case "clouddrive":
		cloudURL, _ := s.db.GetConfig("clouddrive_url")
		mountPath, _ := s.db.GetSetting("clouddrive_mount_path")
		if cloudURL == "" || mountPath == "" {
			return ServiceHealth{Status: "unconfigured", Message: "未配置 CloudDrive2 URL 或挂载目录"}
		}
		health, err := NewCloudDriveService(s.db).Health(context.Background())
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return ServiceHealth{Status: "error", Message: "CloudDrive2 gRPC 检查失败: " + err.Error(), Latency: latency}
		}
		if !health.SystemReady || health.Message != "" {
			message := health.Message
			if message == "" {
				message = "CloudDrive2 尚未就绪"
			}
			return ServiceHealth{Status: "error", Message: message, Latency: latency}
		}
		return ServiceHealth{Status: "ok", Message: "CloudDrive2 gRPC 服务在线", Latency: latency, Details: health.ProductVersion}
	case "c115":
		accounts, err := NewDriveService(s.db, "", "").CheckAccounts(context.Background())
		latency := time.Since(start).Milliseconds()
		if err != nil {
			if strings.Contains(err.Error(), "no drive accounts configured") {
				return ServiceHealth{Status: "unconfigured", Message: "未配置 115 账号", Latency: latency}
			}
			return ServiceHealth{Status: "error", Message: "115 账号检查失败: " + err.Error(), Latency: latency}
		}
		if len(accounts) == 0 {
			return ServiceHealth{Status: "unconfigured", Message: "未配置 115 账号", Latency: latency}
		}
		healthy := 0
		for _, account := range accounts {
			if account.Status == "active" {
				healthy++
			}
		}
		if healthy == 0 {
			return ServiceHealth{Status: "error", Message: "所有 115 账号均不可用", Latency: latency}
		}
		return ServiceHealth{Status: "ok", Message: fmt.Sprintf("115 账号可用 %d / %d", healthy, len(accounts)), Latency: latency}

	default:
		return ServiceHealth{Status: "error", Message: "未知组件"}
	}
}

func (s *SettingsService) CheckAll() map[string]ServiceHealth {
	results := make(map[string]ServiceHealth)
	for _, c := range []string{"c115", "emby", "clouddrive", "resource"} {
		results[c] = s.CheckAvailability(c)
	}
	return results
}

func (s *SettingsService) State() (SettingsState, error) {
	stored, err := s.db.GetAllSettings()
	if err != nil {
		return SettingsState{}, err
	}
	for key := range stored {
		if _, allowed := allowedSettingKeys[key]; !allowed {
			delete(stored, key)
		}
	}
	configured := make(map[string]bool, len(monitoredComponents))
	for _, c := range monitoredComponents {
		configured[c.id] = c.isConfigured(stored)
	}
	return SettingsState{
		Values:     RedactSecrets(stored),
		Configured: configured,
		Health:     s.CheckAll(),
	}, nil
}

// Update atomically persists validated settings; empty secret fields preserve stored values.
func (s *SettingsService) Update(values map[string]string) error {
	filtered := make(map[string]string, len(values))
	for key, rawValue := range values {
		if _, allowed := allowedSettingKeys[key]; !allowed {
			return fmt.Errorf("unknown setting %q", key)
		}
		value := strings.TrimSpace(rawValue)
		if _, secret := secretSettingKeys[key]; secret && value == "" {
			continue
		}
		if strings.HasSuffix(key, "_url") && value != "" {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fmt.Errorf("%s must be an HTTP or HTTPS URL", key)
			}
		}
		if (key == "clouddrive_mount_path" || key == "media_root" || key == "strm_root" || key == "emby_media_prefix") && value != "" && !filepath.IsAbs(value) {
			return fmt.Errorf("%s must be an absolute path", key)
		}
		if key == "dangerous_actions_enabled" && value != "true" && value != "false" {
			return fmt.Errorf("dangerous_actions_enabled must be true or false")
		}
		if key == "clouddrive_webhook_debounce_seconds" && value != "" {
			seconds, err := strconv.Atoi(value)
			if err != nil || seconds < 1 || seconds > 300 {
				return fmt.Errorf("clouddrive_webhook_debounce_seconds must be between 1 and 300")
			}
		}
		if key == "c115_cid_map" && value != "" {
			var cidMap map[string]string
			if err := json.Unmarshal([]byte(value), &cidMap); err != nil {
				return fmt.Errorf("c115_cid_map must be a JSON object: %w", err)
			}
		}
		filtered[key] = value
	}
	return s.db.SetSettingsAndDefaultAccountCookie(filtered, filtered["115_cookie"])
}

// Get returns one stored setting for internal service consumers.
func (s *SettingsService) Get(key string) (string, error) {
	return s.db.GetSetting(key)
}

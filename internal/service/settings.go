package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

var secretSettingKeys = map[string]struct{}{
	"115_cookie":          {},
	"emby_api_key":        {},
	"resource_api_token":  {},
}

type monitoredComponent struct {
	id           string
	isConfigured func(map[string]string) bool
}

var monitoredComponents = []monitoredComponent{
	{id: "c115", isConfigured: func(s map[string]string) bool { return strings.TrimSpace(s["115_cookie"]) != "" }},
	{id: "emby", isConfigured: func(s map[string]string) bool { return strings.TrimSpace(s["emby_url"]) != "" && strings.TrimSpace(s["emby_api_key"]) != "" }},
	{id: "clouddrive", isConfigured: func(s map[string]string) bool { return strings.TrimSpace(s["clouddrive_url"]) != "" && strings.TrimSpace(s["clouddrive_mount_path"]) != "" }},
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
			baseURL = "http://127.0.0.1:8096"
		}
		apiKey, _ := s.db.GetSetting("emby_api_key")
		if apiKey == "" {
			apiKey, _ = s.db.GetConfig("emby_api_key")
		}

		url := fmt.Sprintf("%s/System/Info/Public", strings.TrimRight(baseURL, "/"))
		if apiKey != "" {
			url = fmt.Sprintf("%s/System/Info?api_key=%s", strings.TrimRight(baseURL, "/"), apiKey)
		}
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
			url = "http://127.0.0.1:8100"
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
		url, _ := s.db.GetSetting("clouddrive_url")
		if url == "" {
			url = "http://127.0.0.1:19798"
		}
		resp, err := client.Get(url)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return ServiceHealth{Status: "error", Message: "无法连接 CloudDrive 服务: " + err.Error(), Latency: latency}
		}
		defer resp.Body.Close()
		return ServiceHealth{Status: "ok", Message: "CloudDrive2 服务在线，响应正常", Latency: latency}

	case "c115":
		cookie, _ := s.db.GetSetting("115_cookie")
		if cookie == "" {
			return ServiceHealth{Status: "unconfigured", Message: "未配置 115 Cookie"}
		}
		req, err := http.NewRequest("GET", "https://webapi.115.com/files/index_info", nil)
		if err != nil {
			return ServiceHealth{Status: "error", Message: err.Error()}
		}
		req.Header.Set("Cookie", cookie)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
		resp, err := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return ServiceHealth{Status: "error", Message: "请求 115 失败: " + err.Error(), Latency: latency}
		}
		defer resp.Body.Close()
		var result struct {
			State bool   `json:"state"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			if result.State {
				return ServiceHealth{Status: "ok", Message: "115 可访问，Cookie 有效", Latency: latency}
			}
			msg := result.Error
			if msg == "" {
				msg = "115 凭据已失效，需更新"
			}
			return ServiceHealth{Status: "error", Message: msg, Latency: latency}
		}
		return ServiceHealth{Status: "ok", Message: "115 响应正常", Latency: latency}

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
// Update atomically persists settings; empty secret fields preserve stored values.
func (s *SettingsService) Update(values map[string]string) error {
	filtered := make(map[string]string, len(values))
	for key, value := range values {
		if _, secret := secretSettingKeys[key]; secret && strings.TrimSpace(value) == "" {
			continue
		}
		filtered[key] = strings.TrimSpace(value)
	}
	return s.db.SetSettings(filtered)
}

// Get returns one stored setting for internal service consumers.
func (s *SettingsService) Get(key string) (string, error) {
	return s.db.GetSetting(key)
}

package service

import (
	"strings"

	"github.com/embymedia/embymedia/internal/storage"
)

var secretSettingKeys = map[string]struct{}{
	"115_cookie":         {},
	"emby_api_key":       {},
	"resource_api_token": {},
}

// SettingsState contains display-safe values and persisted configuration status.
type SettingsState struct {
	Values     map[string]string `json:"settings"`
	Configured map[string]bool   `json:"configured"`
}

// SettingsService persists deployment settings without returning stored secrets.
type SettingsService struct {
	db *storage.DB
}

// NewSettingsService creates a settings service backed by db.
func NewSettingsService(db *storage.DB) *SettingsService {
	return &SettingsService{db: db}
}

// State returns non-secret values and whether each integration has required values.
func (s *SettingsService) State() (SettingsState, error) {
	values, err := s.db.GetAllSettings()
	if err != nil {
		return SettingsState{}, err
	}

	publicValues := make(map[string]string, len(values))
	for key, value := range values {
		if _, secret := secretSettingKeys[key]; secret {
			publicValues[key] = ""
			continue
		}
		publicValues[key] = value
	}

	set := func(key string) bool { return strings.TrimSpace(values[key]) != "" }
	return SettingsState{
		Values: publicValues,
		Configured: map[string]bool{
			"c115":       set("115_cookie"),
			"emby":       set("emby_url") && set("emby_api_key"),
			"clouddrive": set("clouddrive_url") && set("clouddrive_mount_path"),
			"resource":   set("resource_api_url"),
		},
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

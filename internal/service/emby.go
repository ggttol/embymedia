package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type EmbyService struct {
	db     *storage.DB
	client *http.Client
}

func NewEmbyService(db *storage.DB) *EmbyService {
	return &EmbyService{
		db: db,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (s *EmbyService) getURLAndKey() (string, string, error) {
	baseURL, err := s.db.GetSetting("emby_url")
	if err != nil || baseURL == "" {
		baseURL, _ = s.db.GetConfig("emby_url")
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8096"
	}
	apiKey, err := s.db.GetSetting("emby_api_key")
	if err != nil || apiKey == "" {
		apiKey, _ = s.db.GetConfig("emby_api_key")
	}
	return strings.TrimRight(baseURL, "/"), apiKey, nil
}

// GetSystemInfo retrieves Emby server status and version
func (s *EmbyService) GetSystemInfo() (map[string]any, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/System/Info?api_key=%s", baseURL, apiKey)

	resp, err := s.client.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("emby connect: %w", err)
	}
	defer resp.Body.Close()

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// ListLibraries fetches all media libraries from Emby
func (s *EmbyService) ListLibraries() ([]domain.EmbyLibrary, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Library/VirtualFolders?api_key=%s", baseURL, apiKey)

	resp, err := s.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []struct {
		Name           string   `json:"Name"`
		CollectionType string   `json:"CollectionType"`
		ItemID         string   `json:"ItemId"`
		Locations      []string `json:"Locations"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	var res []domain.EmbyLibrary
	for _, lib := range raw {
		res = append(res, domain.EmbyLibrary{
			ID:         lib.ItemID,
			Name:       lib.Name,
			Collection: lib.CollectionType,
			Locations:  lib.Locations,
		})
	}
	return res, nil
}

// RefreshLibrary initiates a library scan in Emby
func (s *EmbyService) RefreshLibrary(libraryID string) error {
	baseURL, apiKey, _ := s.getURLAndKey()

	var reqURL string
	if libraryID == "" {
		reqURL = fmt.Sprintf("%s/Library/Refresh?api_key=%s", baseURL, apiKey)
	} else {
		reqURL = fmt.Sprintf("%s/Items/%s/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&api_key=%s", baseURL, libraryID, apiKey)
	}

	req, err := http.NewRequest("POST", reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("emby refresh failed with status %d", resp.StatusCode)
	}
	return nil
}

// ListSessions returns active playback sessions
func (s *EmbyService) ListSessions() ([]domain.EmbyPlaybackSession, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Sessions?api_key=%s", baseURL, apiKey)

	resp, err := s.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []struct {
		ID             string `json:"Id"`
		UserName       string `json:"UserName"`
		Client         string `json:"Client"`
		DeviceName     string `json:"DeviceName"`
		PlayState      struct {
			IsPaused      bool   `json:"IsPaused"`
			PositionTicks int64  `json:"PositionTicks"`
			PlayMethod    string `json:"PlayMethod"`
		} `json:"PlayState"`
		NowPlayingItem *struct {
			Name string `json:"Name"`
			Type string `json:"Type"`
		} `json:"NowPlayingItem"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	var res []domain.EmbyPlaybackSession
	for _, sess := range raw {
		if sess.NowPlayingItem == nil {
			continue
		}
		playState := "Playing"
		if sess.PlayState.IsPaused {
			playState = "Paused"
		}


		res = append(res, domain.EmbyPlaybackSession{
			ID:             sess.ID,
			UserName:       sess.UserName,
			ItemName:       sess.NowPlayingItem.Name,
			ItemType:       sess.NowPlayingItem.Type,
			Client:         sess.Client,
			DeviceName:     sess.DeviceName,
			PlayState:      playState,
			PositionTicks:  sess.PlayState.PositionTicks,
			PlaybackMethod: sess.PlayState.PlayMethod,
		})
	}
	return res, nil
}

func (s *EmbyService) GetLibraries() ([]domain.EmbyLibrary, error) {
	return s.ListLibraries()
}

func (s *EmbyService) MatchMedia(itemID, tmdbID string) error {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Items/RemoteSearch/Apply/%s?api_key=%s", baseURL, itemID, apiKey)
	body := fmt.Sprintf(`{"ProviderIds":{"Tmdb":"%s"}}`, tmdbID)
	resp, err := s.client.Post(reqURL, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// SearchMedia searches items in Emby by keyword
func (s *EmbyService) SearchMedia(term string, limit int) ([]domain.EmbyMediaItem, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	if limit <= 0 {
		limit = 20
	}
	reqURL := fmt.Sprintf("%s/Items?SearchTerm=%s&Recursive=true&Limit=%d&api_key=%s", baseURL, url.QueryEscape(term), limit, apiKey)

	resp, err := s.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Items []struct {
			ID           string            `json:"Id"`
			Name         string            `json:"Name"`
			Type         string            `json:"Type"`
			Path         string            `json:"Path"`
			PremiereDate string            `json:"PremiereDate"`
			ImageTags    map[string]string `json:"ImageTags"`
			ProviderIds  map[string]string `json:"ProviderIds"`
		} `json:"Items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	var res []domain.EmbyMediaItem
	for _, item := range raw.Items {
		_, hasPrimary := item.ImageTags["Primary"]
		_, hasBackdrop := item.ImageTags["Backdrop"]

		res = append(res, domain.EmbyMediaItem{
			ID:           item.ID,
			Name:         item.Name,
			Type:         item.Type,
			Path:         item.Path,
			PremiereDate: item.PremiereDate,
			HasPoster:    hasPrimary,
			HasBackdrop:  hasBackdrop,
			ProviderIDs:  item.ProviderIds,
		})
	}
	return res, nil
}

// CleanupMissingMedia finds items whose backing path does not exist
func (s *EmbyService) GetMediaWithoutPosters() ([]domain.EmbyMediaItem, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Items?Recursive=true&IncludeItemTypes=Movie,Series&ImageTypes=None&api_key=%s", baseURL, apiKey)

	resp, err := s.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		Items []struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
			Type string `json:"Type"`
			Path string `json:"Path"`
		} `json:"Items"`
	}
	_ = json.Unmarshal(body, &raw)

	var res []domain.EmbyMediaItem
	for _, item := range raw.Items {
		res = append(res, domain.EmbyMediaItem{
			ID:        item.ID,
			Name:      item.Name,
			Type:      item.Type,
			Path:      item.Path,
			HasPoster: false,
		})
	}
	return res, nil
}

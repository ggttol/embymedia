package service

import (
	"context"
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

func requireEmbyResponse(response *http.Response, operation string) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
	}
	return nil
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
	if err := requireEmbyResponse(resp, "Emby system info"); err != nil {
		return nil, err
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// ListLibraries fetches all media libraries from Emby.
func (s *EmbyService) ListLibraries() ([]domain.EmbyLibrary, error) {
	return s.ListLibrariesCtx(context.Background())
}

// ListLibrariesCtx fetches all media libraries with cancellation.
func (s *EmbyService) ListLibrariesCtx(ctx context.Context) ([]domain.EmbyLibrary, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Library/VirtualFolders?api_key=%s", baseURL, apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby library list"); err != nil {
		return nil, err
	}

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

// RefreshLibrary initiates a library scan in Emby.
func (s *EmbyService) RefreshLibrary(libraryID string) error {
	return s.RefreshLibraryCtx(context.Background(), libraryID)
}

// RefreshLibraryCtx initiates a cancellable library scan in Emby.
func (s *EmbyService) RefreshLibraryCtx(ctx context.Context, libraryID string) error {
	baseURL, apiKey, _ := s.getURLAndKey()
	var reqURL string
	if libraryID == "" {
		reqURL = fmt.Sprintf("%s/Library/Refresh?api_key=%s", baseURL, url.QueryEscape(apiKey))
	} else {
		reqURL = fmt.Sprintf("%s/Items/%s/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&api_key=%s", baseURL, url.PathEscape(libraryID), url.QueryEscape(apiKey))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh Emby library: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Emby refresh returned HTTP %d", resp.StatusCode)
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
	if err := requireEmbyResponse(resp, "Emby session list"); err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"Id"`
		UserName   string `json:"UserName"`
		Client     string `json:"Client"`
		DeviceName string `json:"DeviceName"`
		PlayState  struct {
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

// GetLibrariesCtx returns Emby libraries with cancellation.
func (s *EmbyService) GetLibrariesCtx(ctx context.Context) ([]domain.EmbyLibrary, error) {
	return s.ListLibrariesCtx(ctx)
}

// GetItem returns the exact Emby item identified by itemID.
func (s *EmbyService) GetItem(itemID string) (*domain.EmbyMediaItem, error) {
	return s.GetItemCtx(context.Background(), itemID)
}

// GetItemCtx returns one exact Emby item with cancellation.
func (s *EmbyService) GetItemCtx(ctx context.Context, itemID string) (*domain.EmbyMediaItem, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, fmt.Errorf("Emby item ID is required")
	}
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Items/%s?Fields=Path,PremiereDate,ProviderIds,ImageTags,BackdropImageTags&api_key=%s", baseURL, url.PathEscape(itemID), url.QueryEscape(apiKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inspect Emby item: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("Emby item %s was not found", itemID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("inspect Emby item returned HTTP %d", resp.StatusCode)
	}
	var raw struct {
		ID                string            `json:"Id"`
		Name              string            `json:"Name"`
		Type              string            `json:"Type"`
		Path              string            `json:"Path"`
		PremiereDate      string            `json:"PremiereDate"`
		ImageTags         map[string]string `json:"ImageTags"`
		BackdropImageTags []string          `json:"BackdropImageTags"`
		ProviderIDs       map[string]string `json:"ProviderIds"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode Emby item: %w", err)
	}
	if raw.ID != itemID {
		return nil, fmt.Errorf("Emby returned item %q for requested item %q", raw.ID, itemID)
	}
	_, hasPoster := raw.ImageTags["Primary"]
	return &domain.EmbyMediaItem{
		ID:           raw.ID,
		Name:         raw.Name,
		Type:         raw.Type,
		Path:         raw.Path,
		PremiereDate: raw.PremiereDate,
		HasPoster:    hasPoster,
		HasBackdrop:  len(raw.BackdropImageTags) > 0,
		ProviderIDs:  raw.ProviderIDs,
	}, nil
}

// MatchMedia applies a TMDB identity and images to one Emby item.
func (s *EmbyService) MatchMedia(itemID, tmdbID string) error {
	return s.MatchMediaCtx(context.Background(), itemID, tmdbID)
}

// MatchMediaCtx applies a TMDB identity and images with cancellation.
func (s *EmbyService) MatchMediaCtx(ctx context.Context, itemID, tmdbID string) error {
	itemID = strings.TrimSpace(itemID)
	tmdbID = strings.TrimSpace(tmdbID)
	if itemID == "" || tmdbID == "" {
		return fmt.Errorf("Emby item ID and TMDB ID are required")
	}
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Items/RemoteSearch/Apply/%s?ReplaceAllImages=true&api_key=%s", baseURL, url.PathEscape(itemID), url.QueryEscape(apiKey))
	body, err := json.Marshal(map[string]any{"ProviderIds": map[string]string{"Tmdb": tmdbID}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("apply Emby metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("apply Emby metadata returned HTTP %d", resp.StatusCode)
	}
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
	if err := requireEmbyResponse(resp, "Emby media search"); err != nil {
		return nil, err
	}

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

// GetMediaWithoutPosters returns Movie and Series items that have no image.
func (s *EmbyService) GetMediaWithoutPosters() ([]domain.EmbyMediaItem, error) {
	return s.GetMediaWithoutPostersCtx(context.Background())
}

// GetMediaWithoutPostersCtx returns missing-poster items with cancellation.
func (s *EmbyService) GetMediaWithoutPostersCtx(ctx context.Context) ([]domain.EmbyMediaItem, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	reqURL := fmt.Sprintf("%s/Items?Recursive=true&IncludeItemTypes=Movie,Series&ImageTypes=None&api_key=%s", baseURL, url.QueryEscape(apiKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby missing-poster list"); err != nil {
		return nil, err
	}
	var raw struct {
		Items []struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
			Type string `json:"Type"`
			Path string `json:"Path"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode Emby missing-poster list: %w", err)
	}
	result := make([]domain.EmbyMediaItem, 0, len(raw.Items))
	for _, item := range raw.Items {
		result = append(result, domain.EmbyMediaItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path})
	}
	return result, nil
}

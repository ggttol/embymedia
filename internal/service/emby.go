package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

func (s *EmbyService) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	baseURL, apiKey, _ := s.getURLAndKey()
	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		request.Header.Set("X-Emby-Token", apiKey)
	}
	return request, nil
}

// GetSystemInfo retrieves Emby server status and version
func (s *EmbyService) GetSystemInfo() (map[string]any, error) {
	req, err := s.newRequest(context.Background(), http.MethodGet, "/System/Info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
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
	req, err := s.newRequest(ctx, http.MethodGet, "/Library/VirtualFolders", nil)
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
	path := "/Library/Refresh"
	if libraryID != "" {
		path = fmt.Sprintf("/Items/%s/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default", url.PathEscape(libraryID))
	}
	req, err := s.newRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh Emby library: %w", err)
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby refresh"); err != nil {
		return err
	}
	return nil
}

type embyTaskResult struct {
	StartTimeUTC time.Time `json:"StartTimeUtc"`
	EndTimeUTC   time.Time `json:"EndTimeUtc"`
	Status       string    `json:"Status"`
	ErrorMessage string    `json:"ErrorMessage"`
}

type embyScheduledTask struct {
	ID                        string          `json:"Id"`
	Key                       string          `json:"Key"`
	State                     string          `json:"State"`
	CurrentProgressPercentage *float64        `json:"CurrentProgressPercentage"`
	LastExecutionResult       *embyTaskResult `json:"LastExecutionResult"`
}

func (s *EmbyService) listScheduledTasks(ctx context.Context) ([]embyScheduledTask, error) {
	req, err := s.newRequest(ctx, http.MethodGet, "/ScheduledTasks", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Emby scheduled tasks: %w", err)
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby scheduled task list"); err != nil {
		return nil, err
	}
	var tasks []embyScheduledTask
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("decode Emby scheduled task list: %w", err)
	}
	return tasks, nil
}

func (s *EmbyService) getScheduledTask(ctx context.Context, id string) (*embyScheduledTask, error) {
	req, err := s.newRequest(ctx, http.MethodGet, "/ScheduledTasks/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read Emby scheduled task: %w", err)
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby scheduled task"); err != nil {
		return nil, err
	}
	var task embyScheduledTask
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&task); err != nil {
		return nil, fmt.Errorf("decode Emby scheduled task: %w", err)
	}
	return &task, nil
}

type embyLibraryScanResult struct {
	TaskID      string
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (s *EmbyService) stopScheduledTask(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := s.newRequest(ctx, http.MethodDelete, "/ScheduledTasks/Running/"+url.PathEscape(id), nil)
	if err != nil {
		return
	}
	resp, err := s.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// RunLibraryScanCtx starts Emby's full-library task and waits for its recorded terminal state.
func (s *EmbyService) RunLibraryScanCtx(ctx context.Context, update func(float64, string) error) (*embyLibraryScanResult, error) {
	tasks, err := s.listScheduledTasks(ctx)
	if err != nil {
		return nil, err
	}
	var scan *embyScheduledTask
	for index := range tasks {
		if tasks[index].Key == "RefreshLibrary" {
			scan = &tasks[index]
			break
		}
	}
	if scan == nil {
		return nil, fmt.Errorf("Emby did not expose the RefreshLibrary scheduled task")
	}
	baseline := time.Time{}
	if scan.LastExecutionResult != nil {
		baseline = scan.LastExecutionResult.StartTimeUTC
	}
	startedByUs := scan.State != "Running"
	if startedByUs {
		req, err := s.newRequest(ctx, http.MethodPost, "/ScheduledTasks/Running/"+url.PathEscape(scan.ID), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("start Emby library scan: %w", err)
		}
		responseErr := requireEmbyResponse(resp, "start Emby library scan")
		resp.Body.Close()
		if responseErr != nil {
			return nil, responseErr
		}
	}
	finished := false
	if startedByUs {
		defer func() {
			if !finished {
				s.stopScheduledTask(scan.ID)
			}
		}()
	}
	message := "Emby accepted the full-library scan"
	if !startedByUs {
		message = "Emby full-library scan was already running; tracking the existing run"
	}
	if err := update(0, message); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	reportedRunning := false
	lastBucket := -1
	lastProgress := -1.0
	for {
		current, err := s.getScheduledTask(ctx, scan.ID)
		if err != nil {
			return nil, err
		}
		if current.State == "Running" {
			progress := 0.0
			if current.CurrentProgressPercentage != nil {
				progress = min(100, max(0, *current.CurrentProgressPercentage))
			}
			message := ""
			if !reportedRunning {
				message = "Emby full-library scan started"
				reportedRunning = true
			}
			bucket := int(progress) / 10
			if bucket > lastBucket {
				lastBucket = bucket
				if message == "" && bucket > 0 {
					message = fmt.Sprintf("Emby scan progress %d%%", bucket*10)
				}
			}
			if progress != lastProgress || message != "" {
				if err := update(progress, message); err != nil {
					return nil, err
				}
				lastProgress = progress
			}
		}
		result := current.LastExecutionResult
		if current.State != "Running" && result != nil && !result.StartTimeUTC.IsZero() && result.StartTimeUTC.After(baseline) {
			finished = true
			if result.Status != "Completed" {
				if result.ErrorMessage != "" {
					return nil, fmt.Errorf("Emby library scan %s: %s", result.Status, result.ErrorMessage)
				}
				return nil, fmt.Errorf("Emby library scan ended with status %s", result.Status)
			}
			return &embyLibraryScanResult{TaskID: scan.ID, Status: result.Status, StartedAt: result.StartTimeUTC, CompletedAt: result.EndTimeUTC}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ListSessions returns active playback sessions.
func (s *EmbyService) ListSessions() ([]domain.EmbyPlaybackSession, error) {
	return s.ListSessionsCtx(context.Background())
}

// ListSessionsCtx returns active playback sessions with cancellation.
func (s *EmbyService) ListSessionsCtx(ctx context.Context) ([]domain.EmbyPlaybackSession, error) {
	req, err := s.newRequest(ctx, http.MethodGet, "/Sessions", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby session list"); err != nil {
		return nil, err
	}
	var raw []struct {
		ID, UserName, Client, DeviceName string
		PlayState                        struct {
			IsPaused      bool
			PositionTicks int64
			PlayMethod    string
		}
		NowPlayingItem *struct{ Name, Type string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	res := make([]domain.EmbyPlaybackSession, 0, len(raw))
	for _, sess := range raw {
		if sess.NowPlayingItem == nil {
			continue
		}
		playState := "Playing"
		if sess.PlayState.IsPaused {
			playState = "Paused"
		}
		res = append(res, domain.EmbyPlaybackSession{ID: sess.ID, UserName: sess.UserName, ItemName: sess.NowPlayingItem.Name, ItemType: sess.NowPlayingItem.Type, Client: sess.Client, DeviceName: sess.DeviceName, PlayState: playState, PositionTicks: sess.PlayState.PositionTicks, PlaybackMethod: sess.PlayState.PlayMethod})
	}
	return res, nil
}

// EnsureNoActivePlayback refuses disruptive maintenance while Emby reports a playing or paused item.
func (s *EmbyService) EnsureNoActivePlayback(ctx context.Context) error {
	sessions, err := s.ListSessionsCtx(ctx)
	if err != nil {
		return fmt.Errorf("check active Emby playback: %w", err)
	}
	if len(sessions) == 0 {
		return nil
	}
	active := make([]string, 0, len(sessions))
	for _, session := range sessions {
		active = append(active, fmt.Sprintf("%s on %s: %s", session.UserName, session.DeviceName, session.ItemName))
	}
	return fmt.Errorf("active Emby playback prevents remount: %s", strings.Join(active, "; "))
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
	query := url.Values{"Ids": {itemID}, "Recursive": {"true"}, "Fields": {"Path,PremiereDate,ProviderIds,ImageTags,BackdropImageTags"}}
	req, err := s.newRequest(ctx, http.MethodGet, "/Items?"+query.Encode(), nil)
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
	type rawItem struct {
		ID                string            `json:"Id"`
		Name              string            `json:"Name"`
		Type              string            `json:"Type"`
		Path              string            `json:"Path"`
		PremiereDate      string            `json:"PremiereDate"`
		ImageTags         map[string]string `json:"ImageTags"`
		BackdropImageTags []string          `json:"BackdropImageTags"`
		ProviderIDs       map[string]string `json:"ProviderIds"`
	}
	var result struct {
		Items []rawItem `json:"Items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Emby item: %w", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != itemID {
		return nil, fmt.Errorf("Emby item %s was not found", itemID)
	}
	raw := result.Items[0]
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
	path := fmt.Sprintf("/Items/RemoteSearch/Apply/%s?ReplaceAllImages=true", url.PathEscape(itemID))
	body, err := json.Marshal(map[string]any{"ProviderIds": map[string]string{"Tmdb": tmdbID}})
	if err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodPost, path, strings.NewReader(string(body)))
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

// SearchMedia searches items in Emby by keyword.
func (s *EmbyService) SearchMedia(term string, limit int) ([]domain.EmbyMediaItem, error) {
	return s.SearchMediaCtx(context.Background(), term, limit)
}

// SearchMediaCtx searches Emby items by keyword with cancellation.
func (s *EmbyService) SearchMediaCtx(ctx context.Context, term string, limit int) ([]domain.EmbyMediaItem, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, fmt.Errorf("Emby search term is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query := url.Values{"SearchTerm": {term}, "Recursive": {"true"}, "Limit": {fmt.Sprint(limit)}, "Fields": {"Path,PremiereDate,ProviderIds,ImageTags,BackdropImageTags"}}
	req, err := s.newRequest(ctx, http.MethodGet, "/Items?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby media search"); err != nil {
		return nil, err
	}
	var raw struct {
		Items []struct {
			ID, Name, Type, Path, PremiereDate string
			ImageTags                          map[string]string
			BackdropImageTags                  []string
			ProviderIds                        map[string]string
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	res := make([]domain.EmbyMediaItem, 0, len(raw.Items))
	for _, item := range raw.Items {
		_, hasPrimary := item.ImageTags["Primary"]
		res = append(res, domain.EmbyMediaItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, PremiereDate: item.PremiereDate, HasPoster: hasPrimary, HasBackdrop: len(item.BackdropImageTags) > 0, ProviderIDs: item.ProviderIds})
	}
	return res, nil
}

// ListSeriesCtx returns every Series directly or recursively owned by one Emby library.
func (s *EmbyService) ListSeriesCtx(ctx context.Context, libraryID string) ([]domain.EmbyMediaItem, error) {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("Emby library ID is required")
	}
	const pageSize = 500
	series := make([]domain.EmbyMediaItem, 0)
	for startIndex := 0; ; {
		query := url.Values{
			"ParentId": {libraryID}, "Recursive": {"true"}, "IncludeItemTypes": {"Series"}, "Fields": {"Path,ProviderIds"},
			"StartIndex": {strconv.Itoa(startIndex)}, "Limit": {strconv.Itoa(pageSize)}, "EnableTotalRecordCount": {"true"},
		}
		req, err := s.newRequest(ctx, http.MethodGet, "/Items?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list Emby Series: %w", err)
		}
		if err := requireEmbyResponse(resp, "Emby Series list"); err != nil {
			resp.Body.Close()
			return nil, err
		}
		var raw struct {
			Items []struct {
				ID, Name, Type, Path string
				ProviderIDs          map[string]string `json:"ProviderIds"`
			}
			Total int `json:"TotalRecordCount"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Emby Series list: %w", decodeErr)
		}
		for _, item := range raw.Items {
			series = append(series, domain.EmbyMediaItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, ProviderIDs: item.ProviderIDs})
		}
		startIndex += len(raw.Items)
		if len(raw.Items) == 0 || startIndex >= raw.Total {
			break
		}
	}
	return series, nil
}

// EmbyMissingEpisode identifies one aired episode that Emby expects but cannot find.
type EmbyMissingEpisode struct {
	SeriesID      string    `json:"series_id"`
	SeriesName    string    `json:"series_name"`
	SeasonNumber  int       `json:"season_number"`
	EpisodeNumber int       `json:"episode_number"`
	PremiereDate  time.Time `json:"premiere_date"`
}

// ListAiredMissingEpisodesCtx returns every numbered missing episode released no later than now for one library.
func (s *EmbyService) ListAiredMissingEpisodesCtx(ctx context.Context, libraryID string, now time.Time) ([]EmbyMissingEpisode, error) {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("Emby library ID is required")
	}
	const pageSize = 500

	missing := make([]EmbyMissingEpisode, 0)
	for startIndex := 0; ; {
		query := url.Values{
			"ParentId": {libraryID}, "Recursive": {"true"}, "Fields": {"SeriesInfo,PremiereDate"},
			"StartIndex": {strconv.Itoa(startIndex)}, "Limit": {strconv.Itoa(pageSize)}, "EnableTotalRecordCount": {"true"},
		}
		req, err := s.newRequest(ctx, http.MethodGet, "/Shows/Missing?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list Emby missing episodes: %w", err)
		}
		if err := requireEmbyResponse(resp, "Emby missing-episode list"); err != nil {
			resp.Body.Close()
			return nil, err
		}
		var raw struct {
			Items []struct {
				SeriesID, SeriesName           string
				ParentIndexNumber, IndexNumber int
				PremiereDate                   time.Time
			}
			Total int `json:"TotalRecordCount"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Emby missing-episode list: %w", decodeErr)
		}
		for _, item := range raw.Items {
			if item.SeriesID == "" || item.SeriesName == "" || item.ParentIndexNumber <= 0 || item.IndexNumber <= 0 || item.PremiereDate.IsZero() || item.PremiereDate.After(now) {
				continue
			}
			missing = append(missing, EmbyMissingEpisode{
				SeriesID: item.SeriesID, SeriesName: item.SeriesName, SeasonNumber: item.ParentIndexNumber,
				EpisodeNumber: item.IndexNumber, PremiereDate: item.PremiereDate,
			})
		}
		startIndex += len(raw.Items)
		if len(raw.Items) == 0 || startIndex >= raw.Total {
			break
		}
	}
	return missing, nil
}

// ListAiredSeriesEpisodesCtx returns numbered episodes already owned by one Series and released no later than now.
func (s *EmbyService) ListAiredSeriesEpisodesCtx(ctx context.Context, seriesID string, now time.Time) ([]EmbyMissingEpisode, error) {
	seriesID = strings.TrimSpace(seriesID)
	if seriesID == "" {
		return nil, fmt.Errorf("Emby Series ID is required")
	}
	query := url.Values{"Fields": {"Path,PremiereDate"}, "Limit": {"10000"}}
	req, err := s.newRequest(ctx, http.MethodGet, "/Shows/"+url.PathEscape(seriesID)+"/Episodes?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Emby Series episodes: %w", err)
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby Series episode list"); err != nil {
		return nil, err
	}
	var raw struct {
		Items []struct {
			ParentIndexNumber, IndexNumber int
			PremiereDate                   time.Time
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode Emby Series episodes: %w", err)
	}
	episodes := make([]EmbyMissingEpisode, 0, len(raw.Items))
	for _, item := range raw.Items {
		if item.ParentIndexNumber <= 0 || item.IndexNumber <= 0 || item.PremiereDate.IsZero() || item.PremiereDate.After(now) {
			continue
		}
		episodes = append(episodes, EmbyMissingEpisode{SeriesID: seriesID, SeasonNumber: item.ParentIndexNumber, EpisodeNumber: item.IndexNumber, PremiereDate: item.PremiereDate})
	}
	return episodes, nil
}

// DeleteItemCtx removes one exact Emby item before its verified obsolete storage root is recycled.
func (s *EmbyService) DeleteItemCtx(ctx context.Context, itemID string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("Emby item ID is required")
	}
	req, err := s.newRequest(ctx, http.MethodDelete, "/Items/"+url.PathEscape(itemID), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete Emby item: %w", err)
	}
	defer resp.Body.Close()
	return requireEmbyResponse(resp, "Emby item deletion")
}

// EmbyMissingPosterReport returns a bounded page and the complete matching count.
type EmbyMissingPosterReport struct {
	Items     []domain.EmbyMediaItem `json:"items"`
	Total     int                    `json:"total_missing"`
	Returned  int                    `json:"returned"`
	Truncated bool                   `json:"truncated"`
}

// GetMediaWithoutPosters returns up to 100 Movie and Series items that have no primary image.
func (s *EmbyService) GetMediaWithoutPosters() (EmbyMissingPosterReport, error) {
	return s.GetMediaWithoutPostersCtx(context.Background())
}

// GetMediaWithoutPostersCtx scans movies and series and returns items without a primary image.
func (s *EmbyService) GetMediaWithoutPostersCtx(ctx context.Context) (EmbyMissingPosterReport, error) {
	return s.scanMissingPostersCtx(ctx, 100)
}

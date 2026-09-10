package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type replacementItem struct {
	ID                string `json:"Id"`
	Type              string
	Path              string
	SeriesID          string `json:"SeriesId"`
	IndexNumber       *int
	IndexNumberEnd    *int
	ParentIndexNumber *int
	IsMissing         bool
}

type replacementUserData struct {
	Played                bool
	PlayCount             int
	IsFavorite            bool
	PlaybackPositionTicks int64
	LastPlayedDate        *string  `json:",omitempty"`
	Rating                *float64 `json:",omitempty"`
	Likes                 *bool    `json:",omitempty"`
}

type replacementUserState struct {
	UserID string
	Key    string
	Data   replacementUserData
}

func (s *EmbyService) replacementJSON(ctx context.Context, method, path string, body, result any) error {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(encoded)
	}
	request, err := s.newRequest(ctx, method, path, input)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := requireEmbyResponse(response, "Emby replacement state"); err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(result)
}

// Virtual episodes retain user state but never count as physical coverage.
// Multi-episode files are rejected because their playback positions cannot be
// mapped unambiguously onto separately encoded replacement episodes.
func (s *EmbyService) replacementItems(ctx context.Context, seriesID, seriesPath string) (map[string]string, map[episodeKey]struct{}, error) {
	items := map[string]string{"Series": seriesID}
	episodes := make(map[episodeKey]struct{})
	for offset := 0; ; {
		query := url.Values{
			"ParentId": {seriesID}, "Recursive": {"true"}, "IncludeItemTypes": {"Season,Episode"},
			"Fields":     {"Path,SeriesId,ParentIndexNumber,IndexNumber,IndexNumberEnd"},
			"StartIndex": {strconv.Itoa(offset)}, "Limit": {"500"}, "EnableTotalRecordCount": {"true"},
		}
		var page struct {
			Items []replacementItem
			Total *int `json:"TotalRecordCount"`
		}
		if err := s.replacementJSON(ctx, http.MethodGet, "/Items?"+query.Encode(), nil, &page); err != nil {
			return nil, nil, err
		}
		if page.Total == nil || *page.Total < offset+len(page.Items) || (*page.Total > offset && len(page.Items) == 0) {
			return nil, nil, fmt.Errorf("incomplete replacement item inventory for Series %s", seriesID)
		}
		for _, item := range page.Items {
			if item.ID == "" || (item.Path != "" && (!inside(seriesPath, item.Path) || filepath.Clean(item.Path) == filepath.Clean(seriesPath))) || (item.SeriesID != "" && item.SeriesID != seriesID) || item.IndexNumber == nil || *item.IndexNumber < 0 {
				return nil, nil, fmt.Errorf("replacement item %s is not bound to its exact Series path", item.ID)
			}
			key := ""
			switch item.Type {
			case "Season":
				key = fmt.Sprintf("Season:%d", *item.IndexNumber)
			case "Episode":
				if item.ParentIndexNumber == nil || *item.ParentIndexNumber < 0 || *item.IndexNumber <= 0 || (item.IndexNumberEnd != nil && *item.IndexNumberEnd != *item.IndexNumber) {
					return nil, nil, fmt.Errorf("episode %s has no one-to-one season/episode identity", item.ID)
				}
				episode := episodeKey{Season: *item.ParentIndexNumber, Episode: *item.IndexNumber}
				if !item.IsMissing && item.Path != "" {
					episodes[episode] = struct{}{}
				}
				key = "Episode:" + episode.String()
			default:
				return nil, nil, fmt.Errorf("unexpected replacement item type %q", item.Type)
			}
			if _, duplicate := items[key]; duplicate {
				return nil, nil, fmt.Errorf("ambiguous replacement identity %s", key)
			}
			items[key] = item.ID
		}
		offset += len(page.Items)
		if offset == *page.Total {
			return items, episodes, nil
		}
	}
}

func (s *EmbyService) replacementUserData(ctx context.Context, userID, itemID string) (replacementUserData, error) {
	var item struct {
		ID       string `json:"Id"`
		UserData *replacementUserData
	}
	path := "/Users/" + url.PathEscape(userID) + "/Items/" + url.PathEscape(itemID)
	if err := s.replacementJSON(ctx, http.MethodGet, path, nil, &item); err != nil {
		return replacementUserData{}, err
	}
	if item.ID != itemID || item.UserData == nil {
		return replacementUserData{}, fmt.Errorf("user %s has no readable state for item %s", userID, itemID)
	}
	return *item.UserData, nil
}

func (s *EmbyService) snapshotReplacementState(ctx context.Context, items map[string]string) ([]replacementUserState, error) {
	var users []struct {
		ID string `json:"Id"`
	}
	if err := s.replacementJSON(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("Emby returned no users for replacement state preservation")
	}
	states := make([]replacementUserState, 0, len(users)*len(items))
	for _, user := range users {
		if strings.TrimSpace(user.ID) == "" {
			return nil, fmt.Errorf("Emby returned a user without an ID")
		}
		for key, itemID := range items {
			data, err := s.replacementUserData(ctx, user.ID, itemID)
			if err != nil {
				return nil, err
			}
			states = append(states, replacementUserState{UserID: user.ID, Key: key, Data: data})
		}
	}
	return states, nil
}

func (s *EmbyService) restoreReplacementState(ctx context.Context, states []replacementUserState, items map[string]string) error {
	for _, state := range states {
		if items[state.Key] == "" {
			return fmt.Errorf("replacement has no destination for user state %s", state.Key)
		}
	}
	// Parent play-state writes can affect children, so episode snapshots are
	// restored last. Every saved field is read back after all writes finish.
	for _, kind := range []string{"Series", "Season:", "Episode:"} {
		for _, state := range states {
			if !strings.HasPrefix(state.Key, kind) {
				continue
			}
			path := "/Users/" + url.PathEscape(state.UserID) + "/Items/" + url.PathEscape(items[state.Key]) + "/UserData"
			if err := s.replacementJSON(ctx, http.MethodPost, path, state.Data, nil); err != nil {
				return err
			}
		}
	}
	for _, state := range states {
		actual, err := s.replacementUserData(ctx, state.UserID, items[state.Key])
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actual, state.Data) {
			return fmt.Errorf("user state verification failed for user %s, %s", state.UserID, state.Key)
		}
	}
	return nil
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/embymedia/embymedia/internal/domain"
)

var (
	metadataYearPattern = regexp.MustCompile(`(?:19|20)\d{2}`)
	metadataCutPattern  = regexp.MustCompile(`(?i)(?:\bS\d{1,2}(?:\s*[-–]\s*S?\d{1,2})?\b|\b(?:720|1080|2160)p\b|\b(?:WEB|BluRay|NF|UHD)\b).*$`)
)

// MetadataCandidate is one Emby provider result safe to show for operator review.
type MetadataCandidate struct {
	Name           string            `json:"name"`
	ProductionYear int               `json:"production_year,omitempty"`
	ProviderIDs    map[string]string `json:"provider_ids"`
	ImageURL       string            `json:"image_url,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Score          int               `json:"score"`
}

// MetadataRepairItem records one missing identity and its automatic or review outcome.
type MetadataRepairItem struct {
	ItemID         string              `json:"item_id"`
	Name           string              `json:"name"`
	Type           string              `json:"type"`
	Path           string              `json:"path"`
	SearchName     string              `json:"search_name"`
	ProductionYear int                 `json:"production_year,omitempty"`
	Status         string              `json:"status"`
	AppliedTMDBID  string              `json:"applied_tmdb_id,omitempty"`
	HasPoster      bool                `json:"has_poster"`
	Candidates     []MetadataCandidate `json:"candidates,omitempty"`
	Reason         string              `json:"reason,omitempty"`
}

// MetadataRepairResult reports safe automatic matches and every item requiring review.
type MetadataRepairResult struct {
	Scanned         int                  `json:"scanned"`
	MissingIdentity int                  `json:"missing_identity"`
	Processed       int                  `json:"processed"`
	AutoMatched     int                  `json:"auto_matched"`
	NeedsReview     int                  `json:"needs_review"`
	NoMatch         int                  `json:"no_match"`
	Items           []MetadataRepairItem `json:"items"`
}

type metadataInventoryItem struct {
	ID, Name, Type, Path string
	ProductionYear       int
	ProviderIDs          map[string]string
}

func normalizeMetadataName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func deriveMetadataQuery(item metadataInventoryItem) (string, int) {
	base := filepath.Base(filepath.Clean(item.Path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = item.Name
	}
	year := item.ProductionYear
	if match := metadataYearPattern.FindString(base); match != "" {
		year, _ = strconv.Atoi(match)
	}
	clean := strings.NewReplacer(".", " ", "_", " ").Replace(base)
	clean = metadataCutPattern.ReplaceAllString(clean, "")
	clean = metadataYearPattern.ReplaceAllString(clean, "")
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		clean = item.Name
	}
	return clean, year
}

func metadataCandidateScore(source string, year int, candidate MetadataCandidate) int {
	sourceKey, candidateKey := normalizeMetadataName(source), normalizeMetadataName(candidate.Name)
	if len([]rune(candidateKey)) < 2 {
		return 0
	}
	score := 0
	switch {
	case sourceKey == candidateKey:
		score = 5
	case strings.Contains(sourceKey, candidateKey):
		score = 3
	case strings.Contains(candidateKey, sourceKey):
		score = 2
	}
	if year > 0 {
		if candidate.ProductionYear != year {
			return 0
		}
		score += 3
	}
	if candidate.ImageURL != "" {
		score++
	}
	return score
}

func (s *EmbyService) listMetadataInventoryCtx(ctx context.Context) ([]metadataInventoryItem, map[string]string, error) {
	const pageSize = 500
	items := make([]metadataInventoryItem, 0)
	existingTMDB := make(map[string]string)
	for start := 0; ; {
		query := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series"}, "Fields": {"Path,ProviderIds,ProductionYear"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(pageSize)}, "EnableTotalRecordCount": {"true"}}
		req, err := s.newRequest(ctx, http.MethodGet, "/Items?"+query.Encode(), nil)
		if err != nil {
			return nil, nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if err := requireEmbyResponse(resp, "Emby metadata inventory"); err != nil {
			resp.Body.Close()
			return nil, nil, err
		}
		var raw struct {
			Items []struct {
				ID, Name, Type, Path string
				ProductionYear       int
				ProviderIDs          map[string]string `json:"ProviderIds"`
			}
			Total int `json:"TotalRecordCount"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("decode Emby metadata inventory: %w", decodeErr)
		}
		for _, item := range raw.Items {
			entry := metadataInventoryItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, ProductionYear: item.ProductionYear, ProviderIDs: item.ProviderIDs}
			items = append(items, entry)
			if tmdb := strings.TrimSpace(item.ProviderIDs["Tmdb"]); tmdb != "" {
				existingTMDB[tmdb] = item.ID
			}
		}
		start += len(raw.Items)
		if len(raw.Items) == 0 || start >= raw.Total {
			break
		}
	}
	return items, existingTMDB, nil
}

func (s *EmbyService) searchMetadataCandidatesCtx(ctx context.Context, item metadataInventoryItem, name string, year int) ([]MetadataCandidate, error) {
	body, err := json.Marshal(map[string]any{"SearchInfo": map[string]any{"Name": name, "Year": year}, "ItemId": item.ID, "IncludeDisabledProviders": false})
	if err != nil {
		return nil, err
	}
	req, err := s.newRequest(ctx, http.MethodPost, "/Items/RemoteSearch/"+url.PathEscape(item.Type), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search Emby metadata: %w", err)
	}
	defer resp.Body.Close()
	if err := requireEmbyResponse(resp, "Emby metadata search"); err != nil {
		return nil, err
	}
	var raw []struct {
		Name           string            `json:"Name"`
		ProductionYear int               `json:"ProductionYear"`
		ProviderIDs    map[string]string `json:"ProviderIds"`
		ImageURL       string            `json:"ImageUrl"`
		Provider       string            `json:"SearchProviderName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	candidates := make([]MetadataCandidate, 0, len(raw))
	for _, candidate := range raw {
		value := MetadataCandidate{Name: candidate.Name, ProductionYear: candidate.ProductionYear, ProviderIDs: candidate.ProviderIDs, ImageURL: candidate.ImageURL, Provider: candidate.Provider}
		value.Score = metadataCandidateScore(name, year, value)
		candidates = append(candidates, value)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	return candidates, nil
}

func (s *EmbyService) applyMetadataCandidateCtx(ctx context.Context, itemID string, candidate MetadataCandidate) error {
	body, err := json.Marshal(map[string]any{
		"Name": candidate.Name, "ProductionYear": candidate.ProductionYear, "ProviderIds": candidate.ProviderIDs,
		"ImageUrl": candidate.ImageURL, "SearchProviderName": candidate.Provider,
	})
	if err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodPost, "/Items/RemoteSearch/Apply/"+url.PathEscape(itemID)+"?ReplaceAllImages=true", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("apply Emby metadata candidate: %w", err)
	}
	defer resp.Body.Close()
	return requireEmbyResponse(resp, "Emby metadata candidate application")
}

func (s *EmbyService) waitForMetadataIdentityCtx(ctx context.Context, itemID, tmdbID string) (*domain.EmbyMediaItem, error) {
	var lastErr error
	for range 10 {
		item, err := s.GetItemCtx(ctx, itemID)
		if err == nil && strings.TrimSpace(item.ProviderIDs["Tmdb"]) == tmdbID {
			return item, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("Emby did not retain the selected TMDB identity")
}

// RepairMetadataCtx applies only unique high-confidence TMDB matches and returns every ambiguous item for review.
func (s *EmbyService) RepairMetadataCtx(ctx context.Context, limit int, autoApply bool, update func(float64, string) error) (MetadataRepairResult, error) {
	inventory, existing, err := s.listMetadataInventoryCtx(ctx)
	if err != nil {
		return MetadataRepairResult{}, err
	}
	result := MetadataRepairResult{Scanned: len(inventory), Items: []MetadataRepairItem{}}
	proposals := make(map[string][]int)
	for _, item := range inventory {
		if strings.TrimSpace(item.ProviderIDs["Tmdb"]) == "" {
			result.MissingIdentity++
		}
	}
	for _, item := range inventory {
		if strings.TrimSpace(item.ProviderIDs["Tmdb"]) != "" || result.Processed >= limit {
			continue
		}
		name, year := deriveMetadataQuery(item)
		entry := MetadataRepairItem{ItemID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, SearchName: name, ProductionYear: year, Status: "no_match"}
		candidates, searchErr := s.searchMetadataCandidatesCtx(ctx, item, name, year)
		result.Processed++
		if searchErr != nil {
			entry.Status = "needs_review"
			entry.Reason = searchErr.Error()
			result.NeedsReview++
		} else {
			entry.Candidates = candidates
			if len(candidates) > 0 && candidates[0].Score >= 7 && strings.TrimSpace(candidates[0].ProviderIDs["Tmdb"]) != "" && (len(candidates) == 1 || candidates[0].Score-candidates[1].Score >= 2) {
				entry.Status = "proposed"
				proposals[candidates[0].ProviderIDs["Tmdb"]] = append(proposals[candidates[0].ProviderIDs["Tmdb"]], len(result.Items))
			} else if len(candidates) > 0 {
				entry.Status = "needs_review"
				entry.Reason = "no unique high-confidence title, year, and type match"
				result.NeedsReview++
			} else {
				result.NoMatch++
			}
		}
		result.Items = append(result.Items, entry)
		if update != nil {
			if err := update(70*float64(result.Processed)/float64(max(1, limit)), fmt.Sprintf("Emby metadata search processed %d/%d", result.Processed, limit)); err != nil {
				return result, err
			}
		}
	}
	for tmdb, indexes := range proposals {
		if len(indexes) != 1 || existing[tmdb] != "" {
			for _, index := range indexes {
				result.Items[index].Status = "needs_review"
				result.Items[index].Reason = "TMDB candidate would duplicate another library item"
				result.NeedsReview++
			}
			continue
		}
		index := indexes[0]
		if !autoApply {
			result.Items[index].Status = "needs_review"
			result.Items[index].Reason = "automatic application is disabled"
			result.NeedsReview++
			continue
		}
		if err := s.applyMetadataCandidateCtx(ctx, result.Items[index].ItemID, result.Items[index].Candidates[0]); err != nil {
			result.Items[index].Status = "needs_review"
			result.Items[index].Reason = err.Error()
			result.NeedsReview++
			continue
		}
		verified, err := s.waitForMetadataIdentityCtx(ctx, result.Items[index].ItemID, tmdb)
		if err != nil {
			result.Items[index].Status = "needs_review"
			result.Items[index].Reason = err.Error()
			result.NeedsReview++
			continue
		}
		result.Items[index].Status = "matched"
		result.Items[index].AppliedTMDBID = tmdb
		result.Items[index].HasPoster = verified.HasPoster
		result.AutoMatched++
	}
	return result, nil
}

func resolveMetadataRepairSpec(payload map[string]any) (int, bool, error) {
	limit, err := integerPayload(payload, "limit", 100, 1, 500)
	if err != nil {
		return 0, false, err
	}
	autoApply, err := booleanPayload(payload, "auto_apply", true)
	if err != nil {
		return 0, false, err
	}
	return limit, autoApply, nil
}

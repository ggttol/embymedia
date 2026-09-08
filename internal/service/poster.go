package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

const (
	posterRepairItemLimit = 5000
	posterRepairWait      = 2 * time.Minute
)

// PosterRepairResult records requested image refreshes and the verified remaining missing posters.
type PosterRepairResult struct {
	Found               int                    `json:"found"`
	RepairRequested     int                    `json:"repair_requested"`
	Repaired            int                    `json:"repaired"`
	Remaining           int                    `json:"remaining"`
	CandidateDownloaded int                    `json:"candidate_downloaded"`
	Failed              []string               `json:"failed,omitempty"`
	RemainingItems      []domain.EmbyMediaItem `json:"remaining_items,omitempty"`
	TimedOut            bool                   `json:"timed_out,omitempty"`
}

func (s *EmbyService) scanMissingPostersCtx(ctx context.Context, resultLimit int) (EmbyMissingPosterReport, error) {
	const pageSize = 500
	items := make([]domain.EmbyMediaItem, 0, min(resultLimit, pageSize))
	totalMissing := 0
	for startIndex := 0; ; {
		query := url.Values{
			"Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series"},
			"Fields": {"Path,ProviderIds,ImageTags"}, "StartIndex": {strconv.Itoa(startIndex)},
			"Limit": {strconv.Itoa(pageSize)}, "EnableTotalRecordCount": {"true"},
		}
		req, err := s.newRequest(ctx, http.MethodGet, "/Items?"+query.Encode(), nil)
		if err != nil {
			return EmbyMissingPosterReport{}, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return EmbyMissingPosterReport{}, err
		}
		if err := requireEmbyResponse(resp, "Emby missing-poster list"); err != nil {
			resp.Body.Close()
			return EmbyMissingPosterReport{}, err
		}
		var raw struct {
			Items []struct {
				ID, Name, Type, Path string
				ImageTags            map[string]string `json:"ImageTags"`
				ProviderIDs          map[string]string `json:"ProviderIds"`
			} `json:"Items"`
			Total int `json:"TotalRecordCount"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			return EmbyMissingPosterReport{}, fmt.Errorf("decode Emby missing-poster list: %w", decodeErr)
		}
		for _, item := range raw.Items {
			if _, hasPrimary := item.ImageTags["Primary"]; hasPrimary {
				continue
			}
			totalMissing++
			if resultLimit <= 0 || len(items) < resultLimit {
				items = append(items, domain.EmbyMediaItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, ProviderIDs: item.ProviderIDs})
			}
		}
		startIndex += len(raw.Items)
		if len(raw.Items) == 0 || startIndex >= raw.Total {
			break
		}
	}
	return EmbyMissingPosterReport{Items: items, Total: totalMissing, Returned: len(items), Truncated: resultLimit > 0 && totalMissing > len(items)}, nil
}

func (s *EmbyService) requestPosterRefreshCtx(ctx context.Context, itemID string) error {
	query := url.Values{"Recursive": {"false"}, "ImageRefreshMode": {"FullRefresh"}, "MetadataRefreshMode": {"Default"}, "ReplaceAllImages": {"false"}}
	req, err := s.newRequest(ctx, http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/Refresh?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh Emby poster: %w", err)
	}
	defer resp.Body.Close()
	return requireEmbyResponse(resp, "Emby poster refresh")
}

func uniquePosterCandidate(candidates []MetadataCandidate, threshold int) *MetadataCandidate {
	if len(candidates) == 0 || candidates[0].Score < threshold || candidates[0].ImageURL == "" {
		return nil
	}
	if len(candidates) > 1 && candidates[0].Score-candidates[1].Score < 2 {
		return nil
	}
	return &candidates[0]
}

func (s *EmbyService) findPosterCandidateCtx(ctx context.Context, item domain.EmbyMediaItem) (*MetadataCandidate, error) {
	inventory := metadataInventoryItem{ID: item.ID, Name: item.Name, Type: item.Type, Path: item.Path, ProviderIDs: item.ProviderIDs}
	name, year := deriveMetadataQuery(inventory)
	for _, attempt := range []struct {
		itemType        string
		year, threshold int
	}{{item.Type, year, 6}, {item.Type, 0, 4}, {map[string]string{"Series": "Movie", "Movie": "Series"}[item.Type], year, 6}, {map[string]string{"Series": "Movie", "Movie": "Series"}[item.Type], 0, 4}} {
		if attempt.itemType == "" {
			continue
		}
		inventory.Type = attempt.itemType
		if attempt.itemType != item.Type {
			inventory.ID = ""
		} else {
			inventory.ID = item.ID
		}
		candidates, err := s.searchMetadataCandidatesCtx(ctx, inventory, name, attempt.year)
		if err != nil {
			continue
		}
		if candidate := uniquePosterCandidate(candidates, attempt.threshold); candidate != nil {
			return candidate, nil
		}
	}
	return nil, nil
}

func (s *EmbyService) downloadPosterCandidateCtx(ctx context.Context, itemID, imageURL string) error {
	query := url.Values{"Type": {"Primary"}, "ImageUrl": {imageURL}}
	req, err := s.newRequest(ctx, http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/RemoteImages/Download?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("download Emby poster candidate: %w", err)
	}
	defer resp.Body.Close()
	return requireEmbyResponse(resp, "Emby poster candidate download")
}

// RepairMissingPostersCtx requests image refreshes, falls back to unique remote candidates, and verifies the result.
func (s *EmbyService) RepairMissingPostersCtx(ctx context.Context, update func(float64, string) error) (PosterRepairResult, error) {
	initial, err := s.scanMissingPostersCtx(ctx, 0)
	if err != nil {
		return PosterRepairResult{}, err
	}
	if initial.Total > posterRepairItemLimit {
		return PosterRepairResult{}, fmt.Errorf("missing-poster repair exceeds %d items", posterRepairItemLimit)
	}
	result := PosterRepairResult{Found: initial.Total, Remaining: initial.Total}
	initialIDs := make(map[string]struct{}, len(initial.Items))
	for index, item := range initial.Items {
		initialIDs[item.ID] = struct{}{}
		if err := s.requestPosterRefreshCtx(ctx, item.ID); err != nil {
			result.Failed = append(result.Failed, item.Name+": "+err.Error())
			continue
		}
		result.RepairRequested++
		if update != nil {
			progress := 10 + 60*float64(index+1)/float64(max(1, len(initial.Items)))
			if err := update(progress, fmt.Sprintf("Emby poster refresh requested %d/%d", index+1, len(initial.Items))); err != nil {
				return result, err
			}
		}
	}
	if initial.Total == 0 {
		return result, nil
	}
	deadline := time.Now().Add(posterRepairWait)
	lastRemaining, stablePolls := -1, 0
	for {
		current, err := s.scanMissingPostersCtx(ctx, 0)
		if err != nil {
			return result, err
		}
		stillMissing := 0
		for _, item := range current.Items {
			if _, targeted := initialIDs[item.ID]; targeted {
				stillMissing++
			}
		}
		result.Remaining = stillMissing
		result.Repaired = result.Found - stillMissing
		result.RemainingItems = current.Items
		if stillMissing == lastRemaining {
			stablePolls++
		} else {
			stablePolls = 0
			lastRemaining = stillMissing
		}
		if stillMissing == 0 || stablePolls >= 3 {
			break
		}
		if time.Now().After(deadline) {
			result.TimedOut = true
			break
		}
		if update != nil {
			if err := update(75+20*float64(result.Repaired)/float64(max(1, result.Found)), fmt.Sprintf("Emby poster verification remaining=%d", stillMissing)); err != nil {
				return result, err
			}
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if result.Remaining > 0 {
		for _, item := range result.RemainingItems {
			if _, targeted := initialIDs[item.ID]; !targeted {
				continue
			}
			candidate, err := s.findPosterCandidateCtx(ctx, item)
			if err != nil || candidate == nil {
				continue
			}
			if err := s.downloadPosterCandidateCtx(ctx, item.ID, candidate.ImageURL); err != nil {
				result.Failed = append(result.Failed, item.Name+": "+err.Error())
				continue
			}
			result.CandidateDownloaded++
		}
		if result.CandidateDownloaded > 0 {
			current, err := s.scanMissingPostersCtx(ctx, 0)
			if err != nil {
				return result, err
			}
			stillMissing := 0
			for _, item := range current.Items {
				if _, targeted := initialIDs[item.ID]; targeted {
					stillMissing++
				}
			}
			result.Remaining = stillMissing
			result.Repaired = result.Found - stillMissing
			result.RemainingItems = current.Items
		}
	}
	if len(result.RemainingItems) > 100 {
		result.RemainingItems = result.RemainingItems[:100]
	}
	return result, nil
}

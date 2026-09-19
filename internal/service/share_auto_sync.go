package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/embymedia/embymedia/internal/domain"
)

// Subscription polling delegates to the same identity-bound missing-episode workflow.
// A timestamp cursor cannot establish which episodes are already in Emby.
func (s *TaskQueueService) runShareAutoSync(ctx context.Context, task domain.AsyncTask) (map[string]any, error) {
	subID, err := stringPayload(task.Payload, "subscription_id", true)
	if err != nil {
		return nil, err
	}
	sub, err := s.db.GetShareSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if sub == nil || !sub.Active {
		return nil, fmt.Errorf("active subscription not found")
	}
	if strings.TrimSpace(sub.TargetCID) == "" {
		return nil, fmt.Errorf("subscription target_cid must identify an existing following Series directory")
	}
	// The single queue worker serializes polling; keep an in-flight child instead of
	// queuing a second transfer while the first is still absent from Emby.
	for _, status := range []string{"pending", "running"} {
		active, err := s.db.ListAsyncTasks(status, 0)
		if err != nil {
			return nil, err
		}
		for _, candidate := range active {
			if candidate.ID == task.ID {
				continue
			}
			parent := &candidate
			if candidate.Type == "quark_to_115_import" {
				parentID, _ := candidate.Payload["parent_task_id"].(string)
				if parentID == "" {
					continue
				}
				parent, err = s.db.GetAsyncTask(parentID)
				if err != nil {
					return nil, err
				}
			}
			if parent.Type == "series_auto_fill" && parent.Payload["subscription_id"] == subID {
				return map[string]any{"stage": "queued", "next_task_id": parent.ID, "active_task_id": candidate.ID, "verification_complete": false}, nil
			}
		}
	}
	library, seriesID, err := s.subscriptionSeries(ctx, sub.TargetCID)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"libraries": []string{library}, "series_ids": []string{seriesID}, "transfer": true,
		"subscription_id": sub.ID,
		"source_shares":   map[string]any{seriesID: map[string]any{"provider": sub.Provider, "url": sub.URL, "password": sub.Password}},
	}
	queued, err := s.Enqueue("series_auto_fill", payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{"stage": "queued", "next_task_id": queued.ID, "verification_complete": false}, nil
}

func (s *TaskQueueService) subscriptionSeries(ctx context.Context, targetCID string) (string, string, error) {
	raw, err := s.db.GetSetting("c115_cid_map")
	if err != nil {
		return "", "", err
	}
	var cidMap map[string]string
	if err := json.Unmarshal([]byte(raw), &cidMap); err != nil {
		return "", "", fmt.Errorf("decode c115_cid_map: %w", err)
	}
	libraries, err := s.embySvc.ListLibrariesCtx(ctx)
	if err != nil {
		return "", "", err
	}
	account, err := s.driveSvc.GetDefaultAccount("115")
	if err != nil {
		return "", "", err
	}
	matchedLibrary, matchedSeries := "", ""
	matches := 0
	for _, library := range libraries {
		if _, eligible := autoFillLibrarySet[library.Name]; !eligible {
			continue
		}
		libraryCID := strings.TrimSpace(cidMap[library.Name])
		if libraryCID == "" {
			continue
		}
		files, err := s.listAllProviderFiles(ctx, "115", account.ID, libraryCID)
		if err != nil {
			return "", "", err
		}
		folder := ""
		for _, file := range files {
			if file.IsFolder && file.FileID == targetCID && file.ParentID == libraryCID {
				folder = file.Name
				break
			}
		}
		if folder == "" {
			continue
		}
		inventory, err := s.embySvc.ListSeriesCtx(ctx, library.ID)
		if err != nil {
			return "", "", err
		}
		for _, item := range inventory {
			path := filepath.Clean(item.Path)
			if filepath.Base(path) == folder && filepath.Base(filepath.Dir(path)) == library.Name && strings.TrimSpace(item.ProviderIDs["Tmdb"]) != "" {
				matchedLibrary, matchedSeries = library.Name, item.ID
				matches++
			}
		}
	}
	if matches != 1 {
		return "", "", fmt.Errorf("subscription target_cid must match exactly one TMDB-bound Series in a following library; found %d", matches)
	}
	return matchedLibrary, matchedSeries, nil
}

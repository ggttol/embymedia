package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/embymedia/embymedia/internal/domain"
)

const (
	mediaIngestVisibilityAttempts = 30
	mediaIngestVisibilityInterval = 2 * time.Second
	mediaIngestMaxNameBytes       = 230
)

var ingestSubtitleExtensions = map[string]struct{}{
	".ass": {}, ".idx": {}, ".smi": {}, ".srt": {}, ".ssa": {}, ".sub": {}, ".sup": {}, ".vtt": {},
}

type mediaIngestCandidate struct {
	Library    domain.EmbyLibrary
	LibraryCID string
	Series     domain.EmbyMediaItem
	SeriesCID  string
}

type mediaIngestMove struct {
	FileID     string `json:"file_id"`
	SourceName string `json:"source_name"`
	TargetName string `json:"target_name"`
	TargetCID  string `json:"target_cid"`
	Status     string `json:"status"`
}

func (s *TaskQueueService) runMediaIngest(ctx context.Context, task domain.AsyncTask) (map[string]any, error) {
	sourceTaskID, _ := stringPayload(task.Payload, "source_task_id", true)
	sourceTask, err := s.db.GetAsyncTask(sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("read source import task: %w", err)
	}
	if sourceTask.Type != "quark_to_115_import" || sourceTask.Status != "completed" {
		return nil, fmt.Errorf("source task must be a completed Quark import")
	}
	state, err := s.db.GetCrossDriveImport(sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("read source import state: %w", err)
	}
	if state.Phase != "verified" || state.DestinationCID == "" || state.AutofillSeriesID != "" {
		return nil, fmt.Errorf("source import is not an unbound verified staging import")
	}
	items, err := s.db.ListCrossDriveItems(sourceTaskID)
	if err != nil {
		return nil, err
	}
	videos := ingestMediaItems(items, true)
	if len(videos) == 0 {
		return map[string]any{"source_task_id": sourceTaskID, "needs_review": 1, "findings": []string{"导入中没有可识别的编号视频文件"}}, nil
	}
	if err := s.db.UpdateAsyncTaskProgress(task.ID, 15); err != nil {
		return nil, err
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("catalog match started for %d verified videos", len(videos))); err != nil {
		return nil, err
	}
	candidate, candidates, err := s.resolveMediaIngestCandidate(ctx, state.C115AccountID, videos)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		finding := "没有唯一、完整且年份一致的现有 Emby 剧集身份；文件保留在 /emby/_待整理"
		if len(candidates) > 1 {
			finding = "多个现有 Emby 剧集同时匹配；文件保留在 /emby/_待整理"
		}
		return map[string]any{
			"source_task_id": sourceTaskID, "stage": "catalog_review", "needs_review": 1,
			"candidate_series": candidates, "findings": []string{finding},
		}, nil
	}
	candidate.SeriesCID, err = s.resolveAutoFillFolder(ctx, candidate.LibraryCID, filepath.Base(filepath.Clean(candidate.Series.Path)))
	if err != nil {
		return nil, fmt.Errorf("resolve canonical 115 Series folder: %w", err)
	}
	if err := s.db.UpdateAsyncTaskProgress(task.ID, 25); err != nil {
		return nil, err
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("catalog matched %s [%s] in %s", candidate.Series.Name, candidate.Series.ProviderIDs["Tmdb"], candidate.Library.Name)); err != nil {
		return nil, err
	}

	movable, findings := buildMediaIngestPlan(candidate.Series, items)
	moves := make([]mediaIngestMove, 0, len(movable))
	expectedEpisodes := make(map[episodeKey]struct{})
	for _, item := range videos {
		for _, key := range episodeKeysFromName(item.Name) {
			expectedEpisodes[key] = struct{}{}
		}
	}
	for index, item := range movable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetName, err := canonicalIngestName(candidate.Series, item)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s：%v", item.Name, err))
			continue
		}
		move, err := s.moveIngestItem(ctx, state.C115AccountID, state.DestinationCID, candidate.SeriesCID, item, targetName)
		if err != nil {
			return nil, err
		}
		moves = append(moves, move)
		progress := 25 + 35*float64(index+1)/float64(max(1, len(movable)))
		if err := s.db.UpdateAsyncTaskProgress(task.ID, progress); err != nil {
			return nil, err
		}
	}
	if len(moves) == 0 {
		findings = append(findings, "没有文件通过身份与目标冲突检查")
		return map[string]any{
			"source_task_id": sourceTaskID, "stage": "organize_review", "needs_review": len(findings),
			"library": candidate.Library.Name, "series": candidate.Series.Name, "tmdb_id": candidate.Series.ProviderIDs["Tmdb"], "findings": findings,
		}, nil
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("organized %d files into %s/%s", len(moves), candidate.Library.Name, filepath.Base(filepath.Clean(candidate.Series.Path)))); err != nil {
		return nil, err
	}
	if _, err := s.mediaSvc.SyncSTRMWithProgress(ctx, candidate.Library.Name, s.taskProgress(task.ID, 62, 78)); err != nil {
		return nil, fmt.Errorf("sync STRM after media ingest: %w", err)
	}
	if err := s.db.AppendTaskLog(task.ID, "STRM synchronization completed for "+candidate.Library.Name); err != nil {
		return nil, err
	}
	scan, err := s.embySvc.RunLibraryScanCtx(ctx, s.taskProgress(task.ID, 80, 95))
	if err != nil {
		return nil, fmt.Errorf("scan Emby after media ingest: %w", err)
	}
	owned, err := s.embySvc.ListAiredSeriesEpisodesCtx(ctx, candidate.Series.ID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("verify Emby Series after media ingest: %w", err)
	}
	ownedSet := make(map[episodeKey]struct{}, len(owned))
	for _, episode := range owned {
		ownedSet[episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}] = struct{}{}
	}
	missing := make([]string, 0)
	for key := range expectedEpisodes {
		if _, ok := ownedSet[key]; !ok {
			missing = append(missing, key.String())
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("Emby scan did not expose imported episodes: %s", strings.Join(missing, ", "))
	}
	if err := s.db.UpdateAsyncTaskProgress(task.ID, 99); err != nil {
		return nil, err
	}
	return map[string]any{
		"source_task_id": sourceTaskID, "stage": "verified", "needs_review": len(findings),
		"library": candidate.Library.Name, "library_id": candidate.Library.ID, "series": candidate.Series.Name,
		"series_id": candidate.Series.ID, "tmdb_id": candidate.Series.ProviderIDs["Tmdb"],
		"destination_cid": candidate.SeriesCID, "destination_path": filepath.Join("/emby", candidate.Library.Name, filepath.Base(filepath.Clean(candidate.Series.Path))),
		"files_organized": len(moves), "episodes_verified": sortedEpisodeLabels(expectedEpisodes), "moves": moves,
		"findings": findings, "emby_task_id": scan.TaskID, "emby_status": scan.Status,
	}, nil
}

func ingestMediaItems(items []domain.CrossDriveItem, videoOnly bool) []domain.CrossDriveItem {
	result := make([]domain.CrossDriveItem, 0, len(items))
	for _, item := range items {
		if item.State != "verified" || item.DestinationID == "" {
			continue
		}
		extension := strings.ToLower(filepath.Ext(item.Name))
		_, video := videoExtensions[extension]
		_, subtitle := ingestSubtitleExtensions[extension]
		if video || (!videoOnly && subtitle) {
			result = append(result, item)
		}
	}
	return result
}

func (s *TaskQueueService) resolveMediaIngestCandidate(ctx context.Context, accountID string, videos []domain.CrossDriveItem) (*mediaIngestCandidate, []map[string]string, error) {
	rawCIDMap, err := s.db.GetSetting("c115_cid_map")
	if err != nil {
		return nil, nil, err
	}
	var cidMap map[string]string
	if err := json.Unmarshal([]byte(rawCIDMap), &cidMap); err != nil {
		return nil, nil, fmt.Errorf("decode c115_cid_map: %w", err)
	}
	libraries, err := s.embySvc.ListLibrariesCtx(ctx)
	if err != nil {
		return nil, nil, err
	}
	matches := make([]mediaIngestCandidate, 0, 2)
	public := make([]map[string]string, 0, 2)
	for _, library := range libraries {
		libraryCID := strings.TrimSpace(cidMap[library.Name])
		if library.Collection != "tvshows" || libraryCID == "" {
			continue
		}
		series, err := s.embySvc.ListSeriesCtx(ctx, library.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range series {
			if !mediaIngestSeriesMatches(item, videos) {
				continue
			}
			folder := filepath.Base(filepath.Clean(item.Path))
			if folder == "." || filepath.Base(filepath.Dir(filepath.Clean(item.Path))) != library.Name || strings.TrimSpace(item.ProviderIDs["Tmdb"]) == "" {
				continue
			}
			matches = append(matches, mediaIngestCandidate{Library: library, LibraryCID: libraryCID, Series: item})
			public = append(public, map[string]string{"library": library.Name, "series": item.Name, "series_id": item.ID, "tmdb_id": item.ProviderIDs["Tmdb"], "folder": folder})
		}
	}
	if len(matches) != 1 {
		return nil, public, nil
	}
	return &matches[0], public, nil
}

func mediaIngestSeriesMatches(series domain.EmbyMediaItem, videos []domain.CrossDriveItem) bool {
	aliases := []string{parseAutoFillLabel(series.Name).title, parseAutoFillLabel(series.OriginalTitle).title}
	folderIdentity := parseAutoFillLabel(filepath.Base(filepath.Clean(series.Path)))
	canonicalYear := parseAutoFillLabel(series.Name).year
	if canonicalYear == 0 {
		canonicalYear = folderIdentity.year
	}
	if len(series.PremiereDate) >= 4 && canonicalYear == 0 {
		fmt.Sscanf(series.PremiereDate[:4], "%d", &canonicalYear)
	}
	strong := 0
	for _, item := range videos {
		keys := episodeKeysFromName(item.Name)
		if len(keys) == 0 {
			return false
		}
		identity := parseAutoFillLabel(item.Name)
		exact, related := importTitleRelation(identity.title, aliases)
		if !related || (identity.year != 0 && canonicalYear != 0 && identity.year != canonicalYear) {
			return false
		}
		if exact && canonicalYear != 0 && identity.year == canonicalYear {
			strong++
		}
		if tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"]); tmdbID != "" && strings.Contains(strings.ToLower(item.Name), "tmdbid="+strings.ToLower(tmdbID)) {
			strong++
		}
	}
	return strong > 0
}

func importTitleRelation(title string, aliases []string) (exact, related bool) {
	for _, alias := range aliases {
		if alias == "" || utf8.RuneCountInString(alias) < 4 {
			continue
		}
		if title == alias {
			return true, true
		}
		if strings.HasPrefix(title, alias) || strings.HasSuffix(title, alias) {
			related = true
		}
	}
	return false, related
}

func buildMediaIngestPlan(series domain.EmbyMediaItem, items []domain.CrossDriveItem) ([]domain.CrossDriveItem, []string) {
	movable := make([]domain.CrossDriveItem, 0, len(items))
	findings := make([]string, 0)
	aliases := []string{parseAutoFillLabel(series.Name).title, parseAutoFillLabel(series.OriginalTitle).title}
	for _, item := range items {
		if item.State != "verified" || item.DestinationID == "" {
			findings = append(findings, item.Name+"：未完成 115 身份核对")
			continue
		}
		extension := strings.ToLower(filepath.Ext(item.Name))
		_, video := videoExtensions[extension]
		_, subtitle := ingestSubtitleExtensions[extension]
		if !video && !subtitle {
			findings = append(findings, item.Name+"：非视频或字幕，保留在 _待整理")
			continue
		}
		if len(episodeKeysFromName(item.Name)) == 0 {
			findings = append(findings, item.Name+"：缺少明确季集编号")
			continue
		}
		if _, related := importTitleRelation(parseAutoFillLabel(item.Name).title, aliases); !related {
			findings = append(findings, item.Name+"：文件名不能证明同一剧集身份")
			continue
		}
		movable = append(movable, item)
	}
	return movable, findings
}

func canonicalIngestName(series domain.EmbyMediaItem, item domain.CrossDriveItem) (string, error) {
	extension := filepath.Ext(item.Name)
	base := strings.TrimSuffix(filepath.Base(item.Name), extension)
	match := seasonEpisodePattern.FindStringIndex(base)
	keys := episodeKeysFromName(item.Name)
	if match == nil || len(keys) == 0 {
		return "", fmt.Errorf("缺少 SxxExx 编号")
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Season != keys[j].Season {
			return keys[i].Season < keys[j].Season
		}
		return keys[i].Episode < keys[j].Episode
	})
	label := keys[0].String()
	if len(keys) > 1 {
		last := keys[len(keys)-1]
		if last.Season == keys[0].Season {
			label += fmt.Sprintf("-E%02d", last.Episode)
		} else {
			label += "-" + last.String()
		}
	}
	suffix := strings.Trim(base[match[1]:], " ._-")
	name := strings.TrimSpace(series.Name) + " " + label
	if suffix != "" {
		name += " - " + suffix
	}
	name += extension
	if strings.ContainsAny(name, "/\x00") {
		return "", fmt.Errorf("规范文件名无效")
	}
	if len([]byte(name)) > mediaIngestMaxNameBytes {
		if len(item.SHA1) < 8 {
			return "", fmt.Errorf("规范文件名过长且缺少 SHA-1")
		}
		name = fmt.Sprintf("%s %s - %s%s", strings.TrimSpace(series.Name), label, strings.ToUpper(item.SHA1[:8]), extension)
	}
	return name, nil
}

func (s *TaskQueueService) moveIngestItem(ctx context.Context, accountID, sourceCID, targetCID string, item domain.CrossDriveItem, targetName string) (mediaIngestMove, error) {
	move := mediaIngestMove{FileID: item.DestinationID, SourceName: item.Name, TargetName: targetName, TargetCID: targetCID}
	sourceFiles, err := s.listAllProviderFiles(ctx, "115", accountID, sourceCID)
	if err != nil {
		return move, err
	}
	targetFiles, err := s.listAllProviderFiles(ctx, "115", accountID, targetCID)
	if err != nil {
		return move, err
	}
	var current *domain.DriveFile
	for index := range targetFiles {
		if targetFiles[index].FileID == item.DestinationID {
			current = &targetFiles[index]
			break
		}
	}
	if current == nil {
		for index := range sourceFiles {
			if sourceFiles[index].FileID == item.DestinationID {
				current = &sourceFiles[index]
				break
			}
		}
		if current == nil || current.ParentID != sourceCID || current.Name != item.Name || current.IsFolder || current.Size != item.Size || !strings.EqualFold(current.Sha1, item.SHA1) {
			return move, fmt.Errorf("source object %s changed before media ingest", item.Name)
		}
		for _, existing := range targetFiles {
			if existing.Name != targetName || existing.FileID == item.DestinationID {
				continue
			}
			if !existing.IsFolder && existing.Size == item.Size && strings.EqualFold(existing.Sha1, item.SHA1) {
				move.Status = "duplicate_retained_in_staging"
				return move, nil
			}
			if len(item.SHA1) < 8 {
				return move, fmt.Errorf("target name %s is occupied and source SHA-1 is unavailable", targetName)
			}
			extension := filepath.Ext(targetName)
			targetName = strings.TrimSuffix(targetName, extension) + " - " + strings.ToUpper(item.SHA1[:8]) + extension
			move.TargetName = targetName
		}
		if err := s.driveSvc.MoveProvider(ctx, "115", accountID, []string{item.DestinationID}, targetCID); err != nil {
			return move, fmt.Errorf("move %s into canonical Series: %w", item.Name, err)
		}
		move.Status = "moved"
	} else if current.ParentID != targetCID || current.IsFolder || current.Size != item.Size || !strings.EqualFold(current.Sha1, item.SHA1) {
		return move, fmt.Errorf("organized object %s changed identity before rename", item.Name)
	}
	if current.Name != targetName {
		if err := s.driveSvc.RenameProvider(ctx, "115", accountID, item.DestinationID, targetName); err != nil {
			return move, fmt.Errorf("rename %s after media ingest: %w", item.Name, err)
		}
		move.Status = "moved_and_renamed"
	}
	verified, err := s.waitForIngestItem(ctx, accountID, targetCID, item.DestinationID, targetName, item.Size, item.SHA1)
	if err != nil {
		return move, err
	}
	if !verified {
		return move, fmt.Errorf("115 did not expose organized file %s", targetName)
	}
	if move.Status == "" {
		move.Status = "already_organized"
	}
	return move, nil
}

func (s *TaskQueueService) waitForIngestItem(ctx context.Context, accountID, parentCID, fileID, name string, size int64, sha1 string) (bool, error) {
	for attempt := range mediaIngestVisibilityAttempts {
		files, err := s.listAllProviderFiles(ctx, "115", accountID, parentCID)
		if err != nil {
			return false, err
		}
		for _, file := range files {
			if file.FileID == fileID && file.ParentID == parentCID && file.Name == name && !file.IsFolder && file.Size == size && strings.EqualFold(file.Sha1, sha1) {
				return true, nil
			}
		}
		if attempt+1 < mediaIngestVisibilityAttempts {
			if err := sleepContext(ctx, mediaIngestVisibilityInterval); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

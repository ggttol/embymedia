package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/embymedia/embymedia/internal/domain"
)

var autoFillLibrarySet = map[string]struct{}{
	"电视剧追更": {},
	"综艺追更":  {},
}

var (
	seasonEpisodePattern  = regexp.MustCompile(`(?i)S(\d{1,2})[ ._-]*E(\d{1,4})`)
	episodeOnlyPattern    = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])EP?[ ._-]?(\d{1,4})(?:[^[:alnum:]]|$)`)
	chineseEpisodePattern = regexp.MustCompile(`第\s*(\d{1,4})\s*集`)
)

const (
	autoFillShareEntryLimit = 10000
	autoFillShareDepthLimit = 20
	autoFillMountWait       = 2 * time.Minute
)

type seriesAutoFillSpec struct {
	Libraries            []string
	Transfer             bool
	ReplaceCompletedPack bool
	CandidateLimit       int
	MaxSeries            int
}

// SeriesAutoFillResult records discovery, exact transfer, and post-scan verification for one Series.
type SeriesAutoFillResult struct {
	SeriesID            string            `json:"series_id"`
	SeriesName          string            `json:"series_name"`
	Folder              string            `json:"folder,omitempty"`
	MissingEpisodes     []string          `json:"missing_episodes"`
	MatchedEpisodes     []string          `json:"matched_episodes,omitempty"`
	Transferred         []string          `json:"transferred_episodes,omitempty"`
	RemainingEpisodes   []string          `json:"remaining_episodes,omitempty"`
	CandidatesChecked   int               `json:"candidates_checked"`
	Issue               string            `json:"issue,omitempty"`
	ReplacementStatus   string            `json:"replacement_status,omitempty"`
	ReplacementFolder   string            `json:"replacement_folder,omitempty"`
	ReplacementRecovery map[string]string `json:"replacement_recovery,omitempty"`
	replacement         *completedPackReplacement
	probeErr            error
}

// LibraryAutoFillResult records one allowed following library's bounded repair result.
type LibraryAutoFillResult struct {
	LibraryID        string                 `json:"library_id"`
	LibraryName      string                 `json:"library_name"`
	MissingCount     int                    `json:"missing_count"`
	MatchedCount     int                    `json:"matched_count"`
	TransferredCount int                    `json:"transferred_count"`
	RemainingCount   int                    `json:"remaining_count"`
	SkippedSeries    int                    `json:"skipped_series"`
	Series           []SeriesAutoFillResult `json:"series"`
	Issues           []string               `json:"issues,omitempty"`
}

type autoFillCandidate struct {
	ID       string
	Title    string
	URL      string
	Password string
}

type autoFillLeaf struct {
	ID        string
	Name      string
	Ancestors []string
}

type episodeKey struct {
	Season  int
	Episode int
}

func (key episodeKey) String() string {
	return fmt.Sprintf("S%02dE%02d", key.Season, key.Episode)
}

func integerPayload(payload map[string]any, key string, fallback, minimum, maximum int) (int, error) {
	value, present := payload[key]
	if !present {
		return fallback, nil
	}
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case int:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = parsed
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if math.Trunc(number) != number || number < float64(minimum) || number > float64(maximum) {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", key, minimum, maximum)
	}
	return int(number), nil
}

func booleanPayload(payload map[string]any, key string, fallback bool) (bool, error) {
	value, present := payload[key]
	if !present {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return result, nil
}

func resolveSeriesAutoFillSpec(payload map[string]any) (seriesAutoFillSpec, error) {
	libraries, err := stringSlicePayload(payload, "libraries")
	if err != nil {
		return seriesAutoFillSpec{}, err
	}
	seen := make(map[string]struct{}, len(libraries))
	for _, library := range libraries {
		if _, allowed := autoFillLibrarySet[library]; !allowed {
			return seriesAutoFillSpec{}, fmt.Errorf("library %q is not eligible for automatic episode completion", library)
		}
		if _, duplicate := seen[library]; duplicate {
			return seriesAutoFillSpec{}, fmt.Errorf("libraries must not contain duplicates")
		}
		seen[library] = struct{}{}
	}
	transfer, err := booleanPayload(payload, "transfer", true)
	if err != nil {
		return seriesAutoFillSpec{}, err
	}
	candidateLimit, err := integerPayload(payload, "candidate_limit", 10, 1, 30)
	if err != nil {
		return seriesAutoFillSpec{}, err
	}
	maxSeries, err := integerPayload(payload, "max_series", 20, 1, 100)
	if err != nil {
		return seriesAutoFillSpec{}, err
	}
	replaceCompletedPack, err := booleanPayload(payload, "replace_completed_pack", false)
	if err != nil {
		return seriesAutoFillSpec{}, err
	}
	return seriesAutoFillSpec{Libraries: libraries, Transfer: transfer, ReplaceCompletedPack: replaceCompletedPack, CandidateLimit: candidateLimit, MaxSeries: maxSeries}, nil
}

func normalizeMediaTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func episodeKeysFromName(name string) []episodeKey {
	keys := make([]episodeKey, 0, 2)
	seen := make(map[episodeKey]struct{})
	for _, match := range seasonEpisodePattern.FindAllStringSubmatch(name, -1) {
		season, _ := strconv.Atoi(match[1])
		episode, _ := strconv.Atoi(match[2])
		key := episodeKey{Season: season, Episode: episode}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	if len(keys) > 0 {
		return keys
	}
	for _, pattern := range []*regexp.Regexp{episodeOnlyPattern, chineseEpisodePattern} {
		for _, match := range pattern.FindAllStringSubmatch(name, -1) {
			episode, _ := strconv.Atoi(match[1])
			key := episodeKey{Season: 1, Episode: episode}
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func parseAutoFillCandidates(result map[string]any) []autoFillCandidate {
	links, _ := result["links"].([]any)
	candidates := make([]autoFillCandidate, 0, len(links))
	seen := make(map[string]struct{})
	for _, value := range links {
		link, ok := value.(map[string]any)
		if !ok {
			continue
		}
		rawURL, _ := link["url"].(string)
		title, _ := link["title"].(string)
		if rawURL == "" || title == "" {
			continue
		}
		if _, duplicate := seen[rawURL]; duplicate {
			continue
		}
		seen[rawURL] = struct{}{}
		password, _ := link["password"].(string)
		var id string
		switch value := link["id"].(type) {
		case float64:
			id = strconv.FormatFloat(value, 'f', -1, 64)
		default:
			id = fmt.Sprint(value)
		}
		candidates = append(candidates, autoFillCandidate{ID: id, Title: title, URL: rawURL, Password: password})
	}
	return candidates
}

func (s *TaskQueueService) searchAutoFillCandidates(ctx context.Context, seriesName string, limit int) ([]autoFillCandidate, error) {
	result, err := s.driveSvc.SearchResourcesCtx(ctx, url.Values{
		"q": {seriesName}, "disk_type": {"115"}, "sort": {"latest"}, "limit": {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	return parseAutoFillCandidates(result), nil
}

func stopAutoFillShareProbes(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var statusErr *ProviderHTTPError
	return errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusMethodNotAllowed || statusErr.StatusCode == http.StatusTooManyRequests)
}

func (s *TaskQueueService) scanAutoFillShare(ctx context.Context, candidate autoFillCandidate) (string, []autoFillLeaf, error) {
	account, err := s.driveSvc.getAccount("")
	if err != nil {
		return "", nil, err
	}
	shareCode, receiveCode, err := ParseShareCode(candidate.URL, candidate.Password)
	if err != nil {
		return "", nil, err
	}
	title := ""
	leaves := make([]autoFillLeaf, 0)
	seenDirectories := make(map[string]struct{})
	var walk func(string, int, []string) error
	walk = func(cid string, depth int, ancestors []string) error {
		if depth > autoFillShareDepthLimit {
			return fmt.Errorf("share directory depth exceeds %d", autoFillShareDepthLimit)
		}
		if _, exists := seenDirectories[cid]; exists {
			return nil
		}
		seenDirectories[cid] = struct{}{}
		pageTitle, entries, snapshotErr := s.driveSvc.snapshotShareEntries(ctx, account, shareCode, receiveCode, cid)
		if title == "" {
			title = pageTitle
		}
		for _, entry := range entries {
			if len(leaves)+len(seenDirectories) > autoFillShareEntryLimit {
				return errors.Join(snapshotErr, fmt.Errorf("share tree exceeds %d entries", autoFillShareEntryLimit))
			}
			if entry.IsDir {
				if snapshotErr != nil {
					continue
				}
				if err := walk(entry.ID, depth+1, append(ancestors, entry.Name)); err != nil {
					return err
				}
				continue
			}
			if _, video := videoExtensions[strings.ToLower(filepath.Ext(entry.Name))]; video {
				leaves = append(leaves, autoFillLeaf{ID: entry.ID, Name: entry.Name, Ancestors: append([]string(nil), ancestors...)})
			}
		}
		return snapshotErr
	}
	if err := walk("0", 0, nil); err != nil {
		return title, leaves, err
	}
	return title, leaves, nil
}

func (s *TaskQueueService) resolveAutoFillFolder(ctx context.Context, libraryCID, folderName string) (string, error) {
	matches := make([]string, 0, 1)
	for offset := 0; ; {
		files, total, err := s.driveSvc.ListFilesPageCtx(ctx, "", libraryCID, offset, 1000)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			if file.IsFolder && file.Name == folderName {
				matches = append(matches, file.FileID)
			}
		}
		offset += len(files)
		if len(files) == 0 || int64(offset) >= total {
			break
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("expected one 115 folder named %q under the library, found %d", folderName, len(matches))
	}
	return matches[0], nil
}

func waitForAutoFillFiles(ctx context.Context, paths []string) error {
	deadline := time.Now().Add(autoFillMountWait)
	for {
		missing := 0
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				missing++
			}
		}
		if missing == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d transferred file(s) did not appear in CloudDrive within %s", missing, autoFillMountWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func sortedEpisodeLabels(keys map[episodeKey]struct{}) []string {
	ordered := make([]episodeKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Season != ordered[j].Season {
			return ordered[i].Season < ordered[j].Season
		}
		return ordered[i].Episode < ordered[j].Episode
	})
	labels := make([]string, 0, len(ordered))
	for _, key := range ordered {
		labels = append(labels, key.String())
	}
	return labels
}

func gapsBySeries(episodes []EmbyMissingEpisode) ([]string, map[string]map[episodeKey]struct{}, map[string]string) {
	order := make([]string, 0)
	gaps := make(map[string]map[episodeKey]struct{})
	names := make(map[string]string)
	for _, episode := range episodes {
		if _, exists := gaps[episode.SeriesID]; !exists {
			gaps[episode.SeriesID] = make(map[episodeKey]struct{})
			order = append(order, episode.SeriesID)
			names[episode.SeriesID] = episode.SeriesName
		}
		gaps[episode.SeriesID][episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}] = struct{}{}
	}
	return order, gaps, names
}

func (s *TaskQueueService) processAutoFillSeries(ctx context.Context, library domain.EmbyLibrary, libraryCID string, gaps map[episodeKey]struct{}, seriesID, seriesName string, canonical *domain.EmbyMediaItem, tmdbCount int, spec seriesAutoFillSpec) (SeriesAutoFillResult, []string) {
	result := SeriesAutoFillResult{SeriesID: seriesID, SeriesName: seriesName, MissingEpisodes: sortedEpisodeLabels(gaps)}
	if canonical == nil || canonical.Type != "Series" {
		result.Issue = fmt.Sprintf("Emby Series %s is absent from its eligible library", seriesID)
		result.RemainingEpisodes = result.MissingEpisodes
		return result, nil
	}
	series := canonical
	tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"])
	if tmdbID == "" || tmdbCount != 1 {
		result.Issue = fmt.Sprintf("canonical TMDB identity is missing or duplicated (%d matching Series)", tmdbCount)
		result.RemainingEpisodes = result.MissingEpisodes
		return result, nil
	}
	folder := filepath.Base(filepath.Clean(series.Path))
	if folder == "." || folder == string(filepath.Separator) || filepath.Base(filepath.Dir(filepath.Clean(series.Path))) != library.Name {
		result.Issue = "Series path is not a direct child of the eligible library"
		result.RemainingEpisodes = result.MissingEpisodes
		return result, nil
	}
	result.Folder = folder
	targetCID, err := s.resolveAutoFillFolder(ctx, libraryCID, folder)
	if err != nil {
		result.Issue = err.Error()
		result.RemainingEpisodes = result.MissingEpisodes
		return result, nil
	}
	candidates, err := s.searchAutoFillCandidates(ctx, seriesName, spec.CandidateLimit)
	if err != nil {
		result.Issue = err.Error()
		result.RemainingEpisodes = result.MissingEpisodes
		return result, nil
	}
	if spec.Transfer && spec.ReplaceCompletedPack {
		replacement, paths, err := s.stageCompletedPack(ctx, library, libraryCID, gaps, series, candidates)
		if replacement != nil {
			result.MatchedEpisodes = result.MissingEpisodes
			result.Transferred = result.MissingEpisodes
			result.ReplacementStatus = "staged"
			result.ReplacementFolder = replacement.NewFolder
			result.replacement = replacement
			return result, paths
		}
		if err != nil {
			result.probeErr = err
			result.Issue = err.Error()
			result.RemainingEpisodes = result.MissingEpisodes
			return result, nil
		}
	}
	unmatched := make(map[episodeKey]struct{}, len(gaps))
	for key := range gaps {
		unmatched[key] = struct{}{}
	}
	matched := make(map[episodeKey]struct{})
	transferred := make(map[episodeKey]struct{})
	lastTransferError := ""
	var inspectionErrors []error
	transferredPaths := make([]string, 0)
	mediaRoot, _ := s.db.GetSetting("media_root")
	for _, candidate := range candidates {
		if len(unmatched) == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			result.probeErr = err
			result.Issue = err.Error()
			break
		}
		result.CandidatesChecked++
		shareTitle, leaves, scanErr := s.scanAutoFillShare(ctx, candidate)
		if scanErr != nil {
			result.probeErr = scanErr
			inspectionErrors = append(inspectionErrors, fmt.Errorf("resource %s: %w", candidate.ID, scanErr))
		}
		selectedIDs := make([]string, 0)
		selectedKeys := make([]episodeKey, 0)
		selectedNames := make([]string, 0)
		claimed := make(map[episodeKey]struct{})
		for _, leaf := range leaves {
			if !autoFillEpisodeIdentityMatches(series, candidate.Title, shareTitle, leaf) {
				continue
			}
			for _, key := range episodeKeysFromName(leaf.Name) {
				if _, needed := unmatched[key]; !needed {
					continue
				}
				if _, duplicate := claimed[key]; duplicate {
					continue
				}
				claimed[key] = struct{}{}
				matched[key] = struct{}{}
				selectedIDs = append(selectedIDs, leaf.ID)
				selectedKeys = append(selectedKeys, key)
				selectedNames = append(selectedNames, leaf.Name)
				break
			}
		}
		if scanErr != nil {
			if stopAutoFillShareProbes(scanErr) {
				break
			}
			continue
		}
		if len(selectedIDs) == 0 {
			continue
		}
		if !spec.Transfer {
			for _, key := range selectedKeys {
				delete(unmatched, key)
			}
			continue
		}
		account, err := s.driveSvc.getAccount("")
		if err != nil {
			result.Issue = err.Error()
			break
		}
		shareCode, receiveCode, err := ParseShareCode(candidate.URL, candidate.Password)
		if err != nil {
			continue
		}
		if _, _, err := s.driveSvc.receiveShareEntriesCtx(ctx, account, shareCode, receiveCode, selectedIDs, targetCID, shareTitle); err != nil {
			lastTransferError = err.Error()
			if stopAutoFillShareProbes(err) {
				result.probeErr = err
				break
			}
			continue
		}
		for index, key := range selectedKeys {
			delete(unmatched, key)
			transferred[key] = struct{}{}
			transferredPaths = append(transferredPaths, filepath.Join(mediaRoot, library.Name, folder, selectedNames[index]))
		}
	}
	if len(unmatched) > 0 && result.Issue == "" {
		switch {
		case spec.Transfer && lastTransferError != "":
			result.Issue = "matched episode resources could not be transferred: " + lastTransferError
		case len(inspectionErrors) > 0:
			result.Issue = fmt.Sprintf("%d of %d candidate inspections failed; %d episode(s) remain unmatched: %v", len(inspectionErrors), result.CandidatesChecked, len(unmatched), errors.Join(inspectionErrors...))
		}
	}
	result.MatchedEpisodes = sortedEpisodeLabels(matched)
	result.Transferred = sortedEpisodeLabels(transferred)
	result.RemainingEpisodes = sortedEpisodeLabels(unmatched)
	if spec.Transfer && len(transferredPaths) > 0 {
		if err := waitForAutoFillFiles(ctx, transferredPaths); err != nil {
			result.Issue = err.Error()
			result.probeErr = errors.Join(result.probeErr, err)
		}
	}
	return result, transferredPaths
}

func loadAutoFillCIDMap(dbSetting string, libraries []string) (map[string]string, error) {
	var cidMap map[string]string
	if err := json.Unmarshal([]byte(dbSetting), &cidMap); err != nil {
		return nil, fmt.Errorf("c115_cid_map must be configured as a JSON object: %w", err)
	}
	for _, library := range libraries {
		if strings.TrimSpace(cidMap[library]) == "" {
			return nil, fmt.Errorf("c115_cid_map is missing %q", library)
		}
	}
	return cidMap, nil
}

func (s *TaskQueueService) runSeriesAutoFill(ctx context.Context, task domain.AsyncTask) (outcome map[string]any, runErr error) {
	spec, err := resolveSeriesAutoFillSpec(task.Payload)
	if err != nil {
		return nil, err
	}
	if spec.Transfer && spec.ReplaceCompletedPack {
		enabled, _ := s.db.GetSetting("dangerous_actions_enabled")
		if enabled != "true" {
			return nil, fmt.Errorf("dangerous_actions_enabled must be true for automatic completed-pack replacement")
		}
	}
	rawCIDMap, err := s.db.GetSetting("c115_cid_map")
	if err != nil {
		return nil, err
	}
	cidMap, err := loadAutoFillCIDMap(rawCIDMap, spec.Libraries)
	if err != nil {
		return nil, err
	}
	libraries, err := s.embySvc.ListLibrariesCtx(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]domain.EmbyLibrary, len(libraries))
	for _, library := range libraries {
		byName[library.Name] = library
	}
	results := make([]LibraryAutoFillResult, 0, len(spec.Libraries))
	totalMissing, totalMatched, totalTransferred := 0, 0, 0
	anyTransferred := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		removedStaging := false
		for libraryIndex := range results {
			for seriesIndex := range results[libraryIndex].Series {
				seriesResult := &results[libraryIndex].Series[seriesIndex]
				replacement := seriesResult.replacement
				if replacement == nil {
					continue
				}
				if replacement.BackupDir != "" {
					seriesResult.ReplacementRecovery = map[string]string{"backup_dir": replacement.BackupDir, "account_id": replacement.AccountID, "quarantine_cid": replacement.QuarantineCID, "original_cid": replacement.OldCID}
				}
				if replacement.Finalized {
					continue
				}
				if err := s.rollbackCompletedPack(cleanupCtx, replacement); err != nil {
					seriesResult.ReplacementStatus = "recovery_required"
					if seriesResult.Issue != "" {
						seriesResult.Issue += "; "
					}
					seriesResult.Issue += err.Error()
					runErr = errors.Join(runErr, fmt.Errorf("restore %s: %w", seriesResult.SeriesName, err))
				} else {
					seriesResult.ReplacementStatus = "kept_old"
					removedStaging = removedStaging || !replacement.RollbackScanned
				}
			}
		}
		if removedStaging {
			_, err := s.embySvc.RunLibraryScanCtx(cleanupCtx, func(float64, string) error { return nil })
			runErr = errors.Join(runErr, err)
		}
		if runErr != nil && len(results) > 0 {
			mode := "transfer"
			if !spec.Transfer {
				mode = "preview"
			}
			missing, matched, transferred, remaining := 0, 0, 0, 0
			for _, libraryResult := range results {
				missing += libraryResult.MissingCount
				matched += libraryResult.MatchedCount
				transferred += libraryResult.TransferredCount
				remaining += libraryResult.RemainingCount
			}
			outcome = map[string]any{"mode": mode, "libraries": results, "missing": missing, "matched": matched, "transferred": transferred, "remaining": remaining, "verification_complete": false}
		}
	}()
	initialSeriesByLibrary := make(map[string]map[string]domain.EmbyMediaItem, len(spec.Libraries))
	for libraryIndex, libraryName := range spec.Libraries {
		library, exists := byName[libraryName]
		if !exists || library.Collection != "tvshows" {
			return nil, fmt.Errorf("eligible Emby library %q is missing or is not a TV library", libraryName)
		}
		if err := s.db.AppendTaskLog(task.ID, "Auto-fill scanning library "+libraryName); err != nil {
			return nil, err
		}
		missing, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, library.ID, time.Now())
		if err != nil {
			return nil, err
		}
		seriesInventory, err := s.embySvc.ListSeriesCtx(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		seriesByID := make(map[string]domain.EmbyMediaItem, len(seriesInventory))
		tmdbCounts := make(map[string]int)
		for _, series := range seriesInventory {
			seriesByID[series.ID] = series
			if tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"]); tmdbID != "" {
				tmdbCounts[tmdbID]++
			}
		}
		initialSeriesByLibrary[library.ID] = seriesByID
		order, gaps, names := gapsBySeries(missing)
		results = append(results, LibraryAutoFillResult{LibraryID: library.ID, LibraryName: libraryName, MissingCount: len(missing), RemainingCount: len(missing), Series: []SeriesAutoFillResult{}})
		libraryResult := &results[len(results)-1]
		duplicateIDs := make([]string, 0)
		for tmdbID, count := range tmdbCounts {
			if count > 1 {
				duplicateIDs = append(duplicateIDs, tmdbID)
			}
		}
		sort.Strings(duplicateIDs)
		for _, tmdbID := range duplicateIDs {
			libraryResult.Issues = append(libraryResult.Issues, fmt.Sprintf("TMDB %s belongs to %d Series in %s", tmdbID, tmdbCounts[tmdbID], libraryName))
		}
		if len(order) > spec.MaxSeries {
			libraryResult.SkippedSeries = len(order) - spec.MaxSeries
			order = order[:spec.MaxSeries]
		}
		for seriesIndex, seriesID := range order {
			progress := 10 + 60*float64(libraryIndex*spec.MaxSeries+seriesIndex)/float64(max(1, len(spec.Libraries)*spec.MaxSeries))
			if err := s.db.UpdateAsyncTaskProgress(task.ID, progress); err != nil {
				return nil, err
			}
			canonical, exists := seriesByID[seriesID]
			var canonicalSeries *domain.EmbyMediaItem
			tmdbCount := 0
			if exists {
				canonicalSeries = &canonical
				tmdbCount = tmdbCounts[strings.TrimSpace(canonical.ProviderIDs["Tmdb"])]
			}
			seriesResult, transferredPaths := s.processAutoFillSeries(ctx, library, cidMap[libraryName], gaps[seriesID], seriesID, names[seriesID], canonicalSeries, tmdbCount, spec)
			libraryResult.Series = append(libraryResult.Series, seriesResult)
			libraryResult.MatchedCount += len(seriesResult.MatchedEpisodes)
			libraryResult.TransferredCount += len(seriesResult.Transferred)
			if seriesResult.replacement != nil {
				if err := s.db.AppendTaskLog(task.ID, "Auto-fill completed pack staged series="+seriesResult.SeriesName); err != nil {
					return nil, err
				}
			}
			if seriesResult.Issue != "" {
				libraryResult.Issues = append(libraryResult.Issues, seriesResult.SeriesName+": "+seriesResult.Issue)
			}
			if len(transferredPaths) > 0 {
				anyTransferred = true
			}
			if stopAutoFillShareProbes(seriesResult.probeErr) {
				return nil, seriesResult.probeErr
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if spec.Transfer && libraryResult.TransferredCount > 0 {
			syncResult, err := s.mediaSvc.SyncSTRMWithProgress(ctx, libraryName, nil)
			if err != nil {
				return nil, err
			}
			if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("Auto-fill STRM sync library=%s created=%d updated=%d", libraryName, syncResult.Created, syncResult.Updated)); err != nil {
				return nil, err
			}
		}
		totalMissing += libraryResult.MissingCount
		totalMatched += libraryResult.MatchedCount
		totalTransferred += libraryResult.TransferredCount
	}
	if spec.Transfer && anyTransferred {
		if _, err := s.embySvc.RunLibraryScanCtx(ctx, s.taskProgress(task.ID, 75, 95)); err != nil {
			return nil, err
		}
	}
	canonicalByLibrary := make(map[string]map[string]domain.EmbyMediaItem, len(results))
	for _, result := range results {
		inventory, err := s.embySvc.ListSeriesCtx(ctx, result.LibraryID)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]domain.EmbyMediaItem, len(inventory))
		for _, series := range inventory {
			byID[series.ID] = series
		}
		canonicalByLibrary[result.LibraryID] = byID
	}
	totalRemaining := 0
	for index := range results {
		remaining, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, results[index].LibraryID, time.Now())
		if err != nil {
			return nil, err
		}
		results[index].RemainingCount = len(remaining)
		totalRemaining += len(remaining)
		remainingBySeries := make(map[string]map[episodeKey]struct{})
		for _, episode := range remaining {
			if remainingBySeries[episode.SeriesID] == nil {
				remainingBySeries[episode.SeriesID] = make(map[episodeKey]struct{})
			}
			remainingBySeries[episode.SeriesID][episodeKey{Season: episode.SeasonNumber, Episode: episode.EpisodeNumber}] = struct{}{}
		}
		for seriesIndex := range results[index].Series {
			results[index].Series[seriesIndex].RemainingEpisodes = sortedEpisodeLabels(remainingBySeries[results[index].Series[seriesIndex].SeriesID])
		}
		currentSeries := canonicalByLibrary[results[index].LibraryID]
		tmdbCounts := make(map[string]int)
		for _, series := range currentSeries {
			if tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"]); tmdbID != "" {
				tmdbCounts[tmdbID]++
			}
		}
		for seriesIndex := range results[index].Series {
			seriesResult := &results[index].Series[seriesIndex]
			current, exists := currentSeries[seriesResult.SeriesID]
			if !exists {
				seriesResult.Issue = "canonical Series disappeared after refresh"
				results[index].Issues = append(results[index].Issues, seriesResult.SeriesName+": "+seriesResult.Issue)
				continue
			}
			tmdbID := strings.TrimSpace(current.ProviderIDs["Tmdb"])
			initial, hadInitial := initialSeriesByLibrary[results[index].LibraryID][seriesResult.SeriesID]
			expectedTMDBCount := 1
			if seriesResult.replacement != nil {
				expectedTMDBCount = 2
			}
			if !hadInitial || current.Path != initial.Path || tmdbID == "" || tmdbID != strings.TrimSpace(initial.ProviderIDs["Tmdb"]) || tmdbCounts[tmdbID] != expectedTMDBCount {
				seriesResult.Issue = "canonical Series path or expected TMDB identity group changed after refresh"
				results[index].Issues = append(results[index].Issues, seriesResult.SeriesName+": "+seriesResult.Issue)
			}
		}
	}
	replacedAny := false
	for libraryIndex := range results {
		for seriesIndex := range results[libraryIndex].Series {
			seriesResult := &results[libraryIndex].Series[seriesIndex]
			if seriesResult.replacement == nil || seriesResult.Issue != "" {
				continue
			}
			newSeriesID, err := s.finalizeCompletedPack(ctx, seriesResult.replacement)
			if err != nil {
				seriesResult.Issue = err.Error()
				seriesResult.ReplacementStatus = "kept_old"
				results[libraryIndex].Issues = append(results[libraryIndex].Issues, seriesResult.SeriesName+": "+err.Error())
				continue
			}
			seriesResult.ReplacementStatus = "replaced"
			seriesResult.SeriesID = newSeriesID
			seriesResult.Folder = seriesResult.replacement.OldFolder
			seriesResult.ReplacementFolder = seriesResult.replacement.OldFolder
			seriesResult.RemainingEpisodes = nil
			if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("Auto-fill completed pack replaced series=%s backup=%s account=%s quarantine_cid=%s original_cid=%s", seriesResult.SeriesName, seriesResult.replacement.BackupDir, seriesResult.replacement.AccountID, seriesResult.replacement.QuarantineCID, seriesResult.replacement.OldCID)); err != nil {
				return nil, err
			}
			replacedAny = true
		}
	}
	if replacedAny {
		totalRemaining = 0
		for index := range results {
			remaining, err := s.embySvc.ListAiredMissingEpisodesCtx(ctx, results[index].LibraryID, time.Now())
			if err != nil {
				return nil, err
			}
			results[index].RemainingCount = len(remaining)
			totalRemaining += len(remaining)
		}
	}
	mode := "transfer"
	if !spec.Transfer {
		mode = "preview"
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("Auto-fill completed mode=%s missing=%d matched=%d transferred=%d remaining=%d", mode, totalMissing, totalMatched, totalTransferred, totalRemaining)); err != nil {
		return nil, err
	}
	result := map[string]any{
		"mode": mode, "libraries": results, "missing": totalMissing, "matched": totalMatched,
		"transferred": totalTransferred, "remaining": totalRemaining,
	}
	issues := make([]string, 0)
	for _, libraryResult := range results {
		issues = append(issues, libraryResult.Issues...)
	}
	if len(issues) > 0 {
		return result, fmt.Errorf("automatic episode completion failed: %s", strings.Join(issues, "; "))
	}
	return result, nil
}

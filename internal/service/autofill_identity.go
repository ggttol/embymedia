package service

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/embymedia/embymedia/internal/domain"
)

var (
	autoFillTMDBPattern   = regexp.MustCompile(`(?i)(?:tmdb(?:[\s._-]*id)?[\s._:=\-\[\]{}]*|themoviedb\.org/(?:tv|movie)/)(\d+)`)
	autoFillYearPattern   = regexp.MustCompile(`(?:^|[ ._(\[])((?:19|20)\d{2})(?:$|[ ._)\]])`)
	autoFillSeasonPattern = regexp.MustCompile(`(?i)(?:\bS|\bSeason[ ._-]*|第\s*)(\d{1,2})(?:\b|E\d|\s*季)`)
)

type autoFillLabelIdentity struct {
	title  string
	year   int
	season int
}

func parseAutoFillLabel(label string) autoFillLabelIdentity {
	if _, video := videoExtensions[strings.ToLower(filepath.Ext(label))]; video {
		label = strings.TrimSuffix(label, filepath.Ext(label))
	}
	label = autoFillTMDBPattern.ReplaceAllString(label, "")
	identity := autoFillLabelIdentity{}
	if match := autoFillYearPattern.FindStringSubmatch(label); match != nil {
		identity.year, _ = strconv.Atoi(match[1])
		label = autoFillYearPattern.ReplaceAllString(label, " ")
	}
	cut := len(label)
	if match := autoFillSeasonPattern.FindStringSubmatchIndex(label); match != nil {
		identity.season, _ = strconv.Atoi(label[match[2]:match[3]])
		cut = match[0]
	}
	for _, pattern := range []*regexp.Regexp{seasonEpisodePattern, episodeOnlyPattern, chineseEpisodePattern} {
		if match := pattern.FindStringIndex(label); match != nil && match[0] < cut {
			cut = match[0]
		}
	}
	label = label[:cut]
	label = strings.NewReplacer(".", " ", "_", " ").Replace(label)
	label = metadataCutPattern.ReplaceAllString(label, "")
	identity.title = normalizeMediaTitle(label)
	return identity
}

func autoFillGenericLabel(title string) bool {
	switch title {
	case "", "合集", "电视剧合集", "综艺合集", "complete", "completepack":
		return true
	default:
		return false
	}
}

type autoFillIdentityEvidence struct {
	matchedTitle  bool
	matchedID     bool
	matchedYear   bool
	canonicalYear int
}

func collectAutoFillIdentityEvidence(series *domain.EmbyMediaItem, rejectOtherTitles bool, labels ...string) (autoFillIdentityEvidence, bool) {
	evidence := autoFillIdentityEvidence{}
	if series == nil {
		return evidence, false
	}
	tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"])
	canonical := parseAutoFillLabel(series.Name)
	originalTitle := parseAutoFillLabel(series.OriginalTitle).title
	folder := parseAutoFillLabel(filepath.Base(filepath.Clean(series.Path)))
	if canonical.title == "" {
		return evidence, false
	}
	if canonical.year == 0 {
		canonical.year = folder.year
	}
	if canonical.year == 0 && len(series.PremiereDate) >= 4 {
		canonical.year, _ = strconv.Atoi(series.PremiereDate[:4])
	}
	if canonical.season == 0 {
		canonical.season = folder.season
	}
	evidence.canonicalYear = canonical.year
	season := canonical.season
	year := canonical.year
	for _, label := range labels {
		labelID := false
		for _, match := range autoFillTMDBPattern.FindAllStringSubmatch(label, -1) {
			if tmdbID == "" || match[1] != tmdbID {
				return evidence, false
			}
			labelID = true
			evidence.matchedID = true
		}
		identity := parseAutoFillLabel(label)
		if identity.year != 0 {
			if year != 0 && identity.year != year {
				return evidence, false
			}
			evidence.matchedYear = true
			year = identity.year
		}
		if identity.season != 0 {
			if season != 0 && identity.season != season {
				return evidence, false
			}
			season = identity.season
		}
		if autoFillGenericLabel(identity.title) {
			continue
		}
		if identity.title == canonical.title || identity.title == originalTitle {
			evidence.matchedTitle = true
			continue
		}
		if !labelID && rejectOtherTitles {
			return evidence, false
		}
	}
	return evidence, true
}

// autoFillIdentityMatches requires every non-generic label to identify the Series.
func autoFillIdentityMatches(series *domain.EmbyMediaItem, labels ...string) bool {
	evidence, agrees := collectAutoFillIdentityEvidence(series, true, labels...)
	return agrees && (evidence.matchedID || (evidence.matchedTitle && evidence.canonicalYear != 0 && evidence.matchedYear))
}

func autoFillLeafSeasonMatches(leaf autoFillLeaf) bool {
	for _, label := range leaf.Ancestors {
		season := parseAutoFillLabel(label).season
		if season == 0 {
			continue
		}
		for _, key := range episodeKeysFromName(leaf.Name) {
			if key.Season != season {
				return false
			}
		}
	}
	return true
}

func autoFillLeafIdentityMatches(series *domain.EmbyMediaItem, leaf autoFillLeaf) bool {
	labels := make([]string, 0, len(leaf.Ancestors)+1)
	labels = append(labels, leaf.Ancestors...)
	labels = append(labels, leaf.Name)
	return autoFillIdentityMatches(series, labels...) && autoFillLeafSeasonMatches(leaf)
}

// autoFillEpisodeIdentityMatches requires the inspected path to name the Series;
// candidate and share titles can only corroborate its release year or TMDB ID.
func autoFillEpisodeIdentityMatches(series *domain.EmbyMediaItem, candidateTitle, shareTitle string, leaf autoFillLeaf) bool {
	liveLabels := make([]string, 0, len(leaf.Ancestors)+1)
	liveLabels = append(liveLabels, leaf.Ancestors...)
	liveLabels = append(liveLabels, leaf.Name)
	liveEvidence, liveLabelsAgree := collectAutoFillIdentityEvidence(series, true, liveLabels...)
	if !liveLabelsAgree || (!liveEvidence.matchedTitle && !liveEvidence.matchedID) {
		return false
	}

	allLabels := make([]string, 0, len(liveLabels)+2)
	allLabels = append(allLabels, candidateTitle, shareTitle)
	allLabels = append(allLabels, liveLabels...)
	allEvidence, allLabelsAgree := collectAutoFillIdentityEvidence(series, false, allLabels...)
	if !allLabelsAgree || (!allEvidence.matchedID && (allEvidence.canonicalYear == 0 || !allEvidence.matchedYear)) {
		return false
	}
	return autoFillLeafSeasonMatches(leaf)
}

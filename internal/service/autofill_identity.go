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

// autoFillIdentityMatches checks each label independently. Callers must also check
// actual directory/file labels without candidate or share advertisements.
func autoFillIdentityMatches(series *domain.EmbyMediaItem, labels ...string) bool {
	if series == nil {
		return false
	}
	tmdbID := strings.TrimSpace(series.ProviderIDs["Tmdb"])
	canonical := parseAutoFillLabel(series.Name)
	originalTitle := parseAutoFillLabel(series.OriginalTitle).title
	folder := parseAutoFillLabel(filepath.Base(filepath.Clean(series.Path)))
	if canonical.title == "" {
		return false
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
	matched, matchedID, matchedYear := false, false, false
	season := canonical.season
	year := canonical.year
	for _, label := range labels {
		labelID := false
		for _, match := range autoFillTMDBPattern.FindAllStringSubmatch(label, -1) {
			if tmdbID == "" || match[1] != tmdbID {
				return false
			}
			labelID = true
		}
		identity := parseAutoFillLabel(label)
		if identity.year != 0 {
			if year != 0 && identity.year != year {
				return false
			}
			matchedYear = true
			year = identity.year
		}
		if identity.season != 0 {
			if season != 0 && identity.season != season {
				return false
			}
			season = identity.season
		}
		if !autoFillGenericLabel(identity.title) {
			if identity.title != canonical.title && identity.title != originalTitle && !labelID {
				return false
			}
			matched = true
		}
		matchedID = matchedID || labelID
	}
	return matchedID || (matched && canonical.year != 0 && matchedYear)
}

func autoFillLeafIdentityMatches(series *domain.EmbyMediaItem, leaf autoFillLeaf) bool {
	labels := make([]string, 0, len(leaf.Ancestors)+1)
	labels = append(labels, leaf.Ancestors...)
	labels = append(labels, leaf.Name)
	if !autoFillIdentityMatches(series, labels...) {
		return false
	}
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

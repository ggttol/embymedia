package service

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	legacySTRMSeasonPattern  = regexp.MustCompile(`(?i)^SE(\d{1,2})$`)
	legacySTRMEpisodePattern = regexp.MustCompile(`(?i)SE(\d{1,2})\.(\d{1,4})`)
)

func canonicalSTRMDirectory(relative string) string {
	parent, directory := filepath.Split(relative)
	season, ok := legacySTRMSeason(directory)
	if !ok {
		return relative
	}
	return parent + fmt.Sprintf("Season %02d", season)
}

// Only explicit, matching season and episode numbers permit a legacy-directory rewrite.
// Series ancestors and release labels remain distinct; media targets retain their source names.
func canonicalSTRMPath(sourceRelative string) (string, error) {
	directory, filename := filepath.Split(sourceRelative)
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	seasonDirectory := strings.TrimSuffix(directory, string(filepath.Separator))
	season, legacy := legacySTRMSeason(filepath.Base(seasonDirectory))
	if !legacy {
		return directory + stem + ".strm", nil
	}

	start, end := -1, -1
	var key episodeKey
	for _, pattern := range []*regexp.Regexp{seasonEpisodePattern, legacySTRMEpisodePattern} {
		for _, match := range pattern.FindAllStringSubmatchIndex(stem, -1) {
			if !strmEpisodeTokenBoundary(stem, match[0], match[1]) {
				continue
			}
			if start >= 0 {
				return "", fmt.Errorf("ambiguous season/episode numbering in %q", sourceRelative)
			}
			start, end = match[0], match[1]
			key.Season, _ = strconv.Atoi(stem[match[2]:match[3]])
			key.Episode, _ = strconv.Atoi(stem[match[4]:match[5]])
		}
	}
	if start < 0 {
		return "", fmt.Errorf("missing explicit season/episode numbering in %q", sourceRelative)
	}
	if key.Season != season {
		return "", fmt.Errorf("filename season %d disagrees with directory season %d in %q", key.Season, season, sourceRelative)
	}
	if key.Episode == 0 {
		return "", fmt.Errorf("episode zero in %q", sourceRelative)
	}
	return canonicalSTRMDirectory(seasonDirectory) + string(filepath.Separator) + stem[:start] + key.String() + stem[end:] + ".strm", nil
}

func legacySTRMSeason(directory string) (int, bool) {
	match := legacySTRMSeasonPattern.FindStringSubmatch(directory)
	if match == nil {
		return 0, false
	}
	season, _ := strconv.Atoi(match[1])
	return season, true
}

func strmEpisodeTokenBoundary(name string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(name[:start])
		if unicode.IsLetter(previous) || unicode.IsNumber(previous) {
			return false
		}
	}
	if end < len(name) {
		next, _ := utf8.DecodeRuneInString(name[end:])
		if unicode.IsLetter(next) || unicode.IsNumber(next) {
			return false
		}
	}
	return true
}

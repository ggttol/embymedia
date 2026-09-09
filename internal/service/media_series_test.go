package service

import "testing"

func TestCanonicalSTRMDirectoryChangesOnlyFinalLegacySeason(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{"TV/SE01/Show/se2", "TV/SE01/Show/Season 02"},
		{"SE00", "Season 00"},
		{"TV/SE02/Show", "TV/SE02/Show"},
		{"TV/Show/Season 02", "TV/Show/Season 02"},
		{"TV/Show/SE002", "TV/Show/SE002"},
		{"TV/Show/SE02 Extras", "TV/Show/SE02 Extras"},
	} {
		t.Run(test.source, func(t *testing.T) {
			if got := canonicalSTRMDirectory(test.source); got != test.want {
				t.Fatalf("canonical directory = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalSTRMPathPreservesSeriesAndReleaseVariants(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{
			"TV/Pack/Show/SE02/24小时.1080P.H265.SE02.01.mkv",
			"TV/Pack/Show/Season 02/24小时.1080P.H265.S02E01.strm",
		},
		{
			"TV/Pack/Show/se2/24小时.2160P.H265.se2.1.HDR.MKV",
			"TV/Pack/Show/Season 02/24小时.2160P.H265.S02E01.HDR.strm",
		},
		{
			"SE02/Show/SE02/Show_[s2-e103]_Director's Cut.mp4",
			"SE02/Show/Season 02/Show_[S02E103]_Director's Cut.strm",
		},
		{
			"TV/Show/SE00/S00E01.Special.mkv",
			"TV/Show/Season 00/S00E01.Special.strm",
		},
	} {
		t.Run(test.source, func(t *testing.T) {
			got, err := canonicalSTRMPath(test.source)
			if err != nil || got != test.want {
				t.Fatalf("canonical path = %q, err = %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestCanonicalSTRMPathLeavesOtherNamingConventionsAlone(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{"Movies/SE02.01.2024.mkv", "Movies/SE02.01.2024.strm"},
		{"TV/Show/Season 02/show.s2e1.mkv", "TV/Show/Season 02/show.s2e1.strm"},
		{"TV/Show/SE02/Extras/Trailer.mp4", "TV/Show/SE02/Extras/Trailer.strm"},
	} {
		t.Run(test.source, func(t *testing.T) {
			got, err := canonicalSTRMPath(test.source)
			if err != nil || got != test.want {
				t.Fatalf("canonical path = %q, err = %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestCanonicalSTRMPathRejectsUncertainEpisodeIdentity(t *testing.T) {
	for _, filename := range []string{
		"Show.1080P.H265.01.mkv",
		"Show.SE03.01.mkv",
		"Show.SE02.00.mkv",
		"Show.SE02.01.S02E01.mkv",
		"Show.S02E01-S03E02.mkv",
		"Show.S02E01.S02E01.mkv",
		"Show.ASE02.01.mkv",
		"Show.SE02.01000.mkv",
		"Show.S02E01E02.mkv",
		"剧SE02.01.mkv",
	} {
		t.Run(filename, func(t *testing.T) {
			got, err := canonicalSTRMPath("TV/Show/SE02/" + filename)
			if err == nil || got != "" {
				t.Fatalf("uncertain identity produced %q, err = %v", got, err)
			}
		})
	}
}

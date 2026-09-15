package service

import (
	"path/filepath"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func lionessSeries() domain.EmbyMediaItem {
	return domain.EmbyMediaItem{
		ID: "series", Name: "特别行动：母狮", OriginalTitle: "Lioness", Type: "Series",
		Path: "/strm-v2/电视剧追更/特别行动：母狮 (2023) [tmdbid=113962]", PremiereDate: "2023-07-23T00:00:00Z",
		ProviderIDs: map[string]string{"Tmdb": "113962"},
	}
}

func TestMediaIngestSeriesMatchRequiresOneStrongIdentityAndAcceptsRelatedReleaseTitle(t *testing.T) {
	videos := []domain.CrossDriveItem{
		{Name: "Special.Ops.Lioness.S01E01.Sacrificial.Soldiers.1080p.mkv"},
		{Name: "Lioness (2023) - S02E01 - Beware the Old Soldier (1080p).mkv"},
	}
	if !mediaIngestSeriesMatches(lionessSeries(), videos) {
		t.Fatal("one exact title/year item did not bind the related release-title items")
	}
	withoutYear := []domain.CrossDriveItem{{Name: "Special.Ops.Lioness.S01E01.1080p.mkv"}}
	if mediaIngestSeriesMatches(lionessSeries(), withoutYear) {
		t.Fatal("weak title suffix without a year or TMDB proof was accepted")
	}
	wrong := lionessSeries()
	wrong.OriginalTitle = "The Lion"
	if mediaIngestSeriesMatches(wrong, videos) {
		t.Fatal("unrelated Series identity was accepted")
	}
}

func TestCanonicalIngestNamePreservesEpisodeReleaseAndSubtitleIdentity(t *testing.T) {
	video := domain.CrossDriveItem{Name: "Special.Ops.Lioness.S01E01.Sacrificial.Soldiers.1080p.REPACK.AMZN.WEB-DL.mkv", SHA1: "0123456789ABCDEF"}
	got, err := canonicalIngestName(lionessSeries(), video)
	if err != nil {
		t.Fatal(err)
	}
	want := "特别行动：母狮 S01E01 - Sacrificial.Soldiers.1080p.REPACK.AMZN.WEB-DL.mkv"
	if got != want {
		t.Fatalf("canonical video name = %q, want %q", got, want)
	}
	subtitle := domain.CrossDriveItem{Name: "Lioness (2023) - S02E01 - Beware the Old Soldier (1080p).CN&EN.ass", SHA1: "0123456789ABCDEF"}
	got, err = canonicalIngestName(lionessSeries(), subtitle)
	if err != nil {
		t.Fatal(err)
	}
	want = "特别行动：母狮 S02E01 - Beware the Old Soldier (1080p).CN&EN.ass"
	if got != want {
		t.Fatalf("canonical subtitle name = %q, want %q", got, want)
	}
}

func TestBuildMediaIngestPlanLeavesUnsupportedFilesInStaging(t *testing.T) {
	items := []domain.CrossDriveItem{
		{State: "verified", DestinationID: "video", Name: "Lioness (2023) - S02E01 - Episode.mkv"},
		{State: "verified", DestinationID: "subtitle", Name: "Lioness (2023) - S02E01 - Episode.ass"},
		{State: "verified", DestinationID: "poster", Name: "频道：看更多.jpg"},
	}
	movable, findings := buildMediaIngestPlan(lionessSeries(), items)
	if len(movable) != 2 || len(findings) != 1 {
		t.Fatalf("movable=%+v findings=%+v", movable, findings)
	}
}

func TestEnqueueMediaIngestIsIdempotentForOneSourceTask(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := NewTaskQueueService(db, NewDriveService(db, "", ""), NewEmbyService(db))
	first, err := queue.enqueueMediaIngest("source-task")
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.enqueueMediaIngest("source-task")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("automatic continuation duplicated: %s != %s", first.ID, second.ID)
	}
	pending, err := db.ListPendingAsyncTasks(10)
	if err != nil || len(pending) != 1 || pending[0].Type != "media_ingest" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

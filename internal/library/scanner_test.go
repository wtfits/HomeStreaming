package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/images"
	"github.com/wtfits/homestreaming/internal/tasks"
	"github.com/wtfits/homestreaming/internal/testutil"
)

func setupScan(t *testing.T) (*Scanner, *db.DB, *db.Library, string) {
	t.Helper()
	if !testutil.HaveFFmpeg() {
		t.Skip("ffmpeg not installed")
	}
	src, err := testutil.CachedTree()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Movies-and-TV")
	if err := testutil.CopyTree(src, root); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	d, err := db.Open(filepath.Join(dataDir, "hs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ff, _ := exec.LookPath("ffmpeg")
	fp, _ := exec.LookPath("ffprobe")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScanner(d, tasks.New(), &images.Store{DataDir: dataDir}, log, func() (string, string) { return ff, fp })
	lib, err := d.CreateLibrary(context.Background(), "Torrent", "mixed", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	return s, d, lib, root
}

func waitScan(t *testing.T, s *Scanner, id string) tasks.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		task, _ := s.Tasks.Get(id)
		if task.Status == "done" || task.Status == "failed" {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("scan timeout")
	return tasks.Task{}
}

func TestScanTree(t *testing.T) {
	s, d, lib, _ := setupScan(t)
	ctx := context.Background()
	task, err := s.Scan(ctx, lib.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(ctx, lib.ID, false); err != ErrScanRunning {
		t.Fatalf("concurrent scan should be rejected, got %v", err)
	}
	task = waitScan(t, s, task.ID)
	if task.Status != "done" {
		t.Fatalf("scan failed: %v", *task.Error)
	}
	r := task.Report
	if r.Added != 12 || r.IgnoredIncomplete != 2 || r.Unsupported != 1 || r.IgnoredSample < 1 || r.Pending != 0 || r.Missing != 0 {
		t.Fatalf("report: %+v", r)
	}
	counts, _ := d.LibraryCounts(ctx, lib.ID)
	if counts.Movies != 4 || counts.Series != 5 || counts.Episodes != 8 || counts.Pending != 0 {
		t.Fatalf("counts: %+v", counts)
	}

	series, _, err := d.ListItems(ctx, db.ItemQuery{LibraryID: lib.ID, Types: []string{"series"}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, it := range series {
		titles = append(titles, it.Title)
		if it.Title == "Arrested Development" && (it.ChildCount == nil || *it.ChildCount != 3) {
			t.Errorf("Arrested Development episodes: %v", it.ChildCount)
		}
		if it.Title == "Over the Garden Wall" && it.Year != 2014 {
			t.Errorf("OTGW year %d", it.Year)
		}
	}
	sort.Strings(titles)
	want := []string{"Arrested Development", "Better Call Saul", "Over the Garden Wall", "Pluribus", "Widow's Bay"}
	if len(titles) != len(want) {
		t.Fatalf("series: %v", titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("series: %v", titles)
		}
	}

	// Probe data: HDR, audio languages (incl. title heuristic), subtitles, images.
	movies, _, _ := d.ListItems(ctx, db.ItemQuery{LibraryID: lib.ID, Types: []string{"movie"}, Limit: 50})
	for _, m := range movies {
		switch m.Title {
		case "Another Film":
			if m.HDR != "hdr10" || m.VideoCodec != "hevc" || m.BitDepth != 10 {
				t.Errorf("hdr movie: %s %s %d", m.HDR, m.VideoCodec, m.BitDepth)
			}
		case "Third Movie":
			if m.AudioLangs != "rus,eng" || m.Year != 2020 {
				t.Errorf("third movie: %q %d", m.AudioLangs, m.Year)
			}
			img, err := d.GetImage(ctx, m.ID, "poster")
			if err != nil || img.Source != "local" {
				t.Errorf("third movie poster: %v", err)
			}
		case "Movie Name":
			if m.Container != "mp4" || m.RuntimeMs < 89000 {
				t.Errorf("movie name: %s %d", m.Container, m.RuntimeMs)
			}
			img, err := d.GetImage(ctx, m.ID, "thumb")
			if err != nil || img.Source != "extracted" {
				t.Errorf("movie thumb: %v", err)
			}
			if _, err := d.GetImage(ctx, m.ID, "poster"); err == nil {
				t.Errorf("random banner.jpg must not become a poster")
			}
		}
	}
	eps, _, _ := d.ListItems(ctx, db.ItemQuery{LibraryID: lib.ID, Types: []string{"episode"}, Search: "Saul", Limit: 50})
	if len(eps) != 1 {
		t.Fatalf("search BCS episodes: %d", len(eps))
	}
	streams, _ := d.GetStreams(ctx, eps[0].ID)
	var audioLangs []string
	textSubs := 0
	for _, st := range streams {
		if st.Type == "audio" {
			audioLangs = append(audioLangs, st.Language)
		}
		if st.Type == "subtitle" && st.IsText {
			textSubs++
		}
	}
	if len(audioLangs) != 3 || audioLangs[0] != "rus" || audioLangs[1] != "rus" || audioLangs[2] != "eng" || textSubs != 1 {
		t.Fatalf("BCS streams: %v subs=%d", audioLangs, textSubs)
	}
	plu, _, _ := d.ListItems(ctx, db.ItemQuery{LibraryID: lib.ID, Types: []string{"episode"}, Search: "Pirate", Limit: 5})
	if len(plu) != 1 {
		t.Fatal("pluribus episode not found")
	}
	subs, _ := d.ListExternalSubtitles(ctx, plu[0].ID)
	if len(subs) != 1 || subs[0].Language != "eng" {
		t.Fatalf("pluribus Subs/English.srt: %+v", subs)
	}

	// Rescan (quick) changes nothing.
	task, _ = s.Scan(ctx, lib.ID, false)
	task = waitScan(t, s, task.ID)
	if task.Report.Added != 0 || task.Report.Updated != 0 || task.Report.Missing != 0 {
		t.Fatalf("rescan report: %+v", task.Report)
	}
}

func TestScanMissingAndUnavailableRoot(t *testing.T) {
	s, d, lib, root := setupScan(t)
	ctx := context.Background()
	task, _ := s.Scan(ctx, lib.ID, false)
	waitScan(t, s, task.ID)

	if err := os.Remove(filepath.Join(root, "Fourth.Movie.2017.720p.WEBRip.VP9.webm")); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Scan(ctx, lib.ID, false)
	task = waitScan(t, s, task.ID)
	if task.Report.Missing != 1 {
		t.Fatalf("missing: %+v", task.Report)
	}
	c, _ := d.LibraryCounts(ctx, lib.ID)
	if c.Movies != 3 || c.Missing != 1 {
		t.Fatalf("counts after delete: %+v", c)
	}
	n, err := d.DeleteMissing(ctx, lib.ID)
	if err != nil || n != 1 {
		t.Fatalf("clean: %d %v", n, err)
	}

	// Unavailable root aborts without marking anything missing.
	moved := root + ".away"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Scan(ctx, lib.ID, false)
	task = waitScan(t, s, task.ID)
	if task.Status != "failed" {
		t.Fatalf("expected failed scan, got %s", task.Status)
	}
	c, _ = d.LibraryCounts(ctx, lib.ID)
	if c.Missing != 0 || c.Movies != 3 {
		t.Fatalf("unavailable root must not mark missing: %+v", c)
	}
}

func TestWatcher(t *testing.T) {
	s, d, lib, root := setupScan(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task, _ := s.Scan(ctx, lib.ID, false)
	waitScan(t, s, task.ID)

	w := &Watcher{Scanner: s, DB: d, Log: s.Log, Settle: 300 * time.Millisecond}
	lib, _ = d.GetLibrary(ctx, lib.ID)
	w.Watch(ctx, lib)
	defer w.Close()

	src := filepath.Join(root, "Widow's Bay (Season 1) WEB-DL 2160p", "Widow's.Bay.S01E03.WEB-DL.2160p.RGzsRutracker.mkv")
	data, _ := os.ReadFile(src)

	// A new season folder with an in-progress download, then the final rename.
	dir := filepath.Join(root, "New.Show.S01.1080p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	part := filepath.Join(dir, "New.Show.S01E01.1080p.mkv.!qB")
	if err := os.WriteFile(part, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate so the settle rule is satisfied quickly.
	old := time.Now().Add(-time.Hour)
	os.Chtimes(part, old, old)
	time.Sleep(800 * time.Millisecond)
	if items, _, _ := d.ListItems(ctx, db.ItemQuery{Search: "New Show", Types: []string{"series"}, Status: "all"}); len(items) != 0 {
		t.Fatal(".!qB file must be ignored")
	}
	final := filepath.Join(dir, "New.Show.S01E01.1080p.mkv")
	if err := os.Rename(part, final); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(final, old, old)

	deadline := time.Now().Add(20 * time.Second)
	var ok bool
	for time.Now().Before(deadline) {
		items, _, _ := d.ListItems(ctx, db.ItemQuery{Search: "New Show", Types: []string{"episode"}})
		if len(items) == 1 && items[0].Status == "ok" {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatal("watcher did not add the renamed file")
	}

	// Delete → missing.
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	ok = false
	for time.Now().Before(deadline) {
		items, _, _ := d.ListItems(ctx, db.ItemQuery{Search: "New Show", Types: []string{"episode"}, Status: "missing"})
		if len(items) == 1 {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatal("deleted file not marked missing")
	}
	s.Wait()
}

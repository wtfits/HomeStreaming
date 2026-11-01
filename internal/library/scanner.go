package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
	"github.com/wtfits/homestreaming/internal/images"
	"github.com/wtfits/homestreaming/internal/tasks"
)

// MaxProbeAttempts before an item is only listed under admin "Problems".
const MaxProbeAttempts = 10

// ErrScanRunning is returned when a scan of the library is already running.
var ErrScanRunning = errors.New("scan already running")

// Tools returns the current ffmpeg and ffprobe paths ("" when missing).
type Tools func() (ffmpegPath, ffprobePath string)

// Scanner scans libraries and processes single-file changes from the watcher.
type Scanner struct {
	DB     *db.DB
	Tasks  *tasks.Manager
	Images *images.Store
	Log    *slog.Logger
	Tools  Tools

	mu       sync.Mutex
	running  map[string]*tasks.Handle
	libLocks map[string]*sync.Mutex
	probeSem chan struct{}
	imageSem chan struct{}
	inflight map[string]bool // item ids being probed/imaged
	wg       sync.WaitGroup
}

// NewScanner creates a scanner (2 probe workers, 1 image worker).
func NewScanner(d *db.DB, t *tasks.Manager, img *images.Store, log *slog.Logger, tools Tools) *Scanner {
	return &Scanner{DB: d, Tasks: t, Images: img, Log: log, Tools: tools,
		running: map[string]*tasks.Handle{}, libLocks: map[string]*sync.Mutex{},
		probeSem: make(chan struct{}, 2), imageSem: make(chan struct{}, 1), inflight: map[string]bool{}}
}

func (s *Scanner) libLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.libLocks[id]
	if m == nil {
		m = &sync.Mutex{}
		s.libLocks[id] = m
	}
	return m
}

// RunningTask returns the running scan task of a library, if any.
func (s *Scanner) RunningTask(libraryID string) (tasks.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.running[libraryID]; h != nil {
		return h.Snapshot(), true
	}
	return tasks.Task{}, false
}

// Wait blocks until background scans finish (tests, shutdown).
func (s *Scanner) Wait() { s.wg.Wait() }

// Scan starts a scan in the background. full re-probes every file.
func (s *Scanner) Scan(ctx context.Context, libraryID string, full bool) (tasks.Task, error) {
	s.mu.Lock()
	if h := s.running[libraryID]; h != nil {
		s.mu.Unlock()
		return h.Snapshot(), ErrScanRunning
	}
	h := s.Tasks.Start("scan", libraryID)
	s.running[libraryID] = h
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.runScan(ctx, libraryID, full, h)
		s.mu.Lock()
		delete(s.running, libraryID)
		s.mu.Unlock()
		h.Finish(err)
		snap := h.Snapshot()
		if err != nil {
			s.Log.Error("scan failed", "library", libraryID, "err", err)
		} else if r := snap.Report; r != nil {
			s.Log.Info("scan finished", "library", libraryID, "added", r.Added, "updated", r.Updated, "missing", r.Missing,
				"pending", r.Pending, "ignoredSample", r.IgnoredSample, "ignoredIncomplete", r.IgnoredIncomplete, "unsupported", r.Unsupported)
		}
	}()
	return h.Snapshot(), nil
}

type candidate struct {
	lp     db.LibraryPath
	rel    string // slash path relative to root
	abs    string
	name   string
	size   int64
	mtime  time.Time
	dirRel string
}

type upsertCache struct {
	series  map[string]*db.Item // series key → series
	seasons map[string]*db.Item // seriesID|season → season
}

func (s *Scanner) runScan(ctx context.Context, libraryID string, full bool, h *tasks.Handle) error {
	lib, err := s.DB.GetLibrary(ctx, libraryID)
	if err != nil {
		return err
	}
	for _, p := range lib.Paths {
		st, err := os.Stat(p.Path)
		if err != nil || !st.IsDir() {
			msg := fmt.Sprintf("library root unavailable: %s", p.Path)
			_ = s.DB.SetLibraryScanResult(ctx, lib.ID, "failed", msg)
			return errors.New(msg)
		}
	}

	lock := s.libLock(lib.ID)
	lock.Lock()
	h.Phase("walk", 0)
	byDir := map[string][]candidate{}
	var dirKeys []string
	for _, lp := range lib.Paths {
		root := lp.Path
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			name := d.Name()
			if d.IsDir() {
				if p == root {
					return nil
				}
				if IsDiscDir(name) || hasDiscChild(p) {
					h.Report(func(r *tasks.ScanReport) { r.Unsupported++ })
					s.Log.Info("skipping disc folder (DVD/Blu-ray playback is not supported yet)", "path", p)
					return fs.SkipDir
				}
				if SkipDir(name) {
					if reExtrasDir.MatchString(name) && strings.HasPrefix(strings.ToLower(name), "sample") {
						h.Report(func(r *tasks.ScanReport) { r.IgnoredSample++ })
					}
					return fs.SkipDir
				}
				return nil
			}
			switch ClassifyFile(name) {
			case FileIncomplete:
				h.Report(func(r *tasks.ScanReport) { r.IgnoredIncomplete++ })
				return nil
			case FileSample:
				h.Report(func(r *tasks.ScanReport) { r.IgnoredSample++ })
				return nil
			case FileIgnored:
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			c := candidate{lp: lp, rel: rel, abs: p, name: name, size: info.Size(), mtime: info.ModTime(), dirRel: path.Dir(rel)}
			key := lp.ID + "|" + c.dirRel
			if _, ok := byDir[key]; !ok {
				dirKeys = append(dirKeys, key)
			}
			byDir[key] = append(byDir[key], c)
			return nil
		})
		if err != nil {
			lock.Unlock()
			_ = s.DB.SetLibraryScanResult(ctx, lib.ID, "failed", err.Error())
			return err
		}
	}

	// Size-ratio sample rule, then flatten.
	var all []candidate
	siblings := map[string][]string{}
	sort.Strings(dirKeys)
	for _, key := range dirKeys {
		group := applySizeRule(byDir[key], func() { h.Report(func(r *tasks.ScanReport) { r.IgnoredSample++ }) })
		var names []string
		for _, c := range group {
			names = append(names, c.name)
		}
		siblings[key] = names
		all = append(all, group...)
	}

	h.Phase("upsert", len(all))
	cache := &upsertCache{series: map[string]*db.Item{}, seasons: map[string]*db.Item{}}
	seen := map[string]map[string]bool{}
	for _, c := range all {
		if ctx.Err() != nil {
			lock.Unlock()
			return ctx.Err()
		}
		if seen[c.lp.ID] == nil {
			seen[c.lp.ID] = map[string]bool{}
		}
		seen[c.lp.ID][c.rel] = true
		added, updated, err := s.upsertFile(ctx, lib, c, siblings[c.lp.ID+"|"+c.dirRel], full, cache)
		if err != nil {
			s.Log.Warn("upsert failed", "path", c.abs, "err", err)
		}
		h.Report(func(r *tasks.ScanReport) {
			if added {
				r.Added++
			} else if updated {
				r.Updated++
			}
		})
		h.Step()
	}
	for _, lp := range lib.Paths {
		n, err := s.DB.MarkMissingExcept(ctx, lp.ID, seen[lp.ID])
		if err != nil {
			lock.Unlock()
			return err
		}
		h.Report(func(r *tasks.ScanReport) { r.Missing += n })
	}
	_ = s.DB.DeleteEmptyVirtualItems(ctx, lib.ID)
	lock.Unlock()

	s.processLibraryQueue(ctx, lib, h)

	pending, _ := s.DB.ItemsNeedingProbe(ctx, lib.ID, 1<<30)
	h.Report(func(r *tasks.ScanReport) { r.Pending = len(pending) })
	return s.DB.SetLibraryScanResult(ctx, lib.ID, "ok", "")
}

func hasDiscChild(dir string) bool {
	for _, n := range []string{"VIDEO_TS", "BDMV", "video_ts"} {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

const sampleRatioMin = 200 << 20

func applySizeRule(group []candidate, onSkip func()) []candidate {
	var largest int64
	for _, c := range group {
		if c.size > largest {
			largest = c.size
		}
	}
	if largest < sampleRatioMin {
		return group
	}
	var out []candidate
	for _, c := range group {
		if c.size < largest/10 {
			onSkip()
			continue
		}
		out = append(out, c)
	}
	return out
}

func upperCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsUpper(r) {
			n++
		}
	}
	return n
}

func (s *Scanner) ensureSeries(ctx context.Context, lib *db.Library, p Parsed, cache *upsertCache) (*db.Item, *db.Item, error) {
	series := cache.series[p.SeriesKey]
	if series == nil {
		var err error
		series, err = s.DB.FindSeries(ctx, lib.ID, p.SeriesKey)
		if errors.Is(err, db.ErrNotFound) {
			series = &db.Item{Type: "series", LibraryID: lib.ID, SeriesKey: p.SeriesKey, Title: p.Title, SortTitle: SortTitle(p.Title), Year: p.Year}
			if err := s.DB.InsertItem(ctx, series); err != nil {
				return nil, nil, err
			}
		} else if err != nil {
			return nil, nil, err
		}
		cache.series[p.SeriesKey] = series
	}
	if (upperCount(p.Title) > upperCount(series.Title) && p.Title != series.Title) || (series.Year == 0 && p.Year != 0) {
		title := series.Title
		if upperCount(p.Title) > upperCount(series.Title) {
			title = p.Title
		}
		if err := s.DB.UpdateSeriesTitle(ctx, series.ID, title, SortTitle(title), p.Year); err != nil {
			return nil, nil, err
		}
		series.Title, series.SortTitle = title, SortTitle(title)
		if series.Year == 0 {
			series.Year = p.Year
		}
	}
	key := fmt.Sprintf("%s|%d", series.ID, p.Season)
	season := cache.seasons[key]
	if season == nil {
		var err error
		season, err = s.DB.FindSeason(ctx, series.ID, p.Season)
		if errors.Is(err, db.ErrNotFound) {
			sn := p.Season
			title := fmt.Sprintf("Season %d", sn)
			if sn == 0 {
				title = "Specials"
			}
			season = &db.Item{Type: "season", LibraryID: lib.ID, ParentID: series.ID, SeriesID: series.ID, Title: title,
				SortTitle: fmt.Sprintf("season %03d", sn), SeasonNumber: &sn}
			if err := s.DB.InsertItem(ctx, season); err != nil {
				return nil, nil, err
			}
		} else if err != nil {
			return nil, nil, err
		}
		cache.seasons[key] = season
	}
	return series, season, nil
}

func intPtr(v int) *int { return &v }

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// upsertFile creates or updates the item for one candidate file.
func (s *Scanner) upsertFile(ctx context.Context, lib *db.Library, c candidate, siblings []string, full bool, cache *upsertCache) (added, updated bool, err error) {
	p := Parse(ParseInput{RelPath: c.rel, LibraryType: lib.Type, Siblings: siblings})
	target := &db.Item{LibraryID: lib.ID, LibraryPathID: c.lp.ID, RelPath: c.rel, Size: c.size, Mtime: db.FormatTime(c.mtime), Confidence: p.Confidence}
	if p.Kind == "episode" {
		series, season, err := s.ensureSeries(ctx, lib, p, cache)
		if err != nil {
			return false, false, err
		}
		target.Type = "episode"
		target.ParentID, target.SeriesID = season.ID, series.ID
		target.SeasonNumber, target.EpisodeNumber = intPtr(p.Season), intPtr(p.Episode)
		if p.EpisodeEnd > 0 {
			target.EpisodeEndNumber = intPtr(p.EpisodeEnd)
		}
		target.Title = p.EpisodeTitle
		if target.Title == "" {
			target.Title = fmt.Sprintf("Episode %d", p.Episode)
			if p.EpisodeEnd > 0 {
				target.Title = fmt.Sprintf("Episodes %d–%d", p.Episode, p.EpisodeEnd)
			}
		}
		target.SortTitle = fmt.Sprintf("%s s%03de%04d", series.SortTitle, p.Season, p.Episode)
		target.Year = p.Year
	} else {
		target.Type = "movie"
		target.Title, target.SortTitle, target.Year = p.Title, SortTitle(p.Title), p.Year
	}

	existing, err := s.DB.GetFileItem(ctx, c.lp.ID, c.rel)
	if errors.Is(err, db.ErrNotFound) {
		target.Status = "pending"
		if err := s.DB.InsertItem(ctx, target); err != nil {
			return false, false, err
		}
		s.syncSubtitles(ctx, target, c, len(siblings))
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	target.ID = existing.ID
	if existing.Type != target.Type || existing.Title != target.Title || existing.ParentID != target.ParentID || existing.SeriesID != target.SeriesID ||
		existing.Year != target.Year || !eqIntPtr(existing.SeasonNumber, target.SeasonNumber) || !eqIntPtr(existing.EpisodeNumber, target.EpisodeNumber) ||
		!eqIntPtr(existing.EpisodeEndNumber, target.EpisodeEndNumber) || existing.Confidence != target.Confidence || existing.SortTitle != target.SortTitle {
		if err := s.DB.UpdateItemNaming(ctx, target); err != nil {
			return false, false, err
		}
		updated = true
	}
	if existing.Status == "missing" {
		if err := s.DB.RestoreMissing(ctx, existing.ID); err != nil {
			return false, updated, err
		}
		updated = true
	}
	if full || existing.Size != c.size || db.ParseTime(existing.Mtime).Unix() != c.mtime.Unix() {
		if err := s.DB.MarkItemChanged(ctx, existing.ID, c.size, db.FormatTime(c.mtime)); err != nil {
			return false, updated, err
		}
		updated = true
	}
	s.syncSubtitles(ctx, target, c, len(siblings))
	return false, updated, nil
}

func (s *Scanner) syncSubtitles(ctx context.Context, it *db.Item, c candidate, videosInDir int) {
	dirRel := c.dirRel
	if dirRel == "." {
		dirRel = ""
	}
	subs := FindExternalSubtitles(filepath.Dir(c.abs), dirRel, stripExt(c.name), videosInDir)
	if err := s.DB.ReplaceExternalSubtitles(ctx, it.ID, subs); err != nil {
		s.Log.Warn("external subtitles", "item", it.ID, "err", err)
	}
}

// processLibraryQueue probes pending items and extracts missing artwork.
func (s *Scanner) processLibraryQueue(ctx context.Context, lib *db.Library, h *tasks.Handle) {
	paths := map[string]string{}
	for _, p := range lib.Paths {
		paths[p.ID] = p.Path
	}
	_, ffprobe := s.Tools()
	pending, err := s.DB.ItemsNeedingProbe(ctx, lib.ID, MaxProbeAttempts)
	if err != nil {
		s.Log.Error("list pending", "err", err)
		return
	}
	if h != nil {
		h.Phase("probe", len(pending))
	}
	if ffprobe == "" {
		for _, it := range pending {
			_ = s.DB.SetItemStatus(ctx, it.ID, "pending", "ffmpeg_missing")
		}
	} else {
		var wg sync.WaitGroup
		for _, it := range pending {
			it := it
			if !s.claim("p:" + it.ID) {
				continue
			}
			wg.Add(1)
			s.probeSem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-s.probeSem; s.release("p:" + it.ID) }()
				s.probeItem(ctx, it, paths[it.LibraryPathID], ffprobe)
				if h != nil {
					h.Step()
				}
			}()
		}
		wg.Wait()
	}

	ffmpegPath, _ := s.Tools()
	needImages, err := s.DB.ItemsNeedingImages(ctx, lib.ID)
	if err != nil {
		return
	}
	if h != nil {
		h.Phase("images", len(needImages))
	}
	for _, it := range needImages {
		if ctx.Err() != nil {
			return
		}
		if !s.claim("i:" + it.ID) {
			continue
		}
		s.imageSem <- struct{}{}
		s.extractImages(ctx, it, paths[it.LibraryPathID], ffmpegPath)
		<-s.imageSem
		s.release("i:" + it.ID)
		if h != nil {
			h.Step()
		}
	}
}

func (s *Scanner) claim(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[key] {
		return false
	}
	s.inflight[key] = true
	return true
}

func (s *Scanner) release(key string) {
	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
}

func (s *Scanner) probeItem(ctx context.Context, it *db.Item, root, ffprobe string) {
	abs := filepath.Join(root, filepath.FromSlash(it.RelPath))
	out, raw, err := ffmpeg.Probe(ctx, ffprobe, abs)
	if err != nil {
		_ = s.DB.RecordProbeFailure(ctx, it.ID, err.Error())
		s.Log.Warn("probe failed", "path", abs, "err", err)
		return
	}
	res, err := ConvertProbe(out, raw, abs, it.Size)
	if err != nil {
		_ = s.DB.RecordProbeFailure(ctx, it.ID, err.Error())
		s.Log.Warn("probe: unplayable", "path", abs, "err", err)
		return
	}
	if err := s.DB.SaveProbe(ctx, it.ID, res); err != nil {
		s.Log.Error("save probe", "path", abs, "err", err)
	}
}

var (
	imgExts = []string{".jpg", ".jpeg", ".png", ".webp"}
)

// findLocalArt returns the first existing image among names in dir.
func findLocalArt(entries map[string]string, names []string) string {
	for _, n := range names {
		for _, e := range imgExts {
			if p, ok := entries[strings.ToLower(n+e)]; ok {
				return p
			}
		}
	}
	return ""
}

func dirEntries(dir string) map[string]string {
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() {
			out[strings.ToLower(e.Name())] = filepath.Join(dir, e.Name())
		}
	}
	return out
}

func (s *Scanner) extractImages(ctx context.Context, it *db.Item, root, ffmpegPath string) {
	abs := filepath.Join(root, filepath.FromSlash(it.RelPath))
	dir := filepath.Dir(abs)
	base := stripExt(filepath.Base(abs))
	entries := dirEntries(dir)
	videos := 0
	for name := range entries {
		if ClassifyFile(name) == FileCandidate {
			videos++
		}
	}
	setLocal := func(itemID, kind, p string) {
		w, hh := images.Dimensions(p)
		_ = s.DB.SetImage(ctx, db.Image{ItemID: itemID, Kind: kind, Source: "local", Path: p, Width: w, Height: hh})
	}
	have := map[string]bool{}
	if it.Type == "movie" {
		poster := []string{base + "-poster"}
		backdrop := []string{base + "-fanart"}
		thumb := []string{base + "-thumb", base + "-landscape"}
		if videos == 1 {
			poster = append(poster, "poster", "folder", "cover", "movie")
			backdrop = append(backdrop, "fanart", "backdrop", "background")
		}
		for kind, names := range map[string][]string{"poster": poster, "backdrop": backdrop, "thumb": thumb} {
			if p := findLocalArt(entries, names); p != "" {
				setLocal(it.ID, kind, p)
				have[kind] = true
			}
		}
	} else {
		if p := findLocalArt(entries, []string{base + "-thumb", base}); p != "" {
			setLocal(it.ID, "thumb", p)
			have["thumb"] = true
		}
		if it.SeriesID != "" {
			seriesDir := dir
			if reSeasonDir.MatchString(filepath.Base(dir)) {
				seriesDir = filepath.Dir(dir)
				entries = dirEntries(seriesDir)
			}
			if _, err := s.DB.GetImage(ctx, it.SeriesID, "poster"); errors.Is(err, db.ErrNotFound) {
				if p := findLocalArt(entries, []string{"poster", "folder", "cover"}); p != "" {
					setLocal(it.SeriesID, "poster", p)
				}
			}
			if _, err := s.DB.GetImage(ctx, it.SeriesID, "backdrop"); errors.Is(err, db.ErrNotFound) {
				if p := findLocalArt(entries, []string{"fanart", "backdrop"}); p != "" {
					setLocal(it.SeriesID, "backdrop", p)
				}
			}
		}
	}
	if ffmpegPath == "" {
		return // retried on the next scan once ffmpeg is available
	}
	runtime := float64(it.RuntimeMs) / 1000
	extract := func(kind string, frac float64, width int) {
		t := runtime * frac
		if t < 2 {
			t = 2
		}
		if runtime > 0 && t > runtime-1 {
			t = runtime / 2
		}
		out := s.Images.ExtractedPath(it.ID, kind)
		_ = os.MkdirAll(filepath.Dir(out), 0o755)
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := ffmpeg.Command(cctx, ffmpegPath, ffmpeg.BuildThumbnail(abs, t, width, it.HDR != "", out)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			s.Log.Warn("thumbnail extraction failed", "path", abs, "kind", kind, "err", err, "out", strings.TrimSpace(string(b)))
			return
		}
		w, hh := images.Dimensions(out)
		_ = s.DB.SetImage(ctx, db.Image{ItemID: it.ID, Kind: kind, Source: "extracted", Path: s.Images.Rel(out), Width: w, Height: hh})
	}
	if !have["thumb"] {
		extract("thumb", 0.10, 640)
	}
	if !have["backdrop"] {
		extract("backdrop", 0.25, 1280)
	}
	_ = s.DB.SetImagesDone(ctx, it.ID, true)
}

// ProcessPath handles a settled watcher event for one absolute path under a
// library root: new/changed files are upserted and probed, vanished files
// (or directories) are marked missing.
func (s *Scanner) ProcessPath(ctx context.Context, libraryID string, lp db.LibraryPath, abs string) error {
	lib, err := s.DB.GetLibrary(ctx, libraryID)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(lp.Path, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %s outside root %s", abs, lp.Path)
	}
	rel = filepath.ToSlash(rel)
	lock := s.libLock(libraryID)

	st, statErr := os.Stat(abs)
	if statErr != nil {
		lock.Lock()
		defer lock.Unlock()
		items, err := s.DB.ListFileItemsUnder(ctx, lp.ID, rel)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.Status != "missing" {
				_ = s.DB.SetItemStatus(ctx, it.ID, "missing", "file not found")
			}
		}
		return nil
	}
	if st.IsDir() {
		return nil // the watcher schedules the files inside separately
	}
	name := filepath.Base(abs)
	if ClassifyFile(name) != FileCandidate || st.Size() == 0 {
		return nil
	}
	// Ignore files inside skipped directories.
	for _, part := range strings.Split(path.Dir(rel), "/") {
		if part != "." && (SkipDir(part) || IsDiscDir(part)) {
			return nil
		}
	}
	dirAbs := filepath.Dir(abs)
	var group []candidate
	ents, _ := os.ReadDir(dirAbs)
	for _, e := range ents {
		if e.IsDir() || ClassifyFile(e.Name()) != FileCandidate {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		r := path.Join(path.Dir(rel), e.Name())
		group = append(group, candidate{lp: lp, rel: r, abs: filepath.Join(dirAbs, e.Name()), name: e.Name(), size: info.Size(), mtime: info.ModTime(), dirRel: path.Dir(rel)})
	}
	group = applySizeRule(group, func() {})
	var names []string
	var self *candidate
	for i := range group {
		names = append(names, group[i].name)
		if group[i].name == name {
			self = &group[i]
		}
	}
	if self == nil {
		return nil // filtered by the size rule
	}
	lock.Lock()
	cache := &upsertCache{series: map[string]*db.Item{}, seasons: map[string]*db.Item{}}
	_, _, err = s.upsertFile(ctx, lib, *self, names, false, cache)
	lock.Unlock()
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.processLibraryQueue(ctx, lib, nil)
	}()
	return nil
}

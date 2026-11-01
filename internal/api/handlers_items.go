package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/stream"
)

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	iq := db.ItemQuery{UserID: ident(r).User.ID, LibraryID: q.Get("libraryId"), Types: splitList(q.Get("type")), ParentID: q.Get("parentId"),
		SeriesID: q.Get("seriesId"), Search: strings.TrimSpace(q.Get("search")), Sort: q.Get("sort"), Order: q.Get("order"), Status: q.Get("status"),
		Offset: queryInt(r, "offset", 0), Limit: queryInt(r, "limit", 50)}
	if iq.Limit > 200 {
		iq.Limit = 200
	}
	if iq.Limit < 1 {
		iq.Limit = 50
	}
	if iq.Offset < 0 {
		iq.Offset = 0
	}
	if iq.Search != "" && len(iq.Types) == 0 {
		iq.Types = []string{"movie", "series"}
	}
	if v := q.Get("played"); v != "" {
		b := v == "true"
		iq.Played = &b
	}
	items, total, err := s.DB.ListItems(r.Context(), iq)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.summaries(r.Context(), items), "total": total, "offset": iq.Offset, "limit": iq.Limit})
}

func (s *Server) itemRoot(ctx context.Context, it *db.Item) (string, error) {
	lp, err := s.DB.GetLibraryPath(ctx, it.LibraryPathID)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(lp.Path, filepath.FromSlash(it.RelPath))
	if rel, err := filepath.Rel(lp.Path, abs); err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path escapes library root")
	}
	return abs, nil
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := ident(r)
	sum, err := s.summaryByID(ctx, id.User.ID, chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	it, err := s.DB.GetItem(ctx, sum.ID)
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	out := map[string]any{}
	// Embed summary fields.
	b := mustMap(sum)
	for k, v := range b {
		out[k] = v
	}
	out["statusReason"] = strPtr(it.StatusReason)
	if it.IsFile() {
		streams, _ := s.DB.GetStreams(ctx, it.ID)
		ext, _ := s.DB.ListExternalSubtitles(ctx, it.ID)
		chapters, _ := s.DB.GetChapters(ctx, it.ID)
		ms := []mediaStreamJSON{}
		for _, m := range streams {
			ms = append(ms, toMediaStream(m))
		}
		subs := subtitleTracks(streams, ext)
		chOut := []map[string]any{}
		for _, c := range chapters {
			chOut = append(chOut, map[string]any{"index": c.Index, "startMs": c.StartMs, "endMs": c.EndMs, "title": c.Title})
		}
		out["mediaStreams"] = ms
		out["audioTracks"] = audioTracks(streams)
		out["subtitleTracks"] = toSubtitleJSON(subs)
		out["chapters"] = chOut
		out["container"] = it.Container
		out["size"] = it.Size
		out["bitrate"] = it.Bitrate / 1000
		out["mtime"] = it.Mtime
		if da := defaultAudio(streams, id.User.Preferences.AudioLanguage); da >= 0 {
			out["defaultAudioTrackId"] = da
		} else {
			out["defaultAudioTrackId"] = nil
		}
		if ds := defaultSubtitle(subs, id.User.Preferences); ds != "" {
			out["defaultSubtitleTrackId"] = ds
		} else {
			out["defaultSubtitleTrackId"] = nil
		}
		if id.User.IsAdmin {
			if p, err := s.itemRoot(ctx, it); err == nil {
				out["path"] = p
			}
		}
	}
	if it.Type == "series" || it.Type == "season" {
		seriesID := it.ID
		if it.Type == "season" {
			seriesID = it.SeriesID
		}
		nu, _ := s.nextUpFor(ctx, id.User.ID, seriesID, true)
		out["nextUp"] = nu
	}
	writeJSON(w, http.StatusOK, out)
}

// nextUpFor returns the episode to play next for a series. With allowStart,
// a series without history returns its first episode; an in-progress episode
// is returned as-is.
func (s *Server) nextUpFor(ctx context.Context, userID, seriesID string, allowStart bool) (*itemSummary, error) {
	eps, err := s.DB.SeriesEpisodes(ctx, userID, seriesID)
	if err != nil {
		return nil, err
	}
	lastPlayed := -1
	for i, e := range eps {
		if !e.Missing && e.PositionMs > 0 && !e.Played {
			if allowStart {
				return s.summaryByID(ctx, userID, e.ID)
			}
			return nil, nil // shown in Continue Watching instead
		}
		if e.Played {
			lastPlayed = i
		}
	}
	if lastPlayed < 0 && !allowStart {
		return nil, nil
	}
	for i := lastPlayed + 1; i < len(eps); i++ {
		if !eps[i].Missing && !eps[i].Played {
			return s.summaryByID(ctx, userID, eps[i].ID)
		}
	}
	return nil, nil
}

func mustMap(v any) map[string]any {
	b, _ := jsonMarshal(v)
	var m map[string]any
	_ = jsonUnmarshal(b, &m)
	return m
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.DB.GetItem(ctx, chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	q := db.ItemQuery{UserID: ident(r).User.ID, Limit: 2000}
	switch {
	case it.Type == "series" && r.URL.Query().Get("type") == "episode":
		q.SeriesID, q.Types, q.Sort = it.ID, []string{"episode"}, "episode"
	case it.Type == "series":
		q.ParentID, q.Types, q.Sort = it.ID, []string{"season"}, "episode"
	case it.Type == "season":
		q.ParentID, q.Types, q.Sort = it.ID, []string{"episode"}, "episode"
	default:
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, _, err := s.DB.ListItems(ctx, q)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.summaries(ctx, items)})
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := ident(r).User.ID
	load := func(ids []string) []itemSummary {
		if len(ids) == 0 {
			return []itemSummary{}
		}
		items, _, err := s.DB.ListItems(ctx, db.ItemQuery{UserID: uid, ItemIDs: ids, Limit: len(ids)})
		if err != nil {
			return []itemSummary{}
		}
		byID := map[string]*db.Item{}
		for _, it := range items {
			byID[it.ID] = it
		}
		var ordered []*db.Item
		for _, id := range ids {
			if it := byID[id]; it != nil {
				ordered = append(ordered, it)
			}
		}
		return s.summaries(ctx, ordered)
	}
	cw, _ := s.DB.ContinueWatching(ctx, uid, 20)
	ra, _ := s.DB.RecentlyAdded(ctx, 30)
	series, _ := s.DB.SeriesWithHistory(ctx, uid)
	nextUp := []itemSummary{}
	for _, sid := range series {
		if len(nextUp) >= 20 {
			break
		}
		if nu, err := s.nextUpFor(ctx, uid, sid, false); err == nil && nu != nil {
			nextUp = append(nextUp, *nu)
		}
	}
	libs, _ := s.DB.ListLibraries(ctx)
	libOut := []map[string]any{}
	for _, l := range libs {
		c, _ := s.DB.LibraryCounts(ctx, l.ID)
		var sections []map[string]any
		if c.Movies > 0 {
			sections = append(sections, map[string]any{"type": "movie", "count": c.Movies})
		}
		if c.Series > 0 {
			sections = append(sections, map[string]any{"type": "series", "count": c.Series})
		}
		if sections == nil {
			sections = []map[string]any{}
		}
		libOut = append(libOut, map[string]any{"library": s.toLibrary(ctx, l), "sections": sections})
	}
	writeJSON(w, http.StatusOK, map[string]any{"continueWatching": load(cw), "nextUp": nextUp, "recentlyAdded": load(ra), "libraries": libOut})
}

// --- images ---

func (s *Server) resolveImage(ctx context.Context, it *db.Item, kind string) (*db.Image, error) {
	try := func(id, k string) *db.Image {
		if id == "" {
			return nil
		}
		img, err := s.DB.GetImage(ctx, id, k)
		if err != nil {
			return nil
		}
		return img
	}
	if img := try(it.ID, kind); img != nil {
		return img, nil
	}
	switch it.Type {
	case "season", "episode":
		if kind == "poster" || kind == "backdrop" {
			if img := try(it.SeriesID, kind); img != nil {
				return img, nil
			}
		}
		if it.Type == "season" && kind != "poster" {
			if ep, err := s.DB.FirstEpisode(ctx, it.ID); err == nil {
				if img := try(ep.ID, kind); img != nil {
					return img, nil
				}
			}
		}
	case "series":
		if kind == "thumb" || kind == "backdrop" {
			if ep, err := s.DB.FirstEpisode(ctx, it.ID); err == nil {
				if img := try(ep.ID, kind); img != nil {
					return img, nil
				}
				if img := try(ep.ID, "thumb"); img != nil && kind == "backdrop" {
					return img, nil
				}
			}
		}
	}
	return nil, db.ErrNotFound
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	kind := chiParam(r, "kind")
	if kind != "poster" && kind != "backdrop" && kind != "thumb" {
		notFound(w, "unknown image kind")
		return
	}
	it, err := s.DB.GetItem(r.Context(), chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	img, err := s.resolveImage(r.Context(), it, kind)
	if err != nil {
		notFound(w, "no "+kind+" image")
		return
	}
	wq, hq := queryInt(r, "w", 0), queryInt(r, "h", 0)
	if wq > 3840 {
		wq = 3840
	}
	if hq > 3840 {
		hq = 3840
	}
	src := s.Images.Resolve(img.Path, img.Source)
	data, etag, err := s.Images.Variant(img.ItemID, kind, src, wq, hq)
	if err != nil {
		notFound(w, "image unavailable")
		return
	}
	etag = `"` + etag + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// --- direct file ---

var containerTypes = map[string]string{".mp4": "video/mp4", ".m4v": "video/x-m4v", ".mov": "video/quicktime", ".mkv": "video/x-matroska",
	".webm": "video/webm", ".avi": "video/x-msvideo", ".ts": "video/mp2t", ".m2ts": "video/mp2t", ".mts": "video/mp2t"}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	it, err := s.DB.GetItem(r.Context(), chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	if !it.IsFile() || it.Status == "missing" {
		notFound(w, "file not available")
		return
	}
	abs, err := s.itemRoot(r.Context(), it)
	if err != nil {
		notFound(w, "file not available")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		notFound(w, "file not available")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	ct := containerTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filepath.Base(abs)}))
	h.Set("Cache-Control", "private, max-age=0")
	// No write deadline for long streams.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	http.ServeContent(w, r, "", st.ModTime(), f)
	if sid := r.URL.Query().Get("sid"); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			sess.Touch()
		}
	}
}

// --- subtitles ---

func (s *Server) subtitleSource(ctx context.Context, it *db.Item, trackID string) (stream.SubtitleSource, error) {
	abs, err := s.itemRoot(ctx, it)
	if err != nil {
		return stream.SubtitleSource{}, err
	}
	streams, _ := s.DB.GetStreams(ctx, it.ID)
	ext, _ := s.DB.ListExternalSubtitles(ctx, it.ID)
	st, _ := os.Stat(abs)
	var mod time.Time
	if st != nil {
		mod = st.ModTime()
	}
	for _, t := range subtitleTracks(streams, ext) {
		if t.ID != trackID {
			continue
		}
		if !t.Text {
			return stream.SubtitleSource{}, stream.ErrNotText
		}
		src := stream.SubtitleSource{ItemID: it.ID, TrackID: t.ID, Input: abs, Index: t.Index, Language: t.Language, SourceMod: mod}
		if t.External {
			lp, err := s.DB.GetLibraryPath(ctx, it.LibraryPathID)
			if err != nil {
				return src, err
			}
			src.Input = filepath.Join(lp.Path, filepath.FromSlash(t.RelPath))
			if st, err := os.Stat(src.Input); err == nil {
				src.SourceMod = st.ModTime()
			}
		}
		return src, nil
	}
	return stream.SubtitleSource{}, db.ErrNotFound
}

func (s *Server) writeVTT(w http.ResponseWriter, r *http.Request, it *db.Item, trackID string, offsetMs int64) {
	src, err := s.subtitleSource(r.Context(), it, trackID)
	switch {
	case errors.Is(err, stream.ErrNotText):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "image-based subtitles cannot be converted (burn-in is planned)", nil)
		return
	case err != nil:
		s.dbErr(w, r, err, "subtitle track")
		return
	}
	data, err := s.Subs.VTT(r.Context(), src, offsetMs)
	if err != nil {
		s.Log.Warn("subtitle conversion failed", "item", it.ID, "track", trackID, "err", err)
		writeError(w, http.StatusUnprocessableEntity, "subtitle_failed", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}

func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	track := strings.TrimSuffix(chiParam(r, "track"), ".vtt")
	it, err := s.DB.GetItem(r.Context(), chiParam(r, "id"))
	if err != nil || !it.IsFile() {
		notFound(w, "item not found")
		return
	}
	off, _ := strconv.ParseInt(r.URL.Query().Get("offsetMs"), 10, 64)
	s.writeVTT(w, r, it, track, off)
}

// --- user data ---

func (s *Server) applyProgress(ctx context.Context, userID string, it *db.Item, positionMs int64, ended bool, countPlay func() bool) (*db.UserItemData, bool, error) {
	u, err := s.DB.GetUserData(ctx, userID, it.ID)
	if err != nil {
		return nil, false, err
	}
	thr := s.Settings().PlayedThresholdPercent
	crosses := ended || (it.RuntimeMs > 0 && positionMs*100 >= it.RuntimeMs*int64(thr))
	allow := false
	if crosses && countPlay != nil {
		allow = countPlay()
	}
	played := stream.ApplyProgress(u, positionMs, it.RuntimeMs, thr, ended, allow, time.Now())
	if err := s.DB.SetUserData(ctx, userID, it.ID, u); err != nil {
		return nil, false, err
	}
	return u, played, nil
}

type progressRequest struct {
	PositionMs int64  `json:"positionMs"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"` // client playback error, logged
}

func (s *Server) handleItemProgress(w http.ResponseWriter, r *http.Request) {
	it, err := s.DB.GetItem(r.Context(), chiParam(r, "id"))
	if err != nil || !it.IsFile() {
		notFound(w, "item not found")
		return
	}
	var req progressRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	u, played, err := s.applyProgress(r.Context(), ident(r).User.ID, it, req.PositionMs, req.State == "ended", func() bool { return true })
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"userData": toUserData(u), "played": played})
}

func (s *Server) handlePlayed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := ident(r).User.ID
	it, err := s.DB.GetItem(ctx, chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	var req struct {
		Played bool `json:"played"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	ids := []string{it.ID}
	if it.Type == "series" || it.Type == "season" {
		ids, err = s.DB.EpisodeIDsUnder(ctx, it.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
	}
	for _, id := range ids {
		u, err := s.DB.GetUserData(ctx, uid, id)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if req.Played && !u.Played {
			u.PlayCount++
			u.LastPlayedAt = db.Now()
		}
		u.Played, u.PositionMs = req.Played, 0
		if err := s.DB.SetUserData(ctx, uid, id, u); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	u, _ := s.DB.GetUserData(ctx, uid, it.ID)
	writeJSON(w, http.StatusOK, map[string]any{"userData": toUserData(u), "updated": len(ids)})
}

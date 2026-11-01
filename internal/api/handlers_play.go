package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/media"
	"github.com/wtfits/homestreaming/internal/netutil"
	"github.com/wtfits/homestreaming/internal/stream"
)

type playInfoRequest struct {
	Profile               stream.Profile  `json:"profile"`
	AudioTrackID          *int            `json:"audioTrackId"`
	SubtitleTrackID       json.RawMessage `json:"subtitleTrackId"`
	StartPositionMs       int64           `json:"startPositionMs"`
	MaxBitrateKbps        int             `json:"maxBitrateKbps"`
	ForceTranscode        bool            `json:"forceTranscode"`
	ReplacesPlaySessionID string          `json:"replacesPlaySessionId"`
}

func minCap(caps ...int) int {
	out := 0
	for _, c := range caps {
		if c > 0 && (out == 0 || c < out) {
			out = c
		}
	}
	return out
}

func (s *Server) hwInfo() stream.HWInfo {
	c := s.Tools.Caps()
	return stream.HWInfo{Backend: c.Active, H264Encoder: c.H264Encoder(), HEVCNVENC: c.Active == "nvenc" && c.HasEncoder("hevc_nvenc"), HWDecode: c.HWDecode}
}

func (s *Server) handlePlayInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := ident(r)
	it, err := s.DB.GetItem(ctx, chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	if !it.IsFile() || it.Status == "missing" {
		notFound(w, "item is not playable")
		return
	}
	if s.Tools.FFmpeg() == "" {
		writeError(w, http.StatusServiceUnavailable, "ffmpeg_missing", "ffmpeg was not found on the server; "+"install it with: winget install Gyan.FFmpeg", nil)
		return
	}
	if it.Status == "pending" {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "this file is still being analysed; try again in a moment", nil)
		return
	}
	var req playInfoRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return
	}
	st := s.Settings()
	prof := req.Profile
	prof.Normalize()
	if ov, ok := st.ProfileOverrides[prof.Platform]; ok {
		prof.ApplyOverride(ov)
	}
	prof.ForceTranscode = prof.ForceTranscode || req.ForceTranscode

	abs, err := s.itemRoot(ctx, it)
	if err != nil {
		notFound(w, "file not available")
		return
	}
	if _, err := os.Stat(abs); err != nil {
		notFound(w, "file not available (drive disconnected?)")
		return
	}
	streams, _ := s.DB.GetStreams(ctx, it.ID)
	ext, _ := s.DB.ListExternalSubtitles(ctx, it.ID)
	subs := subtitleTracks(streams, ext)

	// Audio.
	audioIdx := defaultAudio(streams, id.User.Preferences.AudioLanguage)
	if req.AudioTrackID != nil {
		a := stream.StreamByIndex(streams, *req.AudioTrackID)
		if a == nil || a.Type != "audio" {
			validation(w, map[string]string{"audioTrackId": "unknown audio track"})
			return
		}
		audioIdx = a.Index
	}
	// Subtitles: absent → preference default; null → off; id → that track.
	subID := ""
	if len(req.SubtitleTrackID) == 0 {
		subID = defaultSubtitle(subs, id.User.Preferences)
	} else if string(req.SubtitleTrackID) != "null" {
		if err := json.Unmarshal(req.SubtitleTrackID, &subID); err != nil {
			validation(w, map[string]string{"subtitleTrackId": "must be a string or null"})
			return
		}
	}
	var selSub *subtitleTrack
	if subID != "" {
		for i := range subs {
			if subs[i].ID == subID {
				selSub = &subs[i]
			}
		}
		if selSub == nil {
			validation(w, map[string]string{"subtitleTrackId": "unknown subtitle track"})
			return
		}
		if !selSub.Text {
			validation(w, map[string]string{"subtitleTrackId": "image-based subtitles are not supported yet"})
			return
		}
	}

	ip := s.Resolver.ClientIP(r)
	class := netutil.Class(ip, st.TailnetIsLan)
	capKbps := minCap(s.capFor(class), prof.MaxBitrateKbps, req.MaxBitrateKbps)
	dec := stream.Decide(stream.Input{Item: it, Streams: streams, AudioIndex: audioIdx, Profile: prof, MaxBitrateKbps: capKbps,
		HW: s.hwInfo(), AudioCodecPref: st.AudioTranscodeCodec})

	start := req.StartPositionMs
	if start < 0 || (it.RuntimeMs > 0 && start >= it.RuntimeMs) {
		start = 0
	}
	sess := &stream.Session{ID: db.NewID(), UserID: id.User.ID, Username: id.User.Username, DeviceName: id.Session.DeviceName,
		IP: ip.String(), ConnClass: class, ItemID: it.ID, Item: it, Method: dec.Method, Reasons: dec.Reasons, Decision: dec, Input: abs,
		RuntimeMs: it.RuntimeMs, SegSeconds: st.HLSSegmentSeconds, Engine: prof.Engine, SubtitleTrackID: subID,
		Preset: st.TranscodePreset, Threads: st.TranscodeThreads}
	if v := stream.VideoStream(streams); v != nil {
		sess.VideoIndex = v.Index
	}
	resp := map[string]any{"playSessionId": sess.ID, "method": dec.Method, "reasons": dec.Reasons, "runtimeMs": it.RuntimeMs,
		"connection": map[string]any{"class": class, "maxBitrateKbps": capKbps}}
	if dec.Method == "remux" {
		sess.TimelineOffsetMs = start
		resp["timelineOffsetMs"], resp["startPositionMs"] = start, 0
	} else {
		resp["timelineOffsetMs"], resp["startPositionMs"] = 0, start
	}
	if dec.Method == "direct" {
		resp["engine"], resp["mediaUrl"] = "html5", fmt.Sprintf("/api/v1/items/%s/file?sid=%s", it.ID, sess.ID)
		resp["mimeType"] = containerTypes[strings.ToLower(extOf(abs))]
		resp["transcode"] = nil
	} else {
		resp["engine"], resp["mediaUrl"], resp["mimeType"] = "hls", fmt.Sprintf("/api/v1/play/%s/master.m3u8", sess.ID), "application/vnd.apple.mpegurl"
	}
	// Subtitle delivery.
	subOut := map[string]any{"mode": "none"}
	if sess.IsHLS() && prof.Engine == "native" {
		for _, t := range subs {
			if t.Text {
				sess.InbandSubs = append(sess.InbandSubs, stream.SubtitleRendition{TrackID: t.ID, Name: subtitleDisplay(t), Language: media.BCP47(t.Language),
					Default: t.ID == subID, Forced: t.Forced})
			}
		}
		if selSub != nil {
			subOut = map[string]any{"mode": "inband", "trackId": subID}
		}
	} else if selSub != nil {
		subOut = map[string]any{"mode": "sidecar", "trackId": subID,
			"url": fmt.Sprintf("/api/v1/items/%s/subtitles/%s.vtt?offsetMs=%d", it.ID, subID, sess.TimelineOffsetMs)}
	}
	resp["subtitles"] = subOut
	if audioIdx >= 0 {
		resp["audioTrackId"] = audioIdx
	} else {
		resp["audioTrackId"] = nil
	}
	if subID != "" {
		resp["subtitleTrackId"] = subID
	} else {
		resp["subtitleTrackId"] = nil
	}

	if err := s.Sessions.Create(sess, req.ReplacesPlaySessionID, start); err != nil {
		if errors.Is(err, stream.ErrBusy) {
			writeError(w, http.StatusServiceUnavailable, "transcode_busy", "the server is already transcoding the maximum number of streams",
				map[string]any{"max": st.TranscodeMaxSessions, "active": s.Sessions.ActiveHLS()})
			return
		}
		s.internal(w, r, err)
		return
	}
	if sess.IsHLS() {
		plan := sess.CurrentPlan()
		resp["transcode"] = map[string]any{"backend": stream.BackendOf(plan), "hwDecode": plan.HWDecode, "video": plan, "audio": dec.Audio}
		s.Log.Info("play session", "session", sess.ID, "item", it.Title, "method", dec.Method, "reasons", strings.Join(dec.Reasons, ","),
			"backend", stream.BackendOf(plan), "class", class, "capKbps", capKbps)
	}
	// Remember the chosen languages for this item.
	if u, err := s.DB.GetUserData(ctx, id.User.ID, it.ID); err == nil {
		if a := stream.StreamByIndex(streams, audioIdx); a != nil {
			u.AudioLanguage = a.Language
		}
		if selSub != nil {
			u.SubtitleLanguage = selSub.Language
		}
		_ = s.DB.SetUserData(ctx, id.User.ID, it.ID, u)
	}
	if sum, err := s.summaryByID(ctx, id.User.ID, it.ID); err == nil {
		resp["item"] = sum
	}
	writeJSON(w, http.StatusCreated, resp)
}

func extOf(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i:]
	}
	return ""
}

// session returns the caller's play session (admins may access any).
func (s *Server) session(w http.ResponseWriter, r *http.Request) *stream.Session {
	sess, ok := s.Sessions.Get(chiParam(r, "sid"))
	id := ident(r)
	if !ok || (sess.UserID != id.User.ID && !id.User.IsAdmin) {
		notFound(w, "play session not found (it may have expired)")
		return nil
	}
	return sess
}

func writePlaylist(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleMaster(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	if !sess.IsHLS() {
		notFound(w, "not an HLS session")
		return
	}
	sess.Touch()
	plan := sess.CurrentPlan()
	streams, _ := s.DB.GetStreams(r.Context(), sess.ItemID)
	src := stream.VideoStream(streams)
	codecs := stream.VideoCodecString(plan, src)
	if codecs != "" {
		if ac := stream.AudioCodecString(sess.Decision.Audio); ac != "" {
			codecs += "," + ac
		} else if sess.Decision.Audio.Action != "none" {
			codecs = ""
		}
	}
	var bw int64
	audioKbps := int64(sess.Decision.Audio.BitrateKbps)
	if plan.Action == "copy" {
		bw = sess.Item.Bitrate
		if bw == 0 {
			bw = 8_000_000
		}
	} else {
		vk := int64(plan.MaxrateKbps)
		if vk == 0 {
			switch {
			case plan.Height > 1440:
				vk = 25000
			case plan.Height > 720:
				vk = 10000
			default:
				vk = 4000
			}
		}
		bw = (vk + audioKbps) * 1000
	}
	vr := ""
	if plan.HDR {
		vr = "PQ"
		if plan.Transfer == "arib-std-b67" {
			vr = "HLG"
		}
	}
	w2, h2 := plan.Width, plan.Height
	if w2 == 0 && src != nil {
		w2, h2 = src.Width, src.Height
	}
	writePlaylist(w, stream.MasterPlaylist(bw*12/10, bw, codecs, w2, h2, vr, sess.InbandSubs, tokenSuffix(r)))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	switch sess.Method {
	case "transcode":
		sess.Touch()
		writePlaylist(w, stream.MediaPlaylist(sess.RuntimeMs, sess.SegSeconds, tokenSuffix(r)))
	case "remux":
		pl, err := s.Sessions.RemuxPlaylist(r.Context(), sess, tokenSuffix(r))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "playlist not ready", nil)
			return
		}
		writePlaylist(w, pl)
	default:
		notFound(w, "not an HLS session")
	}
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	if !sess.IsHLS() {
		notFound(w, "not an HLS session")
		return
	}
	p, err := s.Sessions.Init(r.Context(), sess)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "init segment not ready", nil)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, p)
}

func (s *Server) handleSegment(w http.ResponseWriter, r *http.Request) {
	name := chiParam(r, "seg")
	m := stream.SegmentRe.FindStringSubmatch(name)
	if m == nil {
		notFound(w, "unknown file")
		return
	}
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	n, _ := strconv.Atoi(m[1])
	p, err := s.Sessions.Segment(r.Context(), sess, n)
	switch {
	case errors.Is(err, stream.ErrSegmentNotFound):
		notFound(w, "segment not found")
		return
	case errors.Is(err, stream.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "segment not produced in time", nil)
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "video/iso.segment")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, p)
}

func (s *Server) handleSessionSub(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	sess.Touch()
	file := chiParam(r, "file")
	switch {
	case strings.HasSuffix(file, ".m3u8"):
		track := strings.TrimSuffix(file, ".m3u8")
		writePlaylist(w, stream.SubtitlePlaylist(track, sess.RuntimeMs, tokenSuffix(r)))
	case strings.HasSuffix(file, ".vtt"):
		it, err := s.DB.GetItem(r.Context(), sess.ItemID)
		if err != nil {
			s.dbErr(w, r, err, "item")
			return
		}
		s.writeVTT(w, r, it, strings.TrimSuffix(file, ".vtt"), sess.TimelineOffsetMs)
	default:
		notFound(w, "unknown file")
	}
}

func (s *Server) handleSessionProgress(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}
	var req progressRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	sess.SetProgress(req.PositionMs, req.State)
	it, err := s.DB.GetItem(r.Context(), sess.ItemID)
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	u, played, err := s.applyProgress(r.Context(), sess.UserID, it, req.PositionMs, req.State == "ended", sess.MarkPlayCounted)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if req.State == "stopped" || req.State == "ended" {
		s.Sessions.Stop(sess.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"userData": toUserData(u), "played": played})
}

func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	sid := chiParam(r, "sid")
	if sess, ok := s.Sessions.Get(sid); ok {
		id := ident(r)
		if sess.UserID == id.User.ID || id.User.IsAdmin {
			s.Sessions.Stop(sid)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin play sessions ---

func (s *Server) sessionJSON(r *http.Request, sess *stream.Session, detail bool) map[string]any {
	snap := sess.Snapshot()
	out := map[string]any{"id": sess.ID, "userId": sess.UserID, "username": sess.Username, "deviceName": sess.DeviceName, "ip": sess.IP,
		"connectionClass": sess.ConnClass, "method": sess.Method, "reasons": sess.Reasons, "backend": snap.Backend,
		"positionMs": snap.PositionMs, "state": snap.State, "startedAt": db.FormatTime(sess.StartedAt), "lastActivityAt": db.FormatTime(snap.LastActivity),
		"segmentsProduced": snap.SegmentsProduced, "ffmpegRunning": snap.FFmpegRunning, "restarts": snap.Restarts}
	if sess.IsHLS() {
		out["transcode"] = map[string]any{"video": snap.Video, "audio": sess.Decision.Audio, "hwDecode": snap.Video.HWDecode}
	} else {
		out["transcode"] = nil
		out["backend"] = "direct"
	}
	if sum, err := s.summaryByID(r.Context(), sess.UserID, sess.ItemID); err == nil {
		out["item"] = sum
	}
	if detail {
		out["ffmpegArgs"] = snap.Args
		out["ffmpegLogTail"] = snap.LogTail
	}
	return out
}

func (s *Server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, sess := range s.Sessions.List() {
		out = append(out, s.sessionJSON(r, sess, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.Sessions.Get(chiParam(r, "sid"))
	if !ok {
		notFound(w, "play session not found")
		return
	}
	writeJSON(w, http.StatusOK, s.sessionJSON(r, sess, true))
}

func (s *Server) handleAdminStopSession(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Stop(chiParam(r, "sid"))
	w.WriteHeader(http.StatusNoContent)
}

// --- admin settings / problems ---

func (s *Server) settingsJSON() map[string]any {
	st := s.Settings()
	m := mustMap(st)
	caps := s.Tools.Caps()
	m["readOnly"] = map[string]any{"hwaccel": s.Cfg.Transcode.HWAccel, "hwaccelActive": caps.Active, "transcodeDir": s.Cfg.Transcode.Dir,
		"httpAddr": s.Cfg.HTTP.Addr, "ffmpegPath": s.Tools.FFmpeg(), "dataDir": s.Cfg.DataDir}
	return m
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsJSON())
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	st := s.Settings()
	// Copy the overrides map so a failed validation doesn't leak.
	b, _ := json.Marshal(st)
	var next Settings
	_ = json.Unmarshal(b, &next)
	if err := decodeJSON(r, &next); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if errs := next.validate(); len(errs) > 0 {
		validation(w, errs)
		return
	}
	if err := s.saveSettings(r.Context(), next); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsJSON())
}

func (s *Server) handleProblems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := s.DB.ProblemItems(ctx, queryInt(r, "limit", 100))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := []map[string]any{}
	flags := s.imageFlags(ctx, items)
	for _, it := range items {
		reason := it.StatusReason
		if it.Status == "ok" && it.Confidence == "low" {
			reason = "naming:low_confidence"
		}
		p, _ := s.itemRoot(ctx, it)
		out = append(out, map[string]any{"item": toSummary(it, flags[it.ID]), "status": it.Status, "statusReason": reason,
			"probeAttempts": it.ProbeAttempts, "path": p})
	}
	pending, missing, low, _ := s.DB.ProblemCounts(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "counts": map[string]int{"pending": pending, "missing": missing, "lowConfidence": low}})
}

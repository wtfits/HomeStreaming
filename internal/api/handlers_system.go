package api

import (
	"errors"

	"github.com/go-chi/chi/v5"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wtfits/homestreaming/internal/auth"
	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
	"github.com/wtfits/homestreaming/internal/netutil"
)

func (s *Server) ffmpegJSON(full bool) map[string]any {
	b := s.Tools.Binaries()
	out := map[string]any{"found": b.Found}
	if !b.Found {
		out["hint"] = ffmpeg.InstallHint
	}
	if full && b.Found {
		out["path"], out["ffprobePath"], out["version"] = b.FFmpeg, b.FFprobe, b.Version
		if b.VersionWarn != "" {
			out["warning"] = b.VersionWarn
		}
	}
	return out
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if s.Tools.MaybeRelocate(r.Context()) && s.OnFFmpegFound != nil {
		s.OnFFmpegFound()
	}
	n, err := s.DB.CountUsers(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	id := ident(r)
	full := id != nil && id.User.IsAdmin || n == 0
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "HomeStreaming", "serverName": s.Settings().ServerName, "version": s.Version, "apiVersion": 1,
		"serverId": s.ServerID, "setupRequired": n == 0, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"ffmpeg": s.ffmpegJSON(full), "suggestedLibraryPath": suggestedPath(n == 0),
	})
}

// suggestedPath proposes F:\torrent in the setup wizard when it exists.
func suggestedPath(setup bool) string {
	if !setup {
		return ""
	}
	for _, p := range []string{`F:\torrent`, `F:\Torrent`} {
		if runtime.GOOS == "windows" {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": db.Now()})
}

type loginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	ServerName string `json:"serverName"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	ClientType string `json:"clientType"`
	FFmpegPath string `json:"ffmpegPath"`
}

func (s *Server) deviceInfo(r *http.Request, req loginRequest) auth.DeviceInfo {
	return auth.DeviceInfo{DeviceID: req.DeviceID, DeviceName: req.DeviceName, ClientType: req.ClientType,
		UserAgent: r.UserAgent(), IP: s.Resolver.ClientIP(r).String()}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, tok string) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(auth.SessionLifetime.Seconds()), Secure: s.Resolver.IsHTTPS(r)})
}

func (s *Server) connectionJSON(r *http.Request) map[string]any {
	ip := s.Resolver.ClientIP(r)
	class := netutil.Class(ip, s.Settings().TailnetIsLan)
	return map[string]any{"class": class, "ip": ip.String(), "maxBitrateKbps": s.capFor(class)}
}

func (s *Server) capFor(class string) int {
	st := s.Settings()
	if class == "lan" {
		return st.LanMaxBitrateKbps
	}
	return st.RemoteMaxBitrateKbps
}

func (s *Server) loginResponse(w http.ResponseWriter, r *http.Request, status int, tok string, sess *db.Session, user *db.User) {
	s.setSessionCookie(w, r, tok)
	writeJSON(w, status, map[string]any{"token": tok, "user": toUser(user), "session": toSession(sess, sess.ID), "connection": s.connectionJSON(r)})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	n, err := s.DB.CountUsers(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusForbidden, "setup_done", "setup has already been completed", nil)
		return
	}
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	fields := map[string]string{}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || utf8.RuneCountInString(req.Username) > 64 {
		fields["username"] = "required (max 64 characters)"
	}
	if utf8.RuneCountInString(req.Password) < auth.MinPasswordLen {
		fields["password"] = "must be at least 8 characters"
	}
	if req.FFmpegPath != "" {
		if st, err := os.Stat(req.FFmpegPath); err != nil || st.IsDir() {
			fields["ffmpegPath"] = "file not found"
		}
	}
	if len(fields) > 0 {
		validation(w, fields)
		return
	}
	if req.FFmpegPath != "" {
		probe := filepath.Join(filepath.Dir(req.FFmpegPath), strings.Replace(filepath.Base(req.FFmpegPath), "ffmpeg", "ffprobe", 1))
		b := s.Tools.SetPaths(r.Context(), req.FFmpegPath, probe)
		if !b.Found {
			validation(w, map[string]string{"ffmpegPath": "not a working ffmpeg (ffprobe must be next to it)"})
			return
		}
		if err := s.Cfg.SetFFmpegPath(b.FFmpeg, b.FFprobe); err != nil {
			s.Log.Warn("could not persist ffmpeg path", "err", err)
		}
		if s.OnFFmpegFound != nil {
			s.OnFFmpegFound()
		}
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	user, err := s.DB.CreateUser(r.Context(), req.Username, hash, true)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if req.ServerName != "" {
		st := s.Settings()
		st.ServerName = strings.TrimSpace(req.ServerName)
		_ = s.saveSettings(r.Context(), st)
	}
	tok, sess, err := s.Auth.CreateSession(r.Context(), user, s.deviceInfo(r, req))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("first-run setup completed", "admin", user.Username)
	s.loginResponse(w, r, http.StatusCreated, tok, sess, user)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	tok, sess, user, err := s.Auth.Login(r.Context(), req.Username, req.Password, s.deviceInfo(r, req))
	var le *auth.LockedError
	switch {
	case errors.As(err, &le):
		secs := int(le.RetryAfter.Seconds() + 0.999)
		w.Header().Set("Retry-After", itoa(secs))
		writeError(w, http.StatusLocked, "locked", le.Error(), map[string]any{"retryAfterSeconds": secs})
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong username or password", nil)
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.loginResponse(w, r, http.StatusOK, tok, sess, user)
}

func itoa(n int) string { return strconv.Itoa(n) }

func chiParam(r *http.Request, key string) string { return chi.URLParam(r, key) }

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	_ = s.DB.DeleteSession(r.Context(), id.Session.ID)
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	writeJSON(w, http.StatusOK, map[string]any{"user": toUser(id.User), "session": toSession(id.Session, id.Session.ID), "connection": s.connectionJSON(r)})
}

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	var req struct {
		AudioLanguage    *string `json:"audioLanguage"`
		SubtitleLanguage *string `json:"subtitleLanguage"`
		SubtitleMode     *string `json:"subtitleMode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	p := id.User.Preferences
	if req.AudioLanguage != nil {
		p.AudioLanguage = strings.ToLower(strings.TrimSpace(*req.AudioLanguage))
	}
	if req.SubtitleLanguage != nil {
		p.SubtitleLanguage = strings.ToLower(strings.TrimSpace(*req.SubtitleLanguage))
	}
	if req.SubtitleMode != nil {
		switch *req.SubtitleMode {
		case "off", "preferred", "always":
			p.SubtitleMode = *req.SubtitleMode
		default:
			validation(w, map[string]string{"subtitleMode": "must be off, preferred or always"})
			return
		}
	}
	if err := s.DB.UpdatePreferences(r.Context(), id.User.ID, p); err != nil {
		s.internal(w, r, err)
		return
	}
	id.User.Preferences = p
	writeJSON(w, http.StatusOK, toUser(id.User))
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	if !auth.VerifyPassword(req.CurrentPassword, id.User.PasswordHash) {
		writeError(w, http.StatusForbidden, "forbidden", "current password is wrong", nil)
		return
	}
	if utf8.RuneCountInString(req.NewPassword) < auth.MinPasswordLen {
		validation(w, map[string]string{"newPassword": "must be at least 8 characters"})
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.DB.UpdatePassword(r.Context(), id.User.ID, hash); err != nil {
		s.internal(w, r, err)
		return
	}
	if r.URL.Query().Get("keepOtherSessions") != "true" {
		_ = s.DB.DeleteOtherSessions(r.Context(), id.User.ID, id.Session.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	list, err := s.DB.ListSessions(r.Context(), id.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := []sessionJSON{}
	for _, se := range list {
		out = append(out, toSession(se, id.Session.ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := ident(r)
	se, err := s.DB.GetSession(r.Context(), chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "session")
		return
	}
	if se.UserID != id.User.ID && !id.User.IsAdmin {
		notFound(w, "session not found")
		return
	}
	_ = s.DB.DeleteSession(r.Context(), se.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- admin: status, fs, ffmpeg ---

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	caps := s.Tools.Caps()
	b := s.Tools.Binaries()
	lan, tail := netutil.LocalAddresses()
	libs, _ := s.DB.ListLibraries(r.Context())
	var libOut []map[string]any
	for _, l := range libs {
		c, _ := s.DB.LibraryCounts(r.Context(), l.ID)
		libOut = append(libOut, map[string]any{"id": l.ID, "name": l.Name, "counts": c})
	}
	ff := s.ffmpegJSON(true)
	encs := []string{}
	for _, e := range caps.Encoders {
		if strings.HasPrefix(e, "libx26") || strings.Contains(e, "_nvenc") || strings.Contains(e, "_qsv") || strings.Contains(e, "_amf") ||
			strings.Contains(e, "_vaapi") || strings.Contains(e, "_videotoolbox") || e == "aac" || e == "ac3" {
			encs = append(encs, e)
		}
	}
	sort.Strings(encs)
	ff["encoders"] = encs
	_ = b
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "uptimeSeconds": int(time.Since(s.StartedAt).Seconds()), "dataDir": s.Cfg.DataDir, "configPath": s.Cfg.ConfigPath,
		"transcodeDir": s.Cfg.Transcode.Dir, "transcodeDirFreeBytes": diskFree(s.Cfg.Transcode.Dir),
		"ffmpeg":  ff,
		"hwaccel": map[string]any{"configured": caps.Configured, "active": caps.Active, "hwDecode": caps.HWDecode, "selfTests": nonNilTests(caps.SelfTests)},
		"listen":  []string{"http://" + displayAddr(s.Cfg.HTTP.Addr)}, "lanAddresses": nonNil(lan), "tailnetAddresses": nonNil(tail),
		"connection": s.connectionJSON(r), "activePlaySessions": len(s.Sessions.List()), "runningTasks": s.Tasks.Running(),
		"libraries": libOut,
	})
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func nonNilTests(t []ffmpeg.SelfTest) []ffmpeg.SelfTest {
	if t == nil {
		return []ffmpeg.SelfTest{}
	}
	return t
}

func displayAddr(a string) string {
	if strings.HasPrefix(a, ":") {
		return "0.0.0.0" + a
	}
	return a
}

func (s *Server) handleAdminFFmpeg(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	wasFound := s.Tools.Binaries().Found
	var b ffmpeg.Binaries
	if req.Path == "" {
		b = s.Tools.SetPaths(r.Context(), s.Cfg.FFmpeg.Path, s.Cfg.FFmpeg.FFprobePath)
	} else {
		probe := filepath.Join(filepath.Dir(req.Path), strings.Replace(filepath.Base(req.Path), "ffmpeg", "ffprobe", 1))
		b = s.Tools.SetPaths(r.Context(), req.Path, probe)
		if b.Found {
			if err := s.Cfg.SetFFmpegPath(b.FFmpeg, b.FFprobe); err != nil {
				s.Log.Warn("could not persist ffmpeg path", "err", err)
			}
		}
	}
	if b.Found && !wasFound && s.OnFFmpegFound != nil {
		s.OnFFmpegFound()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ffmpeg": s.ffmpegJSON(true), "hwaccel": s.Tools.Caps()})
}

type fsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

func (s *Server) handleFS(w http.ResponseWriter, r *http.Request) {
	roots := fsRoots()
	p := r.URL.Query().Get("path")
	if p == "" {
		if runtime.GOOS == "windows" {
			entries := []fsEntry{}
			for _, rt := range roots {
				entries = append(entries, fsEntry{Name: rt.Name, Path: rt.Path, IsDir: true})
			}
			writeJSON(w, http.StatusOK, map[string]any{"path": "", "parent": nil, "entries": entries, "roots": roots})
			return
		}
		p = "/"
	}
	if !filepath.IsAbs(p) {
		badRequest(w, "path must be absolute")
		return
	}
	p = filepath.Clean(p)
	ents, err := os.ReadDir(p)
	if err != nil {
		notFound(w, "cannot read directory: "+err.Error())
		return
	}
	entries := []fsEntry{}
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") || name == "System Volume Information" {
			continue
		}
		full := filepath.Join(p, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir {
			entries = append(entries, fsEntry{Name: name, Path: full, IsDir: true})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name) })
	var parent any
	if dir := filepath.Dir(p); dir != p {
		parent = dir
	} else if runtime.GOOS == "windows" {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "entries": entries, "roots": roots})
}

type fsRoot struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func fsRoots() []fsRoot {
	if runtime.GOOS != "windows" {
		return []fsRoot{{Name: "/", Path: "/"}}
	}
	var out []fsRoot
	for c := 'A'; c <= 'Z'; c++ {
		p := string(c) + `:\`
		if _, err := os.Stat(p); err == nil {
			out = append(out, fsRoot{Name: p, Path: p})
		}
	}
	return out
}

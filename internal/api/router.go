// Package api implements the HTTP API (docs/API.md) and serves the web client.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wtfits/homestreaming/internal/auth"
	"github.com/wtfits/homestreaming/internal/config"
	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
	"github.com/wtfits/homestreaming/internal/images"
	"github.com/wtfits/homestreaming/internal/library"
	"github.com/wtfits/homestreaming/internal/logging"
	"github.com/wtfits/homestreaming/internal/netutil"
	"github.com/wtfits/homestreaming/internal/stream"
	"github.com/wtfits/homestreaming/internal/tasks"
)

// Server is the HTTP API.
type Server struct {
	Cfg       *config.Config
	DB        *db.DB
	Auth      *auth.Service
	Scanner   *library.Scanner
	Watcher   *library.Watcher
	Tasks     *tasks.Manager
	Images    *images.Store
	Sessions  *stream.Manager
	Subs      *stream.Subtitles
	Tools     *ffmpeg.Toolbox
	Resolver  *netutil.Resolver
	Log       *slog.Logger
	Version   string
	StartedAt time.Time
	ServerID  string
	WebFS     fs.FS // web client (dist); nil = API only

	// OnFFmpegFound is called when ffmpeg becomes available at runtime.
	OnFFmpegFound func()

	settingsMu    sync.RWMutex
	settings      Settings
	loginLimiter  *auth.IPLimiter
	publicLimiter *auth.IPLimiter
	setupMu       sync.Mutex
}

// Init prepares limiters and loads settings.
func (s *Server) Init(ctx context.Context) error {
	s.loginLimiter = auth.NewIPLimiter(10, 10)
	s.publicLimiter = auth.NewIPLimiter(120, 120)
	return s.ReloadSettings(ctx)
}

var mediaRouteRe = regexp.MustCompile(`^/api/v1/(items/[^/]+/(file|image/[^/]+|subtitles/[^/]+)|play/.+)$`)

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverer, s.requestID, s.securityHeaders, s.logRequests)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.cors, s.identify)

		r.Get("/system/info", s.handleSystemInfo)
		r.Get("/system/ping", s.handlePing)
		r.With(s.limitLogin).Post("/system/setup", s.handleSetup)
		r.With(s.limitLogin).Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Patch("/auth/me/preferences", s.handlePreferences)
			r.Post("/auth/me/password", s.handlePassword)
			r.Get("/auth/sessions", s.handleListSessions)
			r.Delete("/auth/sessions/{id}", s.handleDeleteSession)

			r.Get("/libraries", s.handleListLibraries)
			r.Get("/libraries/{id}", s.handleGetLibrary)
			r.Get("/tasks", s.handleListTasks)
			r.Get("/tasks/{id}", s.handleGetTask)

			r.Get("/home", s.handleHome)
			r.Get("/items", s.handleListItems)
			r.Get("/items/{id}", s.handleGetItem)
			r.Get("/items/{id}/children", s.handleChildren)
			r.Get("/items/{id}/image/{kind}", s.handleImage)
			r.Head("/items/{id}/image/{kind}", s.handleImage)
			r.Get("/items/{id}/file", s.handleFile)
			r.Head("/items/{id}/file", s.handleFile)
			r.Get("/items/{id}/subtitles/{track}", s.handleSubtitle)
			r.Post("/items/{id}/play-info", s.handlePlayInfo)
			r.Post("/items/{id}/progress", s.handleItemProgress)
			r.Post("/items/{id}/played", s.handlePlayed)

			r.Get("/play/{sid}/master.m3u8", s.handleMaster)
			r.Get("/play/{sid}/index.m3u8", s.handleIndex)
			r.Get("/play/{sid}/init.mp4", s.handleInit)
			r.Get("/play/{sid}/sub/{file}", s.handleSessionSub)
			r.Get("/play/{sid}/{seg}", s.handleSegment)
			r.Post("/play/{sid}/progress", s.handleSessionProgress)
			r.Delete("/play/{sid}", s.handleStopSession)

			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/admin/status", s.handleAdminStatus)
				r.Get("/admin/fs", s.handleFS)
				r.Post("/admin/ffmpeg", s.handleAdminFFmpeg)
				r.Get("/admin/settings", s.handleGetSettings)
				r.Patch("/admin/settings", s.handlePatchSettings)
				r.Get("/admin/problems", s.handleProblems)
				r.Get("/admin/play-sessions", s.handleAdminSessions)
				r.Get("/admin/play-sessions/{sid}", s.handleAdminSession)
				r.Delete("/admin/play-sessions/{sid}", s.handleAdminStopSession)
				r.Post("/libraries", s.handleCreateLibrary)
				r.Patch("/libraries/{id}", s.handlePatchLibrary)
				r.Delete("/libraries/{id}", s.handleDeleteLibrary)
				r.Post("/libraries/{id}/scan", s.handleScanLibrary)
				r.Post("/libraries/{id}/clean", s.handleCleanLibrary)
				r.Post("/libraries/scan", s.handleScanAll)
			})
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { notFound(w, "no such endpoint") })
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed", nil)
		})
	})

	r.Get("/*", s.serveWeb)
	r.Head("/*", s.serveWeb)
	return r
}

// --- middleware ---

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.internal(w, r, fmt.Errorf("panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [6]byte
		_, _ = rand.Read(b[:])
		w.Header().Set("X-Request-Id", hex.EncodeToString(b[:]))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; worker-src 'self' blob:; connect-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")
		if s.Resolver.IsHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return
		}
		lvl := slog.LevelInfo
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/v1/play/") && r.Method == http.MethodGet || strings.Contains(p, "/image/") || strings.HasSuffix(p, "/progress") {
			lvl = slog.LevelDebug
		}
		s.Log.Log(r.Context(), lvl, "http", "method", r.Method, "path", logging.RedactURL(r.URL.RequestURI()), "status", sw.status,
			"ms", time.Since(start).Milliseconds(), "ip", s.Resolver.ClientIP(r).String())
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With, Range")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges, X-Request-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// identify resolves the caller (if any), enforces the CSRF rule and the
// public rate limit for unauthenticated requests.
func (s *Server) identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowQuery := (r.Method == http.MethodGet || r.Method == http.MethodHead) && mediaRouteRe.MatchString(r.URL.Path)
		tok, viaCookie := auth.TokenFromRequest(r, allowQuery)
		ip := s.Resolver.ClientIP(r).String()
		if tok != "" {
			user, sess, err := s.Auth.Authenticate(r.Context(), tok, ip)
			if err == nil {
				if viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
					ct := r.Header.Get("Content-Type")
					if !strings.HasPrefix(strings.ToLower(ct), "application/json") && r.Header.Get("X-Requested-With") != "HomeStreaming" {
						writeError(w, http.StatusForbidden, "csrf", "cookie-authenticated requests must send JSON", nil)
						return
					}
				}
				r = r.WithContext(auth.WithIdentity(r.Context(), &auth.Identity{User: user, Session: sess, Token: tok, ViaCookie: viaCookie}))
				next.ServeHTTP(w, r)
				return
			} else if !errors.Is(err, auth.ErrUnauthenticated) {
				s.internal(w, r, err)
				return
			}
		}
		if ok, wait := s.publicLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limitLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.Resolver.ClientIP(r).String()
		if ok, wait := s.loginLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts; slow down", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.FromContext(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := auth.FromContext(r.Context())
		if id == nil || !id.User.IsAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "administrator only", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ident(r *http.Request) *auth.Identity { return auth.FromContext(r.Context()) }

// tokenSuffix returns "?token=…" when the request was authenticated by a query
// token, so URIs inside playlists keep working without cookies.
func tokenSuffix(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		if id := ident(r); id != nil && id.Token == t {
			return "?token=" + t
		}
	}
	return ""
}

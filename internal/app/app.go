// Package app wires configuration, storage, services and the HTTP server.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wtfits/homestreaming/internal/api"
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
	"github.com/wtfits/homestreaming/internal/update"
	"github.com/wtfits/homestreaming/web"
)

// Options tweak the app (tests shorten timers).
type Options struct {
	Version     string
	Stderr      io.Writer
	WatchSettle time.Duration // 0 = default 30 s
	IdleTimeout time.Duration // 0 = default 60 s
	NoWatcher   bool
	NoStartScan bool
	// Activate restarts into a freshly installed update (the Windows service
	// uses a helper process); nil means the user restarts by hand.
	Activate      update.Activator
	NoUpdateCheck bool
}

// App is a running server instance.
type App struct {
	Cfg      *config.Config
	DB       *db.DB
	API      *api.Server
	Log      *slog.Logger
	Scanner  *library.Scanner
	Watcher  *library.Watcher
	Sessions *stream.Manager
	Tools    *ffmpeg.Toolbox
	Updater  *update.Updater
	closeLog func()
}

// New builds the app from a loaded config.
func New(ctx context.Context, cfg *config.Config, opt Options) (*App, error) {
	log, closeLog := logging.Setup(cfg.DataDir, cfg.Log.Level, cfg.Log.File, opt.Stderr)
	for _, d := range []string{"images", "cache", "logs"} {
		_ = os.MkdirAll(filepath.Join(cfg.DataDir, d), 0o755)
	}
	dbPath := filepath.Join(cfg.DataDir, "homestreaming.db")
	d, err := db.Open(dbPath)
	if err != nil {
		closeLog()
		return nil, err
	}
	if err := d.CheckWritable(ctx); err != nil {
		d.Close()
		closeLog()
		if strings.Contains(err.Error(), "readonly") || strings.Contains(err.Error(), "read-only") {
			return nil, fmt.Errorf("the database %s is read-only for this account. It was probably created by another account "+
				"(an Administrator prompt or the Windows service). Fix it once from an Administrator prompt:\n  %s\n"+
				"or run HomeStreaming as the Windows service (homestreaming service install)", dbPath, config.UsersModifyCommand(cfg.DataDir))
		}
		return nil, fmt.Errorf("database %s is not writable: %w", dbPath, err)
	}
	kv, _ := d.GetSettings(ctx)
	serverID := kv["server_id"]
	if serverID == "" {
		serverID = db.NewID()
		_ = d.SetSettings(ctx, map[string]string{"server_id": serverID})
	}

	tools := &ffmpeg.Toolbox{Opts: ffmpeg.LocateOptions{ConfigFFmpeg: cfg.FFmpeg.Path, ConfigFFprobe: cfg.FFmpeg.FFprobePath, DataDir: cfg.DataDir},
		HWAccel: cfg.Transcode.HWAccel, HWDecode: cfg.Transcode.HWDecode, CacheDir: filepath.Join(cfg.DataDir, "cache")}
	b := tools.Locate(ctx)
	if !b.Found {
		log.Error("ffmpeg not found — " + ffmpeg.InstallHint)
	} else {
		log.Info("ffmpeg found", "path", b.FFmpeg, "version", b.Version, "via", b.Source)
		if b.VersionWarn != "" {
			log.Warn(b.VersionWarn)
		}
		caps := tools.Caps()
		if caps.Configured != "none" && caps.Active == "none" && len(caps.SelfTests) > 0 {
			var failed []string
			for _, t := range caps.SelfTests {
				if !t.OK {
					failed = append(failed, t.Backend+": "+lastLine(t.Detail))
				}
			}
			log.Warn("hardware transcoding unavailable; using software (libx264)", "configured", caps.Configured, "failed", strings.Join(failed, " | "))
		} else {
			log.Info("transcoding backend", "configured", caps.Configured, "active", caps.Active, "hwDecode", caps.HWDecode)
		}
	}

	img := &images.Store{DataDir: cfg.DataDir}
	tm := tasks.New()
	scanner := library.NewScanner(d, tm, img, log, func() (string, string) { return tools.FFmpeg(), tools.FFprobe() })
	watcher := &library.Watcher{Scanner: scanner, DB: d, Log: log, Settle: opt.WatchSettle}

	srv := &api.Server{Cfg: cfg, DB: d, Auth: &auth.Service{DB: d}, Scanner: scanner, Watcher: watcher, Tasks: tm, Images: img,
		Tools: tools, Resolver: netutil.NewResolver(cfg.HTTP.TrustedProxies), Log: log, Version: opt.Version, StartedAt: time.Now(),
		ServerID: serverID, WebFS: web.Dist()}
	if opt.NoWatcher {
		srv.Watcher = nil
	}
	if err := srv.Init(ctx); err != nil {
		log.Warn("settings", "err", err)
	}
	sessions := stream.NewManager(cfg.Transcode.Dir, tools.FFmpeg, log, func() int { return srv.Settings().TranscodeMaxSessions })
	if opt.IdleTimeout > 0 {
		sessions.IdleTimeout = opt.IdleTimeout
	}
	srv.Sessions = sessions
	srv.Subs = &stream.Subtitles{FFmpeg: tools.FFmpeg, CacheDir: filepath.Join(cfg.DataDir, "cache", "subtitles")}
	up := &update.Updater{Repo: cfg.Update.Repo, Current: opt.Version, DataDir: cfg.DataDir, Log: log, Activate: opt.Activate}
	up.RecordStartup()
	srv.Updater = up
	a := &App{Cfg: cfg, DB: d, API: srv, Log: log, Scanner: scanner, Watcher: watcher, Sessions: sessions, Tools: tools, Updater: up, closeLog: closeLog}
	srv.OnFFmpegFound = func() {
		log.Info("ffmpeg is now available; rescanning libraries")
		a.scanAll(context.WithoutCancel(ctx))
	}
	if srv.WebFS == nil {
		log.Warn("web client not embedded in this build (run `make web` before building)")
	}
	return a, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func (a *App) scanAll(ctx context.Context) {
	libs, err := a.DB.ListLibraries(ctx)
	if err != nil {
		return
	}
	for _, l := range libs {
		_, _ = a.Scanner.Scan(ctx, l.ID, false)
	}
}

// background runs the periodic jobs until ctx ends.
func (a *App) background(ctx context.Context, opt Options) {
	go a.Sessions.Run(ctx)
	if !opt.NoWatcher {
		a.Watcher.Sync(ctx)
	}
	if a.Cfg.Update.Check && !opt.NoUpdateCheck {
		go a.Updater.Run(ctx, 2*time.Minute, 6*time.Hour)
	}
	if !opt.NoStartScan {
		a.scanAll(ctx)
	}
	hourly := time.NewTicker(time.Hour)
	hw := time.NewTicker(10 * time.Minute)
	scanTick := time.NewTicker(time.Minute)
	lastScan := time.Now()
	defer hourly.Stop()
	defer hw.Stop()
	defer scanTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			_ = a.DB.PruneExpiredSessions(ctx)
			_ = a.DB.PruneLoginAttempts(ctx, time.Now().Add(-24*time.Hour))
		case <-hw.C:
			a.Tools.RetryHW(ctx)
		case <-scanTick.C:
			h := a.API.Settings().ScanIntervalHours
			if h > 0 && time.Since(lastScan) >= time.Duration(h)*time.Hour {
				lastScan = time.Now()
				a.scanAll(ctx)
			}
		}
	}
}

// Banner returns the URLs the server can be reached at.
func Banner(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{addr}
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	out := []string{"http://localhost:" + port}
	lan, tail := netutil.LocalAddresses()
	for _, ip := range lan {
		out = append(out, "http://"+ip+":"+port+"  (LAN)")
	}
	for _, ip := range tail {
		out = append(out, "http://"+ip+":"+port+"  (Tailscale)")
	}
	return out
}

// Run serves HTTP(S) until ctx is cancelled, then shuts down gracefully.
func (a *App) Run(ctx context.Context, opt Options) error {
	defer a.Close()
	bctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.background(bctx, opt)

	handler := a.API.Handler()
	srv := &http.Server{Addr: a.Cfg.HTTP.Addr, Handler: handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	ln, err := net.Listen("tcp", a.Cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w (is another instance running?)", a.Cfg.HTTP.Addr, err)
	}
	errCh := make(chan error, 2)
	go func() { errCh <- srv.Serve(ln) }()

	var tlsSrv *http.Server
	if a.Cfg.TLS.CertFile != "" && a.Cfg.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(a.Cfg.TLS.CertFile, a.Cfg.TLS.KeyFile)
		if err != nil {
			a.Log.Error("TLS disabled: cannot load certificate", "err", err)
		} else {
			tlsSrv = &http.Server{Addr: a.Cfg.TLS.Addr, Handler: handler, ReadHeaderTimeout: 15 * time.Second,
				TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
			go func() { errCh <- tlsSrv.ListenAndServeTLS("", "") }()
			a.Log.Info("HTTPS enabled", "addr", a.Cfg.TLS.Addr)
		}
	}
	a.Log.Info("HomeStreaming " + a.API.Version + " is running. Open one of:\n  " + strings.Join(Banner(ln.Addr().String()), "\n  "))
	if a.Cfg.HTTP.Addr == ":8420" || strings.HasSuffix(a.Cfg.HTTP.Addr, ":8420") {
		a.Log.Info("data directory", "path", a.Cfg.DataDir)
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	a.Log.Info("shutting down")
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	if tlsSrv != nil {
		_ = tlsSrv.Shutdown(sctx)
	}
	return nil
}

// Close stops sessions, watchers and closes the database.
func (a *App) Close() {
	a.Sessions.Close()
	a.Watcher.Close()
	_ = os.RemoveAll(a.Cfg.Transcode.Dir)
	_ = a.DB.Close()
	a.closeLog()
}

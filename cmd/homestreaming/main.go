// Command homestreaming is the HomeStreaming media server.
//
//	homestreaming [serve] [flags]          run the server (default)
//	homestreaming doctor [flags]           print diagnostics
//	homestreaming service install|uninstall|start|stop|status
//	homestreaming version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/kardianos/service"

	"github.com/wtfits/homestreaming/internal/app"
	"github.com/wtfits/homestreaming/internal/config"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
	"github.com/wtfits/homestreaming/internal/netutil"
)

var version = "0.1.0-dev"

func parseFlags(args []string) (config.Flags, []string, error) {
	fs := flag.NewFlagSet("homestreaming", flag.ContinueOnError)
	var f config.Flags
	fs.StringVar(&f.DataDir, "data-dir", "", "data directory (default: OS-specific; env HS_DATA_DIR)")
	fs.StringVar(&f.Addr, "addr", "", "HTTP listen address, e.g. :8420 (env HS_HTTP_ADDR)")
	fs.StringVar(&f.FFmpegPath, "ffmpeg", "", "path to ffmpeg (env HS_FFMPEG_PATH)")
	fs.StringVar(&f.FFprobePath, "ffprobe", "", "path to ffprobe (env HS_FFPROBE_PATH)")
	fs.StringVar(&f.TranscodeDir, "transcode-dir", "", "transcode temp directory (env HS_TRANSCODE_DIR)")
	fs.StringVar(&f.HWAccel, "hwaccel", "", "auto|none|nvenc|qsv|amf|vaapi|videotoolbox (env HS_HWACCEL)")
	fs.StringVar(&f.LogLevel, "log-level", "", "debug|info|warn|error (env HS_LOG_LEVEL)")
	err := fs.Parse(args)
	return f, fs.Args(), err
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve", "run":
		err = serve(args)
	case "doctor":
		err = doctor(args)
	case "service":
		err = serviceCmd(args)
	case "version", "--version":
		fmt.Printf("homestreaming %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	case "help", "-h", "--help":
		fmt.Println("usage: homestreaming [serve|doctor|service install|uninstall|start|stop|status|version] [flags]")
	default:
		err = fmt.Errorf("unknown command %q (try: serve, doctor, service, version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	f, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	if !service.Interactive() {
		return runAsService(f)
	}
	cfg, err := config.Load(f, nil)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, cfg, app.Options{Version: version})
	if err != nil {
		return err
	}
	return a.Run(ctx, app.Options{Version: version})
}

func doctor(args []string) error {
	f, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f, nil)
	if err != nil {
		return err
	}
	ctx := context.Background()
	fmt.Printf("HomeStreaming %s (%s/%s)\n\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Data dir:      %s\nConfig:        %s\nTranscode dir: %s\n\n", cfg.DataDir, cfg.ConfigPath, cfg.Transcode.Dir)
	tb := &ffmpeg.Toolbox{Opts: ffmpeg.LocateOptions{ConfigFFmpeg: cfg.FFmpeg.Path, ConfigFFprobe: cfg.FFmpeg.FFprobePath, DataDir: cfg.DataDir},
		HWAccel: cfg.Transcode.HWAccel, HWDecode: cfg.Transcode.HWDecode, CacheDir: filepath.Join(cfg.DataDir, "cache")}
	b := tb.Locate(ctx)
	if !b.Found {
		fmt.Printf("ffmpeg:        NOT FOUND — %s\n", ffmpeg.InstallHint)
	} else {
		fmt.Printf("ffmpeg:        %s (version %s, found via %s)\nffprobe:       %s\n", b.FFmpeg, b.Version, b.Source, b.FFprobe)
		if b.VersionWarn != "" {
			fmt.Printf("               warning: %s\n", b.VersionWarn)
		}
		caps := tb.Caps()
		var hw []string
		for _, e := range caps.Encoders {
			if strings.Contains(e, "nvenc") || strings.Contains(e, "qsv") || strings.Contains(e, "amf") || strings.Contains(e, "vaapi") || strings.Contains(e, "videotoolbox") {
				hw = append(hw, e)
			}
		}
		fmt.Printf("HW encoders:   %s\n", strings.Join(hw, " "))
		fmt.Printf("hwaccel:       configured=%s active=%s hwDecode=%v\n", caps.Configured, caps.Active, caps.HWDecode)
		for _, t := range caps.SelfTests {
			status := "ok"
			if !t.OK {
				status = "FAILED"
			}
			fmt.Printf("  self-test %-6s %-18s %s %s\n", t.Backend, t.Test, status, firstLine(t.Detail))
		}
	}
	fmt.Println("\nListen addresses:")
	for _, u := range app.Banner(cfg.HTTP.Addr) {
		fmt.Println("  " + u)
	}
	lan, tail := netutil.LocalAddresses()
	if len(tail) == 0 {
		fmt.Println("\nTailscale: no 100.x address found on this machine (remote access needs Tailscale; see deploy/tailscale.md)")
	}
	_ = lan
	if runtime.GOOS == "windows" {
		fmt.Println("\nFirewall: make sure inbound TCP 8420 is allowed for all profiles:")
		fmt.Println(`  netsh advfirewall firewall add rule name="HomeStreaming" dir=in action=allow protocol=TCP localport=8420 profile=any`)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Package config resolves the server configuration from defaults, config.toml,
// HS_* environment variables and command-line flags (in increasing precedence).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the bootstrap configuration. Runtime-tunable settings (bitrate caps,
// presets, ...) live in the database settings table instead.
type Config struct {
	DataDir    string `toml:"-"`
	ConfigPath string `toml:"-"`

	HTTP      HTTPConfig      `toml:"http"`
	TLS       TLSConfig       `toml:"tls"`
	FFmpeg    FFmpegConfig    `toml:"ffmpeg"`
	Transcode TranscodeConfig `toml:"transcode"`
	Log       LogConfig       `toml:"log"`
}

type HTTPConfig struct {
	Addr           string   `toml:"addr"`
	TrustedProxies []string `toml:"trusted_proxies"`
	RedirectHTTPS  bool     `toml:"redirect_https"`
	PublicURL      string   `toml:"public_url"`
}

type TLSConfig struct {
	Addr     string `toml:"addr"`
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

type FFmpegConfig struct {
	Path        string `toml:"path"`
	FFprobePath string `toml:"ffprobe_path"`
}

type TranscodeConfig struct {
	Dir      string `toml:"dir"`
	HWAccel  string `toml:"hwaccel"`
	HWDecode bool   `toml:"hw_decode"`
}

type LogConfig struct {
	Level string `toml:"level"`
	File  bool   `toml:"file"`
}

// Flags holds command-line overrides; empty strings mean "not set".
type Flags struct {
	DataDir      string
	Addr         string
	FFmpegPath   string
	FFprobePath  string
	TranscodeDir string
	HWAccel      string
	LogLevel     string
}

// ValidHWAccel lists accepted transcode.hwaccel values.
var ValidHWAccel = []string{"auto", "none", "nvenc", "qsv", "amf", "vaapi", "videotoolbox"}

// Defaults returns the built-in defaults (without a data dir).
func Defaults() Config {
	return Config{
		HTTP:      HTTPConfig{Addr: ":8420"},
		TLS:       TLSConfig{Addr: ":8443"},
		Transcode: TranscodeConfig{HWAccel: "auto", HWDecode: true},
		Log:       LogConfig{Level: "info", File: true},
	}
}

// DefaultDataDir returns the OS-specific default data directory.
func DefaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "HomeStreaming")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "HomeStreaming")
	default:
		if os.Geteuid() == 0 {
			return "/var/lib/homestreaming"
		}
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "homestreaming")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "homestreaming")
	}
}

// Load resolves the configuration. getenv may be nil (os.Getenv is used).
// It creates the data directory and writes a commented default config.toml
// when none exists.
func Load(f Flags, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := Defaults()

	dataDir := DefaultDataDir()
	if v := getenv("HS_DATA_DIR"); v != "" {
		dataDir = v
	}
	if f.DataDir != "" {
		dataDir = f.DataDir
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	cfg.DataDir = abs
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "config.toml")

	if _, err := os.Stat(cfg.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if werr := os.WriteFile(cfg.ConfigPath, []byte(DefaultConfigTOML), 0o644); werr != nil {
			return nil, fmt.Errorf("write default config: %w", werr)
		}
	}
	if _, err := toml.DecodeFile(cfg.ConfigPath, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", cfg.ConfigPath, err)
	}

	applyEnv(&cfg, getenv)
	applyFlags(&cfg, f)

	cfg.Transcode.HWAccel = strings.ToLower(strings.TrimSpace(cfg.Transcode.HWAccel))
	if cfg.Transcode.HWAccel == "" {
		cfg.Transcode.HWAccel = "auto"
	}
	if !contains(ValidHWAccel, cfg.Transcode.HWAccel) {
		return nil, fmt.Errorf("transcode.hwaccel %q invalid (want one of %s)", cfg.Transcode.HWAccel, strings.Join(ValidHWAccel, ", "))
	}
	if cfg.Transcode.Dir == "" {
		cfg.Transcode.Dir = filepath.Join(cfg.DataDir, "transcode")
	}
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8420"
	}
	return &cfg, nil
}

func applyEnv(cfg *Config, getenv func(string) string) {
	set := func(dst *string, key string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	set(&cfg.HTTP.Addr, "HS_HTTP_ADDR")
	set(&cfg.TLS.Addr, "HS_TLS_ADDR")
	set(&cfg.TLS.CertFile, "HS_TLS_CERT_FILE")
	set(&cfg.TLS.KeyFile, "HS_TLS_KEY_FILE")
	set(&cfg.FFmpeg.Path, "HS_FFMPEG_PATH")
	set(&cfg.FFmpeg.FFprobePath, "HS_FFPROBE_PATH")
	set(&cfg.Transcode.Dir, "HS_TRANSCODE_DIR")
	set(&cfg.Transcode.HWAccel, "HS_HWACCEL")
	set(&cfg.Log.Level, "HS_LOG_LEVEL")
	if v := getenv("HS_TRUSTED_PROXIES"); v != "" {
		cfg.HTTP.TrustedProxies = splitList(v)
	}
}

func applyFlags(cfg *Config, f Flags) {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&cfg.HTTP.Addr, f.Addr)
	set(&cfg.FFmpeg.Path, f.FFmpegPath)
	set(&cfg.FFmpeg.FFprobePath, f.FFprobePath)
	set(&cfg.Transcode.Dir, f.TranscodeDir)
	set(&cfg.Transcode.HWAccel, f.HWAccel)
	set(&cfg.Log.Level, f.LogLevel)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SetFFmpegPath persists a confirmed ffmpeg path into config.toml (used by the
// setup wizard), so later service runs do not depend on the locator.
func (c *Config) SetFFmpegPath(ffmpegPath, ffprobePath string) error {
	data, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	inSection := false
	replaced := map[string]bool{}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inSection = t == "[ffmpeg]"
			continue
		}
		if !inSection {
			continue
		}
		for key, val := range map[string]string{"path": ffmpegPath, "ffprobe_path": ffprobePath} {
			if strings.HasPrefix(t, key+" ") || strings.HasPrefix(t, key+"=") {
				lines[i] = fmt.Sprintf("%s = %s", key, tomlString(val))
				replaced[key] = true
			}
		}
	}
	out := strings.Join(lines, "\n")
	if !replaced["path"] || !replaced["ffprobe_path"] {
		out += fmt.Sprintf("\n[ffmpeg]\npath = %s\nffprobe_path = %s\n", tomlString(ffmpegPath), tomlString(ffprobePath))
	}
	if err := os.WriteFile(c.ConfigPath, []byte(out), 0o644); err != nil {
		return err
	}
	c.FFmpeg.Path, c.FFmpeg.FFprobePath = ffmpegPath, ffprobePath
	return nil
}

func tomlString(s string) string {
	// Literal strings avoid escaping Windows backslashes.
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return fmt.Sprintf("%q", s)
}

// DefaultConfigTOML is written to <data>/config.toml on first start.
const DefaultConfigTOML = `# HomeStreaming bootstrap configuration.
# Precedence: command-line flags > HS_* environment variables > this file > defaults.
# Runtime settings (bitrate caps, presets, scan interval) are edited in the web UI (Admin -> Settings).

[http]
addr = ":8420"                # HTTP listen address; ":8420" = all interfaces incl. the Tailscale adapter
trusted_proxies = []          # e.g. ["127.0.0.1"] when fronted by "tailscale serve", or a VPS relay IP
redirect_https = false
public_url = ""               # optional, e.g. "https://desktop-pc.tail1234.ts.net"

[tls]                         # optional HTTPS with provided certificates (e.g. from "tailscale cert")
addr = ":8443"
cert_file = ""
key_file = ""

[ffmpeg]
path = ""                     # empty = auto-locate (winget, PATH, next to the binary, <data>/bin)
ffprobe_path = ""

[transcode]
dir = ""                      # empty = <data>/transcode ; any drive works, e.g. 'D:\hs-transcode'
hwaccel = "auto"              # auto|none|nvenc|qsv|amf|vaapi|videotoolbox
hw_decode = true              # allow NVDEC when the self-test passes

[log]
level = "info"                # debug|info|warn|error
file = true                   # <data>/logs/homestreaming.log, 20 MB x 5 rotated
`

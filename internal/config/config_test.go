package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	// File layer.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[http]
addr = ":9000"
[ffmpeg]
path = "/file/ffmpeg"
[transcode]
dir = "/file/transcode"
hwaccel = "none"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	// File only.
	cfg, err := Load(Flags{DataDir: dir}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":9000" || cfg.FFmpeg.Path != "/file/ffmpeg" || cfg.Transcode.Dir != "/file/transcode" || cfg.Transcode.HWAccel != "none" {
		t.Fatalf("file layer not applied: %+v", cfg)
	}

	// Env overrides file.
	env["HS_HTTP_ADDR"] = ":9100"
	env["HS_FFMPEG_PATH"] = "/env/ffmpeg"
	env["HS_TRANSCODE_DIR"] = "/env/transcode"
	env["HS_HWACCEL"] = "nvenc"
	cfg, err = Load(Flags{DataDir: dir}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":9100" || cfg.FFmpeg.Path != "/env/ffmpeg" || cfg.Transcode.Dir != "/env/transcode" || cfg.Transcode.HWAccel != "nvenc" {
		t.Fatalf("env layer not applied: %+v", cfg)
	}

	// Flags override env.
	cfg, err = Load(Flags{DataDir: dir, Addr: ":9200", FFmpegPath: "/flag/ffmpeg", TranscodeDir: "/flag/transcode", HWAccel: "auto"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":9200" || cfg.FFmpeg.Path != "/flag/ffmpeg" || cfg.Transcode.Dir != "/flag/transcode" || cfg.Transcode.HWAccel != "auto" {
		t.Fatalf("flag layer not applied: %+v", cfg)
	}
}

func TestDefaultsAndConfigWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	cfg, err := Load(Flags{DataDir: dir}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":8420" || cfg.Transcode.HWAccel != "auto" || !cfg.Transcode.HWDecode {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.Transcode.Dir != filepath.Join(dir, "transcode") {
		t.Fatalf("transcode dir default: %s", cfg.Transcode.Dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil || !strings.Contains(string(data), "hwaccel") {
		t.Fatalf("config.toml not written: %v", err)
	}
	if err := cfg.SetFFmpegPath(`C:\ffmpeg\bin\ffmpeg.exe`, `C:\ffmpeg\bin\ffprobe.exe`); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(Flags{DataDir: dir}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.FFmpeg.Path != `C:\ffmpeg\bin\ffmpeg.exe` || cfg2.FFmpeg.FFprobePath != `C:\ffmpeg\bin\ffprobe.exe` {
		t.Fatalf("SetFFmpegPath not persisted: %+v", cfg2.FFmpeg)
	}
}

func TestUpdateDefaultsAndEnv(t *testing.T) {
	cfg, err := Load(Flags{DataDir: t.TempDir()}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Update.Check || cfg.Update.Repo != "wtfits/HomeStreaming" {
		t.Fatalf("update defaults: %+v", cfg.Update)
	}
	env := map[string]string{"HS_UPDATE_CHECK": "0", "HS_UPDATE_REPO": "me/fork"}
	cfg, err = Load(Flags{DataDir: t.TempDir()}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.Check || cfg.Update.Repo != "me/fork" {
		t.Fatalf("update env: %+v", cfg.Update)
	}
}

func TestInvalidHWAccel(t *testing.T) {
	_, err := Load(Flags{DataDir: t.TempDir(), HWAccel: "bogus"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error")
	}
}

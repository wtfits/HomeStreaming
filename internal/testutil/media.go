// Package testutil generates test media with ffmpeg (lavfi sources) mirroring
// the real library's naming, plus helpers for temporary data dirs.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// TreeVersion changes whenever the generated tree changes (cache key).
const TreeVersion = "v3"

// HaveFFmpeg reports whether ffmpeg and ffprobe are on PATH.
func HaveFFmpeg() bool {
	_, e1 := exec.LookPath("ffmpeg")
	_, e2 := exec.LookPath("ffprobe")
	return e1 == nil && e2 == nil
}

type spec struct {
	rel  string
	args []string // ffmpeg args after the global prefix; output path appended
}

const srtEng = "1\n00:00:01,000 --> 00:00:04,000\nHello from the English subtitle.\n\n2\n00:00:05,000 --> 00:00:09,000\nSecond English cue.\n\n3\n00:00:12,000 --> 00:00:18,000\nThird English cue.\n"

// srtRusCP1251 is a Cyrillic subtitle in Windows-1251 (not UTF-8).
var srtRusCP1251 = append([]byte("1\r\n00:00:01,000 --> 00:00:05,000\r\n"), append([]byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2, 0x20, 0xec, 0xe8, 0xf0}, []byte("\r\n\r\n")...)...) // "Привет мир"

func video(w, h, dur int) []string {
	return []string{"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=24:duration=%d", w, h, dur)}
}

func sine(freq, dur int) []string {
	return []string{"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:sample_rate=48000:duration=%d", freq, dur)}
}

func join(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var x264 = []string{"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "48"}

func h264AAC(w, h, dur int) []string {
	return join(video(w, h, dur), sine(440, dur), []string{"-map", "0:v", "-map", "1:a"}, x264, []string{"-c:a", "aac", "-b:a", "96k", "-shortest"})
}

func specs() []spec {
	return []spec{
		{"Movie.Name.2019.1080p.BluRay.x264-GRP/Movie.Name.2019.1080p.BluRay.x264-GRP.mp4",
			append(h264AAC(426, 240, 90), "-movflags", "+faststart", "-metadata:s:a:0", "language=eng")},
		{"Movie.Name.2019.1080p.BluRay.x264-GRP/Sample/movie.name.2019.sample.mp4", h264AAC(160, 90, 2)},
		{"Another.Film.2018.2160p.WEB-DL.DV.HDR10.HEVC-GRP.mkv", join(video(640, 360, 20), sine(330, 20),
			[]string{"-map", "0:v", "-map", "1:a", "-vf", "format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast",
				"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:keyint=48",
				"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
				"-c:a", "aac", "-b:a", "96k", "-shortest", "-metadata:s:a:0", "language=eng"})},
		{"Third Movie (2020)/Third Movie (2020).mkv", join(video(640, 360, 30), sine(220, 30), sine(660, 30),
			[]string{"-map", "0:v", "-map", "1:a", "-map", "2:a"}, x264,
			[]string{"-c:a:0", "ac3", "-ac:a:0", "6", "-b:a:0", "384k", "-c:a:1", "aac", "-b:a:1", "96k",
				"-metadata:s:a:0", "language=rus", "-metadata:s:a:0", "title=Русский (дубляж)", "-disposition:a:0", "default",
				"-metadata:s:a:1", "language=eng", "-metadata:s:a:1", "title=English", "-disposition:a:1", "0", "-shortest"})},
		{"Fourth.Movie.2017.720p.WEBRip.VP9.webm", join(video(426, 240, 20), sine(550, 20),
			[]string{"-map", "0:v", "-map", "1:a", "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "500k", "-g", "48",
				"-c:a", "libopus", "-b:a", "64k", "-shortest"})},
		{"Widow's Bay (Season 1) WEB-DL 2160p/Widow's.Bay.S01E03.WEB-DL.2160p.RGzsRutracker.mkv", h264AAC(426, 240, 20)},
		{"Over the Garden Wall (2014) BDRemux 1080p/[07] The Ringing of the Bell.mkv", h264AAC(426, 240, 20)},
		{"Over the Garden Wall (2014) BDRemux 1080p/[08] Babes in the Wood.mkv", h264AAC(426, 240, 20)},
		{"Better Call Saul S04 2160p/Better.Call.Saul.S04E02.WEB-DL.2160p.SDR.HEVC.2xRus.Eng.mkv", join(video(426, 240, 20), sine(300, 20), sine(400, 20), sine(500, 20),
			[]string{"-f", "srt", "-i", "SUBS_ENG"},
			[]string{"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:a", "-map", "4:s"}, x264,
			[]string{"-c:a", "aac", "-b:a", "96k", "-c:s", "srt",
				"-metadata:s:a:0", "language=rus", "-metadata:s:a:0", "title=Rus (Dub)", "-disposition:a:0", "default",
				"-metadata:s:a:1", "language=und", "-metadata:s:a:1", "title=LostFilm", "-disposition:a:1", "0",
				"-metadata:s:a:2", "language=eng", "-metadata:s:a:2", "title=English", "-disposition:a:2", "0",
				"-metadata:s:s:0", "language=eng", "-shortest"})},
		{"arrested.development.s02.hdtv.720.rus.eng.novafilm.tv/arrested.development.s02e02.hdtv.720.rus.eng.novafilm.tv.mkv", h264AAC(426, 240, 20)},
		{"Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.mkv", h264AAC(426, 240, 20)},
		{"Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E08.Rus.[Ozz].Eng.mkv", h264AAC(426, 240, 20)},
		{"Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Pluribus.S01E02.Pirate.Lady.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP.mkv", h264AAC(426, 240, 20)},
	}
}

var plainFiles = map[string][]byte{
	"Movie.Name.2019.1080p.BluRay.x264-GRP/movie.name.nfo":                                    []byte("nfo"),
	"Movie.Name.2019.1080p.BluRay.x264-GRP/RARBG.txt":                                         []byte("txt"),
	"Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.srt":     []byte(srtEng),
	"Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Subs/English.srt":                         []byte(srtEng),
	"Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E08.Rus.[Ozz].Eng.rus.srt": srtRusCP1251,
	"Downloading.Show.S01E01.1080p.mkv.!qB":                                                   []byte("partial"),
	"Other.Movie.2016.mkv.part":                                                               []byte("partial"),
	"Some DVD (1999)/VIDEO_TS/VTS_01_1.VOB":                                                   []byte("vob"),
	"Some DVD (1999)/VIDEO_TS/VIDEO_TS.IFO":                                                   []byte("ifo"),
	"Some Game/game.exe":                                                                      []byte("exe"),
	"Some Game/UnityPlayer.dll":                                                               []byte("dll"),
	"Some Game/data.assets":                                                                   []byte("assets"),
	"Some Game/readme":                                                                        []byte("readme"),
	"archive.zip":                                                                             []byte("zip"),
	"NOEXT":                                                                                   []byte("noext"),
}

var images = []struct{ rel, color string }{
	{"Movie.Name.2019.1080p.BluRay.x264-GRP/banner.jpg", "yellow"},
	{"Third Movie (2020)/poster.jpg", "red"},
	{"Third Movie (2020)/fanart.jpg", "blue"},
}

// MakeTree generates the test library under dir.
func MakeTree(dir string) error {
	tmp, err := os.MkdirTemp("", "hs-srt")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	srtPath := filepath.Join(tmp, "eng.srt")
	if err := os.WriteFile(srtPath, []byte(srtEng), 0o644); err != nil {
		return err
	}
	for _, s := range specs() {
		out := filepath.Join(dir, filepath.FromSlash(s.rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
		for _, a := range s.args {
			if a == "SUBS_ENG" {
				a = srtPath
			}
			args = append(args, a)
		}
		args = append(args, out)
		if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg %s: %v: %s", s.rel, err, strings.TrimSpace(string(b)))
		}
	}
	for rel, data := range plainFiles {
		out := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
	}
	for _, im := range images {
		out := filepath.Join(dir, filepath.FromSlash(im.rel))
		size := "400x600"
		if strings.Contains(im.rel, "fanart") || strings.Contains(im.rel, "banner") {
			size = "1280x720"
		}
		if b, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c="+im.color+":s="+size,
			"-frames:v", "1", out).CombinedOutput(); err != nil {
			return fmt.Errorf("image %s: %v: %s", im.rel, err, b)
		}
	}
	return os.WriteFile(filepath.Join(dir, ".hs-testmedia-"+TreeVersion), nil, 0o644)
}

var (
	cacheOnce sync.Once
	cacheDir  string
	cacheErr  error
)

// CachedTree returns a directory containing the generated tree, generating it
// once per machine (under the OS temp dir) and reusing it across test runs.
// Tests must copy it (CopyTree) before mutating it.
func CachedTree() (string, error) {
	cacheOnce.Do(func() {
		cacheDir = filepath.Join(os.TempDir(), "homestreaming-testmedia-"+TreeVersion)
		if _, err := os.Stat(filepath.Join(cacheDir, ".hs-testmedia-"+TreeVersion)); err == nil {
			return
		}
		tmp := cacheDir + fmt.Sprintf(".tmp%d", os.Getpid())
		_ = os.RemoveAll(tmp)
		if cacheErr = MakeTree(tmp); cacheErr != nil {
			return
		}
		_ = os.RemoveAll(cacheDir)
		cacheErr = os.Rename(tmp, cacheDir)
	})
	return cacheDir, cacheErr
}

// CopyTree copies src into dst (files are hard-linked when possible).
func CopyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.HasPrefix(info.Name(), ".hs-testmedia") {
			return nil
		}
		if err := os.Link(p, target); err == nil {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
}

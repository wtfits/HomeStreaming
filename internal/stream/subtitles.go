package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wtfits/homestreaming/internal/ffmpeg"
)

// ErrNotText is returned for image-based subtitle tracks.
var ErrNotText = errors.New("subtitle track is not text-based")

// Subtitles converts subtitle tracks to WebVTT with an on-disk cache.
type Subtitles struct {
	FFmpeg   func() string
	CacheDir string // <data>/cache/subtitles
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
}

func (s *Subtitles) lock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	if s.locks[key] == nil {
		s.locks[key] = &sync.Mutex{}
	}
	return s.locks[key]
}

// SubtitleSource identifies what to convert.
type SubtitleSource struct {
	ItemID    string
	TrackID   string // e<idx> | x<id>
	Input     string // media file (embedded) or subtitle file (external)
	Index     int    // embedded stream index; -1 for external
	Language  string
	SourceMod time.Time
}

// VTT returns WebVTT for src shifted earlier by offsetMs.
func (s *Subtitles) VTT(ctx context.Context, src SubtitleSource, offsetMs int64) ([]byte, error) {
	ff := s.FFmpeg()
	if ff == "" {
		return nil, errors.New("ffmpeg missing")
	}
	cache := filepath.Join(s.CacheDir, fmt.Sprintf("%s-%s.vtt", src.ItemID, src.TrackID))
	l := s.lock(cache)
	l.Lock()
	defer l.Unlock()
	data, err := os.ReadFile(cache)
	if st, serr := os.Stat(cache); err != nil || serr != nil || st.ModTime().Before(src.SourceMod) {
		data, err = s.convert(ctx, ff, src)
		if err != nil {
			return nil, err
		}
		_ = os.MkdirAll(s.CacheDir, 0o755)
		_ = os.WriteFile(cache, data, 0o644)
	}
	if offsetMs > 0 {
		return ShiftVTT(data, offsetMs), nil
	}
	return data, nil
}

func (s *Subtitles) convert(ctx context.Context, ff string, src SubtitleSource) ([]byte, error) {
	charenc := ""
	if src.Index < 0 {
		raw, err := os.ReadFile(src.Input)
		if err != nil {
			return nil, err
		}
		raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
		if !utf8.Valid(raw) {
			charenc = guessCharset(raw, src.Language)
		}
	}
	run := func(enc string) ([]byte, error) {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		cmd := ffmpeg.Command(cctx, ff, ffmpeg.BuildSubtitleVTT(src.Input, src.Index, 0, enc)...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("subtitle conversion: %v: %s", err, strings.TrimSpace(errb.String()))
		}
		if !bytes.HasPrefix(bytes.TrimSpace(out.Bytes()), []byte("WEBVTT")) {
			return nil, errors.New("subtitle conversion produced no WebVTT")
		}
		return out.Bytes(), nil
	}
	out, err := run(charenc)
	if err != nil && charenc == "" && src.Index < 0 {
		alt := "WINDOWS-1252"
		if src.Language == "rus" || src.Language == "ukr" {
			alt = "WINDOWS-1251"
		}
		out, err = run(alt)
	}
	return out, err
}

// guessCharset picks a legacy codepage for non-UTF-8 subtitles.
func guessCharset(raw []byte, lang string) string {
	if lang == "rus" || lang == "ukr" {
		return "WINDOWS-1251"
	}
	if lang == "tur" {
		return "WINDOWS-1254"
	}
	// Many bytes in 0xC0–0xFF with few ASCII letters around → Cyrillic.
	hi := 0
	for _, b := range raw {
		if b >= 0xC0 {
			hi++
		}
	}
	if hi > len(raw)/10 {
		return "WINDOWS-1251"
	}
	return "WINDOWS-1252"
}

var cueTimeRe = regexp.MustCompile(`^((?:\d+:)?\d{2}:\d{2}\.\d{3})\s+-->\s+((?:\d+:)?\d{2}:\d{2}\.\d{3})(.*)$`)

func parseVTTTime(s string) int64 {
	parts := strings.Split(s, ":")
	var h, m int64
	var sec float64
	switch len(parts) {
	case 3:
		h, _ = strconv.ParseInt(parts[0], 10, 64)
		m, _ = strconv.ParseInt(parts[1], 10, 64)
		sec, _ = strconv.ParseFloat(parts[2], 64)
	case 2:
		m, _ = strconv.ParseInt(parts[0], 10, 64)
		sec, _ = strconv.ParseFloat(parts[1], 64)
	}
	return h*3600000 + m*60000 + int64(sec*1000+0.5)
}

func formatVTTTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
}

// ShiftVTT moves every cue earlier by offsetMs and drops cues that end before 0.
func ShiftVTT(data []byte, offsetMs int64) []byte {
	blocks := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n")
	var out []string
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		keep := true
		for i, l := range lines {
			m := cueTimeRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			start, end := parseVTTTime(m[1])-offsetMs, parseVTTTime(m[2])-offsetMs
			if end <= 0 {
				keep = false
				break
			}
			lines[i] = formatVTTTime(start) + " --> " + formatVTTTime(end) + m[3]
		}
		if keep {
			out = append(out, strings.Join(lines, "\n"))
		}
	}
	return []byte(strings.Join(out, "\n\n"))
}

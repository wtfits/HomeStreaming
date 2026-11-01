package ffmpeg

import (
	"context"
	"path/filepath"
	"sync"
	"time"
)

// Toolbox holds the located binaries and the capability detector; it can be
// re-located at runtime (setup wizard, "check again", admin path change).
type Toolbox struct {
	Opts LocateOptions
	// HWAccel / HWDecode configure the detector.
	HWAccel  string
	HWDecode bool
	CacheDir string

	mu       sync.RWMutex
	bin      Binaries
	detector *Detector
	lastTry  time.Time
}

// Locate (re)resolves the binaries and runs capability detection.
func (t *Toolbox) Locate(ctx context.Context) Binaries {
	b := Locate(ctx, t.Opts)
	d := &Detector{FFmpeg: b.FFmpeg, Configured: t.HWAccel, HWDecodeOK: t.HWDecode, CacheDir: filepath.Join(t.CacheDir, "selftest")}
	if b.Found {
		d.Detect(ctx)
	}
	t.mu.Lock()
	t.bin, t.detector, t.lastTry = b, d, time.Now()
	t.mu.Unlock()
	return b
}

// MaybeRelocate re-runs Locate when ffmpeg is missing (at most every 5 s).
// It returns true when ffmpeg became available.
func (t *Toolbox) MaybeRelocate(ctx context.Context) bool {
	t.mu.RLock()
	found, last := t.bin.Found, t.lastTry
	t.mu.RUnlock()
	if found || time.Since(last) < 5*time.Second {
		return false
	}
	return t.Locate(ctx).Found
}

// SetPaths configures explicit paths and re-locates.
func (t *Toolbox) SetPaths(ctx context.Context, ffmpegPath, ffprobePath string) Binaries {
	t.mu.Lock()
	t.Opts.ConfigFFmpeg, t.Opts.ConfigFFprobe = ffmpegPath, ffprobePath
	t.mu.Unlock()
	return t.Locate(ctx)
}

// Binaries returns the current binaries.
func (t *Toolbox) Binaries() Binaries {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.bin
}

// FFmpeg returns the ffmpeg path ("" when missing).
func (t *Toolbox) FFmpeg() string { return t.Binaries().FFmpeg }

// FFprobe returns the ffprobe path ("" when missing).
func (t *Toolbox) FFprobe() string { return t.Binaries().FFprobe }

// Caps returns the current capabilities.
func (t *Toolbox) Caps() *Capabilities {
	t.mu.RLock()
	d := t.detector
	t.mu.RUnlock()
	if d == nil {
		return (&Detector{Configured: t.HWAccel}).Current()
	}
	return d.Current()
}

// RetryHW re-runs hardware detection when a backend was wanted but failed.
func (t *Toolbox) RetryHW(ctx context.Context) {
	t.mu.RLock()
	d := t.detector
	t.mu.RUnlock()
	if d != nil && d.NeedsRetry() {
		d.Detect(ctx)
	}
}

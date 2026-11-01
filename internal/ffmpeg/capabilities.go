package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SelfTest is one hardware capability check.
type SelfTest struct {
	Backend string `json:"backend"`
	Test    string `json:"test"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
}

// Capabilities is the result of encoder listing and hardware self-tests.
type Capabilities struct {
	Configured string          `json:"configured"`
	Active     string          `json:"active"` // nvenc|qsv|amf|vaapi|videotoolbox|none
	HWDecode   map[string]bool `json:"hwDecode"`
	SelfTests  []SelfTest      `json:"selfTests"`
	Encoders   []string        `json:"-"`
	CheckedAt  time.Time       `json:"checkedAt"`
	encoderSet map[string]bool
}

// HasEncoder reports whether ffmpeg lists the encoder.
func (c *Capabilities) HasEncoder(name string) bool { return c != nil && c.encoderSet[name] }

// H264Encoder returns the active H.264 encoder name.
func (c *Capabilities) H264Encoder() string {
	if c == nil {
		return "libx264"
	}
	switch c.Active {
	case "nvenc":
		return "h264_nvenc"
	case "qsv":
		return "h264_qsv"
	case "amf":
		return "h264_amf"
	case "vaapi":
		return "h264_vaapi"
	case "videotoolbox":
		return "h264_videotoolbox"
	}
	return "libx264"
}

// Backends in auto preference order with their H.264 encoder.
var autoOrder = []struct{ backend, encoder string }{
	{"nvenc", "h264_nvenc"}, {"qsv", "h264_qsv"}, {"amf", "h264_amf"}, {"vaapi", "h264_vaapi"}, {"videotoolbox", "h264_videotoolbox"},
}

// ListEncoders parses `ffmpeg -encoders`.
func ListEncoders(ctx context.Context, ffmpeg string) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := Command(cctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return nil, err
	}
	var encs []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	started := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "------") {
			started = true
			continue
		}
		if !started {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && len(f[0]) == 6 {
			encs = append(encs, f[1])
		}
	}
	return encs, nil
}

// Detector runs and caches capability detection.
type Detector struct {
	FFmpeg     string
	Configured string // transcode.hwaccel
	HWDecodeOK bool   // transcode.hw_decode
	CacheDir   string // <data>/cache/selftest

	// run is injectable for tests; returns (ok, detail).
	run func(ctx context.Context, args ...string) (bool, string)

	mu   sync.RWMutex
	caps *Capabilities
}

// Current returns the last detection result (never nil after Detect).
func (d *Detector) Current() *Capabilities {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.caps == nil {
		return &Capabilities{Configured: d.Configured, Active: "none", HWDecode: map[string]bool{}, encoderSet: map[string]bool{}}
	}
	return d.caps
}

func (d *Detector) runFF(ctx context.Context, args ...string) (bool, string) {
	if d.run != nil {
		return d.run(ctx, args...)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := Command(cctx, d.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	detail := strings.TrimSpace(stderr.String())
	if len(detail) > 300 {
		detail = detail[len(detail)-300:]
	}
	if err != nil && detail == "" {
		detail = err.Error()
	}
	return err == nil, detail
}

// Detect lists encoders and runs self-tests for the configured backend(s).
func (d *Detector) Detect(ctx context.Context) *Capabilities {
	caps := &Capabilities{Configured: d.Configured, Active: "none", HWDecode: map[string]bool{}, encoderSet: map[string]bool{}, CheckedAt: time.Now()}
	if d.FFmpeg == "" {
		d.store(caps)
		return caps
	}
	encs, _ := ListEncoders(ctx, d.FFmpeg)
	caps.Encoders = encs
	for _, e := range encs {
		caps.encoderSet[e] = true
	}

	var candidates []struct{ backend, encoder string }
	switch d.Configured {
	case "none":
	case "auto", "":
		candidates = autoOrder
	default:
		for _, b := range autoOrder {
			if b.backend == d.Configured {
				candidates = append(candidates, b)
			}
		}
	}

	for _, c := range candidates {
		if !caps.encoderSet[c.encoder] {
			if d.Configured != "auto" {
				caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: c.backend, Test: "encoder_listed", OK: false, Detail: c.encoder + " not compiled into this ffmpeg"})
			}
			continue
		}
		args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=10", "-frames:v", "3"}
		if c.backend == "vaapi" {
			args = append([]string{"-vaapi_device", "/dev/dri/renderD128"}, args...)
			args = append(args, "-vf", "format=nv12,hwupload")
		}
		args = append(args, "-c:v", c.encoder, "-f", "null", "-")
		ok, detail := d.runFF(ctx, args...)
		caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: c.backend, Test: "encode_h264", OK: ok, Detail: detail})
		if !ok {
			continue
		}
		caps.Active = c.backend
		if c.backend == "nvenc" {
			if caps.encoderSet["hevc_nvenc"] {
				ok, detail := d.runFF(ctx, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=10", "-frames:v", "3",
					"-pix_fmt", "p010le", "-c:v", "hevc_nvenc", "-profile:v", "main10", "-f", "null", "-")
				caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: "nvenc", Test: "encode_hevc_main10", OK: ok, Detail: detail})
				if !ok {
					delete(caps.encoderSet, "hevc_nvenc")
				}
			}
			if d.HWDecodeOK {
				d.decodeTests(ctx, caps)
			}
		}
		break
	}
	d.store(caps)
	return caps
}

func (d *Detector) decodeTests(ctx context.Context, caps *Capabilities) {
	if d.CacheDir == "" {
		return
	}
	_ = os.MkdirAll(d.CacheDir, 0o755)
	samples := []struct{ codec, encoder, pixfmt, file string }{
		{"h264", "libx264", "yuv420p", "h264.mp4"},
		{"hevc", "libx265", "yuv420p10le", "hevc10.mp4"},
	}
	for _, s := range samples {
		path := filepath.Join(d.CacheDir, s.file)
		if !isFile(path) {
			if !caps.encoderSet[s.encoder] {
				caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: "nvenc", Test: "decode_" + s.codec, OK: false, Detail: "skipped: " + s.encoder + " unavailable to create a sample"})
				continue
			}
			ok, detail := d.runFF(ctx, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=10", "-frames:v", "10",
				"-pix_fmt", s.pixfmt, "-c:v", s.encoder, path)
			if !ok {
				caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: "nvenc", Test: "decode_" + s.codec, OK: false, Detail: "sample: " + detail})
				continue
			}
		}
		ok, detail := d.runFF(ctx, "-v", "error", "-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-i", path, "-f", "null", "-")
		caps.SelfTests = append(caps.SelfTests, SelfTest{Backend: "nvenc", Test: "decode_" + s.codec, OK: ok, Detail: detail})
		caps.HWDecode[s.codec] = ok
	}
}

func (d *Detector) store(c *Capabilities) {
	d.mu.Lock()
	d.caps = c
	d.mu.Unlock()
}

// NeedsRetry reports whether a hardware backend was wanted but none passed.
func (d *Detector) NeedsRetry() bool {
	c := d.Current()
	return d.FFmpeg != "" && d.Configured != "none" && c.Active == "none" && len(c.SelfTests) > 0
}

// SetRunner replaces the command runner (tests).
func (d *Detector) SetRunner(fn func(ctx context.Context, args ...string) (bool, string)) { d.run = fn }

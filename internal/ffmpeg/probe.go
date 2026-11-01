package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProbeOutput is the subset of `ffprobe -print_format json` we use.
type ProbeOutput struct {
	Streams  []ProbeStream  `json:"streams"`
	Format   ProbeFormat    `json:"format"`
	Chapters []ProbeChapter `json:"chapters"`
}

type ProbeStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Profile          string            `json:"profile"`
	Level            int               `json:"level"`
	CodecTagString   string            `json:"codec_tag_string"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixFmt           string            `json:"pix_fmt"`
	FieldOrder       string            `json:"field_order"`
	RFrameRate       string            `json:"r_frame_rate"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	ColorSpace       string            `json:"color_space"`
	BitRate          string            `json:"bit_rate"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	SampleRate       string            `json:"sample_rate"`
	Disposition      map[string]int    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideDataList     []map[string]any  `json:"side_data_list"`
}

type ProbeFormat struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Size       string            `json:"size"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type ProbeChapter struct {
	ID        int64             `json:"id"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

// Probe runs ffprobe on file. The returned raw JSON is stored for re-deriving.
func Probe(ctx context.Context, ffprobe, file string) (*ProbeOutput, []byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := Command(cctx, ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", "--", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, nil, fmt.Errorf("ffprobe: %s", msg)
	}
	var out ProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, nil, fmt.Errorf("ffprobe json: %w", err)
	}
	return &out, stdout.Bytes(), nil
}

// ParseProbeJSON decodes stored probe JSON.
func ParseProbeJSON(b []byte) (*ProbeOutput, error) {
	var out ProbeOutput
	err := json.Unmarshal(b, &out)
	return &out, err
}

// ParseRate converts "24000/1001" to 23.976.
func ParseRate(s string) float64 {
	if s == "" || s == "0/0" {
		return 0
	}
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, err1 := strconv.ParseFloat(num, 64)
		d, err2 := strconv.ParseFloat(den, 64)
		if err1 != nil || err2 != nil || d == 0 {
			return 0
		}
		return n / d
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// ParseFloat parses a decimal string, 0 on error.
func ParseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// ParseInt parses an integer string, 0 on error.
func ParseInt(s string) int64 {
	i, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return i
}

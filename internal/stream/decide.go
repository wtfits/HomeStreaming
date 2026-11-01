package stream

import (
	"math"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
)

// HWInfo summarizes what the transcoder can use.
type HWInfo struct {
	Backend     string // nvenc|qsv|amf|vaapi|videotoolbox|none
	H264Encoder string // h264_nvenc | libx264 | ...
	HEVCNVENC   bool   // hevc_nvenc usable (HDR passthrough)
	HWDecode    map[string]bool
}

// Input is everything the decision engine looks at.
type Input struct {
	Item           *db.Item
	Streams        []db.MediaStream
	AudioIndex     int // selected audio stream index, -1 = none
	Profile        Profile
	MaxBitrateKbps int // applied cap (0 = unlimited)
	HW             HWInfo
	AudioCodecPref string // auto|aac|ac3
}

// VideoPlan describes the video output.
type VideoPlan struct {
	Action      string  `json:"action"` // copy|encode
	Codec       string  `json:"codec"`
	Encoder     string  `json:"encoder,omitempty"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	MaxrateKbps int     `json:"maxrateKbps"`
	HDR         bool    `json:"hdr"`
	Tonemap     bool    `json:"tonemap"`
	HWDecode    bool    `json:"-"`
	Level       string  `json:"-"`
	Transfer    string  `json:"-"`
	Deinterlace bool    `json:"-"`
	FPS         float64 `json:"-"`
}

// AudioPlan describes the audio output.
type AudioPlan struct {
	Action      string `json:"action"` // copy|encode|none
	Codec       string `json:"codec"`
	Channels    int    `json:"channels"`
	BitrateKbps int    `json:"bitrateKbps"`
	Index       int    `json:"-"`
}

// Decision is the decision engine's output.
type Decision struct {
	Method  string   `json:"method"` // direct|remux|transcode
	Reasons []string `json:"reasons"`
	Video   VideoPlan
	Audio   AudioPlan
}

// fmp4AudioCopy lists codecs that can be copied into fMP4 HLS segments.
var fmp4AudioCopy = []string{"aac", "mp3", "ac3", "eac3", "flac", "alac"}

func addReason(rs []string, r string) []string {
	for _, x := range rs {
		if x == r {
			return rs
		}
	}
	return append(rs, r)
}

// VideoStream returns the primary video stream.
func VideoStream(streams []db.MediaStream) *db.MediaStream {
	for i := range streams {
		if streams[i].Type == "video" {
			return &streams[i]
		}
	}
	return nil
}

// StreamByIndex returns the stream with ffprobe index idx.
func StreamByIndex(streams []db.MediaStream, idx int) *db.MediaStream {
	for i := range streams {
		if streams[i].Index == idx {
			return &streams[i]
		}
	}
	return nil
}

func firstAudio(streams []db.MediaStream) *db.MediaStream {
	for i := range streams {
		if streams[i].Type == "audio" {
			return &streams[i]
		}
	}
	return nil
}

// sourceHDRKind returns the HDR kind for capability matching ("" for SDR).
func sourceHDRKind(v *db.MediaStream) string {
	switch {
	case v.HDR == "dolbyvision" && v.DVProfile == 5:
		return "dv5"
	case v.HDR == "dolbyvision":
		// Profile 8.x / 7: HDR10 (or HLG for 8.4) compatible base layer.
		if v.ColorTransfer == "arib-std-b67" {
			return "hlg"
		}
		return "hdr10"
	}
	return v.HDR
}

// videoChecks returns the reasons the client cannot decode v (empty = OK).
func videoChecks(p *Profile, v *db.MediaStream) []string {
	var rs []string
	c := p.videoCap(v.Codec)
	if c == nil {
		return []string{"video_codec"}
	}
	depth := v.BitDepth
	if depth == 0 {
		depth = 8
	}
	if (c.MaxBitDepth > 0 && depth > c.MaxBitDepth) || (v.Codec == "h264" && depth > 8) {
		rs = addReason(rs, "video_bit_depth")
	}
	if c.MaxLevel > 0 && v.Level > c.MaxLevel {
		rs = addReason(rs, "video_profile_level")
	}
	if (c.MaxWidth > 0 && v.Width > c.MaxWidth) || (c.MaxHeight > 0 && v.Height > c.MaxHeight) {
		rs = addReason(rs, "video_resolution")
	}
	switch kind := sourceHDRKind(v); kind {
	case "":
	case "dv5":
		rs = addReason(rs, "dolby_vision_profile")
	default:
		if !contains(c.HDR, kind) && !(kind == "hdr10" && contains(c.HDR, "dolbyvision") && v.HDR == "dolbyvision") {
			rs = addReason(rs, "hdr_unsupported")
		}
	}
	return rs
}

// Decide runs the decision engine (DESIGN §6.3).
func Decide(in Input) Decision {
	d := Decision{Audio: AudioPlan{Action: "none", Index: -1}}
	p := &in.Profile
	v := VideoStream(in.Streams)
	if v == nil {
		d.Method = "transcode"
		d.Reasons = []string{"video_codec"}
		return d
	}
	var audio *db.MediaStream
	if in.AudioIndex >= 0 {
		audio = StreamByIndex(in.Streams, in.AudioIndex)
	}

	vReasons := videoChecks(p, v)
	videoOK := len(vReasons) == 0
	bitrateOK := in.MaxBitrateKbps == 0 || in.Item.Bitrate <= int64(in.MaxBitrateKbps)*1000
	containerOK := contains(p.Containers, in.Item.Container)

	var reasons []string
	if p.ForceTranscode {
		reasons = append(reasons, "forced")
	}
	if !containerOK {
		reasons = addReason(reasons, "container")
	}
	if v.Codec == "hevc" && p.Platform == "ios" && v.CodecTag != "hvc1" {
		reasons = addReason(reasons, "container") // remux re-tags hev1 → hvc1
	}
	for _, r := range vReasons {
		reasons = addReason(reasons, r)
	}
	if !bitrateOK {
		reasons = addReason(reasons, "bitrate")
	}
	audioOK := true
	if audio != nil {
		if !contains(p.AudioCodecs, audio.Codec) {
			audioOK = false
			reasons = addReason(reasons, "audio_codec")
		}
		if audio.Channels > p.MaxAudioChannels {
			audioOK = false
			reasons = addReason(reasons, "audio_channels")
		}
		if fa := firstAudio(in.Streams); fa != nil && fa.Index != audio.Index {
			reasons = addReason(reasons, "audio_track_not_default")
		}
	}

	if len(reasons) == 0 {
		d.Method = "direct"
		d.Reasons = []string{}
		d.Video = VideoPlan{Action: "copy", Codec: v.Codec, Width: v.Width, Height: v.Height, HDR: v.HDR != ""}
		if audio != nil {
			d.Audio = AudioPlan{Action: "copy", Codec: audio.Codec, Channels: audio.Channels, Index: audio.Index}
		}
		return d
	}

	hlsOK := contains(p.HLSVideoCodecs, v.Codec)
	if !p.ForceTranscode && videoOK && bitrateOK && hlsOK {
		d.Method = "remux"
		d.Reasons = reasons
		d.Video = VideoPlan{Action: "copy", Codec: v.Codec, Width: v.Width, Height: v.Height, HDR: v.HDR != "" && sourceHDRKind(v) != "dv5",
			Transfer: v.ColorTransfer}
		d.Audio = planAudio(in, audio, audioOK)
		return d
	}
	if videoOK && !hlsOK {
		reasons = addReason(reasons, "hls_video_codec")
	}
	d.Method = "transcode"
	d.Reasons = reasons
	d.Audio = planAudio(in, audio, audioOK)
	d.Video = planVideo(in, v, d.Audio)
	return d
}

func planAudio(in Input, a *db.MediaStream, audioOK bool) AudioPlan {
	if a == nil {
		return AudioPlan{Action: "none", Index: -1}
	}
	p := &in.Profile
	capKbps := in.MaxBitrateKbps
	roomy := capKbps == 0 || capKbps >= 10000
	srcKbps := int(a.Bitrate / 1000)
	if audioOK && contains(fmp4AudioCopy, a.Codec) && (roomy || (srcKbps > 0 && srcKbps <= 256 && a.Channels <= 2)) {
		br := srcKbps
		if br == 0 {
			br = 192
			if a.Codec == "ac3" || a.Codec == "eac3" {
				br = 640
			}
		}
		return AudioPlan{Action: "copy", Codec: a.Codec, Channels: a.Channels, BitrateKbps: br, Index: a.Index}
	}
	pref := in.AudioCodecPref
	if pref != "aac" && a.Channels > 2 && contains(p.AudioCodecs, "ac3") && roomy && p.MaxAudioChannels >= 6 {
		return AudioPlan{Action: "encode", Codec: "ac3", Channels: 6, BitrateKbps: 640, Index: a.Index}
	}
	return AudioPlan{Action: "encode", Codec: "aac", Channels: 2, BitrateKbps: 192, Index: a.Index}
}

// ladderHeight returns the max output height for a bitrate cap (0 = source).
func ladderHeight(capKbps int) int {
	switch {
	case capKbps == 0 || capKbps >= 20000:
		return 0
	case capKbps >= 10000:
		return 2160
	case capKbps >= 5000:
		return 1080
	case capKbps >= 2500:
		return 720
	case capKbps >= 1200:
		return 480
	default:
		return 360
	}
}

// fitDims scales (w,h) to fit maxH (landscape: 16:9 box), never upscaling,
// returning even dimensions.
func fitDims(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = math.Min(scale, float64(maxW)/float64(w))
	}
	if maxH > 0 && h > maxH {
		scale = math.Min(scale, float64(maxH)/float64(h))
	}
	ow := int(math.Round(float64(w)*scale/2)) * 2
	oh := int(math.Round(float64(h)*scale/2)) * 2
	if ow < 2 {
		ow = 2
	}
	if oh < 2 {
		oh = 2
	}
	return ow, oh
}

func planVideo(in Input, v *db.MediaStream, audio AudioPlan) VideoPlan {
	p := &in.Profile
	kind := sourceHDRKind(v)
	plan := VideoPlan{Action: "encode", Codec: "h264", Encoder: in.HW.H264Encoder, FPS: v.FrameRate}
	if plan.Encoder == "" {
		plan.Encoder = "libx264"
	}
	plan.Deinterlace = v.FieldOrder != "" && v.FieldOrder != "progressive" && v.FieldOrder != "unknown"

	if kind == "hdr10" || kind == "hlg" {
		hc := p.videoCap("hevc")
		if hc != nil && hc.MaxBitDepth >= 10 && contains(p.HLSVideoCodecs, "hevc") &&
			(contains(hc.HDR, kind) || (kind == "hdr10" && contains(hc.HDR, "dolbyvision") && v.HDR == "dolbyvision")) && in.HW.HEVCNVENC {
			plan.Codec, plan.Encoder, plan.HDR = "hevc", "hevc_nvenc", true
			plan.Transfer = v.ColorTransfer
			if plan.Transfer != "arib-std-b67" {
				plan.Transfer = "smpte2084"
			}
		} else {
			plan.Tonemap = true
		}
	} else if kind == "dv5" {
		plan.Tonemap = true
	}

	// Resolution: bitrate ladder, then the target codec's limits.
	maxH := ladderHeight(in.MaxBitrateKbps)
	maxW := 0
	if maxH > 0 {
		maxW = maxH * 16 / 9
	}
	if c := p.videoCap(plan.Codec); c != nil {
		if c.MaxHeight > 0 && (maxH == 0 || c.MaxHeight < maxH) {
			maxH = c.MaxHeight
		}
		if c.MaxWidth > 0 && (maxW == 0 || c.MaxWidth < maxW) {
			maxW = c.MaxWidth
		}
	}
	w, h := fitDims(v.Width, v.Height, maxW, maxH)
	if w == v.Width && h == v.Height {
		plan.Width, plan.Height = v.Width, v.Height
	} else {
		plan.Width, plan.Height = w, h
	}

	if in.MaxBitrateKbps > 0 {
		audioKbps := audio.BitrateKbps
		if audio.Action == "none" {
			audioKbps = 0
		}
		mr := int(math.Round(0.9 * float64(in.MaxBitrateKbps-audioKbps)))
		if mr < 300 {
			mr = 300
		}
		plan.MaxrateKbps = mr
	}
	if plan.Codec == "h264" {
		plan.Level = ffmpeg.H264Level(plan.Height)
	}
	plan.HWDecode = in.HW.Backend == "nvenc" && in.HW.HWDecode[v.Codec] && v.BitDepth <= 10 && !(v.Codec == "h264" && v.BitDepth > 8)
	if plan.Encoder == "libx264" || (plan.Encoder != "h264_nvenc" && plan.Encoder != "hevc_nvenc") {
		plan.HWDecode = false
	}
	return plan
}

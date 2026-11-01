package ffmpeg

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Common prefix of every ffmpeg invocation we make.
var commonPrefix = []string{"-nostdin", "-hide_banner", "-loglevel", "warning", "-y"}

// TonemapChain converts linear-light HDR (PQ/HLG, BT.2020) to BT.709 SDR 8-bit.
// When w/h are > 0 the first zscale also scales.
func TonemapChain(w, h int) string {
	first := "zscale=t=linear:npl=100"
	if w > 0 && h > 0 {
		first = fmt.Sprintf("zscale=w=%d:h=%d:t=linear:npl=100:f=spline36", w, h)
	}
	return first + ",format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
}

// VideoParams describes the video half of an HLS session.
type VideoParams struct {
	Copy    bool // remux (stream copy)
	TagHvc1 bool // re-tag HEVC as hvc1 (copy or encode)

	Encoder        string // libx264 | h264_nvenc | hevc_nvenc | h264_qsv | h264_amf | h264_vaapi | h264_videotoolbox
	HWDecode       bool   // NVDEC (-hwaccel cuda) decode + scale_cuda
	Width, Height  int    // output size; 0 = keep source size
	Deinterlace    bool
	Tonemap        bool   // HDR -> SDR
	HDRPassthrough bool   // HEVC Main10 PQ/HLG output (hevc_nvenc)
	Transfer       string // smpte2084 | arib-std-b67 (passthrough)
	MaxrateKbps    int    // 0 = quality-only (CRF/CQ)
	Preset         string // libx264 preset
	FPS            float64
	Level          string // H.264 level, e.g. "4.1"
	VAAPIDevice    string
}

// AudioParams describes the audio half; Index < 0 means no audio.
type AudioParams struct {
	Copy        bool
	Codec       string // aac | ac3
	Channels    int
	BitrateKbps int
}

// HLSParams is everything needed to build an HLS remux/transcode command.
type HLSParams struct {
	Input          string
	StartSec       float64 // -ss before -i (0 = from the start)
	VideoIndex     int
	AudioIndex     int // -1 = no audio
	Video          VideoParams
	Audio          AudioParams
	SegmentSeconds int
	StartNumber    int
	TSOffsetSec    float64 // -output_ts_offset (transcode sessions)
	Event          bool    // remux: ffmpeg writes an EVENT playlist
	Threads        int
}

// GOPSize returns the keyframe interval in frames for segment length L.
func GOPSize(fps float64, segSeconds int) int {
	if fps <= 0 || fps > 240 {
		fps = 24
	}
	g := int(math.Round(fps * float64(segSeconds)))
	if g < 1 {
		g = 1
	}
	return g
}

func secs(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// BuildHLS returns the argv (without the binary) for an HLS fMP4 session. The
// command must run with its working directory set to the session directory.
func BuildHLS(p HLSParams) []string {
	a := append([]string{}, commonPrefix...)
	v := p.Video
	cudaDecode := !v.Copy && v.HWDecode && (strings.HasSuffix(v.Encoder, "_nvenc"))
	if cudaDecode {
		a = append(a, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	}
	if v.Encoder == "h264_vaapi" {
		dev := v.VAAPIDevice
		if dev == "" {
			dev = "/dev/dri/renderD128"
		}
		a = append(a, "-vaapi_device", dev)
	}
	if p.StartSec > 0 {
		a = append(a, "-ss", secs(p.StartSec))
	}
	a = append(a, "-i", p.Input, "-map", fmt.Sprintf("0:%d", p.VideoIndex))
	if p.AudioIndex >= 0 {
		a = append(a, "-map", fmt.Sprintf("0:%d", p.AudioIndex))
	}
	a = append(a, "-sn", "-dn", "-map_chapters", "-1")

	if v.Copy {
		a = append(a, "-c:v", "copy")
		if v.TagHvc1 {
			a = append(a, "-tag:v", "hvc1")
		}
	} else {
		if f := videoFilters(v, cudaDecode); f != "" {
			a = append(a, "-vf", f)
		}
		a = append(a, encoderArgs(v, p.SegmentSeconds)...)
	}

	if p.AudioIndex >= 0 {
		a = append(a, audioArgs(p.Audio)...)
	}
	if !v.Copy && p.Threads > 0 && v.Encoder == "libx264" {
		a = append(a, "-threads", strconv.Itoa(p.Threads))
	}
	if p.TSOffsetSec > 0 {
		a = append(a, "-output_ts_offset", secs(p.TSOffsetSec))
	}
	a = append(a, "-f", "hls", "-hls_time", strconv.Itoa(p.SegmentSeconds), "-hls_list_size", "0")
	if p.Event {
		a = append(a, "-hls_playlist_type", "event")
	}
	a = append(a, "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "seg%05d.m4s", "-hls_flags", "independent_segments+temp_file",
		"-start_number", strconv.Itoa(p.StartNumber), "index.m3u8")
	return a
}

func videoFilters(v VideoParams, cudaDecode bool) string {
	scale := v.Width > 0 && v.Height > 0
	var f []string
	switch {
	case v.HDRPassthrough:
		if cudaDecode {
			if scale {
				f = append(f, fmt.Sprintf("scale_cuda=w=%d:h=%d:format=p010le", v.Width, v.Height))
			}
		} else {
			if scale {
				f = append(f, fmt.Sprintf("scale=%d:%d:flags=bicubic", v.Width, v.Height))
			}
			f = append(f, "format=p010le")
		}
	case cudaDecode && v.Tonemap:
		if scale {
			f = append(f, fmt.Sprintf("scale_cuda=w=%d:h=%d:format=p010le", v.Width, v.Height))
		} else {
			f = append(f, "scale_cuda=format=p010le")
		}
		f = append(f, "hwdownload", "format=p010le", TonemapChain(0, 0))
	case cudaDecode:
		if v.Deinterlace {
			f = append(f, "yadif_cuda")
		}
		if scale {
			f = append(f, fmt.Sprintf("scale_cuda=w=%d:h=%d:format=yuv420p", v.Width, v.Height))
		} else {
			f = append(f, "scale_cuda=format=yuv420p")
		}
	default:
		if v.Deinterlace {
			f = append(f, "yadif=mode=send_frame")
		}
		if v.Tonemap {
			if scale {
				f = append(f, TonemapChain(v.Width, v.Height))
			} else {
				f = append(f, TonemapChain(0, 0))
			}
		} else {
			if scale {
				f = append(f, fmt.Sprintf("scale=%d:%d:flags=bicubic", v.Width, v.Height))
			}
			f = append(f, "format=yuv420p")
		}
		if v.Encoder == "h264_vaapi" {
			if f[len(f)-1] == "format=yuv420p" {
				f = f[:len(f)-1]
			}
			f = append(f, "format=nv12", "hwupload")
		}
	}
	return strings.Join(f, ",")
}

func rateArgs(v VideoParams) []string {
	if v.MaxrateKbps <= 0 {
		return nil
	}
	return []string{"-maxrate", fmt.Sprintf("%dk", v.MaxrateKbps), "-bufsize", fmt.Sprintf("%dk", 2*v.MaxrateKbps)}
}

func encoderArgs(v VideoParams, segSeconds int) []string {
	g := strconv.Itoa(GOPSize(v.FPS, segSeconds))
	forceKF := fmt.Sprintf("expr:gte(t,n_forced*%d)", segSeconds)
	level := v.Level
	if level == "" {
		level = "4.1"
	}
	var a []string
	switch v.Encoder {
	case "h264_nvenc":
		a = append(a, "-c:v", "h264_nvenc", "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-cq", "23", "-b:v", "0")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-level", level, "-g", g, "-keyint_min", g, "-no-scenecut", "1", "-forced-idr", "1", "-force_key_frames", forceKF)
	case "hevc_nvenc":
		a = append(a, "-c:v", "hevc_nvenc", "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-cq", "24", "-b:v", "0")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "main10", "-tier", "main")
		if v.HDRPassthrough {
			trc := v.Transfer
			if trc == "" {
				trc = "smpte2084"
			}
			a = append(a, "-color_primaries", "bt2020", "-color_trc", trc, "-colorspace", "bt2020nc", "-color_range", "tv")
		}
		a = append(a, "-tag:v", "hvc1", "-g", g, "-keyint_min", g, "-no-scenecut", "1", "-forced-idr", "1", "-force_key_frames", forceKF)
	case "h264_qsv":
		a = append(a, "-c:v", "h264_qsv", "-preset", "medium", "-global_quality", "23", "-look_ahead", "0")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-g", g, "-force_key_frames", forceKF)
	case "h264_amf":
		a = append(a, "-c:v", "h264_amf", "-quality", "balanced", "-rc", "vbr_peak", "-qp_i", "23", "-qp_p", "23")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-g", g, "-force_key_frames", forceKF)
	case "h264_vaapi":
		a = append(a, "-c:v", "h264_vaapi", "-qp", "23")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-g", g, "-force_key_frames", forceKF)
	case "h264_videotoolbox":
		a = append(a, "-c:v", "h264_videotoolbox", "-q:v", "60")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-g", g, "-force_key_frames", forceKF)
	default: // libx264
		preset := v.Preset
		if preset == "" {
			preset = "veryfast"
		}
		a = append(a, "-c:v", "libx264", "-preset", preset, "-crf", "23")
		a = append(a, rateArgs(v)...)
		a = append(a, "-profile:v", "high", "-level", level, "-pix_fmt", "yuv420p",
			"-g", g, "-keyint_min", g, "-sc_threshold", "0", "-force_key_frames", forceKF)
	}
	return a
}

func audioArgs(au AudioParams) []string {
	if au.Copy {
		return []string{"-c:a", "copy"}
	}
	switch au.Codec {
	case "ac3":
		br := au.BitrateKbps
		if br == 0 {
			br = 640
		}
		a := []string{"-c:a", "ac3", "-b:a", fmt.Sprintf("%dk", br)}
		if au.Channels > 0 && au.Channels < 6 {
			a = append(a, "-ac", strconv.Itoa(au.Channels))
		} else {
			a = append(a, "-ac", "6")
		}
		return a
	default:
		br := au.BitrateKbps
		if br == 0 {
			br = 192
		}
		return []string{"-c:a", "aac", "-ac", "2", "-b:a", fmt.Sprintf("%dk", br)}
	}
}

// BuildThumbnail returns argv extracting one JPEG frame at t seconds.
func BuildThumbnail(input string, t float64, width int, hdr bool, out string) []string {
	vf := fmt.Sprintf("scale=%d:-2", width)
	if hdr {
		vf = TonemapChain(0, 0) + "," + vf
	}
	a := append([]string{}, commonPrefix...)
	a[3] = "error"
	return append(a, "-ss", secs(t), "-i", input, "-map", "0:v:0", "-an", "-sn", "-dn",
		"-frames:v", "1", "-vf", vf, "-q:v", "4", out)
}

// BuildSubtitleVTT returns argv converting a subtitle stream (embedded index
// >= 0) or an external subtitle file (index < 0) to WebVTT on stdout.
func BuildSubtitleVTT(input string, index int, offsetSec float64, charenc string) []string {
	a := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if charenc != "" {
		a = append(a, "-sub_charenc", charenc)
	}
	if offsetSec > 0 {
		a = append(a, "-ss", secs(offsetSec))
	}
	a = append(a, "-i", input)
	if index >= 0 {
		a = append(a, "-map", fmt.Sprintf("0:%d", index))
	} else {
		a = append(a, "-map", "0:s:0")
	}
	return append(a, "-c:s", "webvtt", "-f", "webvtt", "-")
}

// H264Level picks an H.264 level for the output height.
func H264Level(height int) string {
	switch {
	case height > 1088:
		return "5.1"
	default:
		return "4.1"
	}
}

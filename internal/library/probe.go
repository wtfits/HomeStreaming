package library

import (
	"errors"
	"math"
	"path/filepath"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
	"github.com/wtfits/homestreaming/internal/media"
)

// TextSubtitleCodecs are subtitle codecs convertible to WebVTT.
var TextSubtitleCodecs = toSet(`subrip srt ass ssa webvtt mov_text text`)

// ContainerName maps ffprobe's format_name (+ extension) to our container id.
func ContainerName(formatName, file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	f := strings.ToLower(formatName)
	switch {
	case strings.Contains(f, "matroska") || strings.Contains(f, "webm"):
		if ext == ".webm" {
			return "webm"
		}
		return "matroska"
	case strings.Contains(f, "mp4") || strings.Contains(f, "mov"):
		switch ext {
		case ".mov":
			return "mov"
		case ".m4v":
			return "m4v"
		}
		return "mp4"
	case strings.Contains(f, "mpegts"):
		return "mpegts"
	case strings.Contains(f, "avi"):
		return "avi"
	case strings.Contains(f, "asf"):
		return "asf"
	case strings.Contains(f, "flv"):
		return "flv"
	case strings.Contains(f, "mpeg"):
		return "mpeg"
	case strings.Contains(f, "ogg"):
		return "ogg"
	}
	if i := strings.Index(f, ","); i > 0 {
		return f[:i]
	}
	return f
}

func bitDepthOf(s ffmpeg.ProbeStream) int {
	pf := s.PixFmt
	switch {
	case strings.Contains(pf, "p10") || strings.Contains(pf, "10le") || strings.Contains(pf, "10be"):
		return 10
	case strings.Contains(pf, "p12") || strings.Contains(pf, "12le"):
		return 12
	}
	if b := int(ffmpeg.ParseInt(s.BitsPerRawSample)); b > 0 {
		return b
	}
	if pf == "" {
		return 0
	}
	return 8
}

func dvInfo(s ffmpeg.ProbeStream) (profile, compat int, ok bool) {
	for _, sd := range s.SideDataList {
		t, _ := sd["side_data_type"].(string)
		if !strings.Contains(strings.ToLower(t), "dovi") && !strings.Contains(strings.ToLower(t), "dolby vision") {
			continue
		}
		if p, ok := sd["dv_profile"].(float64); ok {
			profile = int(p)
		}
		if c, ok := sd["dv_bl_signal_compatibility_id"].(float64); ok {
			compat = int(c)
		}
		return profile, compat, true
	}
	return 0, 0, false
}

func hdrOf(s ffmpeg.ProbeStream) string {
	if _, _, ok := dvInfo(s); ok {
		return "dolbyvision"
	}
	switch s.ColorTransfer {
	case "smpte2084":
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	}
	return ""
}

// ErrUnplayable is returned for files without a usable video stream/duration.
var ErrUnplayable = errors.New("no video stream or duration")

// ConvertProbe converts ffprobe output into the stored probe result.
func ConvertProbe(out *ffmpeg.ProbeOutput, raw []byte, file string, size int64) (*db.ProbeResult, error) {
	res := &db.ProbeResult{ProbeJSON: string(raw), Container: ContainerName(out.Format.FormatName, file)}
	dur := ffmpeg.ParseFloat(out.Format.Duration)
	var langs []string
	seenLang := map[string]bool{}
	haveVideo := false
	for _, s := range out.Streams {
		ms := db.MediaStream{Index: s.Index, Codec: s.CodecName, Profile: s.Profile, CodecTag: s.CodecTagString,
			Language: "", Title: s.Tags["title"], IsDefault: s.Disposition["default"] == 1, IsForced: s.Disposition["forced"] == 1,
			Bitrate: ffmpeg.ParseInt(s.BitRate)}
		if ms.Bitrate == 0 {
			ms.Bitrate = ffmpeg.ParseInt(s.Tags["BPS"])
		}
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 || s.CodecName == "mjpeg" || s.CodecName == "png" {
				continue
			}
			ms.Type = "video"
			ms.Level = s.Level
			ms.Width, ms.Height = s.Width, s.Height
			ms.PixFmt = s.PixFmt
			ms.BitDepth = bitDepthOf(s)
			ms.FrameRate = math.Round(ffmpeg.ParseRate(s.AvgFrameRate)*1000) / 1000
			if ms.FrameRate == 0 {
				ms.FrameRate = math.Round(ffmpeg.ParseRate(s.RFrameRate)*1000) / 1000
			}
			ms.ColorTransfer, ms.ColorPrimaries, ms.ColorSpace = s.ColorTransfer, s.ColorPrimaries, s.ColorSpace
			ms.HDR = hdrOf(s)
			ms.DVProfile, ms.DVBLCompat, _ = dvInfo(s)
			ms.FieldOrder = s.FieldOrder
			if !haveVideo {
				haveVideo = true
				res.VideoCodec, res.Width, res.Height, res.HDR, res.BitDepth = ms.Codec, ms.Width, ms.Height, ms.HDR, ms.BitDepth
			}
		case "audio":
			ms.Type = "audio"
			ms.Channels = s.Channels
			ms.ChannelLayout = s.ChannelLayout
			ms.SampleRate = int(ffmpeg.ParseInt(s.SampleRate))
			ms.Language = media.StreamLang(s.Tags["language"], ms.Title)
			if ms.Language != "" && !seenLang[ms.Language] {
				seenLang[ms.Language] = true
				langs = append(langs, ms.Language)
			}
		case "subtitle":
			ms.Type = "subtitle"
			ms.Language = media.StreamLang(s.Tags["language"], ms.Title)
			ms.IsText = TextSubtitleCodecs[s.CodecName]
		default:
			continue
		}
		res.Streams = append(res.Streams, ms)
	}
	if !haveVideo || dur <= 0 {
		return nil, ErrUnplayable
	}
	res.RuntimeMs = int64(dur * 1000)
	res.Bitrate = ffmpeg.ParseInt(out.Format.BitRate)
	if res.Bitrate == 0 && dur > 0 && size > 0 {
		res.Bitrate = int64(float64(size*8) / dur)
	}
	res.AudioLangs = strings.Join(langs, ",")
	for i, c := range out.Chapters {
		res.Chapters = append(res.Chapters, db.Chapter{Index: i, StartMs: int64(ffmpeg.ParseFloat(c.StartTime) * 1000),
			EndMs: int64(ffmpeg.ParseFloat(c.EndTime) * 1000), Title: c.Tags["title"]})
	}
	return res, nil
}

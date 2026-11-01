// Package stream decides how an item is played (direct, remux, transcode) and
// runs HLS sessions backed by ffmpeg.
package stream

import "strings"

// VideoCodecCap describes one decodable video codec.
type VideoCodecCap struct {
	Codec       string   `json:"codec"`
	MaxLevel    int      `json:"maxLevel,omitempty"`
	MaxBitDepth int      `json:"maxBitDepth,omitempty"`
	MaxWidth    int      `json:"maxWidth,omitempty"`
	MaxHeight   int      `json:"maxHeight,omitempty"`
	HDR         []string `json:"hdr,omitempty"`
}

// Profile is the client capability profile sent with play-info.
type Profile struct {
	DeviceID         string          `json:"deviceId"`
	DeviceName       string          `json:"deviceName"`
	ClientType       string          `json:"clientType"`
	Platform         string          `json:"platform"` // ios|webos|desktop|android|other
	Engine           string          `json:"engine"`   // native|hlsjs
	Containers       []string        `json:"containers"`
	VideoCodecs      []VideoCodecCap `json:"videoCodecs"`
	AudioCodecs      []string        `json:"audioCodecs"`
	MaxAudioChannels int             `json:"maxAudioChannels"`
	HLSVideoCodecs   []string        `json:"hlsVideoCodecs"`
	// HLSAudioCodecs are the audio codecs the HLS engine accepts in fMP4
	// segments. With hls.js that is what MediaSource accepts, which can be less
	// than the <video> element plays directly (the LG webOS browser decodes AC-3
	// in files but may refuse it in MediaSource). Empty = AudioCodecs.
	HLSAudioCodecs []string `json:"hlsAudioCodecs,omitempty"`
	MaxBitrateKbps int      `json:"maxBitrateKbps"`
	ForceTranscode bool     `json:"forceTranscode"`
}

// CodecOverride adds/removes entries for a platform (admin setting).
type CodecOverride struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

// PlatformOverride is one platform's entry in profile_overrides_json.
type PlatformOverride struct {
	VideoCodecs *CodecOverride `json:"videoCodecs,omitempty"`
	AudioCodecs *CodecOverride `json:"audioCodecs,omitempty"`
	Containers  *CodecOverride `json:"containers,omitempty"`
}

var hdrAll = []string{"hdr10", "hlg", "dolbyvision"}

// BaseProfile returns the server-side base profile of a platform (DESIGN §6.2).
func BaseProfile(platform string) Profile {
	switch platform {
	case "ios":
		return Profile{Platform: "ios", Engine: "native", Containers: []string{"mp4", "mov", "m4v"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxLevel: 52, MaxBitDepth: 8, MaxWidth: 3840, MaxHeight: 2160},
				{Codec: "hevc", MaxLevel: 153, MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160, HDR: hdrAll},
			},
			AudioCodecs: []string{"aac", "mp3", "ac3", "eac3", "alac", "flac"}, MaxAudioChannels: 6, HLSVideoCodecs: []string{"h264", "hevc"}}
	case "webos":
		return Profile{Platform: "webos", Engine: "hlsjs", Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxLevel: 51, MaxBitDepth: 8, MaxWidth: 3840, MaxHeight: 2160},
				{Codec: "hevc", MaxLevel: 153, MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160, HDR: hdrAll},
			},
			AudioCodecs: []string{"aac", "mp3", "ac3", "eac3"}, MaxAudioChannels: 6, HLSVideoCodecs: []string{"h264", "hevc"}}
	case "desktop", "android":
		return Profile{Platform: platform, Engine: "hlsjs", Containers: []string{"mp4", "webm", "matroska"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxLevel: 51, MaxBitDepth: 8, MaxWidth: 3840, MaxHeight: 2160},
				{Codec: "vp9", MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160},
				{Codec: "av1", MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160},
			},
			AudioCodecs: []string{"aac", "mp3", "opus", "flac", "vorbis"}, MaxAudioChannels: 6, HLSVideoCodecs: []string{"h264"}}
	default:
		return Profile{Platform: "other", Engine: "hlsjs", Containers: []string{"mp4"},
			VideoCodecs:      []VideoCodecCap{{Codec: "h264", MaxLevel: 41, MaxBitDepth: 8, MaxWidth: 1920, MaxHeight: 1080}},
			AudioCodecs:      []string{"aac", "mp3"},
			MaxAudioChannels: 2, HLSVideoCodecs: []string{"h264"}}
	}
}

// Normalize fills missing fields from the base profile of the platform and
// lowercases codec names.
func (p *Profile) Normalize() {
	switch p.Platform {
	case "ios", "webos", "desktop", "android":
	default:
		p.Platform = "other"
	}
	base := BaseProfile(p.Platform)
	if p.Engine != "native" && p.Engine != "hlsjs" {
		p.Engine = base.Engine
	}
	if len(p.Containers) == 0 {
		p.Containers = base.Containers
	}
	if len(p.VideoCodecs) == 0 {
		p.VideoCodecs = base.VideoCodecs
	}
	if len(p.AudioCodecs) == 0 {
		p.AudioCodecs = base.AudioCodecs
	}
	if p.MaxAudioChannels <= 0 {
		p.MaxAudioChannels = base.MaxAudioChannels
	}
	if len(p.HLSVideoCodecs) == 0 {
		p.HLSVideoCodecs = base.HLSVideoCodecs
	}
	if len(p.HLSAudioCodecs) == 0 {
		p.HLSAudioCodecs = append([]string(nil), p.AudioCodecs...)
	}
	for i := range p.Containers {
		p.Containers[i] = strings.ToLower(p.Containers[i])
	}
	for i := range p.AudioCodecs {
		p.AudioCodecs[i] = strings.ToLower(p.AudioCodecs[i])
	}
	for i := range p.HLSVideoCodecs {
		p.HLSVideoCodecs[i] = strings.ToLower(p.HLSVideoCodecs[i])
	}
	for i := range p.HLSAudioCodecs {
		p.HLSAudioCodecs[i] = strings.ToLower(p.HLSAudioCodecs[i])
	}
	for i := range p.VideoCodecs {
		p.VideoCodecs[i].Codec = strings.ToLower(p.VideoCodecs[i].Codec)
	}
}

// ApplyOverride applies an admin override to the profile.
func (p *Profile) ApplyOverride(o PlatformOverride) {
	if o.AudioCodecs != nil {
		p.AudioCodecs = applyList(p.AudioCodecs, *o.AudioCodecs)
		if len(p.HLSAudioCodecs) > 0 {
			p.HLSAudioCodecs = applyList(p.HLSAudioCodecs, CodecOverride{Remove: o.AudioCodecs.Remove})
		}
	}
	if o.Containers != nil {
		p.Containers = applyList(p.Containers, *o.Containers)
	}
	if o.VideoCodecs != nil {
		names := make([]string, 0, len(p.VideoCodecs))
		for _, v := range p.VideoCodecs {
			names = append(names, v.Codec)
		}
		keep := applyList(names, *o.VideoCodecs)
		var out []VideoCodecCap
		for _, n := range keep {
			found := false
			for _, v := range p.VideoCodecs {
				if v.Codec == n {
					out = append(out, v)
					found = true
					break
				}
			}
			if !found {
				out = append(out, VideoCodecCap{Codec: n, MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160})
			}
		}
		p.VideoCodecs = out
	}
}

func applyList(list []string, o CodecOverride) []string {
	rm := map[string]bool{}
	for _, r := range o.Remove {
		rm[strings.ToLower(r)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, x := range list {
		if !rm[x] && !seen[x] {
			out = append(out, x)
			seen[x] = true
		}
	}
	for _, a := range o.Add {
		a = strings.ToLower(a)
		if !seen[a] {
			out = append(out, a)
			seen[a] = true
		}
	}
	return out
}

// hlsAudio returns the audio codecs usable in HLS segments.
func (p *Profile) hlsAudio() []string {
	if len(p.HLSAudioCodecs) > 0 {
		return p.HLSAudioCodecs
	}
	return p.AudioCodecs
}

func (p *Profile) videoCap(codec string) *VideoCodecCap {
	for i := range p.VideoCodecs {
		if p.VideoCodecs[i].Codec == codec {
			return &p.VideoCodecs[i]
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

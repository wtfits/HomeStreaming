package stream

import (
	"fmt"
	"math"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
)

// CodecString returns the RFC 6381 CODECS entry for the video output ("" if unknown).
func VideoCodecString(plan VideoPlan, src *db.MediaStream) string {
	if plan.Action == "encode" {
		switch plan.Codec {
		case "h264":
			if plan.Level == "5.1" {
				return "avc1.640033"
			}
			return "avc1.640029"
		case "hevc":
			lvl := 150
			if plan.Height <= 1088 {
				lvl = 123
			}
			return fmt.Sprintf("hvc1.2.4.L%d.B0", lvl)
		}
		return ""
	}
	if src == nil {
		return ""
	}
	switch src.Codec {
	case "h264":
		pp := map[string]string{"Baseline": "42", "Constrained Baseline": "42", "Main": "4d", "High": "64", "High 10": "6e"}[src.Profile]
		if pp == "" || src.Level <= 0 {
			return ""
		}
		return fmt.Sprintf("avc1.%s00%02x", pp, src.Level)
	case "hevc":
		if src.Level <= 0 {
			return ""
		}
		if src.Profile == "Main 10" || src.BitDepth == 10 {
			return fmt.Sprintf("hvc1.2.4.L%d.B0", src.Level)
		}
		return fmt.Sprintf("hvc1.1.6.L%d.B0", src.Level)
	case "av1":
		return ""
	}
	return ""
}

// AudioCodecString returns the CODECS entry for the audio output.
func AudioCodecString(a AudioPlan) string {
	switch a.Codec {
	case "aac":
		return "mp4a.40.2"
	case "mp3":
		return "mp4a.40.34"
	case "ac3":
		return "ac-3"
	case "eac3":
		return "ec-3"
	case "flac":
		return "fLaC"
	case "alac":
		return "alac"
	}
	return ""
}

// MediaPlaylist builds the predictive VOD playlist of a transcode session.
func MediaPlaylist(runtimeMs int64, segSeconds int, suffix string) string {
	n := SegmentCount(runtimeMs, segSeconds)
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", segSeconds)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"init.mp4%s\"\n", suffix)
	total := float64(runtimeMs) / 1000
	for i := 0; i < n; i++ {
		d := float64(segSeconds)
		if i == n-1 {
			d = total - float64(segSeconds*(n-1))
			if d <= 0 {
				d = float64(segSeconds)
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\nseg%05d.m4s%s\n", d, i, suffix)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// SegmentCount returns ceil(runtime / L).
func SegmentCount(runtimeMs int64, segSeconds int) int {
	if runtimeMs <= 0 {
		return 1
	}
	return int(math.Ceil(float64(runtimeMs) / 1000 / float64(segSeconds)))
}

// SubtitleRendition is an in-band subtitle entry of the master playlist.
type SubtitleRendition struct {
	TrackID  string
	Name     string
	Language string // BCP-47
	Default  bool
	Forced   bool
}

// MasterPlaylist builds the single-variant master playlist.
func MasterPlaylist(bandwidth, avgBandwidth int64, codecs string, width, height int, videoRange string, subs []SubtitleRendition, suffix string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, s := range subs {
		def, auto := "NO", "YES"
		if s.Default {
			def = "YES"
		}
		forced := "NO"
		if s.Forced {
			forced = "YES"
		}
		lang := ""
		if s.Language != "" {
			lang = fmt.Sprintf(",LANGUAGE=\"%s\"", s.Language)
		}
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"%s\"%s,DEFAULT=%s,AUTOSELECT=%s,FORCED=%s,URI=\"sub/%s.m3u8%s\"\n",
			strings.ReplaceAll(s.Name, "\"", "'"), lang, def, auto, forced, s.TrackID, suffix)
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d", bandwidth, avgBandwidth)
	if codecs != "" {
		fmt.Fprintf(&b, ",CODECS=\"%s\"", codecs)
	}
	if width > 0 && height > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", width, height)
	}
	if videoRange != "" {
		fmt.Fprintf(&b, ",VIDEO-RANGE=%s", videoRange)
	}
	if len(subs) > 0 {
		b.WriteString(",SUBTITLES=\"subs\"")
	}
	fmt.Fprintf(&b, "\nindex.m3u8%s\n", suffix)
	return b.String()
}

// SubtitlePlaylist is a one-segment VOD playlist referencing the full WebVTT.
func SubtitlePlaylist(trackID string, runtimeMs int64, suffix string) string {
	secs := float64(runtimeMs) / 1000
	return fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:%.6f,\n%s.vtt%s\n#EXT-X-ENDLIST\n",
		int(math.Ceil(secs)), secs, trackID, suffix)
}

// RewriteEventPlaylist injects EXT-X-START and appends suffix to URIs of an
// ffmpeg-written EVENT playlist.
func RewriteEventPlaylist(src, suffix string) (string, int) {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	var out []string
	segs := 0
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		switch {
		case i == 0 && l == "#EXTM3U":
			out = append(out, l, "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES")
			continue
		case strings.HasPrefix(l, "#EXT-X-MAP:") && suffix != "":
			l = strings.Replace(l, `init.mp4"`, `init.mp4`+suffix+`"`, 1)
		case l != "" && !strings.HasPrefix(l, "#"):
			segs++
			l += suffix
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n") + "\n", segs
}

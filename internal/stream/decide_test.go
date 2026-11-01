package stream

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
)

type src struct {
	container string
	bitrate   int64 // bps
	v         db.MediaStream
	audio     []db.MediaStream
	subs      []db.MediaStream
}

func h264(w, h int) db.MediaStream {
	return db.MediaStream{Index: 0, Type: "video", Codec: "h264", Profile: "High", Level: 41, Width: w, Height: h, BitDepth: 8, FrameRate: 23.976}
}

func hevc10HDR(w, h int) db.MediaStream {
	return db.MediaStream{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Level: 153, CodecTag: "hvc1", Width: w, Height: h, BitDepth: 10,
		ColorTransfer: "smpte2084", HDR: "hdr10", FrameRate: 23.976}
}

func aud(idx int, codec string, ch int, lang string) db.MediaStream {
	return db.MediaStream{Index: idx, Type: "audio", Codec: codec, Channels: ch, Language: lang, Bitrate: 192000}
}

func (s src) input(platform string, audioIdx int, capKbps int, hw HWInfo) Input {
	streams := append([]db.MediaStream{s.v}, s.audio...)
	streams = append(streams, s.subs...)
	p := BaseProfile(platform)
	p.Normalize()
	return Input{Item: &db.Item{Container: s.container, Bitrate: s.bitrate}, Streams: streams, AudioIndex: audioIdx, Profile: p, MaxBitrateKbps: capKbps, HW: hw}
}

var noHW = HWInfo{Backend: "none", H264Encoder: "libx264"}
var nvenc = HWInfo{Backend: "nvenc", H264Encoder: "h264_nvenc", HEVCNVENC: true, HWDecode: map[string]bool{"h264": true, "hevc": true}}

func TestDecisionTruthTable(t *testing.T) {
	mp4h264 := src{container: "mp4", bitrate: 5_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "aac", 2, "eng")}}
	mkvh264 := src{container: "matroska", bitrate: 5_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "aac", 2, "eng")}}
	mkvHDR := src{container: "matroska", bitrate: 60_000_000, v: hevc10HDR(3840, 2160), audio: []db.MediaStream{aud(1, "eac3", 6, "rus"), aud(2, "eac3", 6, "eng")}}
	dv5 := mkvHDR
	dv5.v.HDR, dv5.v.DVProfile = "dolbyvision", 5
	dv8 := mkvHDR
	dv8.v.HDR, dv8.v.DVProfile, dv8.v.DVBLCompat = "dolbyvision", 8, 1
	hi10 := mp4h264
	hi10.v.BitDepth, hi10.v.Profile = 10, "High 10"
	webm := src{container: "webm", bitrate: 2_000_000, v: db.MediaStream{Index: 0, Type: "video", Codec: "vp9", Width: 1280, Height: 720, BitDepth: 8}, audio: []db.MediaStream{aud(1, "opus", 2, "eng")}}
	mkvAC3 := src{container: "matroska", bitrate: 5_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "ac3", 6, "rus"), aud(2, "aac", 2, "eng")}}
	mkvDTS := src{container: "matroska", bitrate: 20_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "dts", 6, "eng")}}
	mkvTrueHD := src{container: "matroska", bitrate: 30_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "truehd", 8, "eng")}}
	mp4Hev1 := src{container: "mp4", bitrate: 8_000_000, v: hevc10HDR(3840, 2160), audio: []db.MediaStream{aud(1, "aac", 2, "eng")}}
	mp4Hev1.v.CodecTag = "hev1"

	cases := []struct {
		name    string
		in      Input
		method  string
		reasons []string
		vcodec  string
		acodec  string
		tonemap bool
		hdr     bool
		height  int
		maxrate int
		vAction string
		aAction string
	}{
		{"desktop mp4 h264 → direct", mp4h264.input("desktop", 1, 0, noHW), "direct", nil, "", "", false, false, 0, 0, "copy", "copy"},
		{"desktop mkv h264 → direct (chromium plays mkv)", mkvh264.input("desktop", 1, 0, noHW), "direct", nil, "", "", false, false, 0, 0, "copy", "copy"},
		{"ios mkv h264 → remux", mkvh264.input("ios", 1, 0, noHW), "remux", []string{"container"}, "", "aac", false, false, 0, 0, "copy", "copy"},
		{"webos mkv h264 → remux", mkvh264.input("webos", 1, 0, noHW), "remux", []string{"container"}, "", "aac", false, false, 0, 0, "copy", "copy"},
		{"ios mp4 h264 → direct", mp4h264.input("ios", 1, 0, noHW), "direct", nil, "", "", false, false, 0, 0, "copy", "copy"},
		{"ios hevc HDR mkv LAN → remux with eac3 copy", mkvHDR.input("ios", 2, 0, noHW), "remux", []string{"container", "audio_track_not_default"}, "", "eac3", false, false, 0, 0, "copy", "copy"},
		{"webos hevc HDR mkv LAN → remux", mkvHDR.input("webos", 1, 0, noHW), "remux", []string{"container"}, "", "eac3", false, false, 0, 0, "copy", "copy"},
		{"desktop hevc HDR → transcode tonemap (sw)", mkvHDR.input("desktop", 1, 0, noHW), "transcode", []string{"video_codec", "audio_codec"}, "h264", "aac", true, false, 2160, 0, "encode", "encode"},
		{"desktop hevc HDR nvenc → h264 tonemap", mkvHDR.input("desktop", 1, 0, nvenc), "transcode", []string{"video_codec", "audio_codec"}, "h264", "aac", true, false, 2160, 0, "encode", "encode"},
		{"ios hevc HDR remote 6M nvenc → hevc passthrough", mkvHDR.input("ios", 1, 6000, nvenc), "transcode", []string{"container", "bitrate"}, "hevc", "aac", false, true, 1080, 5227, "encode", "encode"},
		{"ios hevc HDR remote 6M no nvenc → h264 tonemap", mkvHDR.input("ios", 1, 6000, noHW), "transcode", []string{"container", "bitrate"}, "h264", "aac", true, false, 1080, 5227, "encode", "encode"},
		{"webos DV p5 → transcode tonemap", dv5.input("webos", 1, 0, nvenc), "transcode", []string{"container", "dolby_vision_profile"}, "h264", "eac3", true, false, 2160, 0, "encode", "copy"},
		{"ios DV p8 → remux (HDR10 base)", dv8.input("ios", 1, 0, noHW), "remux", []string{"container"}, "", "eac3", false, false, 0, 0, "copy", "copy"},
		{"desktop h264 10-bit → transcode bit depth", hi10.input("desktop", 1, 0, noHW), "transcode", []string{"video_bit_depth"}, "h264", "aac", false, false, 1080, 0, "encode", "copy"},
		{"ios h264 10-bit → transcode", hi10.input("ios", 1, 0, noHW), "transcode", []string{"video_bit_depth"}, "h264", "aac", false, false, 1080, 0, "encode", "copy"},
		{"desktop vp9 webm → direct", webm.input("desktop", 1, 0, noHW), "direct", nil, "", "", false, false, 0, 0, "copy", "copy"},
		{"ios vp9 webm → transcode", webm.input("ios", 1, 0, noHW), "transcode", []string{"container", "video_codec", "audio_codec"}, "h264", "aac", false, false, 720, 0, "encode", "encode"},
		{"desktop ac3 rus default → remux audio encode", mkvAC3.input("desktop", 1, 0, noHW), "remux", []string{"audio_codec"}, "", "aac", false, false, 0, 0, "copy", "encode"},
		{"desktop non-default eng aac → remux copy", mkvAC3.input("desktop", 2, 0, noHW), "remux", []string{"audio_track_not_default"}, "", "aac", false, false, 0, 0, "copy", "copy"},
		{"webos ac3 → remux copy ac3", mkvAC3.input("webos", 1, 0, noHW), "remux", []string{"container"}, "", "ac3", false, false, 0, 0, "copy", "copy"},
		{"webos dts → remux ac3 5.1", mkvDTS.input("webos", 1, 0, noHW), "remux", []string{"container", "audio_codec"}, "", "ac3", false, false, 0, 0, "copy", "encode"},
		{"ios truehd 7.1 → remux ac3", mkvTrueHD.input("ios", 1, 0, noHW), "remux", []string{"container", "audio_codec", "audio_channels"}, "", "ac3", false, false, 0, 0, "copy", "encode"},
		{"desktop dts remote 6M → transcode, aac stereo", mkvDTS.input("desktop", 1, 6000, noHW), "transcode", []string{"bitrate", "audio_codec"}, "h264", "aac", false, false, 1080, 5227, "encode", "encode"},
		{"ios ac3 remote under cap → remux aac stereo", mkvAC3.input("ios", 1, 6000, noHW), "remux", []string{"container"}, "", "aac", false, false, 0, 0, "copy", "encode"},
		{"ios hev1 mp4 → remux (retag)", mp4Hev1.input("ios", 1, 0, noHW), "remux", []string{"container"}, "", "aac", false, false, 0, 0, "copy", "copy"},
		{"other profile hevc → transcode 1080 cap by codec", mkvHDR.input("other", 1, 0, noHW), "transcode", []string{"container", "video_codec", "audio_codec", "audio_channels"}, "h264", "aac", true, false, 1080, 0, "encode", "encode"},
		{"remote 3M → 720p, small aac copied", mp4h264.input("desktop", 1, 3000, noHW), "transcode", []string{"bitrate"}, "h264", "aac", false, false, 720, 2527, "encode", "copy"},
		{"remote 1M → 360p", mp4h264.input("desktop", 1, 1000, noHW), "transcode", []string{"bitrate"}, "h264", "aac", false, false, 360, 727, "encode", "copy"},
		{"remote 15M 4K HDR no nvenc → 2160 tonemap, eac3 copied", mkvHDR.input("ios", 1, 15000, noHW), "transcode", []string{"container", "bitrate"}, "h264", "eac3", true, false, 2160, 13327, "encode", "copy"},
	}
	for _, c := range cases {
		d := Decide(c.in)
		if d.Method != c.method {
			t.Errorf("%s: method %s want %s (reasons %v)", c.name, d.Method, c.method, d.Reasons)
			continue
		}
		if c.reasons == nil {
			c.reasons = []string{}
		}
		if !reflect.DeepEqual(d.Reasons, c.reasons) {
			t.Errorf("%s: reasons %v want %v", c.name, d.Reasons, c.reasons)
		}
		if d.Video.Action != c.vAction || d.Audio.Action != c.aAction {
			t.Errorf("%s: actions v=%s a=%s want v=%s a=%s", c.name, d.Video.Action, d.Audio.Action, c.vAction, c.aAction)
		}
		if c.acodec != "" && d.Audio.Codec != c.acodec {
			t.Errorf("%s: audio codec %s want %s", c.name, d.Audio.Codec, c.acodec)
		}
		if d.Method == "transcode" {
			if d.Video.Codec != c.vcodec || d.Video.Tonemap != c.tonemap || d.Video.HDR != c.hdr || d.Video.Height != c.height || d.Video.MaxrateKbps != c.maxrate {
				t.Errorf("%s: video %+v want codec=%s tonemap=%v hdr=%v height=%d maxrate=%d", c.name, d.Video, c.vcodec, c.tonemap, c.hdr, c.height, c.maxrate)
			}
		}
	}
	if len(cases) < 25 {
		t.Fatal("truth table must have >= 25 cases")
	}
}

func TestForceTranscode(t *testing.T) {
	s := src{container: "mp4", bitrate: 5_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "aac", 2, "eng")}}
	in := s.input("desktop", 1, 0, noHW)
	in.Profile.ForceTranscode = true
	d := Decide(in)
	if d.Method != "transcode" || d.Reasons[0] != "forced" {
		t.Fatalf("forced: %+v", d)
	}
}

// The LG webOS browser plays AC-3 in files but its MediaSource may refuse it:
// HLS sessions must then carry AAC, while direct play keeps AC-3.
func TestHLSAudioCodecsLimitStreamingAudio(t *testing.T) {
	mkvDTS := src{container: "matroska", bitrate: 16_000_000, v: hevc10HDR(3840, 2160), audio: []db.MediaStream{aud(1, "dts", 6, "rus"), aud(3, "dts", 6, "eng")}}
	mkvDTS.v.HDR, mkvDTS.v.ColorTransfer = "", "bt709"
	mkvAC3 := src{container: "matroska", bitrate: 5_000_000, v: h264(1920, 1080), audio: []db.MediaStream{aud(1, "ac3", 6, "eng")}}
	mp4AC3 := mkvAC3
	mp4AC3.container = "mp4"

	mse := func(in Input) Input {
		in.Profile.HLSAudioCodecs = []string{"aac", "mp3"}
		return in
	}
	cases := []struct {
		name   string
		in     Input
		method string
		action string
		codec  string
		ch     int
	}{
		{"webOS MSE without AC-3: DTS → AAC stereo", mse(mkvDTS.input("webos", 3, 0, nvenc)), "remux", "encode", "aac", 2},
		{"webOS MSE without AC-3: AC-3 not copied", mse(mkvAC3.input("webos", 1, 0, nvenc)), "remux", "encode", "aac", 2},
		{"webOS MSE without AC-3: direct play keeps AC-3", mse(mp4AC3.input("webos", 1, 0, nvenc)), "direct", "copy", "ac3", 6},
		{"webOS MSE with AC-3: DTS → AC-3 5.1", mkvDTS.input("webos", 3, 0, nvenc), "remux", "encode", "ac3", 6},
		{"webOS MSE with AC-3: AC-3 copied", mkvAC3.input("webos", 1, 0, nvenc), "remux", "copy", "ac3", 6},
	}
	for _, c := range cases {
		d := Decide(c.in)
		if d.Method != c.method || d.Audio.Action != c.action || d.Audio.Codec != c.codec || d.Audio.Channels != c.ch {
			t.Errorf("%s: got %s audio %s %s %dch, want %s audio %s %s %dch", c.name, d.Method, d.Audio.Action, d.Audio.Codec, d.Audio.Channels,
				c.method, c.action, c.codec, c.ch)
		}
	}

	// Normalize: a client that does not send hlsAudioCodecs keeps the old behaviour.
	p := Profile{Platform: "webos", AudioCodecs: []string{"AAC", "AC3"}}
	p.Normalize()
	if !reflect.DeepEqual(p.HLSAudioCodecs, []string{"aac", "ac3"}) {
		t.Fatalf("normalize: %v", p.HLSAudioCodecs)
	}
}

func TestPlaylists(t *testing.T) {
	pl := MediaPlaylist(20500, 6, "")
	if strings.Count(pl, "#EXTINF:") != 4 || !strings.Contains(pl, "#EXTINF:2.500000,\nseg00003.m4s") || !strings.HasSuffix(pl, "#EXT-X-ENDLIST\n") {
		t.Fatalf("playlist:\n%s", pl)
	}
	pl = MediaPlaylist(12000, 6, "?token=abc")
	if !strings.Contains(pl, `#EXT-X-MAP:URI="init.mp4?token=abc"`) || !strings.Contains(pl, "seg00001.m4s?token=abc") {
		t.Fatalf("suffix: %s", pl)
	}
	m := MasterPlaylist(6000000, 5000000, "avc1.640029,mp4a.40.2", 1920, 1080, "", []SubtitleRendition{{TrackID: "e3", Name: "English", Language: "en", Default: true}}, "")
	if !strings.Contains(m, `URI="sub/e3.m3u8"`) || !strings.Contains(m, `SUBTITLES="subs"`) || !strings.Contains(m, `CODECS="avc1.640029,mp4a.40.2"`) {
		t.Fatalf("master: %s", m)
	}
	ev, n := RewriteEventPlaylist("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.0,\nseg00000.m4s\n", "?token=x")
	if n != 1 || !strings.Contains(ev, "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES") || !strings.Contains(ev, "seg00000.m4s?token=x") || !strings.Contains(ev, `init.mp4?token=x"`) {
		t.Fatalf("event: %s", ev)
	}
}

func TestShiftVTT(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nA\n\n00:01:05.500 --> 00:01:07.000\nB\n"
	out := string(ShiftVTT([]byte(in), 60000))
	if strings.Contains(out, "\nA") || !strings.Contains(out, "00:00:05.500 --> 00:00:07.000\nB") || !strings.HasPrefix(out, "WEBVTT") {
		t.Fatalf("shift: %q", out)
	}
}

func TestApplyProgress(t *testing.T) {
	now := time.Now()
	u := &db.UserItemData{}
	if ApplyProgress(u, 3000, 600000, 92, false, true, now) || u.PositionMs != 0 {
		t.Fatal("early position should not be in progress")
	}
	ApplyProgress(u, 120000, 600000, 92, false, true, now)
	if u.PositionMs != 120000 || u.Played {
		t.Fatalf("in progress: %+v", u)
	}
	if !ApplyProgress(u, 560000, 600000, 92, false, true, now) || !u.Played || u.PositionMs != 0 || u.PlayCount != 1 {
		t.Fatalf("played: %+v", u)
	}
}

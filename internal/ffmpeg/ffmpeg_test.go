package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLocateWingetAllUsers(t *testing.T) {
	root := t.TempDir()
	users := filepath.Join(root, "Users")
	old := filepath.Join(users, "alice", "AppData", "Local", "Microsoft", "WinGet", "Packages", "Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe", "ffmpeg-6.1.1-full_build", "bin")
	newer := filepath.Join(users, "can", "AppData", "Local", "Microsoft", "WinGet", "Packages", "Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe", "ffmpeg-7.1-full_build", "bin")
	for _, d := range []string{old, newer} {
		touch(t, filepath.Join(d, "ffmpeg.exe"))
		touch(t, filepath.Join(d, "ffprobe.exe"))
	}
	o := LocateOptions{GOOS: "windows", UsersDir: users, SystemDrive: filepath.Join(root, "C:"), ExeDir: filepath.Join(root, "exe"),
		LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	ff, fp, src, err := FindPaths(o)
	if err != nil {
		t.Fatal(err)
	}
	if ff != filepath.Join(newer, "ffmpeg.exe") || fp != filepath.Join(newer, "ffprobe.exe") || src != "winget" {
		t.Fatalf("got %s %s %s", ff, fp, src)
	}

	// Config path wins over everything.
	cfgPath := filepath.Join(root, "custom", "ffmpeg.exe")
	touch(t, cfgPath)
	touch(t, filepath.Join(root, "custom", "ffprobe.exe"))
	o.ConfigFFmpeg = cfgPath
	ff, _, src, err = FindPaths(o)
	if err != nil || ff != cfgPath || src != "config" {
		t.Fatalf("config path not preferred: %s %s %v", ff, src, err)
	}

	// A configured path that does not exist is an error (no silent fallback).
	o.ConfigFFmpeg = filepath.Join(root, "nope.exe")
	if _, _, _, err := FindPaths(o); err == nil {
		t.Fatal("expected error for missing configured path")
	}
}

func TestLocateNothing(t *testing.T) {
	root := t.TempDir()
	o := LocateOptions{GOOS: "windows", UsersDir: filepath.Join(root, "Users"), SystemDrive: filepath.Join(root, "C:"), ExeDir: root,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	if _, _, _, err := FindPaths(o); err == nil {
		t.Fatal("expected not found")
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string][3]any{
		"ffmpeg version 6.1.1-3ubuntu5 Copyright (c) 2000-2023":         {"6.1.1", 6, 1},
		"ffmpeg version 7.1-full_build-www.gyan.dev Copyright (c) 2000": {"7.1", 7, 1},
		"ffmpeg version n7.0.2 Copyright":                               {"7.0.2", 7, 0},
		"ffmpeg version N-112345-gabcdef Copyright":                     {"N-112345-gabcdef", 99, 0},
	}
	for in, want := range cases {
		v, maj, min := ParseVersion(in)
		if v != want[0] || maj != want[1] || min != want[2] {
			t.Errorf("%q → %s %d %d", in, v, maj, min)
		}
	}
}

func join(a []string) string { return strings.Join(a, " ") }

func TestBuildHLSSoftwareSDR(t *testing.T) {
	got := join(BuildHLS(HLSParams{
		Input: "/m/a.mkv", StartSec: 60, VideoIndex: 0, AudioIndex: 1, SegmentSeconds: 6, StartNumber: 10, TSOffsetSec: 60,
		Video: VideoParams{Encoder: "libx264", Width: 1920, Height: 1080, MaxrateKbps: 5227, Preset: "veryfast", FPS: 23.976, Level: "4.1"},
		Audio: AudioParams{Codec: "aac", Channels: 2, BitrateKbps: 192},
	}))
	want := "-nostdin -hide_banner -loglevel warning -y -ss 60 -i /m/a.mkv -map 0:0 -map 0:1 -sn -dn -map_chapters -1 " +
		"-vf scale=1920:1080:flags=bicubic,format=yuv420p -c:v libx264 -preset veryfast -crf 23 -maxrate 5227k -bufsize 10454k -profile:v high -level 4.1 -pix_fmt yuv420p " +
		"-g 144 -keyint_min 144 -sc_threshold 0 -force_key_frames expr:gte(t,n_forced*6) -c:a aac -ac 2 -b:a 192k -output_ts_offset 60 " +
		"-f hls -hls_time 6 -hls_list_size 0 -hls_segment_type fmp4 -hls_fmp4_init_filename init.mp4 -hls_segment_filename seg%05d.m4s -hls_flags independent_segments+temp_file -start_number 10 index.m3u8"
	if got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
}

func TestBuildHLSSoftwareTonemap(t *testing.T) {
	got := join(BuildHLS(HLSParams{
		Input: "in.mkv", VideoIndex: 0, AudioIndex: 1, SegmentSeconds: 6,
		Video: VideoParams{Encoder: "libx264", Width: 1280, Height: 720, Tonemap: true, FPS: 24},
		Audio: AudioParams{Copy: true},
	}))
	if !strings.Contains(got, "-vf zscale=w=1280:h=720:t=linear:npl=100:f=spline36,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p ") {
		t.Fatalf("tonemap chain missing: %s", got)
	}
	if !strings.Contains(got, "-c:a copy") || strings.Contains(got, "-output_ts_offset") || strings.Contains(got, "-ss ") {
		t.Fatalf("unexpected args: %s", got)
	}
}

func TestBuildHLSNVENC(t *testing.T) {
	// SDR with NVDEC.
	got := join(BuildHLS(HLSParams{Input: "in.mkv", VideoIndex: 0, AudioIndex: 2, SegmentSeconds: 6,
		Video: VideoParams{Encoder: "h264_nvenc", HWDecode: true, Width: 1920, Height: 1080, MaxrateKbps: 5227, FPS: 25},
		Audio: AudioParams{Codec: "ac3", Channels: 6, BitrateKbps: 640}}))
	for _, want := range []string{"-hwaccel cuda -hwaccel_output_format cuda -i in.mkv", "-vf scale_cuda=w=1920:h=1080:format=yuv420p",
		"-c:v h264_nvenc -preset p4 -tune hq -rc vbr -cq 23 -b:v 0 -maxrate 5227k -bufsize 10454k -profile:v high -level 4.1 -g 150 -keyint_min 150 -no-scenecut 1 -forced-idr 1",
		"-c:a ac3 -b:a 640k -ac 6"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	// SDR without NVDEC (fallback rung 2).
	got = join(BuildHLS(HLSParams{Input: "in.mkv", VideoIndex: 0, AudioIndex: -1, SegmentSeconds: 6,
		Video: VideoParams{Encoder: "h264_nvenc", Width: 1280, Height: 720, FPS: 24}}))
	if strings.Contains(got, "-hwaccel") || !strings.Contains(got, "-vf scale=1280:720:flags=bicubic,format=yuv420p -c:v h264_nvenc") || strings.Contains(got, "-c:a") {
		t.Fatalf("bad no-nvdec args: %s", got)
	}
	// HDR -> SDR hybrid.
	got = join(BuildHLS(HLSParams{Input: "in.mkv", VideoIndex: 0, AudioIndex: 1, SegmentSeconds: 6,
		Video: VideoParams{Encoder: "h264_nvenc", HWDecode: true, Width: 1920, Height: 1080, Tonemap: true, FPS: 24}, Audio: AudioParams{Codec: "aac"}}))
	if !strings.Contains(got, "-vf scale_cuda=w=1920:h=1080:format=p010le,hwdownload,format=p010le,zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p ") {
		t.Fatalf("bad hybrid tonemap: %s", got)
	}
	// HEVC Main10 HDR passthrough.
	got = join(BuildHLS(HLSParams{Input: "in.mkv", VideoIndex: 0, AudioIndex: 1, SegmentSeconds: 6,
		Video: VideoParams{Encoder: "hevc_nvenc", HWDecode: true, Width: 1920, Height: 1080, HDRPassthrough: true, Transfer: "smpte2084", MaxrateKbps: 5227, FPS: 24},
		Audio: AudioParams{Codec: "aac"}}))
	for _, want := range []string{"-vf scale_cuda=w=1920:h=1080:format=p010le -c:v hevc_nvenc", "-profile:v main10 -tier main -color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc -color_range tv -tag:v hvc1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestBuildHLSRemux(t *testing.T) {
	got := join(BuildHLS(HLSParams{Input: "in.mkv", StartSec: 30, VideoIndex: 0, AudioIndex: 1, SegmentSeconds: 6, Event: true,
		Video: VideoParams{Copy: true, TagHvc1: true}, Audio: AudioParams{Copy: true}}))
	want := "-nostdin -hide_banner -loglevel warning -y -ss 30 -i in.mkv -map 0:0 -map 0:1 -sn -dn -map_chapters -1 -c:v copy -tag:v hvc1 -c:a copy " +
		"-f hls -hls_time 6 -hls_list_size 0 -hls_playlist_type event -hls_segment_type fmp4 -hls_fmp4_init_filename init.mp4 -hls_segment_filename seg%05d.m4s -hls_flags independent_segments+temp_file -start_number 0 index.m3u8"
	if got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
}

func TestBuildThumbnailAndSubtitle(t *testing.T) {
	got := join(BuildThumbnail("in.mkv", 12.5, 640, false, "out.jpg"))
	if got != "-nostdin -hide_banner -loglevel error -y -ss 12.5 -i in.mkv -map 0:v:0 -an -sn -dn -frames:v 1 -vf scale=640:-2 -q:v 4 out.jpg" {
		t.Fatalf("thumb: %s", got)
	}
	got = join(BuildSubtitleVTT("in.mkv", 3, 60, ""))
	if got != "-nostdin -hide_banner -loglevel error -ss 60 -i in.mkv -map 0:3 -c:s webvtt -f webvtt -" {
		t.Fatalf("sub: %s", got)
	}
	got = join(BuildSubtitleVTT("ext.srt", -1, 0, "WINDOWS-1251"))
	if got != "-nostdin -hide_banner -loglevel error -sub_charenc WINDOWS-1251 -i ext.srt -map 0:s:0 -c:s webvtt -f webvtt -" {
		t.Fatalf("ext sub: %s", got)
	}
}

func TestDetectorFallsBackWhenSelfTestFails(t *testing.T) {
	d := &Detector{FFmpeg: "ffmpeg", Configured: "nvenc"}
	d.SetRunner(func(ctx context.Context, args ...string) (bool, string) {
		return false, "No NVENC capable devices found"
	})
	// Encoders are listed by running the real binary; emulate via a fake list.
	caps := d.Detect(context.Background())
	if caps.Active != "none" {
		t.Fatalf("expected none, got %s", caps.Active)
	}
	if caps.H264Encoder() != "libx264" {
		t.Fatalf("encoder: %s", caps.H264Encoder())
	}
}

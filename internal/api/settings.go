package api

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/wtfits/homestreaming/internal/stream"
)

// Settings are the runtime settings stored in the DB settings table.
type Settings struct {
	ServerName             string                             `json:"serverName"`
	RemoteMaxBitrateKbps   int                                `json:"remoteMaxBitrateKbps"`
	LanMaxBitrateKbps      int                                `json:"lanMaxBitrateKbps"`
	TailnetIsLan           bool                               `json:"tailnetIsLan"`
	TranscodeMaxSessions   int                                `json:"transcodeMaxSessions"`
	TranscodePreset        string                             `json:"transcodePreset"`
	TranscodeThreads       int                                `json:"transcodeThreads"`
	HLSSegmentSeconds      int                                `json:"hlsSegmentSeconds"`
	ScanIntervalHours      int                                `json:"scanIntervalHours"`
	PlayedThresholdPercent int                                `json:"playedThresholdPercent"`
	AudioTranscodeCodec    string                             `json:"audioTranscodeCodec"`
	ProfileOverrides       map[string]stream.PlatformOverride `json:"profileOverrides"`
}

// DefaultSettings returns the built-in defaults (DESIGN §3.1).
func DefaultSettings() Settings {
	return Settings{ServerName: "HomeStreaming", RemoteMaxBitrateKbps: 6000, LanMaxBitrateKbps: 0, TailnetIsLan: false,
		TranscodeMaxSessions: 3, TranscodePreset: "veryfast", TranscodeThreads: 0, HLSSegmentSeconds: 6, ScanIntervalHours: 6,
		PlayedThresholdPercent: 92, AudioTranscodeCodec: "auto", ProfileOverrides: map[string]stream.PlatformOverride{}}
}

var x264Presets = map[string]bool{"ultrafast": true, "superfast": true, "veryfast": true, "faster": true, "fast": true, "medium": true, "slow": true, "slower": true, "veryslow": true}

func (s *Server) loadSettings(ctx context.Context) (Settings, error) {
	kv, err := s.DB.GetSettings(ctx)
	if err != nil {
		return DefaultSettings(), err
	}
	st := DefaultSettings()
	atoi := func(k string, dst *int) {
		if v, ok := kv[k]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	if v, ok := kv["server_name"]; ok && v != "" {
		st.ServerName = v
	}
	atoi("remote_max_bitrate_kbps", &st.RemoteMaxBitrateKbps)
	atoi("lan_max_bitrate_kbps", &st.LanMaxBitrateKbps)
	if v, ok := kv["tailnet_is_lan"]; ok {
		st.TailnetIsLan = v == "true"
	}
	atoi("transcode_max_sessions", &st.TranscodeMaxSessions)
	if v, ok := kv["transcode_preset"]; ok && x264Presets[v] {
		st.TranscodePreset = v
	}
	atoi("transcode_threads", &st.TranscodeThreads)
	atoi("hls_segment_seconds", &st.HLSSegmentSeconds)
	atoi("scan_interval_hours", &st.ScanIntervalHours)
	atoi("played_threshold_percent", &st.PlayedThresholdPercent)
	if v, ok := kv["audio_transcode_codec"]; ok && (v == "auto" || v == "aac" || v == "ac3") {
		st.AudioTranscodeCodec = v
	}
	if v, ok := kv["profile_overrides_json"]; ok && v != "" {
		_ = json.Unmarshal([]byte(v), &st.ProfileOverrides)
	}
	return st, nil
}

// Settings returns the cached settings.
func (s *Server) Settings() Settings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.settings
}

// ReloadSettings refreshes the settings cache from the DB.
func (s *Server) ReloadSettings(ctx context.Context) error {
	st, err := s.loadSettings(ctx)
	s.settingsMu.Lock()
	s.settings = st
	s.settingsMu.Unlock()
	return err
}

func (s *Server) saveSettings(ctx context.Context, st Settings) error {
	ov, _ := json.Marshal(st.ProfileOverrides)
	kv := map[string]string{
		"server_name":              st.ServerName,
		"remote_max_bitrate_kbps":  strconv.Itoa(st.RemoteMaxBitrateKbps),
		"lan_max_bitrate_kbps":     strconv.Itoa(st.LanMaxBitrateKbps),
		"tailnet_is_lan":           strconv.FormatBool(st.TailnetIsLan),
		"transcode_max_sessions":   strconv.Itoa(st.TranscodeMaxSessions),
		"transcode_preset":         st.TranscodePreset,
		"transcode_threads":        strconv.Itoa(st.TranscodeThreads),
		"hls_segment_seconds":      strconv.Itoa(st.HLSSegmentSeconds),
		"scan_interval_hours":      strconv.Itoa(st.ScanIntervalHours),
		"played_threshold_percent": strconv.Itoa(st.PlayedThresholdPercent),
		"audio_transcode_codec":    st.AudioTranscodeCodec,
		"profile_overrides_json":   string(ov),
	}
	if err := s.DB.SetSettings(ctx, kv); err != nil {
		return err
	}
	return s.ReloadSettings(ctx)
}

// validate returns field → message for invalid settings.
func (st Settings) validate() map[string]string {
	errs := map[string]string{}
	if st.RemoteMaxBitrateKbps < 0 {
		errs["remoteMaxBitrateKbps"] = "must be >= 0"
	}
	if st.LanMaxBitrateKbps < 0 {
		errs["lanMaxBitrateKbps"] = "must be >= 0"
	}
	if st.TranscodeMaxSessions < 1 || st.TranscodeMaxSessions > 16 {
		errs["transcodeMaxSessions"] = "must be 1–16"
	}
	if st.HLSSegmentSeconds < 2 || st.HLSSegmentSeconds > 10 {
		errs["hlsSegmentSeconds"] = "must be 2–10"
	}
	if st.PlayedThresholdPercent < 50 || st.PlayedThresholdPercent > 100 {
		errs["playedThresholdPercent"] = "must be 50–100"
	}
	if !x264Presets[st.TranscodePreset] {
		errs["transcodePreset"] = "unknown libx264 preset"
	}
	if st.TranscodeThreads < 0 || st.TranscodeThreads > 64 {
		errs["transcodeThreads"] = "must be 0–64"
	}
	if st.ScanIntervalHours < 0 || st.ScanIntervalHours > 168 {
		errs["scanIntervalHours"] = "must be 0–168"
	}
	if st.AudioTranscodeCodec != "auto" && st.AudioTranscodeCodec != "aac" && st.AudioTranscodeCodec != "ac3" {
		errs["audioTranscodeCodec"] = "must be auto, aac or ac3"
	}
	if st.ServerName == "" {
		errs["serverName"] = "required"
	}
	return errs
}

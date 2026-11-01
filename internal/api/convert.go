package api

import (
	"context"
	"strconv"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/media"
)

type userJSON struct {
	ID          string         `json:"id"`
	Username    string         `json:"username"`
	IsAdmin     bool           `json:"isAdmin"`
	CreatedAt   string         `json:"createdAt"`
	Preferences db.Preferences `json:"preferences"`
}

func toUser(u *db.User) userJSON {
	return userJSON{ID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin, CreatedAt: u.CreatedAt, Preferences: u.Preferences}
}

type sessionJSON struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	ClientType string `json:"clientType"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
	ExpiresAt  string `json:"expiresAt"`
	Current    bool   `json:"current"`
}

func toSession(s *db.Session, currentID string) sessionJSON {
	return sessionJSON{ID: s.ID, DeviceID: s.DeviceID, DeviceName: s.DeviceName, ClientType: s.ClientType, IP: s.IP,
		CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt, Current: s.ID == currentID}
}

type libraryJSON struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"`
	Paths          []string         `json:"paths"`
	PathIDs        []string         `json:"pathIds"`
	CreatedAt      string           `json:"createdAt"`
	LastScanAt     *string          `json:"lastScanAt"`
	LastScanStatus *string          `json:"lastScanStatus"`
	LastScanError  *string          `json:"lastScanError"`
	Counts         db.LibraryCounts `json:"counts"`
	Scanning       bool             `json:"scanning"`
	ScanTaskID     string           `json:"scanTaskId,omitempty"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) toLibrary(ctx context.Context, l *db.Library) libraryJSON {
	j := libraryJSON{ID: l.ID, Name: l.Name, Type: l.Type, Paths: []string{}, PathIDs: []string{}, CreatedAt: l.CreatedAt,
		LastScanAt: strPtr(l.LastScanAt), LastScanStatus: strPtr(l.LastScanStatus), LastScanError: strPtr(l.LastScanError)}
	for _, p := range l.Paths {
		j.Paths = append(j.Paths, p.Path)
		j.PathIDs = append(j.PathIDs, p.ID)
	}
	j.Counts, _ = s.DB.LibraryCounts(ctx, l.ID)
	if t, ok := s.Scanner.RunningTask(l.ID); ok {
		j.Scanning = true
		j.ScanTaskID = t.ID
	}
	return j
}

type videoSummary struct {
	Codec    string  `json:"codec"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	HDR      *string `json:"hdr"`
	BitDepth int     `json:"bitDepth"`
}

type userDataJSON struct {
	PositionMs   int64   `json:"positionMs"`
	Played       bool    `json:"played"`
	PlayCount    int     `json:"playCount"`
	LastPlayedAt *string `json:"lastPlayedAt"`
}

func toUserData(u *db.UserItemData) userDataJSON {
	if u == nil {
		return userDataJSON{}
	}
	return userDataJSON{PositionMs: u.PositionMs, Played: u.Played, PlayCount: u.PlayCount, LastPlayedAt: strPtr(u.LastPlayedAt)}
}

type itemSummary struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	LibraryID        string          `json:"libraryId"`
	Title            string          `json:"title"`
	SortTitle        string          `json:"sortTitle"`
	Year             *int            `json:"year"`
	Overview         string          `json:"overview"`
	RuntimeMs        *int64          `json:"runtimeMs"`
	PremiereDate     *string         `json:"premiereDate"`
	AddedAt          string          `json:"addedAt"`
	ParentID         *string         `json:"parentId"`
	SeriesID         *string         `json:"seriesId"`
	SeriesTitle      *string         `json:"seriesTitle"`
	SeasonNumber     *int            `json:"seasonNumber"`
	EpisodeNumber    *int            `json:"episodeNumber"`
	EpisodeEndNumber *int            `json:"episodeEndNumber"`
	Images           map[string]bool `json:"images"`
	Video            *videoSummary   `json:"video"`
	AudioLanguages   []string        `json:"audioLanguages"`
	ChildCount       *int            `json:"childCount"`
	UnplayedCount    *int            `json:"unplayedCount"`
	Status           string          `json:"status"`
	UserData         userDataJSON    `json:"userData"`
}

func intPtrNZ(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func toSummary(it *db.Item, imgs map[string]bool) itemSummary {
	j := itemSummary{ID: it.ID, Type: it.Type, LibraryID: it.LibraryID, Title: it.Title, SortTitle: it.SortTitle, Year: intPtrNZ(it.Year),
		Overview: it.Overview, PremiereDate: strPtr(it.PremiereDate), AddedAt: it.AddedAt, ParentID: strPtr(it.ParentID),
		SeriesID: strPtr(it.SeriesID), SeriesTitle: strPtr(it.SeriesTitle), SeasonNumber: it.SeasonNumber, EpisodeNumber: it.EpisodeNumber,
		EpisodeEndNumber: it.EpisodeEndNumber, Images: imgs, AudioLanguages: []string{}, ChildCount: it.ChildCount, UnplayedCount: it.UnplayedCount,
		Status: it.Status, UserData: toUserData(it.UserData)}
	if j.Images == nil {
		j.Images = map[string]bool{"poster": false, "backdrop": false, "thumb": false}
	}
	if it.IsFile() {
		if it.RuntimeMs > 0 {
			rt := it.RuntimeMs
			j.RuntimeMs = &rt
		}
		if it.VideoCodec != "" {
			j.Video = &videoSummary{Codec: it.VideoCodec, Width: it.Width, Height: it.Height, HDR: strPtr(it.HDR), BitDepth: it.BitDepth}
		}
		if it.AudioLangs != "" {
			j.AudioLanguages = strings.Split(it.AudioLangs, ",")
		}
	}
	return j
}

// imageFlags computes image availability (with fallbacks) for a set of items.
func (s *Server) imageFlags(ctx context.Context, items []*db.Item) map[string]map[string]bool {
	ids := make([]string, 0, len(items)*2)
	seen := map[string]bool{}
	for _, it := range items {
		for _, id := range []string{it.ID, it.SeriesID} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	kinds, _ := s.DB.ImageKinds(ctx, ids)
	out := map[string]map[string]bool{}
	for _, it := range items {
		own := kinds[it.ID]
		ser := kinds[it.SeriesID]
		f := map[string]bool{"poster": own["poster"], "backdrop": own["backdrop"], "thumb": own["thumb"]}
		switch it.Type {
		case "season", "episode":
			f["poster"] = f["poster"] || ser["poster"]
			f["backdrop"] = f["backdrop"] || ser["backdrop"]
		case "series":
			// Episodes provide thumbs/backdrops once extracted.
			if it.ChildCount != nil && *it.ChildCount > 0 {
				f["thumb"], f["backdrop"] = true, true
			}
		}
		out[it.ID] = f
	}
	return out
}

func (s *Server) summaries(ctx context.Context, items []*db.Item) []itemSummary {
	flags := s.imageFlags(ctx, items)
	out := make([]itemSummary, 0, len(items))
	for _, it := range items {
		out = append(out, toSummary(it, flags[it.ID]))
	}
	return out
}

// summaryByID loads one item as a summary with user data.
func (s *Server) summaryByID(ctx context.Context, userID, id string) (*itemSummary, error) {
	items, _, err := s.DB.ListItems(ctx, db.ItemQuery{UserID: userID, ItemIDs: []string{id}, Status: "all", Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, db.ErrNotFound
	}
	sums := s.summaries(ctx, items)
	return &sums[0], nil
}

type mediaStreamJSON struct {
	Index          int      `json:"index"`
	Type           string   `json:"type"`
	Codec          string   `json:"codec"`
	Profile        *string  `json:"profile"`
	Level          *int     `json:"level"`
	CodecTag       *string  `json:"codecTag"`
	Width          *int     `json:"width"`
	Height         *int     `json:"height"`
	PixFmt         *string  `json:"pixFmt"`
	BitDepth       *int     `json:"bitDepth"`
	FrameRate      *float64 `json:"frameRate"`
	ColorTransfer  *string  `json:"colorTransfer"`
	ColorPrimaries *string  `json:"colorPrimaries"`
	ColorSpace     *string  `json:"colorSpace"`
	HDR            *string  `json:"hdr"`
	DVProfile      *int     `json:"dvProfile"`
	DVBLCompatID   *int     `json:"dvBlCompatId"`
	FieldOrder     *string  `json:"fieldOrder"`
	Bitrate        *int     `json:"bitrate"`
	Channels       *int     `json:"channels"`
	ChannelLayout  *string  `json:"channelLayout"`
	SampleRate     *int     `json:"sampleRate"`
	Language       *string  `json:"language"`
	Title          *string  `json:"title"`
	IsDefault      bool     `json:"isDefault"`
	IsForced       bool     `json:"isForced"`
	IsText         *bool    `json:"isText"`
}

func toMediaStream(m db.MediaStream) mediaStreamJSON {
	j := mediaStreamJSON{Index: m.Index, Type: m.Type, Codec: m.Codec, Profile: strPtr(m.Profile), Level: intPtrNZ(m.Level), CodecTag: strPtr(m.CodecTag),
		Language: strPtr(m.Language), Title: strPtr(m.Title), IsDefault: m.IsDefault, IsForced: m.IsForced, Bitrate: intPtrNZ(int(m.Bitrate / 1000))}
	switch m.Type {
	case "video":
		j.Width, j.Height, j.PixFmt, j.BitDepth = intPtrNZ(m.Width), intPtrNZ(m.Height), strPtr(m.PixFmt), intPtrNZ(m.BitDepth)
		if m.FrameRate > 0 {
			fr := m.FrameRate
			j.FrameRate = &fr
		}
		j.ColorTransfer, j.ColorPrimaries, j.ColorSpace, j.HDR = strPtr(m.ColorTransfer), strPtr(m.ColorPrimaries), strPtr(m.ColorSpace), strPtr(m.HDR)
		j.DVProfile, j.DVBLCompatID, j.FieldOrder = intPtrNZ(m.DVProfile), intPtrNZ(m.DVBLCompat), strPtr(m.FieldOrder)
	case "audio":
		j.Channels, j.ChannelLayout, j.SampleRate = intPtrNZ(m.Channels), strPtr(m.ChannelLayout), intPtrNZ(m.SampleRate)
	case "subtitle":
		t := m.IsText
		j.IsText = &t
	}
	return j
}

type audioTrackJSON struct {
	ID            int    `json:"id"`
	Codec         string `json:"codec"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channelLayout"`
	Language      string `json:"language"`
	Title         string `json:"title"`
	DisplayName   string `json:"displayName"`
	IsDefault     bool   `json:"isDefault"`
	Bitrate       *int   `json:"bitrate"`
}

type subtitleTrackJSON struct {
	ID          string `json:"id"`
	Codec       string `json:"codec"`
	Language    string `json:"language"`
	Title       string `json:"title"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
	IsForced    bool   `json:"isForced"`
	IsText      bool   `json:"isText"`
	IsExternal  bool   `json:"isExternal"`
}

func channelsName(ch int) string {
	switch ch {
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	if ch > 0 {
		return strconv.Itoa(ch) + "ch"
	}
	return ""
}

func audioDisplay(m db.MediaStream) string {
	parts := []string{media.LangName(m.Language)}
	if m.Title != "" && !strings.EqualFold(m.Title, media.LangName(m.Language)) {
		parts = append(parts, "("+m.Title+")")
	}
	parts = append(parts, "·", strings.ToUpper(m.Codec), channelsName(m.Channels))
	return strings.Join(parts, " ")
}

func audioTracks(streams []db.MediaStream) []audioTrackJSON {
	out := []audioTrackJSON{}
	for _, m := range streams {
		if m.Type != "audio" {
			continue
		}
		out = append(out, audioTrackJSON{ID: m.Index, Codec: m.Codec, Channels: m.Channels, ChannelLayout: m.ChannelLayout, Language: m.Language,
			Title: m.Title, DisplayName: audioDisplay(m), IsDefault: m.IsDefault, Bitrate: intPtrNZ(int(m.Bitrate / 1000))})
	}
	return out
}

// subtitleTrack is the internal union of embedded and external subtitles.
type subtitleTrack struct {
	ID       string
	Codec    string
	Language string
	Title    string
	Default  bool
	Forced   bool
	Text     bool
	External bool
	Index    int    // embedded stream index
	RelPath  string // external
}

func subtitleTracks(streams []db.MediaStream, ext []db.ExternalSubtitle) []subtitleTrack {
	var out []subtitleTrack
	for _, m := range streams {
		if m.Type == "subtitle" {
			out = append(out, subtitleTrack{ID: "e" + strconv.Itoa(m.Index), Codec: m.Codec, Language: m.Language, Title: m.Title,
				Default: m.IsDefault, Forced: m.IsForced, Text: m.IsText, Index: m.Index})
		}
	}
	for _, e := range ext {
		out = append(out, subtitleTrack{ID: "x" + e.ID, Codec: e.Format, Language: e.Language, Title: e.Title, Forced: e.IsForced,
			Text: true, External: true, Index: -1, RelPath: e.RelPath})
	}
	return out
}

func subtitleDisplay(t subtitleTrack) string {
	name := media.LangName(t.Language)
	if t.Title != "" && !strings.EqualFold(t.Title, name) {
		name += " (" + t.Title + ")"
	}
	if t.Forced {
		name += " · Forced"
	}
	if !t.Text {
		name += " · image (not supported yet)"
	}
	return name
}

func toSubtitleJSON(ts []subtitleTrack) []subtitleTrackJSON {
	out := []subtitleTrackJSON{}
	for _, t := range ts {
		out = append(out, subtitleTrackJSON{ID: t.ID, Codec: t.Codec, Language: t.Language, Title: t.Title, DisplayName: subtitleDisplay(t),
			IsDefault: t.Default, IsForced: t.Forced, IsText: t.Text, IsExternal: t.External})
	}
	return out
}

// defaultAudio picks the default audio stream index per DESIGN §6.6 (-1 if none).
func defaultAudio(streams []db.MediaStream, pref string) int {
	first, def := -1, -1
	for _, m := range streams {
		if m.Type != "audio" {
			continue
		}
		if first < 0 {
			first = m.Index
		}
		if def < 0 && m.IsDefault {
			def = m.Index
		}
		if pref != "" && m.Language == pref {
			return m.Index
		}
	}
	if def >= 0 {
		return def
	}
	return first
}

// defaultSubtitle picks the default subtitle track id ("" = off).
func defaultSubtitle(ts []subtitleTrack, p db.Preferences) string {
	switch p.SubtitleMode {
	case "preferred", "always":
		for _, t := range ts {
			if t.Text && !t.Forced && p.SubtitleLanguage != "" && t.Language == p.SubtitleLanguage {
				return t.ID
			}
		}
		for _, t := range ts {
			if t.Text && p.SubtitleLanguage != "" && t.Language == p.SubtitleLanguage {
				return t.ID
			}
		}
		if p.SubtitleMode == "always" {
			for _, t := range ts {
				if t.Text {
					return t.ID
				}
			}
		}
	}
	return ""
}

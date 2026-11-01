package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Item is a row of the items table.
type Item struct {
	ID        string
	Type      string // movie|series|season|episode
	LibraryID string
	ParentID  string
	SeriesID  string
	SeriesKey string
	Title     string
	SortTitle string
	Year      int
	Overview  string

	SeasonNumber     *int
	EpisodeNumber    *int
	EpisodeEndNumber *int
	PremiereDate     string

	LibraryPathID string
	RelPath       string
	Size          int64
	Mtime         string
	Container     string
	RuntimeMs     int64
	Bitrate       int64 // bits per second
	VideoCodec    string
	Width         int
	Height        int
	HDR           string
	BitDepth      int
	AudioLangs    string
	ProbeJSON     string
	Confidence    string

	Status        string // ok|pending|missing
	StatusReason  string
	ProbeAttempts int
	ImagesDone    bool
	AddedAt       string
	UpdatedAt     string

	// Joined/derived (list queries only).
	SeriesTitle   string
	ChildCount    *int
	UnplayedCount *int
	UserData      *UserItemData
}

// IsFile reports whether the item is backed by a file.
func (it *Item) IsFile() bool { return it.Type == "movie" || it.Type == "episode" }

const itemCols = `i.id, i.type, i.library_id, COALESCE(i.parent_id,''), COALESCE(i.series_id,''), COALESCE(i.series_key,''),
 i.title, i.sort_title, COALESCE(i.year,0), COALESCE(i.overview,''), i.season_number, i.episode_number, i.episode_end_number, COALESCE(i.premiere_date,''),
 COALESCE(i.library_path_id,''), COALESCE(i.rel_path,''), COALESCE(i.size,0), COALESCE(i.mtime,''), COALESCE(i.container,''), COALESCE(i.runtime_ms,0), COALESCE(i.bitrate,0),
 COALESCE(i.video_codec,''), COALESCE(i.width,0), COALESCE(i.height,0), COALESCE(i.hdr,''), COALESCE(i.bit_depth,0), COALESCE(i.audio_langs,''), COALESCE(i.probe_json,''), i.confidence,
 i.status, COALESCE(i.status_reason,''), i.probe_attempts, i.images_done, i.added_at, i.updated_at`

func nullToPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func ptrToNull(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

// scanItemRow scans itemCols (+ extra destinations) from row.
func scanItemRow(row interface{ Scan(...any) error }, extra ...any) (*Item, error) {
	it := &Item{}
	var season, ep, epEnd sql.NullInt64
	var imagesDone int
	dests := []any{&it.ID, &it.Type, &it.LibraryID, &it.ParentID, &it.SeriesID, &it.SeriesKey,
		&it.Title, &it.SortTitle, &it.Year, &it.Overview, &season, &ep, &epEnd, &it.PremiereDate,
		&it.LibraryPathID, &it.RelPath, &it.Size, &it.Mtime, &it.Container, &it.RuntimeMs, &it.Bitrate,
		&it.VideoCodec, &it.Width, &it.Height, &it.HDR, &it.BitDepth, &it.AudioLangs, &it.ProbeJSON, &it.Confidence,
		&it.Status, &it.StatusReason, &it.ProbeAttempts, &imagesDone, &it.AddedAt, &it.UpdatedAt}
	dests = append(dests, extra...)
	if err := row.Scan(dests...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	it.SeasonNumber = nullToPtr(season)
	it.EpisodeNumber = nullToPtr(ep)
	it.EpisodeEndNumber = nullToPtr(epEnd)
	it.ImagesDone = imagesDone == 1
	return it, nil
}

func (d *DB) GetItem(ctx context.Context, id string) (*Item, error) {
	var seriesTitle string
	it, err := scanItemRow(d.R.QueryRowContext(ctx, `SELECT `+itemCols+`, COALESCE((SELECT s.title FROM items s WHERE s.id = i.series_id),'') FROM items i WHERE i.id = ?`, id), &seriesTitle)
	if err != nil {
		return nil, err
	}
	it.SeriesTitle = seriesTitle
	return it, nil
}

// GetFileItem looks up a file-backed item by its root and relative path.
func (d *DB) GetFileItem(ctx context.Context, libraryPathID, relPath string) (*Item, error) {
	return scanItemRow(d.R.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_path_id = ? AND i.rel_path = ?`, libraryPathID, relPath))
}

// ListFileItemsUnder returns file-backed items of a root whose rel_path is
// prefix or below it ("" = all).
func (d *DB) ListFileItemsUnder(ctx context.Context, libraryPathID, prefix string) ([]*Item, error) {
	q := `SELECT ` + itemCols + ` FROM items i WHERE i.library_path_id = ?`
	args := []any{libraryPathID}
	if prefix != "" {
		q += ` AND (i.rel_path = ? OR i.rel_path LIKE ? ESCAPE '\')`
		args = append(args, prefix, escapeLike(prefix)+"/%")
	}
	return d.queryItems(ctx, q, args...)
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (d *DB) queryItems(ctx context.Context, q string, args ...any) ([]*Item, error) {
	rows, err := d.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItemRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// InsertItem inserts a new item row.
func (d *DB) InsertItem(ctx context.Context, it *Item) error {
	if it.ID == "" {
		it.ID = NewID()
	}
	now := Now()
	if it.AddedAt == "" {
		it.AddedAt = now
	}
	it.UpdatedAt = now
	if it.Confidence == "" {
		it.Confidence = "high"
	}
	if it.Status == "" {
		it.Status = "ok"
	}
	_, err := d.W.ExecContext(ctx, `INSERT INTO items(id, type, library_id, parent_id, series_id, series_key, title, sort_title, year, overview,
		season_number, episode_number, episode_end_number, premiere_date, library_path_id, rel_path, size, mtime, container, runtime_ms, bitrate,
		video_codec, width, height, hdr, bit_depth, audio_langs, probe_json, confidence, status, status_reason, probe_attempts, images_done, added_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.Type, it.LibraryID, nullStr(it.ParentID), nullStr(it.SeriesID), nullStr(it.SeriesKey), it.Title, it.SortTitle, nullInt(int64(it.Year)), it.Overview,
		ptrToNull(it.SeasonNumber), ptrToNull(it.EpisodeNumber), ptrToNull(it.EpisodeEndNumber), nullStr(it.PremiereDate),
		nullStr(it.LibraryPathID), nullStr(it.RelPath), it.Size, nullStr(it.Mtime), nullStr(it.Container), it.RuntimeMs, it.Bitrate,
		nullStr(it.VideoCodec), it.Width, it.Height, nullStr(it.HDR), it.BitDepth, nullStr(it.AudioLangs), nullStr(it.ProbeJSON), it.Confidence,
		it.Status, nullStr(it.StatusReason), it.ProbeAttempts, boolInt(it.ImagesDone), it.AddedAt, it.UpdatedAt)
	return err
}

// UpdateItemNaming updates parse-derived fields of an item.
func (d *DB) UpdateItemNaming(ctx context.Context, it *Item) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET type = ?, parent_id = ?, series_id = ?, series_key = ?, title = ?, sort_title = ?, year = ?,
		season_number = ?, episode_number = ?, episode_end_number = ?, confidence = ?, updated_at = ? WHERE id = ?`,
		it.Type, nullStr(it.ParentID), nullStr(it.SeriesID), nullStr(it.SeriesKey), it.Title, it.SortTitle, nullInt(int64(it.Year)),
		ptrToNull(it.SeasonNumber), ptrToNull(it.EpisodeNumber), ptrToNull(it.EpisodeEndNumber), it.Confidence, Now(), it.ID)
	return err
}

// MarkItemChanged records new size/mtime and re-queues the item for probing.
func (d *DB) MarkItemChanged(ctx context.Context, id string, size int64, mtime string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET size = ?, mtime = ?, status = 'pending', status_reason = NULL, probe_attempts = 0, images_done = 0, updated_at = ? WHERE id = ?`,
		size, mtime, Now(), id)
	return err
}

// SetItemStatus sets status (and reason) of an item.
func (d *DB) SetItemStatus(ctx context.Context, id, status, reason string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET status = ?, status_reason = ?, updated_at = ? WHERE id = ?`, status, nullStr(reason), Now(), id)
	return err
}

// RestoreMissing turns a missing item back into pending (file reappeared).
func (d *DB) RestoreMissing(ctx context.Context, id string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET status = CASE WHEN probe_json IS NULL OR probe_json = '' THEN 'pending' ELSE 'ok' END, status_reason = NULL, updated_at = ? WHERE id = ? AND status = 'missing'`, Now(), id)
	return err
}

// ProbeResult carries the fields written after a successful probe.
type ProbeResult struct {
	Container  string
	RuntimeMs  int64
	Bitrate    int64
	VideoCodec string
	Width      int
	Height     int
	HDR        string
	BitDepth   int
	AudioLangs string
	ProbeJSON  string
	Streams    []MediaStream
	Chapters   []Chapter
}

// SaveProbe stores probe output and marks the item ok.
func (d *DB) SaveProbe(ctx context.Context, itemID string, p *ProbeResult) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE items SET container = ?, runtime_ms = ?, bitrate = ?, video_codec = ?, width = ?, height = ?, hdr = ?, bit_depth = ?,
		audio_langs = ?, probe_json = ?, status = 'ok', status_reason = NULL, updated_at = ? WHERE id = ?`,
		p.Container, p.RuntimeMs, p.Bitrate, nullStr(p.VideoCodec), p.Width, p.Height, nullStr(p.HDR), p.BitDepth, nullStr(p.AudioLangs), p.ProbeJSON, Now(), itemID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_streams WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, s := range p.Streams {
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_streams(item_id, idx, type, codec, profile, level, codec_tag, width, height, pix_fmt, bit_depth, frame_rate,
			color_transfer, color_primaries, color_space, hdr, dv_profile, dv_bl_compat, field_order, bitrate, channels, channel_layout, sample_rate,
			language, title, is_default, is_forced, is_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			itemID, s.Index, s.Type, s.Codec, nullStr(s.Profile), s.Level, nullStr(s.CodecTag), s.Width, s.Height, nullStr(s.PixFmt), s.BitDepth, s.FrameRate,
			nullStr(s.ColorTransfer), nullStr(s.ColorPrimaries), nullStr(s.ColorSpace), nullStr(s.HDR), s.DVProfile, s.DVBLCompat, nullStr(s.FieldOrder), s.Bitrate,
			s.Channels, nullStr(s.ChannelLayout), s.SampleRate, nullStr(s.Language), nullStr(s.Title), boolInt(s.IsDefault), boolInt(s.IsForced), boolInt(s.IsText)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, c := range p.Chapters {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chapters(item_id, idx, start_ms, end_ms, title) VALUES(?,?,?,?,?)`, itemID, c.Index, c.StartMs, c.EndMs, nullStr(c.Title)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordProbeFailure keeps the item pending and increments its attempt count.
func (d *DB) RecordProbeFailure(ctx context.Context, itemID, reason string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET status = 'pending', status_reason = ?, probe_attempts = probe_attempts + 1, updated_at = ? WHERE id = ?`, reason, Now(), itemID)
	return err
}

func (d *DB) SetImagesDone(ctx context.Context, itemID string, done bool) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET images_done = ? WHERE id = ?`, boolInt(done), itemID)
	return err
}

// ItemsNeedingProbe returns pending file items of a library with attempts < max.
func (d *DB) ItemsNeedingProbe(ctx context.Context, libraryID string, maxAttempts int) ([]*Item, error) {
	return d.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_id = ? AND i.status = 'pending' AND i.type IN ('movie','episode') AND i.probe_attempts < ? ORDER BY i.added_at`, libraryID, maxAttempts)
}

// ItemsNeedingImages returns ok file items without extracted images.
func (d *DB) ItemsNeedingImages(ctx context.Context, libraryID string) ([]*Item, error) {
	return d.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_id = ? AND i.status = 'ok' AND i.type IN ('movie','episode') AND i.images_done = 0 ORDER BY i.added_at`, libraryID)
}

// --- series / seasons ---

func (d *DB) FindSeries(ctx context.Context, libraryID, key string) (*Item, error) {
	return scanItemRow(d.R.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_id = ? AND i.type = 'series' AND i.series_key = ?`, libraryID, key))
}

func (d *DB) FindSeason(ctx context.Context, seriesID string, season int) (*Item, error) {
	return scanItemRow(d.R.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.parent_id = ? AND i.type = 'season' AND i.season_number = ?`, seriesID, season))
}

func (d *DB) UpdateSeriesTitle(ctx context.Context, id, title, sortTitle string, year int) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET title = ?, sort_title = ?, year = COALESCE(year, ?), updated_at = ? WHERE id = ?`, title, sortTitle, nullInt(int64(year)), Now(), id)
	return err
}

// DeleteEmptyVirtualItems removes seasons without episodes and series without
// seasons in a library.
func (d *DB) DeleteEmptyVirtualItems(ctx context.Context, libraryID string) error {
	if _, err := d.W.ExecContext(ctx, `DELETE FROM items WHERE library_id = ? AND type = 'season' AND NOT EXISTS (SELECT 1 FROM items e WHERE e.parent_id = items.id)`, libraryID); err != nil {
		return err
	}
	_, err := d.W.ExecContext(ctx, `DELETE FROM items WHERE library_id = ? AND type = 'series' AND NOT EXISTS (SELECT 1 FROM items e WHERE e.parent_id = items.id)`, libraryID)
	return err
}

// DeleteMissing deletes missing items of a library and returns the count.
func (d *DB) DeleteMissing(ctx context.Context, libraryID string) (int, error) {
	res, err := d.W.ExecContext(ctx, `DELETE FROM items WHERE library_id = ? AND status = 'missing'`, libraryID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if err := d.DeleteEmptyVirtualItems(ctx, libraryID); err != nil {
		return int(n), err
	}
	return int(n), nil
}

// MarkMissingExcept marks file items of a root missing unless their rel_path
// is in seen. Returns the number newly marked.
func (d *DB) MarkMissingExcept(ctx context.Context, libraryPathID string, seen map[string]bool) (int, error) {
	items, err := d.ListFileItemsUnder(ctx, libraryPathID, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if it.Status == "missing" || seen[it.RelPath] {
			continue
		}
		if err := d.SetItemStatus(ctx, it.ID, "missing", "file not found"); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// --- streams, chapters, external subtitles, images ---

type MediaStream struct {
	Index          int
	Type           string
	Codec          string
	Profile        string
	Level          int
	CodecTag       string
	Width          int
	Height         int
	PixFmt         string
	BitDepth       int
	FrameRate      float64
	ColorTransfer  string
	ColorPrimaries string
	ColorSpace     string
	HDR            string
	DVProfile      int
	DVBLCompat     int
	FieldOrder     string
	Bitrate        int64
	Channels       int
	ChannelLayout  string
	SampleRate     int
	Language       string
	Title          string
	IsDefault      bool
	IsForced       bool
	IsText         bool
}

func (d *DB) GetStreams(ctx context.Context, itemID string) ([]MediaStream, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT idx, type, codec, COALESCE(profile,''), COALESCE(level,0), COALESCE(codec_tag,''), COALESCE(width,0), COALESCE(height,0),
		COALESCE(pix_fmt,''), COALESCE(bit_depth,0), COALESCE(frame_rate,0), COALESCE(color_transfer,''), COALESCE(color_primaries,''), COALESCE(color_space,''),
		COALESCE(hdr,''), COALESCE(dv_profile,0), COALESCE(dv_bl_compat,0), COALESCE(field_order,''), COALESCE(bitrate,0), COALESCE(channels,0), COALESCE(channel_layout,''),
		COALESCE(sample_rate,0), COALESCE(language,''), COALESCE(title,''), is_default, is_forced, is_text FROM media_streams WHERE item_id = ? ORDER BY idx`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaStream
	for rows.Next() {
		var s MediaStream
		var def, forced, text int
		if err := rows.Scan(&s.Index, &s.Type, &s.Codec, &s.Profile, &s.Level, &s.CodecTag, &s.Width, &s.Height, &s.PixFmt, &s.BitDepth, &s.FrameRate,
			&s.ColorTransfer, &s.ColorPrimaries, &s.ColorSpace, &s.HDR, &s.DVProfile, &s.DVBLCompat, &s.FieldOrder, &s.Bitrate, &s.Channels, &s.ChannelLayout,
			&s.SampleRate, &s.Language, &s.Title, &def, &forced, &text); err != nil {
			return nil, err
		}
		s.IsDefault, s.IsForced, s.IsText = def == 1, forced == 1, text == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

type Chapter struct {
	Index   int
	StartMs int64
	EndMs   int64
	Title   string
}

func (d *DB) GetChapters(ctx context.Context, itemID string) ([]Chapter, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT idx, start_ms, end_ms, COALESCE(title,'') FROM chapters WHERE item_id = ? ORDER BY idx`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.Index, &c.StartMs, &c.EndMs, &c.Title); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type ExternalSubtitle struct {
	ID       string
	ItemID   string
	RelPath  string
	Format   string
	Language string
	Title    string
	IsForced bool
	IsSDH    bool
}

// ReplaceExternalSubtitles syncs the external subtitle rows of an item,
// keeping ids of unchanged rel_paths stable.
func (d *DB) ReplaceExternalSubtitles(ctx context.Context, itemID string, subs []ExternalSubtitle) error {
	existing, err := d.ListExternalSubtitles(ctx, itemID)
	if err != nil {
		return err
	}
	byPath := map[string]ExternalSubtitle{}
	for _, e := range existing {
		byPath[e.RelPath] = e
	}
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	for _, s := range subs {
		if e, ok := byPath[s.RelPath]; ok {
			keep[e.ID] = true
			if _, err := tx.ExecContext(ctx, `UPDATE external_subtitles SET format = ?, language = ?, title = ?, is_forced = ?, is_sdh = ? WHERE id = ?`,
				s.Format, nullStr(s.Language), nullStr(s.Title), boolInt(s.IsForced), boolInt(s.IsSDH), e.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_subtitles(id, item_id, rel_path, format, language, title, is_forced, is_sdh) VALUES(?,?,?,?,?,?,?,?)`,
			NewID(), itemID, s.RelPath, s.Format, nullStr(s.Language), nullStr(s.Title), boolInt(s.IsForced), boolInt(s.IsSDH)); err != nil {
			return err
		}
	}
	for _, e := range existing {
		if !keep[e.ID] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM external_subtitles WHERE id = ?`, e.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) ListExternalSubtitles(ctx context.Context, itemID string) ([]ExternalSubtitle, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT id, item_id, rel_path, format, COALESCE(language,''), COALESCE(title,''), is_forced, is_sdh FROM external_subtitles WHERE item_id = ? ORDER BY rel_path`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalSubtitle
	for rows.Next() {
		var s ExternalSubtitle
		var forced, sdh int
		if err := rows.Scan(&s.ID, &s.ItemID, &s.RelPath, &s.Format, &s.Language, &s.Title, &forced, &sdh); err != nil {
			return nil, err
		}
		s.IsForced, s.IsSDH = forced == 1, sdh == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

type Image struct {
	ItemID  string
	Kind    string
	Source  string
	Path    string
	Width   int
	Height  int
	Updated string
}

func (d *DB) SetImage(ctx context.Context, img Image) error {
	_, err := d.W.ExecContext(ctx, `INSERT INTO images(item_id, kind, source, path, width, height, updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(item_id, kind) DO UPDATE SET source = excluded.source, path = excluded.path, width = excluded.width, height = excluded.height, updated_at = excluded.updated_at`,
		img.ItemID, img.Kind, img.Source, img.Path, img.Width, img.Height, Now())
	return err
}

func (d *DB) GetImage(ctx context.Context, itemID, kind string) (*Image, error) {
	var img Image
	err := d.R.QueryRowContext(ctx, `SELECT item_id, kind, source, path, COALESCE(width,0), COALESCE(height,0), updated_at FROM images WHERE item_id = ? AND kind = ?`, itemID, kind).
		Scan(&img.ItemID, &img.Kind, &img.Source, &img.Path, &img.Width, &img.Height, &img.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &img, err
}

// ImageKinds returns item id → set of image kinds present for the given ids.
func (d *DB) ImageKinds(ctx context.Context, ids []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := d.R.QueryContext(ctx, fmt.Sprintf(`SELECT item_id, kind FROM images WHERE item_id IN (%s)`, ph), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, kind string
			if err := rows.Scan(&id, &kind); err != nil {
				rows.Close()
				return nil, err
			}
			if out[id] == nil {
				out[id] = map[string]bool{}
			}
			out[id][kind] = true
		}
		rows.Close()
	}
	return out, nil
}

// FirstEpisode returns the first (by season, episode) non-missing episode of a series or season.
func (d *DB) FirstEpisode(ctx context.Context, parentID string) (*Item, error) {
	return scanItemRow(d.R.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE (i.series_id = ? OR i.parent_id = ?) AND i.type = 'episode' AND i.status <> 'missing'
		ORDER BY CASE WHEN COALESCE(i.season_number,0) = 0 THEN 1 ELSE 0 END, i.season_number, i.episode_number LIMIT 1`, parentID, parentID))
}

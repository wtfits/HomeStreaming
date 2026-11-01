package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// UserItemData is per-user playback state of an item.
type UserItemData struct {
	PositionMs       int64
	Played           bool
	PlayCount        int
	LastPlayedAt     string
	AudioLanguage    string
	SubtitleLanguage string
}

func (d *DB) GetUserData(ctx context.Context, userID, itemID string) (*UserItemData, error) {
	var u UserItemData
	var played int
	var last sql.NullString
	err := d.R.QueryRowContext(ctx, `SELECT position_ms, played, play_count, last_played_at, COALESCE(audio_language,''), COALESCE(subtitle_language,'')
		FROM user_item_data WHERE user_id = ? AND item_id = ?`, userID, itemID).Scan(&u.PositionMs, &played, &u.PlayCount, &last, &u.AudioLanguage, &u.SubtitleLanguage)
	if errors.Is(err, sql.ErrNoRows) {
		return &UserItemData{}, nil
	}
	if err != nil {
		return nil, err
	}
	u.Played = played == 1
	u.LastPlayedAt = last.String
	return &u, nil
}

func (d *DB) SetUserData(ctx context.Context, userID, itemID string, u *UserItemData) error {
	_, err := d.W.ExecContext(ctx, `INSERT INTO user_item_data(user_id, item_id, position_ms, played, play_count, last_played_at, audio_language, subtitle_language, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position_ms = excluded.position_ms, played = excluded.played, play_count = excluded.play_count,
		last_played_at = excluded.last_played_at, audio_language = excluded.audio_language, subtitle_language = excluded.subtitle_language, updated_at = excluded.updated_at`,
		userID, itemID, u.PositionMs, boolInt(u.Played), u.PlayCount, nullStr(u.LastPlayedAt), nullStr(u.AudioLanguage), nullStr(u.SubtitleLanguage), Now())
	return err
}

// ItemQuery filters GET /items.
type ItemQuery struct {
	UserID    string
	LibraryID string
	Types     []string
	ParentID  string
	SeriesID  string
	Search    string
	Sort      string // title|added|year|premiere|lastPlayed|episode
	Order     string // asc|desc
	Played    *bool
	Status    string // visible (default: ok+pending)|ok|pending|missing|all
	Offset    int
	Limit     int
	ItemIDs   []string
}

const userDataCols = `COALESCE(u.position_ms,0), COALESCE(u.played,0), COALESCE(u.play_count,0), COALESCE(u.last_played_at,'')`

const derivedCols = `COALESCE(s.title,''),
 CASE WHEN i.type='series' THEN (SELECT COUNT(*) FROM items e WHERE e.series_id = i.id AND e.type = 'episode' AND e.status <> 'missing')
      WHEN i.type='season' THEN (SELECT COUNT(*) FROM items e WHERE e.parent_id = i.id AND e.type = 'episode' AND e.status <> 'missing') END,
 CASE WHEN i.type='series' THEN (SELECT COUNT(*) FROM items e LEFT JOIN user_item_data ue ON ue.item_id = e.id AND ue.user_id = ?1 WHERE e.series_id = i.id AND e.type = 'episode' AND e.status <> 'missing' AND COALESCE(ue.played,0) = 0)
      WHEN i.type='season' THEN (SELECT COUNT(*) FROM items e LEFT JOIN user_item_data ue ON ue.item_id = e.id AND ue.user_id = ?1 WHERE e.parent_id = i.id AND e.type = 'episode' AND e.status <> 'missing' AND COALESCE(ue.played,0) = 0) END`

// ListItems returns a page of items with user data and derived fields.
func (d *DB) ListItems(ctx context.Context, q ItemQuery) ([]*Item, int, error) {
	where := []string{"1=1"}
	args := []any{q.UserID} // ?1 is the user id (used by derivedCols and the user-data join)

	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if q.LibraryID != "" {
		add("i.library_id = ?", q.LibraryID)
	}
	if len(q.Types) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.Types)), ",")
		a := make([]any, len(q.Types))
		for i, t := range q.Types {
			a[i] = t
		}
		add("i.type IN ("+ph+")", a...)
	}
	if len(q.ItemIDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.ItemIDs)), ",")
		a := make([]any, len(q.ItemIDs))
		for i, t := range q.ItemIDs {
			a[i] = t
		}
		add("i.id IN ("+ph+")", a...)
	}
	if q.ParentID != "" {
		add("i.parent_id = ?", q.ParentID)
	}
	if q.SeriesID != "" {
		add("i.series_id = ?", q.SeriesID)
	}
	if q.Search != "" {
		pat := "%" + escapeLike(strings.ToLower(q.Search)) + "%"
		add(`(lower(i.title) LIKE ? ESCAPE '\' OR lower(COALESCE(s.title,'')) LIKE ? ESCAPE '\')`, pat, pat)
	}
	switch q.Status {
	case "ok", "pending", "missing":
		add("i.status = ?", q.Status)
	case "all":
	default:
		where = append(where, "i.status <> 'missing'")
	}
	if q.Status != "all" && q.Status != "missing" {
		// Hide series/seasons whose episodes are all missing.
		where = append(where, `(i.type NOT IN ('series','season') OR EXISTS (SELECT 1 FROM items e WHERE (e.series_id = i.id OR e.parent_id = i.id) AND e.type = 'episode' AND e.status <> 'missing'))`)
	}
	if q.Played != nil {
		if *q.Played {
			where = append(where, "COALESCE(u.played,0) = 1")
		} else {
			where = append(where, "COALESCE(u.played,0) = 0")
		}
	}

	from := ` FROM items i LEFT JOIN items s ON s.id = i.series_id LEFT JOIN user_item_data u ON u.item_id = i.id AND u.user_id = ?1 WHERE ` + strings.Join(where, " AND ")

	var total int
	if err := d.R.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := q.Order
	desc := func(def bool) string {
		if order == "desc" || (order == "" && def) {
			return " DESC"
		}
		return " ASC"
	}
	var orderBy string
	switch q.Sort {
	case "added":
		orderBy = "i.added_at" + desc(true) + ", i.sort_title"
	case "year":
		orderBy = "COALESCE(i.year,0)" + desc(false) + ", i.sort_title"
	case "premiere":
		orderBy = "COALESCE(i.premiere_date,'')" + desc(false) + ", i.sort_title"
	case "lastPlayed":
		orderBy = "COALESCE(u.last_played_at,'')" + desc(true) + ", i.sort_title"
	case "episode":
		orderBy = "CASE WHEN COALESCE(i.season_number,0) = 0 THEN 1 ELSE 0 END, i.season_number" + desc(false) + ", i.episode_number" + desc(false) + ", i.sort_title"
	default:
		orderBy = "i.sort_title" + desc(false) + ", i.id"
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	rowsArgs := append(append([]any{}, args...), limit, q.Offset)
	rows, err := d.R.QueryContext(ctx, `SELECT `+itemCols+`, `+userDataCols+`, `+derivedCols+from+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, rowsArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		var ud UserItemData
		var played int
		var seriesTitle string
		var childCount, unplayed sql.NullInt64
		it, err := scanItemRow(rows, &ud.PositionMs, &played, &ud.PlayCount, &ud.LastPlayedAt, &seriesTitle, &childCount, &unplayed)
		if err != nil {
			return nil, 0, err
		}
		ud.Played = played == 1
		it.UserData = &ud
		it.SeriesTitle = seriesTitle
		it.ChildCount = nullToPtr(childCount)
		it.UnplayedCount = nullToPtr(unplayed)
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// ContinueWatching returns in-progress movies/episodes, most recent first.
func (d *DB) ContinueWatching(ctx context.Context, userID string, limit int) ([]string, error) {
	return d.queryIDs(ctx, `SELECT i.id FROM items i JOIN user_item_data u ON u.item_id = i.id AND u.user_id = ?
		WHERE i.type IN ('movie','episode') AND i.status <> 'missing' AND u.position_ms > 0 AND u.played = 0
		ORDER BY u.last_played_at DESC LIMIT ?`, userID, limit)
}

// RecentlyAdded returns newest movies/episodes.
func (d *DB) RecentlyAdded(ctx context.Context, limit int) ([]string, error) {
	return d.queryIDs(ctx, `SELECT i.id FROM items i WHERE i.type IN ('movie','episode') AND i.status <> 'missing' ORDER BY i.added_at DESC, i.sort_title LIMIT ?`, limit)
}

// SeriesWithHistory returns series ids where the user has played or started an
// episode, most recently watched first.
func (d *DB) SeriesWithHistory(ctx context.Context, userID string) ([]string, error) {
	return d.queryIDs(ctx, `SELECT i.series_id FROM items i JOIN user_item_data u ON u.item_id = i.id AND u.user_id = ?
		WHERE i.type = 'episode' AND i.series_id IS NOT NULL AND (u.played = 1 OR u.position_ms > 0)
		GROUP BY i.series_id ORDER BY MAX(COALESCE(u.last_played_at,'')) DESC`, userID)
}

// EpisodeState is an episode with its user data, ordered for next-up logic.
type EpisodeState struct {
	ID         string
	Season     int
	Episode    int
	Played     bool
	PositionMs int64
	Missing    bool
}

// SeriesEpisodes returns all episodes of a series in (season, episode) order,
// specials (season 0) last.
func (d *DB) SeriesEpisodes(ctx context.Context, userID, seriesID string) ([]EpisodeState, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT i.id, COALESCE(i.season_number,0), COALESCE(i.episode_number,0), COALESCE(u.played,0), COALESCE(u.position_ms,0), i.status = 'missing'
		FROM items i LEFT JOIN user_item_data u ON u.item_id = i.id AND u.user_id = ?
		WHERE i.series_id = ? AND i.type = 'episode'
		ORDER BY CASE WHEN COALESCE(i.season_number,0) = 0 THEN 1 ELSE 0 END, i.season_number, i.episode_number`, userID, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodeState
	for rows.Next() {
		var e EpisodeState
		var played, missing int
		if err := rows.Scan(&e.ID, &e.Season, &e.Episode, &played, &e.PositionMs, &missing); err != nil {
			return nil, err
		}
		e.Played, e.Missing = played == 1, missing == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *DB) queryIDs(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := d.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EpisodeIDsUnder returns episode ids of a series or season.
func (d *DB) EpisodeIDsUnder(ctx context.Context, id string) ([]string, error) {
	return d.queryIDs(ctx, `SELECT id FROM items WHERE type = 'episode' AND (series_id = ? OR parent_id = ?)`, id, id)
}

// ProblemItems returns pending, missing and low-confidence items.
func (d *DB) ProblemItems(ctx context.Context, limit int) ([]*Item, error) {
	return d.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.type IN ('movie','episode') AND (i.status <> 'ok' OR i.confidence = 'low') ORDER BY i.status, i.rel_path LIMIT ?`, limit)
}

// ProblemCounts returns counts of pending, missing, low-confidence items.
func (d *DB) ProblemCounts(ctx context.Context) (pending, missing, low int, err error) {
	err = d.R.QueryRowContext(ctx, `SELECT COALESCE(SUM(status='pending'),0), COALESCE(SUM(status='missing'),0), COALESCE(SUM(status='ok' AND confidence='low'),0)
		FROM items WHERE type IN ('movie','episode')`).Scan(&pending, &missing, &low)
	return
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Preferences are per-user playback preferences.
type Preferences struct {
	AudioLanguage    string `json:"audioLanguage"`
	SubtitleLanguage string `json:"subtitleLanguage"`
	SubtitleMode     string `json:"subtitleMode"` // off|preferred|always
}

// DefaultPreferences returns the defaults for new users.
func DefaultPreferences() Preferences {
	return Preferences{AudioLanguage: "eng", SubtitleLanguage: "", SubtitleMode: "off"}
}

type User struct {
	ID           string
	Username     string
	PasswordHash string
	IsAdmin      bool
	Preferences  Preferences
	Disabled     bool
	CreatedAt    string
	UpdatedAt    string
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	DeviceID   string
	DeviceName string
	ClientType string
	UserAgent  string
	IP         string
	CreatedAt  string
	LastSeenAt string
	ExpiresAt  string
}

const userCols = `id, username, password_hash, is_admin, preferences_json, disabled, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var prefs string
	var isAdmin, disabled int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &prefs, &disabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin, u.Disabled = isAdmin == 1, disabled == 1
	u.Preferences = DefaultPreferences()
	_ = json.Unmarshal([]byte(prefs), &u.Preferences)
	return &u, nil
}

func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) CreateUser(ctx context.Context, username, passwordHash string, isAdmin bool) (*User, error) {
	now := Now()
	prefs, _ := json.Marshal(DefaultPreferences())
	u := &User{ID: NewID(), Username: username, PasswordHash: passwordHash, IsAdmin: isAdmin, Preferences: DefaultPreferences(), CreatedAt: now, UpdatedAt: now}
	_, err := d.W.ExecContext(ctx, `INSERT INTO users(`+userCols+`) VALUES(?,?,?,?,?,0,?,?)`,
		u.ID, u.Username, u.PasswordHash, boolInt(isAdmin), string(prefs), now, now)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (d *DB) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(d.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(d.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, username))
}

func (d *DB) UpdatePreferences(ctx context.Context, userID string, p Preferences) error {
	b, _ := json.Marshal(p)
	_, err := d.W.ExecContext(ctx, `UPDATE users SET preferences_json = ?, updated_at = ? WHERE id = ?`, string(b), Now(), userID)
	return err
}

func (d *DB) UpdatePassword(ctx context.Context, userID, hash string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, Now(), userID)
	return err
}

// --- sessions ---

const sessionCols = `id, user_id, token_hash, device_id, device_name, client_type, COALESCE(user_agent,''), COALESCE(ip,''), created_at, last_seen_at, expires_at`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var s Session
	if err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.DeviceID, &s.DeviceName, &s.ClientType, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// CreateSession inserts a session; an existing session of the same user and
// device id is replaced.
func (d *DB) CreateSession(ctx context.Context, s *Session) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.DeviceID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND device_id = ?`, s.UserID, s.DeviceID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, user_id, token_hash, device_id, device_name, client_type, user_agent, ip, created_at, last_seen_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, s.ID, s.UserID, s.TokenHash, s.DeviceID, s.DeviceName, s.ClientType, s.UserAgent, s.IP, s.CreatedAt, s.LastSeenAt, s.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) GetSessionByTokenHash(ctx context.Context, hash string) (*Session, error) {
	return scanSession(d.R.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE token_hash = ?`, hash))
}

func (d *DB) GetSession(ctx context.Context, id string) (*Session, error) {
	return scanSession(d.R.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
}

func (d *DB) TouchSession(ctx context.Context, id, ip string, lastSeen, expires time.Time) error {
	_, err := d.W.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ?, ip = ? WHERE id = ?`, FormatTime(lastSeen), FormatTime(expires), ip, id)
	return err
}

func (d *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (d *DB) DeleteOtherSessions(ctx context.Context, userID, keepID string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepID)
	return err
}

func (d *DB) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) PruneExpiredSessions(ctx context.Context) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, Now())
	return err
}

// --- login attempts ---

type LoginAttempt struct {
	Key         string
	Failures    int
	LockedUntil time.Time
}

func (d *DB) GetLoginAttempt(ctx context.Context, key string) (*LoginAttempt, error) {
	var a LoginAttempt
	var locked sql.NullString
	err := d.R.QueryRowContext(ctx, `SELECT key, failures, locked_until FROM login_attempts WHERE key = ?`, key).Scan(&a.Key, &a.Failures, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return &LoginAttempt{Key: key}, nil
	}
	if err != nil {
		return nil, err
	}
	if locked.Valid {
		a.LockedUntil = ParseTime(locked.String)
	}
	return &a, nil
}

func (d *DB) SetLoginAttempt(ctx context.Context, a *LoginAttempt) error {
	var locked sql.NullString
	if !a.LockedUntil.IsZero() {
		locked = sql.NullString{String: FormatTime(a.LockedUntil), Valid: true}
	}
	_, err := d.W.ExecContext(ctx, `INSERT INTO login_attempts(key, failures, locked_until, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET failures = excluded.failures, locked_until = excluded.locked_until, updated_at = excluded.updated_at`,
		a.Key, a.Failures, locked, Now())
	return err
}

func (d *DB) ResetLoginAttempt(ctx context.Context, key string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM login_attempts WHERE key = ?`, key)
	return err
}

func (d *DB) PruneLoginAttempts(ctx context.Context, olderThan time.Time) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM login_attempts WHERE updated_at < ? AND (locked_until IS NULL OR locked_until < ?)`, FormatTime(olderThan), Now())
	return err
}

// --- settings ---

func (d *DB) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (d *DB) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

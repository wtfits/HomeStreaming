// Package db wraps the SQLite database: opening, migrations and typed queries.
package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB holds a single-connection write pool and a multi-connection read pool.
type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*DB, error) {
	pragmas := []string{"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "busy_timeout(5000)"}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	dsn := "file:" + path + "?" + q.Encode()

	w, err := sql.Open("sqlite", dsn+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)

	d := &DB{W: w, R: r, Path: path}
	if err := d.migrate(context.Background()); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// CheckWritable performs a small write; it fails when SQLite fell back to a
// read-only open (the OS denied write access to the database file).
func (d *DB) CheckWritable(ctx context.Context) error {
	_, err := d.W.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES('last_start', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, Now(), Now())
	return err
}

// Close checkpoints the WAL and closes both pools.
func (d *DB) Close() error {
	_, _ = d.W.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err1 := d.R.Close()
	err2 := d.W.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %s", name)
		}
		migs = append(migs, mig{v, name})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		var n int
		if err := d.W.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := d.W.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, m.version, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// NewID returns a random 32-char lowercase hex id.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// TimeFormat is the canonical timestamp layout stored in the database.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Now returns the current UTC time formatted for storage.
func Now() string { return FormatTime(time.Now()) }

// FormatTime formats t for storage.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTime parses a stored timestamp; zero time on error.
func ParseTime(s string) time.Time {
	t, err := time.Parse(TimeFormat, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(i int64) sql.NullInt64 {
	return sql.NullInt64{Int64: i, Valid: i != 0}
}

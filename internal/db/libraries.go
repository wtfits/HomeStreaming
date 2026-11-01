package db

import (
	"context"
	"database/sql"
	"errors"
)

type Library struct {
	ID             string
	Name           string
	Type           string // mixed|movies|shows
	Paths          []LibraryPath
	CreatedAt      string
	LastScanAt     string
	LastScanStatus string
	LastScanError  string
}

type LibraryPath struct {
	ID        string
	LibraryID string
	Path      string
	Position  int
}

type LibraryCounts struct {
	Movies   int `json:"movies"`
	Series   int `json:"series"`
	Episodes int `json:"episodes"`
	Pending  int `json:"pending"`
	Missing  int `json:"missing"`
}

func (d *DB) CreateLibrary(ctx context.Context, name, typ string, paths []string) (*Library, error) {
	lib := &Library{ID: NewID(), Name: name, Type: typ, CreatedAt: Now()}
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO libraries(id, name, type, created_at) VALUES(?,?,?,?)`, lib.ID, name, typ, lib.CreatedAt); err != nil {
		return nil, err
	}
	for i, p := range paths {
		lp := LibraryPath{ID: NewID(), LibraryID: lib.ID, Path: p, Position: i}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_paths(id, library_id, path, position) VALUES(?,?,?,?)`, lp.ID, lp.LibraryID, lp.Path, lp.Position); err != nil {
			return nil, err
		}
		lib.Paths = append(lib.Paths, lp)
	}
	return lib, tx.Commit()
}

func (d *DB) ListLibraries(ctx context.Context) ([]*Library, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT id, name, type, created_at, COALESCE(last_scan_at,''), COALESCE(last_scan_status,''), COALESCE(last_scan_error,'') FROM libraries ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	var libs []*Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.CreatedAt, &l.LastScanAt, &l.LastScanStatus, &l.LastScanError); err != nil {
			rows.Close()
			return nil, err
		}
		libs = append(libs, &l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	paths, err := d.listAllPaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		l.Paths = paths[l.ID]
	}
	return libs, nil
}

func (d *DB) listAllPaths(ctx context.Context) (map[string][]LibraryPath, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT id, library_id, path, position FROM library_paths ORDER BY library_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]LibraryPath{}
	for rows.Next() {
		var p LibraryPath
		if err := rows.Scan(&p.ID, &p.LibraryID, &p.Path, &p.Position); err != nil {
			return nil, err
		}
		out[p.LibraryID] = append(out[p.LibraryID], p)
	}
	return out, rows.Err()
}

func (d *DB) GetLibrary(ctx context.Context, id string) (*Library, error) {
	var l Library
	err := d.R.QueryRowContext(ctx, `SELECT id, name, type, created_at, COALESCE(last_scan_at,''), COALESCE(last_scan_status,''), COALESCE(last_scan_error,'') FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Type, &l.CreatedAt, &l.LastScanAt, &l.LastScanStatus, &l.LastScanError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := d.R.QueryContext(ctx, `SELECT id, library_id, path, position FROM library_paths WHERE library_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p LibraryPath
		if err := rows.Scan(&p.ID, &p.LibraryID, &p.Path, &p.Position); err != nil {
			return nil, err
		}
		l.Paths = append(l.Paths, p)
	}
	return &l, rows.Err()
}

func (d *DB) GetLibraryPath(ctx context.Context, id string) (*LibraryPath, error) {
	var p LibraryPath
	err := d.R.QueryRowContext(ctx, `SELECT id, library_id, path, position FROM library_paths WHERE id = ?`, id).Scan(&p.ID, &p.LibraryID, &p.Path, &p.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (d *DB) RenameLibrary(ctx context.Context, id, name string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE libraries SET name = ? WHERE id = ?`, name, id)
	return err
}

// PathUpdate relocates an existing root (ID set) or adds a new one (ID empty).
type PathUpdate struct {
	ID   string
	Path string
}

// SetLibraryPaths replaces the library's root list. Existing rows referenced by
// id keep their id (and therefore their items); rows not referenced are deleted
// together with their items.
func (d *DB) SetLibraryPaths(ctx context.Context, libraryID string, updates []PathUpdate) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	for i, u := range updates {
		if u.ID != "" {
			keep[u.ID] = true
			if _, err := tx.ExecContext(ctx, `UPDATE library_paths SET path = ?, position = ? WHERE id = ? AND library_id = ?`, u.Path, i, u.ID, libraryID); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM library_paths WHERE library_id = ?`, libraryID)
	if err != nil {
		return err
	}
	var existing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, id)
	}
	rows.Close()
	for _, id := range existing {
		if !keep[id] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM library_paths WHERE id = ?`, id); err != nil {
				return err
			}
		}
	}
	for i, u := range updates {
		if u.ID == "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_paths(id, library_id, path, position) VALUES(?,?,?,?)`, NewID(), libraryID, u.Path, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) DeleteLibrary(ctx context.Context, id string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	return err
}

func (d *DB) SetLibraryScanResult(ctx context.Context, id, status, errMsg string) error {
	_, err := d.W.ExecContext(ctx, `UPDATE libraries SET last_scan_at = ?, last_scan_status = ?, last_scan_error = ? WHERE id = ?`, Now(), status, nullStr(errMsg), id)
	return err
}

func (d *DB) LibraryCounts(ctx context.Context, libraryID string) (LibraryCounts, error) {
	var c LibraryCounts
	err := d.R.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN type='movie' AND status<>'missing' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN type='series' AND EXISTS(SELECT 1 FROM items e WHERE e.series_id = items.id AND e.status <> 'missing') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN type='episode' AND status<>'missing' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='missing' THEN 1 ELSE 0 END),0)
		FROM items WHERE library_id = ?`, libraryID).Scan(&c.Movies, &c.Series, &c.Episodes, &c.Pending, &c.Missing)
	return c, err
}

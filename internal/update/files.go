package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Pending describes an installed update whose restart has not been confirmed.
// It is stored in <data>/update/pending.json.
type Pending struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Exe       string    `json:"exe"` // the executable path (now the new version)
	Old       string    `json:"old"` // the previous executable, kept for rollback
	StartedAt time.Time `json:"startedAt"`
}

// Result is the outcome of the last update, stored in <data>/update/result.json.
type Result struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

func stateDir(dataDir string) string    { return filepath.Join(dataDir, "update") }
func pendingPath(dataDir string) string { return filepath.Join(stateDir(dataDir), "pending.json") }
func resultPath(dataDir string) string  { return filepath.Join(stateDir(dataDir), "result.json") }
func downloadDir(dataDir string) string { return filepath.Join(stateDir(dataDir), "download") }

// ReadPending returns the pending update, if any.
func ReadPending(dataDir string) (*Pending, error) {
	var p Pending
	if err := readJSON(pendingPath(dataDir), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WritePending records an installed update awaiting restart.
func WritePending(dataDir string, p Pending) error { return writeJSON(pendingPath(dataDir), p) }

// ClearPending removes the pending marker.
func ClearPending(dataDir string) { _ = os.Remove(pendingPath(dataDir)) }

// ReadResult returns the last recorded update outcome, if any.
func ReadResult(dataDir string) (*Result, error) {
	var r Result
	if err := readJSON(resultPath(dataDir), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteResult records the outcome of an update.
func WriteResult(dataDir string, r Result) error { return writeJSON(resultPath(dataDir), r) }

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Sibling returns exe with a tag inserted before the extension:
// homestreaming.exe → homestreaming.old.exe, homestreaming → homestreaming.old.
func Sibling(exe, tag string) string {
	ext := filepath.Ext(exe)
	if !strings.EqualFold(ext, ".exe") {
		ext = ""
	}
	return strings.TrimSuffix(exe, ext) + "." + tag + ext
}

// extractBinary copies the file named bin (at any depth) out of a .zip or
// .tar.gz archive into dst with executable permissions.
func extractBinary(archive, bin, dst string) error {
	var src io.ReadCloser
	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return fmt.Errorf("open archive: %w", err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && path.Base(f.Name) == bin {
				if src, err = f.Open(); err != nil {
					return err
				}
				break
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("open archive: %w", err)
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("read archive: %w", err)
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == bin {
				src = io.NopCloser(tr)
				break
			}
		}
	}
	if src == nil {
		return fmt.Errorf("%s not found in %s", bin, filepath.Base(archive))
	}
	defer src.Close()
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(src, 1<<30)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Swap moves the running executable aside (to Sibling(exe, "old")) and puts
// next in its place. Windows allows renaming, but not overwriting, a running
// executable, so this works while the server runs. It returns the old path.
func Swap(exe, next string) (string, error) {
	old := Sibling(exe, "old")
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove the previous backup %s: %w", old, err)
	}
	if err := os.Rename(exe, old); err != nil {
		return "", fmt.Errorf("move the current program aside: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return "", fmt.Errorf("put the new program in place: %w (and restoring the old one failed: %v)", err, rerr)
		}
		return "", fmt.Errorf("put the new program in place: %w", err)
	}
	return old, nil
}

// Rollback restores the previous executable saved by Swap. The rejected
// binary is kept as Sibling(exe, "failed") for diagnosis.
func Rollback(exe, old string) error {
	if _, err := os.Stat(old); err != nil {
		return fmt.Errorf("previous version %s is missing: %w", old, err)
	}
	failed := Sibling(exe, "failed")
	_ = os.Remove(failed)
	if err := os.Rename(exe, failed); err != nil {
		return fmt.Errorf("move the new program aside: %w", err)
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Rename(failed, exe)
		return fmt.Errorf("restore the previous program: %w", err)
	}
	return nil
}

// dirWritable reports whether files can be created in dir.
func dirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".hs-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// inContainer reports whether the server runs inside Docker/Podman, where the
// image, not the binary, must be updated.
func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

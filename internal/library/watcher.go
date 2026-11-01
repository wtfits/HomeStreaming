package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/wtfits/homestreaming/internal/db"
)

// DefaultSettle is how long a path must be quiet (no events, mtime older than
// this) before the watcher processes it — torrents preallocate files, so
// mtime quiescence is the completion signal.
const DefaultSettle = 30 * time.Second

// Watcher watches every directory of every library root (fsnotify is not
// recursive on Windows or Linux) and feeds settled changes to the scanner.
type Watcher struct {
	Scanner *Scanner
	DB      *db.DB
	Log     *slog.Logger
	Settle  time.Duration

	mu   sync.Mutex
	libs map[string]*libWatch
}

type libWatch struct {
	w      *fsnotify.Watcher
	lib    *db.Library
	mu     sync.Mutex
	timers map[string]*time.Timer
	cancel context.CancelFunc
}

func (w *Watcher) settle() time.Duration {
	if w.Settle > 0 {
		return w.Settle
	}
	return DefaultSettle
}

// Sync starts watchers for all libraries (restarting existing ones).
func (w *Watcher) Sync(ctx context.Context) {
	libs, err := w.DB.ListLibraries(ctx)
	if err != nil {
		w.Log.Error("watcher: list libraries", "err", err)
		return
	}
	for _, l := range libs {
		w.Watch(ctx, l)
	}
}

// Watch (re)starts the watcher of one library.
func (w *Watcher) Watch(ctx context.Context, lib *db.Library) {
	w.Unwatch(lib.ID)
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		w.Log.Error("watcher: create", "err", err)
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	lw := &libWatch{w: fw, lib: lib, timers: map[string]*time.Timer{}, cancel: cancel}
	for _, p := range lib.Paths {
		w.addTree(lw, p.Path)
	}
	w.mu.Lock()
	if w.libs == nil {
		w.libs = map[string]*libWatch{}
	}
	w.libs[lib.ID] = lw
	w.mu.Unlock()
	go w.loop(cctx, lw)
}

// Unwatch stops the watcher of a library.
func (w *Watcher) Unwatch(libraryID string) {
	w.mu.Lock()
	lw := w.libs[libraryID]
	delete(w.libs, libraryID)
	w.mu.Unlock()
	if lw != nil {
		lw.cancel()
		_ = lw.w.Close()
		lw.mu.Lock()
		for _, t := range lw.timers {
			t.Stop()
		}
		lw.mu.Unlock()
	}
}

// Close stops all watchers.
func (w *Watcher) Close() {
	w.mu.Lock()
	ids := make([]string, 0, len(w.libs))
	for id := range w.libs {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	for _, id := range ids {
		w.Unwatch(id)
	}
}

func (w *Watcher) addTree(lw *libWatch, root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (SkipDir(d.Name()) || IsDiscDir(d.Name())) {
			return fs.SkipDir
		}
		if err := lw.w.Add(p); err != nil {
			w.Log.Warn("watcher: add", "path", p, "err", err)
		}
		return nil
	})
}

func (w *Watcher) rootFor(lw *libWatch, p string) (db.LibraryPath, bool) {
	best := -1
	var out db.LibraryPath
	for _, lp := range lw.lib.Paths {
		root := filepath.Clean(lp.Path)
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			if len(root) > best {
				best, out = len(root), lp
			}
		}
	}
	return out, best >= 0
}

func (w *Watcher) loop(ctx context.Context, lw *libWatch) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-lw.w.Events:
			if !ok {
				return
			}
			w.handle(ctx, lw, ev)
		case err, ok := <-lw.w.Errors:
			if !ok {
				return
			}
			w.Log.Warn("watcher error; scheduling a quick scan in 60s", "library", lw.lib.ID, "err", err)
			time.AfterFunc(60*time.Second, func() {
				if ctx.Err() == nil {
					_, _ = w.Scanner.Scan(ctx, lw.lib.ID, false)
				}
			})
		}
	}
}

func (w *Watcher) handle(ctx context.Context, lw *libWatch, ev fsnotify.Event) {
	p := filepath.Clean(ev.Name)
	if ev.Has(fsnotify.Chmod) && !ev.Has(fsnotify.Write) && !ev.Has(fsnotify.Create) {
		return
	}
	if ev.Has(fsnotify.Create) {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			name := filepath.Base(p)
			if SkipDir(name) || IsDiscDir(name) {
				return
			}
			w.addTree(lw, p)
			// Files created together with the directory produce no events.
			_ = filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					w.schedule(ctx, lw, fp)
				}
				return nil
			})
			return
		}
	}
	w.schedule(ctx, lw, p)
}

func (w *Watcher) schedule(ctx context.Context, lw *libWatch, p string) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if t := lw.timers[p]; t != nil {
		t.Stop()
	}
	lw.timers[p] = time.AfterFunc(w.settle(), func() { w.fire(ctx, lw, p) })
}

func (w *Watcher) fire(ctx context.Context, lw *libWatch, p string) {
	if ctx.Err() != nil {
		return
	}
	lw.mu.Lock()
	delete(lw.timers, p)
	lw.mu.Unlock()

	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		// Still being written? Re-check after another settle period.
		if age := time.Since(st.ModTime()); age < w.settle() {
			lw.mu.Lock()
			lw.timers[p] = time.AfterFunc(w.settle()-age+50*time.Millisecond, func() { w.fire(ctx, lw, p) })
			lw.mu.Unlock()
			return
		}
		if f, err := os.Open(p); err != nil {
			w.schedule(ctx, lw, p) // locked by the downloader
			return
		} else {
			f.Close()
		}
	}
	lp, ok := w.rootFor(lw, p)
	if !ok {
		return
	}
	if err := w.Scanner.ProcessPath(ctx, lw.lib.ID, lp, p); err != nil {
		w.Log.Warn("watcher: process", "path", p, "err", err)
	}
}

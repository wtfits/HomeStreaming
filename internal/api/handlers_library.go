package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/library"
)

func (s *Server) handleListLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.DB.ListLibraries(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := []libraryJSON{}
	for _, l := range libs {
		out = append(out, s.toLibrary(r.Context(), l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleGetLibrary(w http.ResponseWriter, r *http.Request) {
	l, err := s.DB.GetLibrary(r.Context(), chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "library")
		return
	}
	writeJSON(w, http.StatusOK, s.toLibrary(r.Context(), l))
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || (filepath.Separator == '\\' && strings.EqualFold(a, b))
}

func nested(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, sep)+sep) || strings.HasPrefix(b, strings.TrimSuffix(a, sep)+sep)
}

// validatePaths checks library roots; exclude is the library being edited.
func (s *Server) validatePaths(ctx context.Context, paths []string, exclude string) (map[string]string, bool) {
	fields := map[string]string{}
	if len(paths) == 0 {
		fields["paths"] = "at least one folder is required"
		return fields, false
	}
	for i, p := range paths {
		if !filepath.IsAbs(p) {
			fields["paths"] = "folders must be absolute paths: " + p
			return fields, false
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			fields["paths"] = "folder does not exist: " + p
			return fields, false
		}
		for j := 0; j < i; j++ {
			if nested(p, paths[j]) {
				fields["paths"] = "folders overlap: " + p
				return fields, false
			}
		}
	}
	libs, _ := s.DB.ListLibraries(ctx)
	for _, l := range libs {
		if l.ID == exclude {
			continue
		}
		for _, lp := range l.Paths {
			for _, p := range paths {
				if nested(p, lp.Path) {
					return map[string]string{"paths": "folder overlaps library " + l.Name + ": " + lp.Path}, true
				}
			}
		}
	}
	return nil, false
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string   `json:"name"`
		Type  string   `json:"type"`
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Type == "" {
		req.Type = "mixed"
	}
	fields := map[string]string{}
	if req.Name == "" {
		fields["name"] = "required"
	}
	if req.Type != "mixed" && req.Type != "movies" && req.Type != "shows" {
		fields["type"] = "must be mixed, movies or shows"
	}
	for i := range req.Paths {
		req.Paths[i] = strings.TrimSpace(req.Paths[i])
	}
	if f, conflict := s.validatePaths(r.Context(), req.Paths, ""); f != nil {
		if conflict {
			writeError(w, http.StatusConflict, "conflict", f["paths"], nil)
			return
		}
		for k, v := range f {
			fields[k] = v
		}
	}
	if len(fields) > 0 {
		validation(w, fields)
		return
	}
	lib, err := s.DB.CreateLibrary(r.Context(), req.Name, req.Type, req.Paths)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	task, _ := s.Scanner.Scan(context.WithoutCancel(r.Context()), lib.ID, false)
	if s.Watcher != nil {
		s.Watcher.Watch(context.WithoutCancel(r.Context()), lib)
	}
	j := s.toLibrary(r.Context(), lib)
	j.ScanTaskID = task.ID
	w.Header().Set("Location", "/api/v1/tasks/"+task.ID)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handlePatchLibrary(w http.ResponseWriter, r *http.Request) {
	lib, err := s.DB.GetLibrary(r.Context(), chiParam(r, "id"))
	if err != nil {
		s.dbErr(w, r, err, "library")
		return
	}
	var req struct {
		Name  *string           `json:"name"`
		Paths []json.RawMessage `json:"paths"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid JSON")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			validation(w, map[string]string{"name": "required"})
			return
		}
		if err := s.DB.RenameLibrary(r.Context(), lib.ID, name); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if req.Paths != nil {
		var updates []db.PathUpdate
		var plain []string
		for i, raw := range req.Paths {
			var p string
			var obj struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			}
			switch {
			case json.Unmarshal(raw, &p) == nil:
				u := db.PathUpdate{Path: strings.TrimSpace(p)}
				if i < len(lib.Paths) {
					u.ID = lib.Paths[i].ID // relocate by position
				}
				updates = append(updates, u)
				plain = append(plain, u.Path)
			case json.Unmarshal(raw, &obj) == nil:
				updates = append(updates, db.PathUpdate{ID: obj.ID, Path: strings.TrimSpace(obj.Path)})
				plain = append(plain, strings.TrimSpace(obj.Path))
			default:
				badRequest(w, "paths must be strings or {id, path} objects")
				return
			}
		}
		if f, conflict := s.validatePaths(r.Context(), plain, lib.ID); f != nil {
			if conflict {
				writeError(w, http.StatusConflict, "conflict", f["paths"], nil)
				return
			}
			validation(w, f)
			return
		}
		if err := s.DB.SetLibraryPaths(r.Context(), lib.ID, updates); err != nil {
			s.internal(w, r, err)
			return
		}
		lib, _ = s.DB.GetLibrary(r.Context(), lib.ID)
		if s.Watcher != nil {
			s.Watcher.Watch(context.WithoutCancel(r.Context()), lib)
		}
		_, _ = s.Scanner.Scan(context.WithoutCancel(r.Context()), lib.ID, false)
	}
	lib, _ = s.DB.GetLibrary(r.Context(), lib.ID)
	writeJSON(w, http.StatusOK, s.toLibrary(r.Context(), lib))
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id := chiParam(r, "id")
	if _, err := s.DB.GetLibrary(r.Context(), id); err != nil {
		s.dbErr(w, r, err, "library")
		return
	}
	if s.Watcher != nil {
		s.Watcher.Unwatch(id)
	}
	if err := s.DB.DeleteLibrary(r.Context(), id); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	id := chiParam(r, "id")
	if _, err := s.DB.GetLibrary(r.Context(), id); err != nil {
		s.dbErr(w, r, err, "library")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	_ = decodeJSON(r, &req)
	task, err := s.Scanner.Scan(context.WithoutCancel(r.Context()), id, req.Mode == "full")
	if errors.Is(err, library.ErrScanRunning) {
		writeError(w, http.StatusConflict, "scan_running", "a scan of this library is already running", map[string]any{"task": task})
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleScanAll(w http.ResponseWriter, r *http.Request) {
	libs, err := s.DB.ListLibraries(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var out []any
	for _, l := range libs {
		t, _ := s.Scanner.Scan(context.WithoutCancel(r.Context()), l.ID, false)
		out = append(out, t)
	}
	if out == nil {
		out = []any{}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"tasks": out})
}

func (s *Server) handleCleanLibrary(w http.ResponseWriter, r *http.Request) {
	id := chiParam(r, "id")
	if _, err := s.DB.GetLibrary(r.Context(), id); err != nil {
		s.dbErr(w, r, err, "library")
		return
	}
	n, err := s.DB.DeleteMissing(r.Context(), id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Tasks.List(r.URL.Query().Get("status"))})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Tasks.Get(chiParam(r, "id"))
	if !ok {
		notFound(w, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

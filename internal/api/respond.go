package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/wtfits/homestreaming/internal/db"
)

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: msg, Details: details}})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusBadRequest, "bad_request", msg, nil)
}

func validation(w http.ResponseWriter, fields map[string]string) {
	d := map[string]any{}
	for k, v := range fields {
		d[k] = v
	}
	writeError(w, http.StatusBadRequest, "validation", "validation failed", d)
}

func notFound(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusNotFound, "not_found", msg, nil)
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("internal error", "method", r.Method, "path", r.URL.Path, "requestId", w.Header().Get("X-Request-Id"), "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error (see server log, request id "+w.Header().Get("X-Request-Id")+")", nil)
}

// dbErr maps db.ErrNotFound to 404 and anything else to 500.
func (s *Server) dbErr(w http.ResponseWriter, r *http.Request, err error, what string) {
	if errors.Is(err, db.ErrNotFound) {
		notFound(w, what+" not found")
		return
	}
	s.internal(w, r, err)
}

// decodeJSON reads a JSON body (max 1 MiB). Empty bodies decode to zero values.
func decodeJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

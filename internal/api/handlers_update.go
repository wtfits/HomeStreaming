package api

import (
	"context"
	"net/http"
	"time"

	"github.com/wtfits/homestreaming/internal/update"
)

type updateStatus struct {
	update.Status
	ActivePlaySessions int `json:"activePlaySessions"`
}

func (s *Server) updateJSON() updateStatus {
	st := updateStatus{Status: s.Updater.Status()}
	if s.Sessions != nil {
		st.ActivePlaySessions = len(s.Sessions.List())
	}
	return st
}

// GET /admin/update
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateJSON())
}

// POST /admin/update/check — asks GitHub now. A failed check is reported in
// checkError, not as an HTTP error, so the UI can show it next to the button.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_ = s.Updater.Check(ctx)
	writeJSON(w, http.StatusOK, s.updateJSON())
}

// POST /admin/update/install — downloads, verifies and installs the latest
// release in the background; poll GET /admin/update for progress.
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if err := s.Updater.StartInstall(); err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
		return
	}
	s.Log.Info("update started", "by", ident(r).User.Username)
	writeJSON(w, http.StatusAccepted, s.updateJSON())
}

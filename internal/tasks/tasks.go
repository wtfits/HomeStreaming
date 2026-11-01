// Package tasks tracks background jobs (scans) and their progress.
package tasks

import (
	"sync"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
)

// ScanReport counts what a scan did.
type ScanReport struct {
	Added             int `json:"added"`
	Updated           int `json:"updated"`
	Missing           int `json:"missing"`
	Pending           int `json:"pending"`
	IgnoredSample     int `json:"ignoredSample"`
	IgnoredIncomplete int `json:"ignoredIncomplete"`
	Unsupported       int `json:"unsupported"`
}

// Progress is done/total of the current phase.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Task is a JSON-serializable snapshot of a background job.
type Task struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	LibraryID  string      `json:"libraryId,omitempty"`
	Status     string      `json:"status"` // queued|running|done|failed
	Phase      string      `json:"phase"`  // walk|upsert|probe|images
	Progress   Progress    `json:"progress"`
	StartedAt  string      `json:"startedAt"`
	FinishedAt *string     `json:"finishedAt"`
	Error      *string     `json:"error"`
	Report     *ScanReport `json:"report,omitempty"`
}

// Handle mutates a running task.
type Handle struct {
	m  *Manager
	id string
}

// Manager keeps the last 50 tasks in memory.
type Manager struct {
	mu    sync.Mutex
	tasks []*Task
}

func New() *Manager { return &Manager{} }

// Start creates a running task.
func (m *Manager) Start(kind, libraryID string) *Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &Task{ID: db.NewID(), Kind: kind, LibraryID: libraryID, Status: "running", Phase: "walk", StartedAt: db.FormatTime(time.Now()), Report: &ScanReport{}}
	m.tasks = append(m.tasks, t)
	if len(m.tasks) > 50 {
		// Drop the oldest finished task.
		for i, old := range m.tasks {
			if old.Status != "running" && old.Status != "queued" {
				m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
				break
			}
		}
	}
	return &Handle{m: m, id: t.ID}
}

func (m *Manager) find(id string) *Task {
	for _, t := range m.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Get returns a snapshot of a task.
func (m *Manager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.find(id)
	if t == nil {
		return Task{}, false
	}
	return snapshot(t), true
}

// List returns snapshots, newest first, optionally filtered by status.
func (m *Manager) List(status string) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Task{}
	for i := len(m.tasks) - 1; i >= 0; i-- {
		t := m.tasks[i]
		if status == "" || t.Status == status {
			out = append(out, snapshot(t))
		}
	}
	return out
}

// Running counts running tasks.
func (m *Manager) Running() int { return len(m.List("running")) }

func snapshot(t *Task) Task {
	c := *t
	if t.Report != nil {
		r := *t.Report
		c.Report = &r
	}
	return c
}

// ID returns the task id.
func (h *Handle) ID() string { return h.id }

// Snapshot returns the current state.
func (h *Handle) Snapshot() Task {
	t, _ := h.m.Get(h.id)
	return t
}

func (h *Handle) update(fn func(t *Task)) {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if t := h.m.find(h.id); t != nil {
		fn(t)
	}
}

// Phase switches the phase and resets progress.
func (h *Handle) Phase(phase string, total int) {
	h.update(func(t *Task) { t.Phase = phase; t.Progress = Progress{Total: total} })
}

// Step advances progress by one.
func (h *Handle) Step() { h.update(func(t *Task) { t.Progress.Done++ }) }

// Report mutates the scan report.
func (h *Handle) Report(fn func(r *ScanReport)) { h.update(func(t *Task) { fn(t.Report) }) }

// Finish marks the task done or failed.
func (h *Handle) Finish(err error) {
	h.update(func(t *Task) {
		now := db.FormatTime(time.Now())
		t.FinishedAt = &now
		if err != nil {
			msg := err.Error()
			t.Error = &msg
			t.Status = "failed"
		} else {
			t.Status = "done"
		}
	})
}

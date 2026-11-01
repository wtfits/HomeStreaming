package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/ffmpeg"
)

var (
	ErrSessionNotFound = errors.New("play session not found")
	ErrSegmentNotFound = errors.New("segment not found")
	ErrNotReady        = errors.New("not produced in time")
	ErrBusy            = errors.New("too many transcode sessions")
)

// SegmentRe validates segment file names.
var SegmentRe = regexp.MustCompile(`^seg(\d{5})\.m4s$`)

// Session is one playback of one item by one client.
type Session struct {
	ID               string
	UserID           string
	Username         string
	DeviceName       string
	IP               string
	ConnClass        string
	ItemID           string
	Item             *db.Item
	Method           string
	Reasons          []string
	Decision         Decision
	Input            string
	VideoIndex       int
	RuntimeMs        int64
	TimelineOffsetMs int64
	SegSeconds       int
	Engine           string
	SubtitleTrackID  string
	InbandSubs       []SubtitleRendition
	Dir              string
	StartedAt        time.Time
	Preset           string
	Threads          int

	mu           sync.Mutex
	lastActivity time.Time
	positionMs   int64
	state        string
	playCounted  bool
	stopped      bool

	cmd      *exec.Cmd
	running  bool
	runStart int
	gen      int
	restarts int
	args     []string
	logTail  []string
	rungs    []VideoPlan
	rung     int
	exited   chan struct{}
}

// Manager owns all play sessions.
type Manager struct {
	Dir         string
	FFmpeg      func() string
	Log         *slog.Logger
	IdleTimeout time.Duration
	MaxSessions func() int

	mu       sync.Mutex
	sessions map[string]*Session
	stopCh   chan struct{}
	once     sync.Once
}

// NewManager creates a manager and wipes the transcode directory.
func NewManager(dir string, ffmpegPath func() string, log *slog.Logger, maxSessions func() int) *Manager {
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o755)
	return &Manager{Dir: dir, FFmpeg: ffmpegPath, Log: log, IdleTimeout: 60 * time.Second, MaxSessions: maxSessions,
		sessions: map[string]*Session{}, stopCh: make(chan struct{})}
}

// Run starts the idle reaper (blocks until ctx ends or Close).
func (m *Manager) Run(ctx context.Context) {
	tick := m.IdleTimeout / 4
	if tick > 5*time.Second {
		tick = 5 * time.Second
	}
	if tick < 50*time.Millisecond {
		tick = 50 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-t.C:
			m.reap()
		}
	}
}

func (m *Manager) reap() {
	m.mu.Lock()
	var idle []*Session
	for _, s := range m.sessions {
		s.mu.Lock()
		if time.Since(s.lastActivity) > m.IdleTimeout {
			idle = append(idle, s)
		}
		s.mu.Unlock()
	}
	m.mu.Unlock()
	for _, s := range idle {
		m.Log.Info("play session idle; stopping", "session", s.ID, "item", s.ItemID)
		m.Stop(s.ID)
	}
}

// Close stops every session and the reaper.
func (m *Manager) Close() {
	m.once.Do(func() { close(m.stopCh) })
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// Get returns a session.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List returns all sessions.
func (m *Manager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// ActiveHLS counts remux/transcode sessions.
func (m *Manager) ActiveHLS() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.Method != "direct" {
			n++
		}
	}
	return n
}

// IsHLS reports whether the session is served as HLS.
func (s *Session) IsHLS() bool { return s.Method != "direct" }

// Touch records activity.
func (s *Session) Touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

// SetProgress records the client position/state.
func (s *Session) SetProgress(positionMs int64, state string) {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.positionMs = positionMs
	s.state = state
	s.mu.Unlock()
}

// MarkPlayCounted returns true the first time it is called (one count per session).
func (s *Session) MarkPlayCounted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playCounted {
		return false
	}
	s.playCounted = true
	return true
}

// Snapshot is an admin view of a session.
type Snapshot struct {
	PositionMs       int64
	State            string
	LastActivity     time.Time
	SegmentsProduced int
	FFmpegRunning    bool
	Restarts         int
	Args             []string
	LogTail          string
	Backend          string
	Video            VideoPlan
}

// Snapshot returns the current state.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{PositionMs: s.positionMs, State: s.state, LastActivity: s.lastActivity, FFmpegRunning: s.running, Restarts: s.restarts,
		Args: append([]string{}, s.args...), LogTail: strings.Join(s.logTail, "\n")}
	if s.IsHLS() {
		snap.Video = s.currentPlan()
		snap.Backend = BackendOf(snap.Video)
		entries, _ := os.ReadDir(s.Dir)
		for _, e := range entries {
			if SegmentRe.MatchString(e.Name()) {
				snap.SegmentsProduced++
			}
		}
	}
	return snap
}

// BackendOf names the backend used by a video plan.
func BackendOf(p VideoPlan) string {
	if p.Action == "copy" {
		return "copy"
	}
	switch {
	case strings.HasSuffix(p.Encoder, "_nvenc"):
		return "nvenc"
	case strings.HasSuffix(p.Encoder, "_qsv"):
		return "qsv"
	case strings.HasSuffix(p.Encoder, "_amf"):
		return "amf"
	case strings.HasSuffix(p.Encoder, "_vaapi"):
		return "vaapi"
	case strings.HasSuffix(p.Encoder, "_videotoolbox"):
		return "videotoolbox"
	}
	return "software"
}

func (s *Session) currentPlan() VideoPlan {
	if len(s.rungs) == 0 {
		return s.Decision.Video
	}
	return s.rungs[s.rung]
}

// CurrentPlan returns the active video plan (after any fallback).
func (s *Session) CurrentPlan() VideoPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentPlan()
}

func softwarePlan(p VideoPlan) VideoPlan {
	sw := p
	sw.Encoder = "libx264"
	sw.Codec = "h264"
	sw.HWDecode = false
	if p.HDR {
		sw.HDR = false
		sw.Tonemap = true
	}
	sw.Level = ffmpeg.H264Level(sw.Height)
	return sw
}

func buildRungs(p VideoPlan) []VideoPlan {
	if p.Action != "encode" {
		return []VideoPlan{p}
	}
	rungs := []VideoPlan{p}
	if strings.HasSuffix(p.Encoder, "_nvenc") && p.HWDecode {
		noDec := p
		noDec.HWDecode = false
		rungs = append(rungs, noDec)
	}
	if p.Encoder != "libx264" {
		rungs = append(rungs, softwarePlan(p))
	}
	return rungs
}

// Create registers a session and starts ffmpeg for HLS methods.
func (m *Manager) Create(s *Session, replaces string, startPositionMs int64) error {
	if replaces != "" {
		if old, ok := m.Get(replaces); ok && old.UserID == s.UserID {
			m.Stop(replaces)
		}
	}
	s.lastActivity = time.Now()
	s.StartedAt = time.Now()
	s.state = "playing"
	if s.IsHLS() {
		if m.FFmpeg() == "" {
			return errors.New("ffmpeg missing")
		}
		if max := m.MaxSessions(); max > 0 && m.ActiveHLS() >= max {
			return ErrBusy
		}
		s.Dir = filepath.Join(m.Dir, s.ID)
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			return err
		}
		s.rungs = buildRungs(s.Decision.Video)
		start := 0
		if s.Method == "transcode" {
			start = int(startPositionMs / 1000 / int64(s.SegSeconds))
			if n := SegmentCount(s.RuntimeMs, s.SegSeconds); start >= n {
				start = n - 1
			}
		}
		s.mu.Lock()
		err := m.startRun(s, start)
		s.mu.Unlock()
		if err != nil {
			_ = os.RemoveAll(s.Dir)
			return err
		}
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (s *Session) buildArgs(startSeg int) []string {
	plan := s.currentPlan()
	p := ffmpeg.HLSParams{Input: s.Input, VideoIndex: s.VideoIndex, AudioIndex: -1, SegmentSeconds: s.SegSeconds, Threads: s.Threads}
	a := s.Decision.Audio
	if a.Action != "none" && a.Index >= 0 {
		p.AudioIndex = a.Index
		p.Audio = ffmpeg.AudioParams{Copy: a.Action == "copy", Codec: a.Codec, Channels: a.Channels, BitrateKbps: a.BitrateKbps}
	}
	if s.Method == "remux" {
		p.StartSec = float64(s.TimelineOffsetMs) / 1000
		p.Event = true
		p.Video = ffmpeg.VideoParams{Copy: true, TagHvc1: plan.Codec == "hevc"}
		return ffmpeg.BuildHLS(p)
	}
	t := float64(startSeg * s.SegSeconds)
	p.StartSec, p.TSOffsetSec, p.StartNumber = t, t, startSeg
	p.Video = ffmpeg.VideoParams{Encoder: plan.Encoder, HWDecode: plan.HWDecode, Width: plan.Width, Height: plan.Height,
		Deinterlace: plan.Deinterlace, Tonemap: plan.Tonemap, HDRPassthrough: plan.HDR && plan.Codec == "hevc", Transfer: plan.Transfer,
		MaxrateKbps: plan.MaxrateKbps, Preset: s.Preset, FPS: plan.FPS, Level: plan.Level, TagHvc1: plan.Codec == "hevc"}
	return ffmpeg.BuildHLS(p)
}

// startRun starts ffmpeg at segment startSeg. Caller holds s.mu.
func (m *Manager) startRun(s *Session, startSeg int) error {
	args := s.buildArgs(startSeg)
	cmd := ffmpeg.Command(context.Background(), m.FFmpeg(), args...)
	cmd.Dir = s.Dir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := ffmpeg.Start(cmd); err != nil {
		return err
	}
	s.gen++
	gen := s.gen
	s.cmd, s.running, s.runStart, s.args = cmd, true, startSeg, args
	exited := make(chan struct{})
	s.exited = exited
	m.Log.Debug("ffmpeg started", "session", s.ID, "startSegment", startSeg, "backend", BackendOf(s.currentPlan()), "args", strings.Join(args, " "))

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			s.mu.Lock()
			s.logTail = append(s.logTail, line)
			if len(s.logTail) > 200 {
				s.logTail = s.logTail[len(s.logTail)-200:]
			}
			s.mu.Unlock()
		}
		_, _ = io.Copy(io.Discard, stderr)
		werr := cmd.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		defer close(exited)
		if s.gen != gen {
			return // superseded by a restart
		}
		s.running = false
		if werr == nil || s.stopped {
			return
		}
		produced := s.highestSegment(startSeg) >= startSeg
		tail := strings.Join(lastN(s.logTail, 10), " | ")
		if !produced && s.rung < len(s.rungs)-1 {
			from := BackendOf(s.rungs[s.rung])
			s.rung++
			m.Log.Warn("ffmpeg failed before the first segment; falling back", "session", s.ID, "from", from,
				"to", BackendOf(s.rungs[s.rung]), "hwDecode", s.rungs[s.rung].HWDecode, "err", werr, "stderr", tail)
			if err := m.startRun(s, startSeg); err != nil {
				m.Log.Error("ffmpeg fallback start failed", "session", s.ID, "err", err)
			}
			return
		}
		m.Log.Error("ffmpeg exited with an error", "session", s.ID, "err", werr, "stderr", tail)
	}()
	return nil
}

func lastN(l []string, n int) []string {
	if len(l) <= n {
		return l
	}
	return l[len(l)-n:]
}

func (s *Session) segPath(n int) string { return filepath.Join(s.Dir, fmt.Sprintf("seg%05d.m4s", n)) }

// highestSegment returns the highest consecutive produced segment >= from (from-1 if none).
func (s *Session) highestSegment(from int) int {
	h := from - 1
	for i := from; ; i++ {
		if _, err := os.Stat(s.segPath(i)); err != nil {
			return h
		}
		h = i
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// killLocked stops the running ffmpeg and waits for it to exit. Caller holds s.mu.
func (s *Session) killLocked() {
	if s.cmd == nil || !s.running {
		return
	}
	s.gen++ // the exit handler must not treat this as a failure
	_ = s.cmd.Process.Kill()
	ch := s.exited
	s.running = false
	s.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
	}
	s.mu.Lock()
}

// Stop ends a session: kills ffmpeg and deletes its directory.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.killLocked()
	s.mu.Unlock()
	if s.Dir != "" {
		for i := 0; i < 5; i++ { // Windows may hold handles briefly
			if err := os.RemoveAll(s.Dir); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func waitFor(ctx context.Context, cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// SegmentWaitTimeout bounds how long a segment request blocks.
var SegmentWaitTimeout = 30 * time.Second

// Segment returns the path of segment n, (re)starting ffmpeg when needed.
func (m *Manager) Segment(ctx context.Context, s *Session, n int) (string, error) {
	s.Touch()
	if !s.IsHLS() {
		return "", ErrSegmentNotFound
	}
	p := s.segPath(n)
	if s.Method == "remux" {
		if fileExists(p) {
			return p, nil
		}
		ok := waitFor(ctx, func() bool {
			if fileExists(p) {
				return true
			}
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()
			return !running
		}, SegmentWaitTimeout)
		if ok && fileExists(p) {
			return p, nil
		}
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			return "", ErrSegmentNotFound
		}
		return "", ErrNotReady
	}
	if n < 0 || n >= SegmentCount(s.RuntimeMs, s.SegSeconds) {
		return "", ErrSegmentNotFound
	}
	if fileExists(p) {
		return p, nil
	}
	s.mu.Lock()
	h := s.highestSegment(s.runStart)
	if !(s.running && n >= s.runStart && n <= h+3) {
		s.killLocked()
		entries, _ := os.ReadDir(s.Dir)
		for _, e := range entries {
			if mm := SegmentRe.FindStringSubmatch(e.Name()); mm != nil {
				var k int
				fmt.Sscanf(mm[1], "%d", &k)
				if k >= n {
					_ = os.Remove(filepath.Join(s.Dir, e.Name()))
				}
			}
		}
		s.restarts++
		m.Log.Debug("transcode seek: restarting ffmpeg", "session", s.ID, "segment", n)
		if err := m.startRun(s, n); err != nil {
			s.mu.Unlock()
			return "", err
		}
	}
	s.mu.Unlock()
	if waitFor(ctx, func() bool { return fileExists(p) }, SegmentWaitTimeout) {
		s.Touch()
		return p, nil
	}
	return "", ErrNotReady
}

// Init returns the init segment path (waits up to 15 s).
func (m *Manager) Init(ctx context.Context, s *Session) (string, error) {
	s.Touch()
	p := filepath.Join(s.Dir, "init.mp4")
	if waitFor(ctx, func() bool { return fileExists(p) }, 15*time.Second) {
		return p, nil
	}
	return "", ErrNotReady
}

// RemuxPlaylist returns ffmpeg's EVENT playlist once it lists a segment.
func (m *Manager) RemuxPlaylist(ctx context.Context, s *Session, suffix string) (string, error) {
	s.Touch()
	p := filepath.Join(s.Dir, "index.m3u8")
	var out string
	ok := waitFor(ctx, func() bool {
		b, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		var segs int
		out, segs = RewriteEventPlaylist(string(b), suffix)
		return segs > 0
	}, 15*time.Second)
	if !ok {
		return "", ErrNotReady
	}
	return out, nil
}

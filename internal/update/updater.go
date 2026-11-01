package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrRestartRequired is returned by an Activator when the new version is in
// place but HomeStreaming has to be restarted by hand (console runs).
var ErrRestartRequired = errors.New("restart required")

// Activator starts the newly installed executable, typically by restarting
// the Windows service from a helper process. It runs after the swap.
type Activator func(p Pending) error

// States reported by Status.State.
const (
	StateIdle            = "idle"
	StateDownloading     = "downloading"
	StateInstalling      = "installing"
	StateRestarting      = "restarting"
	StateRestartRequired = "restart_required"
	StateFailed          = "failed"
)

// maxDownload caps a release archive.
const maxDownload = 512 << 20

// Updater checks for, downloads and installs new releases.
type Updater struct {
	Repo     string // owner/name; default DefaultRepo
	Current  string // running version
	DataDir  string
	APIBase  string // default https://api.github.com
	Client   *http.Client
	Log      *slog.Logger
	Activate Activator              // nil = ErrRestartRequired
	ExePath  func() (string, error) // default: os.Executable with symlinks resolved
	GOOS     string                 // default runtime.GOOS
	GOARCH   string                 // default runtime.GOARCH
	// SkipBinaryCheck disables running the downloaded binary's "version"
	// command before swapping (tests on another platform).
	SkipBinaryCheck bool
	InContainer     func() bool // default: detects Docker/Podman
	// RestartTimeout is how long to wait for the Activator's restart before
	// restoring the old binary (default 4 min).
	RestartTimeout time.Duration

	mu         sync.Mutex
	latest     *Release
	checkedAt  time.Time
	checkErr   string
	state      string
	stateErr   string
	done       int64
	total      int64
	lastResult *Result
}

// Status is the JSON shape of GET /admin/update.
type Status struct {
	Current             string     `json:"current"`
	DevBuild            bool       `json:"devBuild"`
	Latest              *Release   `json:"latest"`
	Available           bool       `json:"available"`
	CheckedAt           *time.Time `json:"checkedAt"`
	CheckError          string     `json:"checkError,omitempty"`
	CanInstall          bool       `json:"canInstall"`
	CannotInstallReason string     `json:"cannotInstallReason,omitempty"`
	State               string     `json:"state"`
	Error               string     `json:"error,omitempty"`
	DownloadedBytes     int64      `json:"downloadedBytes"`
	TotalBytes          int64      `json:"totalBytes"`
	LastResult          *Result    `json:"lastResult,omitempty"`
	Repo                string     `json:"repo"`
}

func (u *Updater) repo() string {
	if u.Repo != "" {
		return u.Repo
	}
	return DefaultRepo
}

func (u *Updater) apiBase() string {
	if u.APIBase != "" {
		return u.APIBase
	}
	return "https://api.github.com"
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (u *Updater) log() *slog.Logger {
	if u.Log != nil {
		return u.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

func (u *Updater) userAgent() string { return "HomeStreaming/" + u.Current }

func (u *Updater) exe() (string, error) {
	if u.ExePath != nil {
		return u.ExePath()
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// RecordStartup finishes the bookkeeping of an update that restarted into this
// process: it records success when this is the version that was installed.
// Call it once at startup.
func (u *Updater) RecordStartup() {
	_ = os.RemoveAll(downloadDir(u.DataDir))
	if p, err := ReadPending(u.DataDir); err == nil {
		ClearPending(u.DataDir)
		if SameVersion(p.To, u.Current) {
			r := Result{From: p.From, To: p.To, OK: true, Message: "Updated from " + p.From + " to " + p.To + ".", At: time.Now().UTC()}
			_ = WriteResult(u.DataDir, r)
			u.log().Info("update installed", "from", p.From, "to", p.To)
		} else {
			r := Result{From: p.From, To: p.To, OK: false, At: time.Now().UTC(),
				Message: "The update to " + p.To + " did not start; still running " + u.Current + "."}
			_ = WriteResult(u.DataDir, r)
			u.log().Warn("update did not take effect", "from", p.From, "to", p.To, "running", u.Current)
		}
	}
	if r, err := ReadResult(u.DataDir); err == nil {
		u.mu.Lock()
		u.lastResult = r
		u.mu.Unlock()
	}
}

// Check asks GitHub for the latest release.
func (u *Updater) Check(ctx context.Context) error {
	rel, err := fetchLatest(ctx, u.client(), u.apiBase(), u.repo(), u.userAgent())
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now().UTC()
	switch {
	case errors.Is(err, errNoReleases):
		u.latest, u.checkErr = nil, ""
		return nil
	case err != nil:
		u.checkErr = err.Error()
		return err
	}
	u.latest, u.checkErr = rel, ""
	if newer, _ := Newer(u.Current, rel.Version); newer {
		u.log().Info("update available", "current", u.Current, "latest", rel.Version)
	}
	return nil
}

// Run checks after delay and then every interval until ctx ends.
func (u *Updater) Run(ctx context.Context, delay, interval time.Duration) {
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := u.Check(ctx); err != nil && ctx.Err() == nil {
				u.log().Warn("update check failed", "err", err)
			}
			t.Reset(interval)
		}
	}
}

// Status returns a snapshot for the API.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := Status{Current: u.Current, Latest: u.latest, CheckError: u.checkErr, State: u.state, Error: u.stateErr,
		DownloadedBytes: u.done, TotalBytes: u.total, LastResult: u.lastResult, Repo: u.repo()}
	if st.State == "" {
		st.State = StateIdle
	}
	if !u.checkedAt.IsZero() {
		at := u.checkedAt
		st.CheckedAt = &at
	}
	if _, release := ParseVersion(u.Current); !release {
		st.DevBuild = true
	}
	if u.latest != nil {
		st.Available, _ = Newer(u.Current, u.latest.Version)
	}
	if reason := u.cannotInstallLocked(); reason != "" {
		st.CannotInstallReason = reason
	} else {
		st.CanInstall = true
	}
	return st
}

// cannotInstallLocked explains why Install would be refused ("" = it can run).
func (u *Updater) cannotInstallLocked() string {
	switch {
	case u.latest == nil:
		return "No release to install."
	case u.state == StateDownloading || u.state == StateInstalling || u.state == StateRestarting:
		return "An update is already in progress."
	case u.state == StateRestartRequired:
		return "An update is installed; restart HomeStreaming to finish it."
	}
	if newer, _ := Newer(u.Current, u.latest.Version); !newer {
		return "You are on the latest version."
	}
	container := inContainer
	if u.InContainer != nil {
		container = u.InContainer
	}
	if container() {
		return "HomeStreaming runs in a container: pull the new image instead."
	}
	if _, ok := u.latest.Asset(AssetName(u.goos(), u.goarch())); !ok {
		return "This release has no build for " + u.goos() + "/" + u.goarch() + "."
	}
	if _, ok := u.latest.Asset(ChecksumsAsset); !ok {
		return "This release has no " + ChecksumsAsset + " file to verify the download."
	}
	exe, err := u.exe()
	if err != nil {
		return "Cannot locate the running program: " + err.Error()
	}
	if err := dirWritable(filepath.Dir(exe)); err != nil {
		if u.goos() == "windows" {
			return "HomeStreaming cannot write to " + filepath.Dir(exe) + ". Run it as the Windows service (homestreaming service install) or as Administrator to install updates."
		}
		return "HomeStreaming cannot write to " + filepath.Dir(exe) + " (" + err.Error() + ")."
	}
	return ""
}

// StartInstall begins installing the latest release in the background.
func (u *Updater) StartInstall() error {
	u.mu.Lock()
	if reason := u.cannotInstallLocked(); reason != "" {
		u.mu.Unlock()
		return errors.New(reason)
	}
	rel := u.latest
	u.state, u.stateErr, u.done, u.total = StateDownloading, "", 0, 0
	u.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = u.install(ctx, rel)
	}()
	return nil
}

// Install installs the latest release and waits for the swap (the CLI path).
// The returned Pending describes what was installed.
func (u *Updater) Install(ctx context.Context) (*Pending, error) {
	u.mu.Lock()
	if reason := u.cannotInstallLocked(); reason != "" {
		u.mu.Unlock()
		return nil, errors.New(reason)
	}
	rel := u.latest
	u.state, u.stateErr = StateDownloading, ""
	u.mu.Unlock()
	p, err := u.stage(ctx, rel)
	if err != nil {
		u.fail(err)
		return nil, err
	}
	u.setState(StateIdle)
	return p, nil
}

func (u *Updater) setState(s string) {
	u.mu.Lock()
	u.state = s
	u.mu.Unlock()
}

func (u *Updater) fail(err error) {
	u.log().Error("update failed", "err", err)
	u.mu.Lock()
	u.state, u.stateErr = StateFailed, err.Error()
	u.mu.Unlock()
}

// install runs stage and then hands over to the Activator.
func (u *Updater) install(ctx context.Context, rel *Release) error {
	p, err := u.stage(ctx, rel)
	if err != nil {
		u.fail(err)
		return err
	}
	act := u.Activate
	if act == nil {
		act = func(Pending) error { return ErrRestartRequired }
	}
	u.setState(StateRestarting)
	if err := act(*p); err != nil {
		if errors.Is(err, ErrRestartRequired) {
			u.log().Info("update installed; restart HomeStreaming to run it", "version", p.To)
			u.setState(StateRestartRequired)
			return nil
		}
		// The new binary is in place but could not be started automatically;
		// put the old one back so a later restart runs a known-good version.
		if rerr := Rollback(p.Exe, p.Old); rerr != nil {
			err = fmt.Errorf("%w (rolling back also failed: %v)", err, rerr)
		}
		ClearPending(u.DataDir)
		u.fail(fmt.Errorf("restart: %w", err))
		return err
	}
	u.log().Info("update installed; restarting", "from", p.From, "to", p.To)
	// If nothing restarts this process (the helper died), put the old
	// version back so the next manual restart runs a known-good build.
	time.AfterFunc(u.restartTimeout(), func() {
		u.mu.Lock()
		stuck := u.state == StateRestarting
		u.mu.Unlock()
		if !stuck {
			return
		}
		err := errors.New("the server was not restarted in time; the previous version was restored")
		if rerr := Rollback(p.Exe, p.Old); rerr != nil {
			err = fmt.Errorf("the server was not restarted in time and restoring the previous version failed: %v", rerr)
		}
		ClearPending(u.DataDir)
		u.fail(err)
	})
	return nil
}

func (u *Updater) restartTimeout() time.Duration {
	if u.RestartTimeout > 0 {
		return u.RestartTimeout
	}
	return 4 * time.Minute
}

// stage downloads and verifies rel, then swaps the executable and writes the
// pending marker. On error the running executable is left untouched.
func (u *Updater) stage(ctx context.Context, rel *Release) (*Pending, error) {
	archiveName := AssetName(u.goos(), u.goarch())
	archive, _ := rel.Asset(archiveName)
	sumsAsset, _ := rel.Asset(ChecksumsAsset)
	dir := downloadDir(u.DataDir)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	sumsPath := filepath.Join(dir, ChecksumsAsset)
	if _, err := download(ctx, u.client(), sumsAsset.URL, sumsPath, u.userAgent(), 1<<20, nil); err != nil {
		return nil, err
	}
	f, err := os.Open(sumsPath)
	if err != nil {
		return nil, err
	}
	want := parseChecksums(f)[archiveName]
	f.Close()
	if want == "" {
		return nil, fmt.Errorf("%s does not list %s", ChecksumsAsset, archiveName)
	}

	u.mu.Lock()
	u.total = archive.Size
	u.mu.Unlock()
	u.log().Info("downloading update", "version", rel.Version, "asset", archiveName)
	archivePath := filepath.Join(dir, archiveName)
	got, err := download(ctx, u.client(), archive.URL, archivePath, u.userAgent(), maxDownload, func(n int64) {
		u.mu.Lock()
		u.done = n
		u.mu.Unlock()
	})
	if err != nil {
		return nil, err
	}
	if got != want {
		return nil, fmt.Errorf("checksum mismatch for %s (expected %s, got %s); the download was not installed", archiveName, want, got)
	}

	u.setState(StateInstalling)
	exe, err := u.exe()
	if err != nil {
		return nil, err
	}
	next := Sibling(exe, "new")
	if err := extractBinary(archivePath, BinaryName(u.goos()), next); err != nil {
		return nil, err
	}
	if !u.SkipBinaryCheck {
		if err := checkBinary(ctx, next, rel.Version); err != nil {
			_ = os.Remove(next)
			return nil, err
		}
	}
	old, err := Swap(exe, next)
	if err != nil {
		_ = os.Remove(next)
		return nil, err
	}
	p := Pending{From: u.Current, To: rel.Version, Exe: exe, Old: old, StartedAt: time.Now().UTC()}
	if err := WritePending(u.DataDir, p); err != nil {
		_ = Rollback(exe, old)
		return nil, fmt.Errorf("record the update: %w", err)
	}
	return &p, nil
}

// checkBinary runs "<bin> version" and expects it to report want.
func checkBinary(ctx context.Context, bin, want string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the downloaded program does not run on this computer: %v %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), strings.TrimPrefix(want, "v")) {
		return fmt.Errorf("the downloaded program reports %q, expected version %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

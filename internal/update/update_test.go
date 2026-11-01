package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseVersionAndNewer(t *testing.T) {
	for _, c := range []struct {
		s  string
		ok bool
	}{
		{"v0.1.42", true}, {"0.1.42", true}, {"v1.0.0", true},
		{"0.1.0-dev", false}, {"a2d4144", false}, {"v0.1.41-3-gabc1234-dirty", false}, {"v01.0.0", false}, {"", false}, {"v1.2", false},
	} {
		if _, ok := ParseVersion(c.s); ok != c.ok {
			t.Errorf("ParseVersion(%q) ok=%v, want %v", c.s, ok, c.ok)
		}
	}
	for _, c := range []struct {
		cur, latest string
		newer, dev  bool
	}{
		{"v0.1.9", "v0.1.10", true, false},
		{"v0.1.10", "v0.1.9", false, false},
		{"v0.1.10", "v0.1.10", false, false},
		{"0.2.0", "v0.10.0", true, false},
		{"v1.0.0", "v0.9.99", false, false},
		{"a2d4144", "v0.1.3", true, true},
		{"0.1.0-dev", "v0.1.3", true, true},
		{"v0.1.3", "nightly", false, false},
	} {
		newer, dev := Newer(c.cur, c.latest)
		if newer != c.newer || dev != c.dev {
			t.Errorf("Newer(%q, %q) = %v,%v want %v,%v", c.cur, c.latest, newer, dev, c.newer, c.dev)
		}
	}
}

func TestSiblingAndHealthURL(t *testing.T) {
	if got := Sibling(`C:\Program Files\HomeStreaming\homestreaming.exe`, "old"); got != `C:\Program Files\HomeStreaming\homestreaming.old.exe` {
		t.Errorf("Sibling exe: %s", got)
	}
	if got := Sibling("/usr/local/bin/homestreaming", "new"); got != "/usr/local/bin/homestreaming.new" {
		t.Errorf("Sibling unix: %s", got)
	}
	for addr, want := range map[string]string{
		":8420":            "http://127.0.0.1:8420/api/v1/system/info",
		"0.0.0.0:8420":     "http://127.0.0.1:8420/api/v1/system/info",
		"[::]:9000":        "http://127.0.0.1:9000/api/v1/system/info",
		"192.168.0.5:8420": "http://192.168.0.5:8420/api/v1/system/info",
	} {
		if got := HealthURL(addr); got != want {
			t.Errorf("HealthURL(%q) = %s, want %s", addr, got, want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	m := parseChecksums(strings.NewReader(h + "  homestreaming-windows-amd64.zip\n" + h + " *other.tar.gz\nnot a line\n"))
	if m["homestreaming-windows-amd64.zip"] != h || m["other.tar.gz"] != h || len(m) != 2 {
		t.Fatalf("parseChecksums: %v", m)
	}
}

// fakeGitHub serves a latest release with the given archive for goos/goarch.
type fakeGitHub struct {
	*httptest.Server
	mu      sync.Mutex
	version string
	assets  map[string][]byte
	badSum  bool
}

func newFakeGitHub(t *testing.T, version string, assets map[string][]byte) *fakeGitHub {
	f := &fakeGitHub{version: version, assets: assets}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/repos/owner/repo/releases/latest" {
			var list []map[string]any
			for name, b := range f.assets {
				list = append(list, map[string]any{"name": name, "browser_download_url": f.URL + "/dl/" + name, "size": len(b)})
			}
			var sums bytes.Buffer
			for name, b := range f.assets {
				h := sha256.Sum256(b)
				if f.badSum {
					h[0] ^= 0xff
				}
				fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
			}
			f.assets[ChecksumsAsset] = sums.Bytes()
			list = append(list, map[string]any{"name": ChecksumsAsset, "browser_download_url": f.URL + "/dl/" + ChecksumsAsset, "size": sums.Len()})
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": f.version, "name": f.version, "body": "- notes", "html_url": "https://example/r",
				"published_at": time.Now().UTC().Format(time.RFC3339), "assets": list})
			return
		}
		if b, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func zipWith(t *testing.T, name string, content []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"README.md", "deploy/windows.md", name} {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if n == name {
			_, _ = w.Write(content)
		} else {
			_, _ = w.Write([]byte("doc"))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tgzWith(t *testing.T, name string, content []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "./README.md", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("doc"))
	_ = tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func newTestUpdater(t *testing.T, gh *fakeGitHub, goos, current string) (*Updater, string) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", BinaryName(goos))
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{Repo: "owner/repo", Current: current, DataDir: filepath.Join(dir, "data"), APIBase: gh.URL, GOOS: goos, GOARCH: "amd64",
		ExePath: func() (string, error) { return exe, nil }, SkipBinaryCheck: true, InContainer: func() bool { return false }}
	return u, exe
}

func waitState(t *testing.T, u *Updater, want ...string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := u.Status()
		for _, w := range want {
			if st.State == w {
				return st
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state never became %v (now %+v)", want, u.Status())
	return Status{}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallSwapsAndRecordsResult(t *testing.T) {
	gh := newFakeGitHub(t, "v0.1.7", map[string][]byte{
		AssetName("windows", "amd64"): zipWith(t, "homestreaming.exe", []byte("NEW")),
		AssetName("linux", "amd64"):   tgzWith(t, "homestreaming", []byte("NEW-LINUX")),
	})
	u, exe := newTestUpdater(t, gh, "windows", "v0.1.6")
	activatedCh := make(chan Pending, 1)
	u.Activate = func(p Pending) error { activatedCh <- p; return nil }

	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if !st.Available || !st.CanInstall || st.DevBuild || st.Latest.Version != "v0.1.7" || st.Latest.Notes != "- notes" {
		t.Fatalf("status before install: %+v", st)
	}
	if err := u.StartInstall(); err != nil {
		t.Fatal(err)
	}
	var activated *Pending
	select {
	case p := <-activatedCh:
		activated = &p
	case <-time.After(10 * time.Second):
		t.Fatal("activator not called")
	}
	waitState(t, u, StateRestarting)
	if got := mustRead(t, exe); got != "NEW" {
		t.Fatalf("exe content %q", got)
	}
	if got := mustRead(t, Sibling(exe, "old")); got != "OLD" {
		t.Fatalf("old exe content %q", got)
	}
	if activated == nil || activated.From != "v0.1.6" || activated.To != "v0.1.7" || activated.Exe != exe {
		t.Fatalf("activator got %+v", activated)
	}
	if _, err := ReadPending(u.DataDir); err != nil {
		t.Fatalf("pending not written: %v", err)
	}
	if err := u.StartInstall(); err == nil {
		t.Fatal("second install while restarting should be refused")
	}
	if _, err := ReadPending(u.DataDir); err != nil {
		t.Fatal(err)
	}

	// The restarted process (new version) confirms the update.
	u2 := &Updater{Current: "v0.1.7", DataDir: u.DataDir}
	u2.RecordStartup()
	st2 := u2.Status()
	if st2.LastResult == nil || !st2.LastResult.OK || st2.LastResult.To != "v0.1.7" {
		t.Fatalf("last result %+v", st2.LastResult)
	}
	if _, err := ReadPending(u.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending should be cleared, got %v", err)
	}

	// Linux archives (.tar.gz) work the same way.
	ul, exeL := newTestUpdater(t, gh, "linux", "a2d4144")
	_ = ul.Check(context.Background())
	if st := ul.Status(); !st.Available || !st.DevBuild {
		t.Fatalf("dev build should see the release: %+v", st)
	}
	if _, err := ul.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, exeL); got != "NEW-LINUX" {
		t.Fatalf("linux exe content %q", got)
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	gh := newFakeGitHub(t, "v0.2.0", map[string][]byte{AssetName("windows", "amd64"): zipWith(t, "homestreaming.exe", []byte("EVIL"))})
	gh.badSum = true
	u, exe := newTestUpdater(t, gh, "windows", "v0.1.0")
	u.Activate = func(Pending) error { t.Error("must not activate"); return nil }
	_ = u.Check(context.Background())
	if err := u.StartInstall(); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, u, StateFailed)
	if !strings.Contains(st.Error, "checksum mismatch") {
		t.Fatalf("error %q", st.Error)
	}
	if got := mustRead(t, exe); got != "OLD" {
		t.Fatalf("exe changed to %q", got)
	}
	if _, err := os.Stat(Sibling(exe, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staged binary left behind")
	}
	// A failed install can be retried.
	if !u.Status().CanInstall {
		t.Fatalf("retry should be allowed: %+v", u.Status())
	}
}

func TestInstallActivationOutcomes(t *testing.T) {
	gh := newFakeGitHub(t, "v0.3.0", map[string][]byte{AssetName("windows", "amd64"): zipWith(t, "homestreaming.exe", []byte("NEW"))})

	// Console run: the binary is replaced, a manual restart finishes it.
	u, exe := newTestUpdater(t, gh, "windows", "v0.2.0")
	_ = u.Check(context.Background())
	_ = u.StartInstall()
	waitState(t, u, StateRestartRequired)
	if mustRead(t, exe) != "NEW" || u.Status().CanInstall {
		t.Fatalf("restart-required state wrong: %+v", u.Status())
	}

	// The helper could not be started: the old binary comes back.
	u, exe = newTestUpdater(t, gh, "windows", "v0.2.0")
	u.Activate = func(Pending) error { return errors.New("spawn failed") }
	_ = u.Check(context.Background())
	_ = u.StartInstall()
	st := waitState(t, u, StateFailed)
	if mustRead(t, exe) != "OLD" || !strings.Contains(st.Error, "spawn failed") {
		t.Fatalf("after failed activation exe=%q status=%+v", mustRead(t, exe), st)
	}
	if _, err := ReadPending(u.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending should be cleared after rollback")
	}
}

func TestStatusReasons(t *testing.T) {
	gh := newFakeGitHub(t, "v0.1.0", map[string][]byte{AssetName("windows", "amd64"): zipWith(t, "homestreaming.exe", []byte("X"))})
	u, _ := newTestUpdater(t, gh, "windows", "v0.1.0")
	_ = u.Check(context.Background())
	if st := u.Status(); st.Available || st.CanInstall || !strings.Contains(st.CannotInstallReason, "latest") {
		t.Fatalf("up to date: %+v", st)
	}
	u, _ = newTestUpdater(t, gh, "darwin", "v0.0.1")
	_ = u.Check(context.Background())
	if st := u.Status(); !st.Available || st.CanInstall || !strings.Contains(st.CannotInstallReason, "darwin/amd64") {
		t.Fatalf("missing platform: %+v", st)
	}
	u, _ = newTestUpdater(t, gh, "windows", "v0.0.1")
	u.InContainer = func() bool { return true }
	_ = u.Check(context.Background())
	if st := u.Status(); st.CanInstall || !strings.Contains(st.CannotInstallReason, "container") {
		t.Fatalf("container: %+v", st)
	}

	// No releases yet: not an error.
	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	u, _ = newTestUpdater(t, gh, "windows", "v0.0.1")
	u.APIBase = empty.URL
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := u.Status(); st.Latest != nil || st.CheckError != "" || st.CheckedAt == nil {
		t.Fatalf("no releases: %+v", st)
	}
}

type fakeService struct {
	mu      sync.Mutex
	running bool
	starts  int
	onStart func()
}

func (f *fakeService) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}

func (f *fakeService) Start() error {
	f.mu.Lock()
	f.running = true
	f.starts++
	cb := f.onStart
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

func (f *fakeService) Running() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, nil
}

// versionServer answers /system/info with whatever version the "installed"
// executable file contains, like a real restart would.
func versionServer(t *testing.T, exe string, svc *fakeService) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, _ := svc.Running(); !ok {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		b, _ := os.ReadFile(exe)
		_ = json.NewEncoder(w).Encode(map[string]string{"version": strings.TrimSpace(string(b))})
	}))
	t.Cleanup(s.Close)
	return s
}

func stagedSwap(t *testing.T, oldV, newV string) (dataDir, exe string, p Pending) {
	dir := t.TempDir()
	exe = filepath.Join(dir, "homestreaming.exe")
	_ = os.WriteFile(exe, []byte(oldV), 0o755)
	next := Sibling(exe, "new")
	_ = os.WriteFile(next, []byte(newV), 0o755)
	old, err := Swap(exe, next)
	if err != nil {
		t.Fatal(err)
	}
	p = Pending{From: oldV, To: newV, Exe: exe, Old: old, StartedAt: time.Now()}
	dataDir = filepath.Join(dir, "data")
	_ = WritePending(dataDir, p)
	return dataDir, exe, p
}

func TestFinishServiceSuccess(t *testing.T) {
	dataDir, exe, p := stagedSwap(t, "v0.1.0", "v0.1.1")
	svc := &fakeService{running: true}
	hs := versionServer(t, exe, svc)
	r := FinishService(context.Background(), dataDir, svc, p, FinishOptions{HealthURL: hs.URL, Poll: 10 * time.Millisecond, StartTimeout: 2 * time.Second})
	if !r.OK || mustRead(t, exe) != "v0.1.1" || svc.starts != 1 {
		t.Fatalf("result %+v exe=%q starts=%d", r, mustRead(t, exe), svc.starts)
	}
	if got, err := ReadResult(dataDir); err != nil || !got.OK {
		t.Fatalf("result.json %+v %v", got, err)
	}
}

func TestFinishServiceRollsBack(t *testing.T) {
	dataDir, exe, p := stagedSwap(t, "v0.1.0", "v0.1.1")
	svc := &fakeService{running: true}
	// The new build "crashes": the service reports stopped right after start.
	svc.onStart = func() {
		if strings.TrimSpace(mustRead(t, exe)) == "v0.1.1" {
			_ = svc.Stop()
		}
	}
	hs := versionServer(t, exe, svc)
	r := FinishService(context.Background(), dataDir, svc, p, FinishOptions{HealthURL: hs.URL, Poll: 10 * time.Millisecond, StartTimeout: 300 * time.Millisecond})
	if r.OK || !strings.Contains(r.Message, "v0.1.0 was restored") {
		t.Fatalf("result %+v", r)
	}
	if mustRead(t, exe) != "v0.1.0" || mustRead(t, Sibling(exe, "failed")) != "v0.1.1" {
		t.Fatalf("exe=%q failed=%q", mustRead(t, exe), mustRead(t, Sibling(exe, "failed")))
	}
	if ok, _ := svc.Running(); !ok || svc.starts != 2 {
		t.Fatalf("service should run the old version again (starts=%d)", svc.starts)
	}
	if _, err := ReadPending(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending should be cleared")
	}
	// The old version starting up must not overwrite the rollback message.
	u := &Updater{Current: "v0.1.0", DataDir: dataDir}
	u.RecordStartup()
	if lr := u.Status().LastResult; lr == nil || lr.OK || !strings.Contains(lr.Message, "restored") {
		t.Fatalf("last result %+v", lr)
	}
}

func TestRestartWatchdogRestoresOldBinary(t *testing.T) {
	gh := newFakeGitHub(t, "v0.4.0", map[string][]byte{AssetName("windows", "amd64"): zipWith(t, "homestreaming.exe", []byte("NEW"))})
	u, exe := newTestUpdater(t, gh, "windows", "v0.3.0")
	u.RestartTimeout = 50 * time.Millisecond
	u.Activate = func(Pending) error { return nil } // helper "started" but never restarts us
	_ = u.Check(context.Background())
	_ = u.StartInstall()
	st := waitState(t, u, StateFailed)
	if mustRead(t, exe) != "OLD" || !strings.Contains(st.Error, "not restarted") {
		t.Fatalf("exe=%q status=%+v", mustRead(t, exe), st)
	}
}

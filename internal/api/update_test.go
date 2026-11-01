package api_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wtfits/homestreaming/internal/update"
)

// archiveFor packs content as the release binary for this platform.
func archiveFor(t *testing.T, content []byte) []byte {
	var buf bytes.Buffer
	bin := update.BinaryName(runtime.GOOS)
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(bin)
		_, _ = w.Write(content)
		_ = zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "./" + bin, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUpdateEndpoints(t *testing.T) {
	e := newEnv(t, "none", 0)
	out := e.must("POST", "/api/v1/system/setup", map[string]any{"username": "can", "password": "password123", "deviceId": "d", "deviceName": "Go test"}, 201)
	e.token = out["token"].(string)

	archiveName := update.AssetName(runtime.GOOS, runtime.GOARCH)
	archive := archiveFor(t, []byte("NEW BUILD"))
	sum := sha256.Sum256(archive)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + archiveName + "\n")
	var gh *httptest.Server
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.0.0", "name": "v9.0.0", "body": "- volume slider", "html_url": "https://example/r",
				"published_at": time.Now().UTC().Format(time.RFC3339), "assets": []map[string]any{
					{"name": archiveName, "browser_download_url": gh.URL + "/dl/archive", "size": len(archive)},
					{"name": update.ChecksumsAsset, "browser_download_url": gh.URL + "/dl/sums", "size": len(sums)},
				}})
		case "/dl/archive":
			_, _ = w.Write(archive)
		case "/dl/sums":
			_, _ = w.Write(sums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()

	exe := filepath.Join(t.TempDir(), update.BinaryName(runtime.GOOS))
	if err := os.WriteFile(exe, []byte("OLD BUILD"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := e.app.Updater
	u.APIBase, u.Repo = gh.URL, "owner/repo"
	u.ExePath = func() (string, error) { return exe, nil }
	u.SkipBinaryCheck = true
	u.InContainer = func() bool { return false }

	st := e.must("GET", "/api/v1/admin/update", nil, 200)
	if st["current"] != "test" || st["devBuild"] != true || st["latest"] != nil || st["canInstall"] != false || st["state"] != "idle" {
		t.Fatalf("initial status %v", st)
	}
	e.must("POST", "/api/v1/admin/update/install", map[string]any{}, 409)

	st = e.must("POST", "/api/v1/admin/update/check", map[string]any{}, 200)
	latest, _ := st["latest"].(map[string]any)
	if latest == nil || latest["version"] != "v9.0.0" || latest["notes"] != "- volume slider" || st["available"] != true || st["canInstall"] != true {
		t.Fatalf("after check %v", st)
	}

	e.must("POST", "/api/v1/admin/update/install", map[string]any{}, 202)
	deadline := time.Now().Add(15 * time.Second)
	for {
		st = e.must("GET", "/api/v1/admin/update", nil, 200)
		if st["state"] == "restart_required" {
			break
		}
		if st["state"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("install did not finish: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW BUILD" {
		t.Fatalf("binary not replaced: %q", b)
	}
	if b, _ := os.ReadFile(update.Sibling(exe, "old")); string(b) != "OLD BUILD" {
		t.Fatalf("old binary not kept: %q", b)
	}
}

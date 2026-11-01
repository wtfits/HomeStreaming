package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultRepo is the GitHub repository whose releases are installed.
const DefaultRepo = "wtfits/HomeStreaming"

// ChecksumsAsset is the release asset listing "<sha256>  <file>" lines.
const ChecksumsAsset = "SHA256SUMS"

// Asset is one downloadable file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a published GitHub release.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
	Assets      []Asset   `json:"-"`
}

// Asset returns the asset with the given name.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the release archive for a platform, as produced by
// scripts/build-dist.sh.
func AssetName(goos, goarch string) string {
	if goos == "windows" {
		return "homestreaming-" + goos + "-" + goarch + ".zip"
	}
	return "homestreaming-" + goos + "-" + goarch + ".tar.gz"
}

// BinaryName is the executable inside a release archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "homestreaming.exe"
	}
	return "homestreaming"
}

// errNoReleases means the repository has not published a release yet.
var errNoReleases = errors.New("no releases published yet")

// fetchLatest returns the newest non-draft, non-prerelease release.
func fetchLatest(ctx context.Context, client *http.Client, apiBase, repo, userAgent string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiBase, "/")+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNoReleases
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("GitHub rate limit reached; try again later (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var gh struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		Assets      []Asset   `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&gh); err != nil {
		return nil, fmt.Errorf("read GitHub release: %w", err)
	}
	if gh.TagName == "" || gh.Draft || gh.Prerelease {
		return nil, errNoReleases
	}
	name := gh.Name
	if name == "" {
		name = gh.TagName
	}
	return &Release{Version: gh.TagName, Name: name, Notes: gh.Body, URL: gh.HTMLURL, PublishedAt: gh.PublishedAt, Assets: gh.Assets}, nil
}

// parseChecksums reads "<hex>  <name>" lines (sha256sum output; a leading
// "*" for binary mode is accepted).
func parseChecksums(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out
}

// download fetches url into path, calling progress with the bytes written so
// far, and returns the file's SHA-256.
func download(ctx context.Context, client *http.Client, url, path, userAgent string, limit int64, progress func(int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{report: progress}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	if n > limit {
		return "", fmt.Errorf("download is larger than %d MB", limit>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	n      int64
	report func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.report != nil {
		p.report(p.n)
	}
	return len(b), nil
}

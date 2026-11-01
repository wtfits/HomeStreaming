// Package ffmpeg locates ffmpeg/ffprobe, detects capabilities (including
// hardware encoders) and builds command lines.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Binaries describes the resolved ffmpeg installation.
type Binaries struct {
	Found       bool   `json:"found"`
	FFmpeg      string `json:"path,omitempty"`
	FFprobe     string `json:"ffprobePath,omitempty"`
	Version     string `json:"version,omitempty"`
	Major       int    `json:"-"`
	Minor       int    `json:"-"`
	Source      string `json:"-"` // which locator rule matched
	VersionWarn string `json:"-"`
}

// LocateOptions parameterizes the locator (fields are injectable for tests).
type LocateOptions struct {
	ConfigFFmpeg  string
	ConfigFFprobe string
	DataDir       string
	ExeDir        string
	GOOS          string
	UsersDir      string // Windows: C:\Users
	SystemDrive   string // Windows: C:
	LookPath      func(string) (string, error)
}

// ErrNotFound is returned when ffmpeg cannot be located.
var ErrNotFound = errors.New("ffmpeg not found")

// InstallHint is shown wherever a missing ffmpeg is reported.
const InstallHint = "install with: winget install Gyan.FFmpeg (then restart HomeStreaming) or set ffmpeg.path in config.toml"

func (o *LocateOptions) defaults() {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.ExeDir == "" {
		if exe, err := os.Executable(); err == nil {
			o.ExeDir = filepath.Dir(exe)
		}
	}
	if o.GOOS == "windows" {
		if o.SystemDrive == "" {
			o.SystemDrive = os.Getenv("SystemDrive")
			if o.SystemDrive == "" {
				o.SystemDrive = "C:"
			}
		}
		if o.UsersDir == "" {
			o.UsersDir = o.SystemDrive + `\Users`
		}
	}
}

func (o *LocateOptions) exe(name string) string {
	if o.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Candidates returns the ordered list of (rule, path-or-glob) candidates.
func (o *LocateOptions) candidates() [][2]string {
	o.defaults()
	ff := o.exe("ffmpeg")
	var c [][2]string
	if o.ConfigFFmpeg != "" {
		c = append(c, [2]string{"config", o.ConfigFFmpeg})
	}
	if o.DataDir != "" {
		c = append(c, [2]string{"datadir", filepath.Join(o.DataDir, "bin", ff)})
	}
	if o.ExeDir != "" {
		c = append(c, [2]string{"exedir", filepath.Join(o.ExeDir, ff)})
		c = append(c, [2]string{"exedir", filepath.Join(o.ExeDir, "ffmpeg", "bin", ff)})
	}
	c = append(c, [2]string{"path", ""}) // exec.LookPath
	switch o.GOOS {
	case "windows":
		c = append(c,
			[2]string{"winget", filepath.Join(o.UsersDir, "*", "AppData", "Local", "Microsoft", "WinGet", "Packages", "Gyan.FFmpeg*", "ffmpeg-*", "bin", ff)},
			[2]string{"winget", filepath.Join(o.UsersDir, "*", "AppData", "Local", "Microsoft", "WinGet", "Links", ff)},
			[2]string{"winget", filepath.Join(o.SystemDrive+`\`, "Program Files", "WinGet", "Packages", "Gyan.FFmpeg*", "ffmpeg-*", "bin", ff)},
			[2]string{"well-known", filepath.Join(o.SystemDrive+`\`, "ffmpeg", "bin", ff)},
			[2]string{"well-known", filepath.Join(o.SystemDrive+`\`, "ProgramData", "chocolatey", "bin", ff)},
		)
	case "darwin":
		c = append(c, [2]string{"well-known", "/opt/homebrew/bin/ffmpeg"}, [2]string{"well-known", "/usr/local/bin/ffmpeg"})
	default:
		c = append(c, [2]string{"well-known", "/usr/bin/ffmpeg"}, [2]string{"well-known", "/usr/local/bin/ffmpeg"})
	}
	return c
}

var pathVersionRe = regexp.MustCompile(`ffmpeg-n?(\d+(?:\.\d+)*)`)

// FindPaths resolves ffmpeg and ffprobe paths without running them.
func FindPaths(o LocateOptions) (ffmpegPath, ffprobePath, source string, err error) {
	for _, cand := range o.candidates() {
		rule, p := cand[0], cand[1]
		var found string
		switch {
		case rule == "path":
			if lp, lerr := o.LookPath("ffmpeg"); lerr == nil {
				found = lp
			}
		case strings.ContainsAny(p, "*?["):
			matches, _ := filepath.Glob(p)
			matches = filterExisting(matches)
			if len(matches) > 0 {
				sortNewestFirst(matches)
				found = matches[0]
			}
		default:
			if isFile(p) {
				found = p
			}
		}
		if found == "" {
			if rule == "config" {
				return "", "", "", fmt.Errorf("%w: configured ffmpeg.path %q does not exist", ErrNotFound, p)
			}
			continue
		}
		probe := o.ConfigFFprobe
		if probe == "" || !isFile(probe) {
			probe = filepath.Join(filepath.Dir(found), o.exe("ffprobe"))
			if !isFile(probe) {
				if lp, lerr := o.LookPath("ffprobe"); lerr == nil {
					probe = lp
				} else {
					return "", "", "", fmt.Errorf("%w: ffprobe not found next to %s", ErrNotFound, found)
				}
			}
		}
		return found, probe, rule, nil
	}
	return "", "", "", ErrNotFound
}

func filterExisting(paths []string) []string {
	var out []string
	for _, p := range paths {
		if isFile(p) {
			out = append(out, p)
		}
	}
	return out
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// sortNewestFirst orders glob matches by the version embedded in the path
// (ffmpeg-7.1-full_build > ffmpeg-6.1.1), then lexically descending.
func sortNewestFirst(paths []string) {
	ver := func(p string) []int {
		m := pathVersionRe.FindStringSubmatch(filepath.ToSlash(p))
		if m == nil {
			return nil
		}
		var out []int
		for _, part := range strings.Split(m[1], ".") {
			n, _ := strconv.Atoi(part)
			out = append(out, n)
		}
		return out
	}
	sort.SliceStable(paths, func(i, j int) bool {
		vi, vj := ver(paths[i]), ver(paths[j])
		for k := 0; k < len(vi) && k < len(vj); k++ {
			if vi[k] != vj[k] {
				return vi[k] > vj[k]
			}
		}
		if len(vi) != len(vj) {
			return len(vi) > len(vj)
		}
		return paths[i] > paths[j]
	})
}

var versionRe = regexp.MustCompile(`ffmpeg version n?((\d+)\.(\d+)(?:\.\d+)?)`)

// ParseVersion extracts the version from `ffmpeg -version` output. Git builds
// ("N-112345-g...") are treated as very new.
func ParseVersion(out string) (version string, major, minor int) {
	first := strings.SplitN(out, "\n", 2)[0]
	if m := versionRe.FindStringSubmatch(first); m != nil {
		major, _ = strconv.Atoi(m[2])
		minor, _ = strconv.Atoi(m[3])
		return m[1], major, minor
	}
	if strings.HasPrefix(first, "ffmpeg version N-") {
		return strings.Fields(first)[2], 99, 0
	}
	return "", 0, 0
}

// Locate finds ffmpeg/ffprobe and reads the version.
func Locate(ctx context.Context, o LocateOptions) Binaries {
	ff, fp, src, err := FindPaths(o)
	if err != nil {
		return Binaries{}
	}
	b := Binaries{Found: true, FFmpeg: ff, FFprobe: fp, Source: src}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := Command(cctx, ff, "-hide_banner", "-version").Output()
	if err != nil {
		return Binaries{}
	}
	b.Version, b.Major, b.Minor = ParseVersion(string(out))
	if b.Version == "" {
		// Some builds omit "-hide_banner" support for -version; retry plain.
		if out2, err2 := Command(cctx, ff, "-version").Output(); err2 == nil {
			b.Version, b.Major, b.Minor = ParseVersion(string(out2))
		}
	}
	switch {
	case b.Major > 0 && (b.Major < 5 || (b.Major == 5 && b.Minor < 1)):
		b.VersionWarn = fmt.Sprintf("ffmpeg %s is too old (need >= 5.1)", b.Version)
	case b.Major > 0 && b.Major == 6 && b.Minor < 1, b.Major == 5:
		b.VersionWarn = fmt.Sprintf("ffmpeg %s works but 6.1 or newer is recommended", b.Version)
	}
	return b
}

// Package update keeps HomeStreaming current: it checks GitHub Releases,
// downloads and verifies the build for this platform, swaps the executable
// and, with the caller's help, restarts the server — rolling back to the
// previous binary when the new one does not come up.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version vMAJOR.MINOR.PATCH.
type Version struct{ Major, Minor, Patch int }

// ParseVersion accepts release versions such as "v0.1.42" or "0.1.42".
// Development builds ("0.1.0-dev", a git hash, "v0.1.41-3-gabc1234-dirty")
// are not release versions and return false.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return Version{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, true
}

// Less reports whether v sorts before o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Newer reports whether latest should replace current. A development build
// (current is not a release version) accepts any release, so a hand-built
// or branch binary can move onto the release track.
func Newer(current, latest string) (newer, devBuild bool) {
	l, ok := ParseVersion(latest)
	if !ok {
		return false, false
	}
	c, ok := ParseVersion(current)
	if !ok {
		return true, true
	}
	return c.Less(l), false
}

// SameVersion compares two version strings, ignoring a leading "v".
func SameVersion(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

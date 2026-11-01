package library

import (
	"path/filepath"
	"regexp"
	"strings"
)

var videoExts = toSet(`.mkv .mp4 .m4v .mov .webm .avi .ts .m2ts .mts .mpg .mpeg .wmv .flv .ogv .vob`)

var incompleteSuffixes = []string{".part", ".!qb", ".!ut", ".crdownload", ".tmp", ".download", ".bts", ".dctmp", ".aria2"}

var (
	reSampleFile = regexp.MustCompile(`(?i)(^|[ ._-])sample([ ._-]|$)`)
	reExtrasDir  = regexp.MustCompile(`(?i)^(sample|samples|extras?|featurettes|behind the scenes|deleted scenes|interviews|trailers?|proof|screens?|screenshots|other)$`)
)

var systemDirs = toSet(`$recycle.bin system~volume~information .git node_modules @eadir #recycle lost+found .trash .trashes .spotlight-v100 .fseventsd`)

// IsVideo reports whether name has a candidate video extension.
func IsVideo(name string) bool {
	return videoExts[strings.ToLower(filepath.Ext(name))]
}

// IsIncomplete reports in-progress download names (.part, .!qB, ...).
func IsIncomplete(name string) bool {
	low := strings.ToLower(name)
	for _, s := range incompleteSuffixes {
		if strings.HasSuffix(low, s) {
			return true
		}
	}
	return false
}

// IsHidden reports dot/tilde files.
func IsHidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~")
}

// IsSampleFile reports sample clips by name.
func IsSampleFile(name string) bool {
	return reSampleFile.MatchString(stripExt(name))
}

// SkipDir reports directories that are never entered.
func SkipDir(name string) bool {
	low := strings.ToLower(name)
	return systemDirs[strings.ReplaceAll(low, " ", "~")] || IsHidden(name) || reExtrasDir.MatchString(name)
}

// IsDiscDir reports DVD/Blu-ray structures (VIDEO_TS, BDMV).
func IsDiscDir(name string) bool {
	u := strings.ToUpper(name)
	return u == "VIDEO_TS" || u == "BDMV"
}

// Classification of a directory entry for scan reporting.
const (
	FileCandidate  = "candidate"
	FileIncomplete = "incomplete"
	FileSample     = "sample"
	FileIgnored    = "ignored"
)

// ClassifyFile applies the per-file ignore rules (not the size-ratio rule).
func ClassifyFile(name string) string {
	switch {
	case IsHidden(name):
		return FileIgnored
	case IsIncomplete(name):
		return FileIncomplete
	case !IsVideo(name):
		return FileIgnored
	case IsSampleFile(name):
		return FileSample
	}
	return FileCandidate
}

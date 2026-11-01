package library

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wtfits/homestreaming/internal/db"
	"github.com/wtfits/homestreaming/internal/media"
)

var subExts = toSet(`.srt .vtt .ass .ssa`)

var reLeadingIndex = regexp.MustCompile(`^\d+[_ .-]+`)

// FindExternalSubtitles discovers sidecar subtitles for a video.
// absDir is the video's directory, relDir its slash path relative to the root,
// base the video file name without extension, videosInDir the candidate count.
func FindExternalSubtitles(absDir, relDir, base string, videosInDir int) []db.ExternalSubtitle {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}
	var out []db.ExternalSubtitle
	lowBase := strings.ToLower(base)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			low := strings.ToLower(name)
			if videosInDir == 1 && (low == "subs" || low == "subtitles") {
				out = append(out, subsFolder(filepath.Join(absDir, name), path.Join(relDir, name))...)
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !subExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		lowStem := strings.ToLower(stem)
		if lowStem != lowBase && !strings.HasPrefix(lowStem, lowBase+".") {
			continue
		}
		s := db.ExternalSubtitle{RelPath: path.Join(relDir, name), Format: ext[1:], Title: name}
		if rest := strings.TrimPrefix(stem[len(base):], "."); rest != "" {
			for _, part := range strings.Split(rest, ".") {
				lp := strings.ToLower(part)
				switch lp {
				case "forced":
					s.IsForced = true
				case "sdh", "hi", "cc":
					s.IsSDH = true
				default:
					if l := media.KnownLang(lp); l != "" && s.Language == "" {
						s.Language = l
					}
				}
			}
		}
		out = append(out, s)
	}
	return out
}

func subsFolder(abs, rel string) []db.ExternalSubtitle {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil
	}
	var out []db.ExternalSubtitle
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !subExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		s := db.ExternalSubtitle{RelPath: path.Join(rel, name), Format: ext[1:], Title: name}
		langPart := reLeadingIndex.ReplaceAllString(stem, "") // "2_English" → "English"
		for _, tok := range strings.FieldsFunc(langPart, func(r rune) bool { return r == '.' || r == '_' || r == ' ' || r == '-' }) {
			lt := strings.ToLower(tok)
			if lt == "forced" {
				s.IsForced = true
				continue
			}
			if lt == "sdh" {
				s.IsSDH = true
				continue
			}
			if l := media.KnownLang(tok); l != "" && s.Language == "" {
				s.Language = l
			}
		}
		out = append(out, s)
	}
	return out
}

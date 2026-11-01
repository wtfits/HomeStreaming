// Package library scans library roots: file naming/classification, ignore
// rules, probing, artwork, external subtitles and the filesystem watcher.
package library

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Parsed is the result of classifying one video file.
type Parsed struct {
	Kind         string // movie | episode
	Title        string // movie title or series title
	Year         int
	Season       int
	Episode      int
	EpisodeEnd   int
	EpisodeTitle string
	SeriesKey    string
	Confidence   string // high | low
}

// ParseInput is the context the parser needs for one file.
type ParseInput struct {
	RelPath     string   // slash-separated path relative to the library root
	LibraryType string   // mixed | movies | shows
	Siblings    []string // file names of candidate videos in the same folder (including this one)
}

var (
	reE1          = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})(?:[ ._-]*E(\d{1,3}))?\b`)
	reE2          = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	reFolderSeasn = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bS(\d{1,2})\b`),
		regexp.MustCompile(`(?i)\(?\bSeason (\d{1,2})\)?`),
		regexp.MustCompile(`(?i)\bSeason[ ._]?(\d{1,2})\b`),
	}
	reFileEpE3 = []*regexp.Regexp{
		regexp.MustCompile(`^\[?(\d{1,3})\]?[ ._-]+`),
		regexp.MustCompile(`(?i)^E(\d{1,3})\b`),
		regexp.MustCompile(`(?i)\bE(\d{1,3})\b`),
	}
	reE4Bracket   = regexp.MustCompile(`^\[(\d{1,3})\]\s*(.+)$`)
	reE4Plain     = regexp.MustCompile(`^(\d{1,3})[ .-]+(.+)$`)
	reDated       = regexp.MustCompile(`\b(\d{4})[.-](\d{2})[.-](\d{2})\b`)
	reSeasonDir   = regexp.MustCompile(`(?i)^(season|series|s|сезон)[ ._-]*\d{1,2}$|^specials$`)
	reYearTok     = regexp.MustCompile(`^(19|20)\d{2}$`)
	reBracketYear = regexp.MustCompile(`^[(\[]((?:19|20)\d{2})[)\]]$`)
	reResolution  = regexp.MustCompile(`(?i)^\d{3,4}[pi]$`)
	reAudioCh     = regexp.MustCompile(`(?i)^(ddp|dd\+?|aac|ac3|eac3|dts|truehd|flac|opus|lpcm|atmos)?[1-7]ch[01]$`)
	reAudioCodecN = regexp.MustCompile(`(?i)^(ddp|dd|eac3|ac3|aac|dts)\d$`)
	reMultiLang   = regexp.MustCompile(`(?i)^\d+x(rus|eng|ukr|tur)$`)
	reSeasonTok   = regexp.MustCompile(`(?i)^s\d{1,2}$`)
	reDottedCodec = regexp.MustCompile(`(?i)\b([hx])\.(26[45])\b`)
	reDottedAudio = regexp.MustCompile(`(?i)\b(ddp|dd|aac|ac3|eac3|dts|truehd|flac|opus|lpcm|atmos)?([1-7])\.([01])\b`)
	reBracketGrp  = regexp.MustCompile(`[(\[][^)\]]*[)\]]`)
)

// stopTags end a title (Appendix C).
var stopTags = toSet(`2160p 1080p 1080i 720p 576p 480p 720 1080 2160 4k uhd
web-dl webdl web webrip web-rip bluray blu-ray bdrip bdremux remux hdtv hdtvrip dvdrip dvd dvd9 dvd5 hdrip brrip amzn nf atvp dsnp hmax hulu itunes pcok
x264 x265 h264 h265 hevc avc av1 xvid divx vp9
hdr hdr10 hdr10+ hdr10plus dv dovi sdr hlg 10bit 8bit
aac ac3 dd ddp eac3 dts dts-hd dtshd truehd atmos flac mp3 opus 2ch 6ch
rus eng ukr tur ger fre fra spa ita jpn kor chi multi dual dub sub subs
proper repack rerip extended unrated uncut remastered internal limited complete retail theatrical imax hybrid
season`)

// stripTags are release groups/sites removed without ending the title.
var stripTags = toSet(`rutracker rgzsrutracker novafilm lostfilm newstudio hdrezka kubik amedia ozz jaskier rarbg yts yify fgt ctrlhd ntb ntg ethel`)

func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

func isStopTag(tok string) bool {
	low := strings.ToLower(strings.Trim(tok, "-–:,"))
	if low == "" {
		return false
	}
	if stopTags[low] || reResolution.MatchString(low) || reAudioCh.MatchString(low) || reAudioCodecN.MatchString(low) ||
		reMultiLang.MatchString(low) || reSeasonTok.MatchString(low) {
		return true
	}
	if i := strings.Index(low, "-"); i > 0 { // x264-GRP, Atmos-GRP, 1080p-GRP
		pre := low[:i]
		if stopTags[pre] || reResolution.MatchString(pre) || reAudioCh.MatchString(pre) || reAudioCodecN.MatchString(pre) {
			return true
		}
	}
	return false
}

// normalizeName rewrites dotted tags and replaces '.'/'_' with spaces.
func normalizeName(s string) string {
	s = reDottedCodec.ReplaceAllString(s, "$1$2")
	s = reDottedAudio.ReplaceAllString(s, "${1}${2}ch${3}")
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// extractTitle returns the title portion of a name segment and its year.
func extractTitle(seg string) (title string, year int, stopped bool) {
	s := normalizeName(seg)
	// Bracketed years count as the year; bracket groups are then removed.
	bracketYear := 0
	for _, g := range reBracketGrp.FindAllString(s, -1) {
		if m := reBracketYear.FindStringSubmatch(g); m != nil && bracketYear == 0 {
			bracketYear, _ = strconv.Atoi(m[1])
		}
	}
	s = reBracketGrp.ReplaceAllString(s, " ")
	toks := strings.Fields(s)
	var out []string
	for i, tok := range toks {
		low := strings.ToLower(tok)
		if bracketYear == 0 && reYearTok.MatchString(tok) {
			if i+1 < len(toks) && reYearTok.MatchString(toks[i+1]) {
				out = append(out, tok) // "Blade Runner 2049 2017", "1917 2019"
				continue
			}
			if len(out) == 0 {
				out = append(out, tok) // a title that is only a year
				continue
			}
			year, _ = strconv.Atoi(tok)
			stopped = true
			break
		}
		if isStopTag(tok) {
			stopped = true
			break
		}
		if stripTags[low] {
			continue
		}
		if low == "by" && i == len(toks)-2 {
			break
		}
		out = append(out, tok)
	}
	if year == 0 {
		year = bracketYear
	}
	return cleanTitle(strings.Join(out, " ")), year, stopped
}

// extractEpisodeTitle returns the text after an episode marker up to the first tag.
func extractEpisodeTitle(seg string) string {
	s := normalizeName(seg)
	toks := strings.Fields(s)
	var out []string
	for i, tok := range toks {
		if len(out) == 0 && strings.Trim(tok, "-–:,") == "" {
			continue
		}
		if strings.HasPrefix(tok, "[") || strings.HasPrefix(tok, "(") || reYearTok.MatchString(tok) || isStopTag(tok) {
			break
		}
		if stripTags[strings.ToLower(tok)] {
			continue
		}
		if strings.ToLower(tok) == "by" && i == len(toks)-2 {
			break
		}
		out = append(out, tok)
	}
	return cleanTitle(strings.Join(out, " "))
}

func cleanTitle(s string) string {
	s = strings.Trim(strings.Join(strings.Fields(s), " "), " -–:,")
	if s != "" && s == strings.ToLower(s) {
		s = titleCase(s)
	}
	return s
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// SeriesKey normalizes a title for merging: NFKD, lowercase, letters/digits only.
func SeriesKey(title string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(title)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SortTitle returns the sort key of a title (lowercase, leading article removed).
func SortTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(t, art) && len(t) > len(art) {
			return t[len(art):]
		}
	}
	return t
}

func stripExt(name string) string {
	return strings.TrimSuffix(name, path.Ext(name))
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// folderTitle derives a series title (and year) from a folder chain, skipping
// pure season folders ("Season 02").
func folderTitle(dirs []string) (string, int) {
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if reSeasonDir.MatchString(strings.TrimSpace(d)) {
			continue
		}
		t, y, _ := extractTitle(cutAtSeasonMarker(d))
		if t != "" {
			return t, y
		}
	}
	return "", 0
}

func cutAtSeasonMarker(s string) string {
	cut := len(s)
	for _, re := range reFolderSeasn {
		if loc := re.FindStringIndex(s); loc != nil && loc[0] < cut {
			// Keep a bracketed year that precedes the marker; text after is tags.
			cut = loc[0]
		}
	}
	return s[:cut]
}

func folderSeason(d string) (int, bool) {
	for _, re := range reFolderSeasn {
		if m := re.FindStringSubmatch(d); m != nil {
			return atoi(m[1]), true
		}
	}
	return 0, false
}

// Parse classifies a video file and extracts naming information.
func Parse(in ParseInput) Parsed {
	rel := strings.Trim(in.RelPath, "/")
	dir, file := path.Split(rel)
	base := stripExt(file)
	var dirs []string
	if d := strings.Trim(dir, "/"); d != "" {
		dirs = strings.Split(d, "/")
	}
	parent := ""
	if len(dirs) > 0 {
		parent = dirs[len(dirs)-1]
	}
	libType := in.LibraryType
	if libType == "" {
		libType = "mixed"
	}

	if libType != "movies" {
		if p, ok := parseEpisode(base, parent, dirs, in.Siblings); ok {
			p.SeriesKey = SeriesKey(p.Title)
			return p
		}
		if libType == "shows" {
			title, year := folderTitle(dirs)
			if title == "" {
				title, year, _ = extractTitle(base)
			}
			idx := 1
			sib := append([]string{}, in.Siblings...)
			sort.Strings(sib)
			for i, s := range sib {
				if s == file {
					idx = i + 1
				}
			}
			return Parsed{Kind: "episode", Title: title, Year: year, Season: 1, Episode: idx, SeriesKey: SeriesKey(title), Confidence: "low"}
		}
	}
	return parseMovie(base, parent, in.Siblings)
}

func parseEpisode(base, parent string, dirs, siblings []string) (Parsed, bool) {
	p := Parsed{Kind: "episode", Confidence: "high"}
	// E1 / E2 on the file name.
	try := func(name string) (before, after string, ok bool) {
		if m := reE1.FindStringSubmatchIndex(name); m != nil {
			p.Season = atoi(name[m[2]:m[3]])
			p.Episode = atoi(name[m[4]:m[5]])
			if m[6] >= 0 {
				p.EpisodeEnd = atoi(name[m[6]:m[7]])
			}
			return name[:m[0]], name[m[1]:], true
		}
		if m := reE2.FindStringSubmatchIndex(name); m != nil {
			p.Season = atoi(name[m[2]:m[3]])
			p.Episode = atoi(name[m[4]:m[5]])
			return name[:m[0]], name[m[1]:], true
		}
		return "", "", false
	}
	if before, after, ok := try(base); ok {
		p.Title, p.Year, _ = extractTitle(before)
		p.EpisodeTitle = extractEpisodeTitle(after)
		if p.Title == "" {
			p.Title, p.Year = folderTitleWithYear(dirs, p.Year)
		} else if p.Year == 0 {
			_, p.Year = folderTitle(dirs)
		}
		return p, p.Title != ""
	}
	// E1 / E2 on the folder name (single-video folders only).
	if parent != "" && len(siblings) <= 1 {
		if before, _, ok := try(parent); ok {
			p.Title, p.Year, _ = extractTitle(before)
			if p.Title != "" {
				p.EpisodeTitle = ""
				return p, true
			}
		}
	}
	// E3: season from the folder, episode from the file.
	if parent != "" {
		if season, ok := folderSeason(parent); ok {
			for _, re := range reFileEpE3 {
				if m := re.FindStringSubmatchIndex(base); m != nil {
					p.Season = season
					p.Episode = atoi(base[m[2]:m[3]])
					p.EpisodeTitle = extractEpisodeTitle(base[m[1]:])
					p.Title, p.Year = folderTitle(dirs)
					return p, p.Title != ""
				}
			}
		}
	}
	// E4: "[07] Title" / "07 - Title" with >= 2 siblings of the same form.
	for _, re := range []*regexp.Regexp{reE4Bracket, reE4Plain} {
		m := re.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		n := 0
		for _, s := range siblings {
			if re.MatchString(stripExt(s)) {
				n++
			}
		}
		if n < 2 {
			continue
		}
		p.Season = 1
		p.Episode = atoi(m[1])
		p.EpisodeTitle = extractEpisodeTitle(m[2])
		p.Title, p.Year = folderTitle(dirs)
		return p, p.Title != ""
	}
	return Parsed{}, false
}

func folderTitleWithYear(dirs []string, year int) (string, int) {
	t, y := folderTitle(dirs)
	if year != 0 {
		y = year
	}
	return t, y
}

var junkNames = toSet(`movie video film sample main feature`)

func parseMovie(base, parent string, siblings []string) Parsed {
	p := Parsed{Kind: "movie", Confidence: "high"}
	useFolder := false
	if parent != "" && len(siblings) <= 1 && !reSeasonDir.MatchString(parent) {
		_, fy, _ := extractTitle(parent)
		fileJunk := junkNames[strings.ToLower(base)] || strings.EqualFold(normalizeName(base), normalizeName(parent))
		if fy != 0 || fileJunk {
			useFolder = true
		}
	}
	src := base
	if useFolder {
		src = parent
	}
	var stopped bool
	p.Title, p.Year, stopped = extractTitle(src)
	if useFolder && p.Year == 0 {
		_, p.Year, _ = extractTitle(base)
	}
	if p.Title == "" {
		p.Title = cleanTitle(normalizeName(base))
	}
	if (!stopped && p.Year == 0) || reDated.MatchString(base) {
		p.Confidence = "low"
	}
	p.SeriesKey = ""
	return p
}

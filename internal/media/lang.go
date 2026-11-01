// Package media holds small media helpers shared by the scanner and the
// streaming code: language normalization and display names.
package media

import (
	"regexp"
	"strings"
)

var langAliases = map[string]string{
	"en": "eng", "eng": "eng", "english": "eng",
	"ru": "rus", "rus": "rus", "russian": "rus",
	"tr": "tur", "tur": "tur", "turkish": "tur",
	"uk": "ukr", "ukr": "ukr", "ukrainian": "ukr",
	"de": "ger", "ger": "ger", "deu": "ger", "german": "ger",
	"fr": "fre", "fre": "fre", "fra": "fre", "french": "fre",
	"es": "spa", "spa": "spa", "spanish": "spa",
	"it": "ita", "ita": "ita", "italian": "ita",
	"ja": "jpn", "jpn": "jpn", "japanese": "jpn",
	"ko": "kor", "kor": "kor", "korean": "kor",
	"zh": "chi", "chi": "chi", "zho": "chi", "chinese": "chi",
	"pt": "por", "por": "por", "portuguese": "por",
	"pl": "pol", "pol": "pol", "polish": "pol",
	"nl": "dut", "dut": "dut", "nld": "dut", "dutch": "dut",
	"ar": "ara", "ara": "ara", "arabic": "ara",
	"sv": "swe", "swe": "swe", "swedish": "swe",
	"cs": "cze", "cze": "cze", "ces": "cze", "czech": "cze",
	"he": "heb", "heb": "heb", "hebrew": "heb",
	"hi": "hin", "hin": "hin", "hindi": "hin",
	"el": "gre", "gre": "gre", "ell": "gre", "greek": "gre",
}

var names = map[string]string{
	"eng": "English", "rus": "Russian", "tur": "Turkish", "ukr": "Ukrainian", "ger": "German", "fre": "French",
	"spa": "Spanish", "ita": "Italian", "jpn": "Japanese", "kor": "Korean", "chi": "Chinese", "por": "Portuguese",
	"pol": "Polish", "dut": "Dutch", "ara": "Arabic", "swe": "Swedish", "cze": "Czech", "heb": "Hebrew", "hin": "Hindi", "gre": "Greek",
}

var bcp47 = map[string]string{
	"eng": "en", "rus": "ru", "tur": "tr", "ukr": "uk", "ger": "de", "fre": "fr", "spa": "es", "ita": "it", "jpn": "ja",
	"kor": "ko", "chi": "zh", "por": "pt", "pol": "pl", "dut": "nl", "ara": "ar", "swe": "sv", "cze": "cs", "heb": "he", "hin": "hi", "gre": "el",
}

// NormalizeLang maps a language tag or name to ISO 639-2/B ("" if unknown/und).
func NormalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "und" || s == "unk" || s == "mul" || s == "zxx" {
		return ""
	}
	if i := strings.IndexAny(s, "-_"); i > 0 { // en-US
		s = s[:i]
	}
	if v, ok := langAliases[s]; ok {
		return v
	}
	if len(s) == 3 {
		return s
	}
	return ""
}

// KnownLang is like NormalizeLang but only accepts known aliases (used for
// guessing languages from file names, where "s01" must not become a language).
func KnownLang(s string) string {
	return langAliases[strings.ToLower(strings.TrimSpace(s))]
}

// LangName returns an English display name for an ISO 639-2 code.
func LangName(code string) string {
	if n, ok := names[code]; ok {
		return n
	}
	if code == "" {
		return "Unknown"
	}
	return strings.ToUpper(code)
}

// BCP47 returns the 2-letter tag used in HLS LANGUAGE attributes.
func BCP47(code string) string {
	if v, ok := bcp47[code]; ok {
		return v
	}
	return code
}

var (
	rusTitleRe = regexp.MustCompile(`(?i)rus|рус|russian|lostfilm|novafilm|kubik|кубик|hdrezka|amedia|newstudio|jaskier|ozz|dub|дубляж|многоголос|одноголос`)
	engTitleRe = regexp.MustCompile(`(?i)\beng|english|original|orig\b|оригинал`)
	turTitleRe = regexp.MustCompile(`(?i)\btur|türk|turkish|türkçe`)
)

// StreamLang returns the normalized language of an audio/subtitle stream,
// falling back to title heuristics for untagged Russian-release tracks.
func StreamLang(tag, title string) string {
	if l := NormalizeLang(tag); l != "" {
		return l
	}
	switch {
	case title == "":
		return ""
	case engTitleRe.MatchString(title):
		return "eng"
	case turTitleRe.MatchString(title):
		return "tur"
	case rusTitleRe.MatchString(title):
		return "rus"
	}
	return ""
}

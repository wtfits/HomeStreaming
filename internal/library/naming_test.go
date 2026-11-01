package library

import (
	"path"
	"testing"
)

type fixture struct {
	rel      string
	siblings []string
	want     Parsed
}

func TestNamingFixtures(t *testing.T) {
	fixtures := []fixture{
		{"Widow's Bay (Season 1) WEB-DL 2160p/Widow's.Bay.S01E03.WEB-DL.2160p.RGzsRutracker.mkv", nil,
			Parsed{Kind: "episode", Title: "Widow's Bay", Season: 1, Episode: 3, SeriesKey: "widowsbay"}},
		{"Over the Garden Wall (2014) BDRemux 1080p/[07] The Ringing of the Bell.mkv",
			[]string{"[07] The Ringing of the Bell.mkv", "[08] Babes in the Wood.mkv"},
			Parsed{Kind: "episode", Title: "Over the Garden Wall", Year: 2014, Season: 1, Episode: 7, EpisodeTitle: "The Ringing of the Bell", SeriesKey: "overthegardenwall"}},
		{"Better Call Saul S04 2160p/Better.Call.Saul.S04E02.WEB-DL.2160p.SDR.HEVC.2xRus.Eng.mkv", nil,
			Parsed{Kind: "episode", Title: "Better Call Saul", Season: 4, Episode: 2, SeriesKey: "bettercallsaul"}},
		{"arrested.development.s02.hdtv.720.rus.eng.novafilm.tv/arrested.development.s02e02.hdtv.720.rus.eng.novafilm.tv.mkv", nil,
			Parsed{Kind: "episode", Title: "Arrested Development", Season: 2, Episode: 2, SeriesKey: "arresteddevelopment"}},
		{"Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.mkv", nil,
			Parsed{Kind: "episode", Title: "Arrested Development", Season: 1, Episode: 7, SeriesKey: "arresteddevelopment"}},
		{"Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Pluribus.S01E02.Pirate.Lady.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP.mkv", nil,
			Parsed{Kind: "episode", Title: "Pluribus", Season: 1, Episode: 2, EpisodeTitle: "Pirate Lady", SeriesKey: "pluribus"}},
		{"Movie.Name.2019.1080p.BluRay.x264-GRP/Movie.Name.2019.1080p.BluRay.x264-GRP.mkv", []string{"Movie.Name.2019.1080p.BluRay.x264-GRP.mkv"},
			Parsed{Kind: "movie", Title: "Movie Name", Year: 2019}},
		{"Some Movie (2021)/Some Movie (2021).mkv", []string{"Some Movie (2021).mkv"},
			Parsed{Kind: "movie", Title: "Some Movie", Year: 2021}},
		{"Another.Film.2018.REPACK.2160p.WEB-DL.DV.HDR10.HEVC.DDP5.1.Atmos-GRP.mkv", nil,
			Parsed{Kind: "movie", Title: "Another Film", Year: 2018}},
		{"Show.Name.1x05.Episode.Title.720p.HDTV.mkv", nil,
			Parsed{Kind: "episode", Title: "Show Name", Season: 1, Episode: 5, EpisodeTitle: "Episode Title", SeriesKey: "showname"}},
		{"Show Name/Season 02/Show Name - S02E10 - The End.mkv", nil,
			Parsed{Kind: "episode", Title: "Show Name", Season: 2, Episode: 10, EpisodeTitle: "The End", SeriesKey: "showname"}},
		{"Show.Name.S01E01E02.720p.mkv", nil,
			Parsed{Kind: "episode", Title: "Show Name", Season: 1, Episode: 1, EpisodeEnd: 2, SeriesKey: "showname"}},
		// Additional cases.
		{"Third Movie (2020)/Third Movie (2020).mkv", []string{"Third Movie (2020).mkv"},
			Parsed{Kind: "movie", Title: "Third Movie", Year: 2020}},
		{"Fourth.Movie.2017.720p.WEBRip.VP9.webm", nil,
			Parsed{Kind: "movie", Title: "Fourth Movie", Year: 2017}},
		{"Blade.Runner.2049.2017.2160p.mkv", nil,
			Parsed{Kind: "movie", Title: "Blade Runner 2049", Year: 2017}},
		{"1917.2019.1080p.BluRay.mkv", nil,
			Parsed{Kind: "movie", Title: "1917", Year: 2019}},
		{"Show S02/03. The Third One.mkv", []string{"03. The Third One.mkv"},
			Parsed{Kind: "episode", Title: "Show", Season: 2, Episode: 3, EpisodeTitle: "The Third One", SeriesKey: "show"}},
		{"Some Show/Season 1/S01E04.mkv", nil,
			Parsed{Kind: "episode", Title: "Some Show", Season: 1, Episode: 4, SeriesKey: "someshow"}},
		{"Movie Folder (2015)/movie.mkv", []string{"movie.mkv"},
			Parsed{Kind: "movie", Title: "Movie Folder", Year: 2015}},
		{"Downloading.Show.S01E01.1080p.mkv", nil,
			Parsed{Kind: "episode", Title: "Downloading Show", Season: 1, Episode: 1, SeriesKey: "downloadingshow"}},
	}
	for _, f := range fixtures {
		sib := f.siblings
		if sib == nil {
			sib = []string{path.Base(f.rel)}
		}
		got := Parse(ParseInput{RelPath: f.rel, LibraryType: "mixed", Siblings: sib})
		got.Confidence = ""
		if got != f.want {
			t.Errorf("%s\n got  %+v\n want %+v", f.rel, got, f.want)
		}
	}
}

func TestSeriesMerge(t *testing.T) {
	a := Parse(ParseInput{RelPath: "arrested.development.s02.hdtv.720.rus.eng.novafilm.tv/arrested.development.s02e02.hdtv.720.rus.eng.novafilm.tv.mkv"})
	b := Parse(ParseInput{RelPath: "Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.mkv"})
	if a.SeriesKey != b.SeriesKey || a.SeriesKey == "" {
		t.Fatalf("keys differ: %q %q", a.SeriesKey, b.SeriesKey)
	}
}

func TestLibraryTypes(t *testing.T) {
	p := Parse(ParseInput{RelPath: "Show.Name.S01E01.mkv", LibraryType: "movies"})
	if p.Kind != "movie" {
		t.Fatalf("movies library must not detect episodes: %+v", p)
	}
	p = Parse(ParseInput{RelPath: "Cartoon Collection/Funny Cat.mkv", LibraryType: "shows", Siblings: []string{"Angry Dog.mkv", "Funny Cat.mkv"}})
	if p.Kind != "episode" || p.Episode != 2 || p.Season != 1 || p.Title != "Cartoon Collection" || p.Confidence != "low" {
		t.Fatalf("shows fallback: %+v", p)
	}
}

func TestTagTokens(t *testing.T) {
	for _, tok := range []string{"2160p", "WEB-DL", "x264-GRP", "HEVC", "DDP5ch1", "Atmos-GRP", "2xRus", "S04", "HDR10", "BDRemux", "720", "REPACK", "10bit"} {
		if !isStopTag(tok) {
			t.Errorf("%s should be a stop tag", tok)
		}
	}
	for _, tok := range []string{"Bay", "Saul", "Spider-Man", "Lady", "Wall", "51", "Flux"} {
		if isStopTag(tok) {
			t.Errorf("%s should not be a stop tag", tok)
		}
	}
	if normalizeName("Movie.H.264.DDP5.1.mkv") != "Movie H264 DDP5ch1 mkv" {
		t.Errorf("normalize: %q", normalizeName("Movie.H.264.DDP5.1.mkv"))
	}
}

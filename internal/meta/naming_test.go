package meta

import "testing"

func TestTitleString(t *testing.T) {
	if got := (Title{Name: "Blade Runner", Year: 1982}).String(); got != "Blade Runner (1982)" {
		t.Errorf("String = %q", got)
	}
	if got := (Title{Name: "Unknown Film"}).String(); got != "Unknown Film" {
		t.Errorf("String without a year = %q", got)
	}
}

// The edition tag is always present, so a re-encode coexists in Plex rather
// than replacing what is there.
func TestVideoNameCarriesEdition(t *testing.T) {
	title := Title{Name: "Blade Runner", Year: 1982}
	if got, want := title.VideoName("Archive"), "Blade Runner (1982) {edition-Archive}.mp4"; got != want {
		t.Errorf("VideoName = %q, want %q", got, want)
	}
}

// Plex matches a sidecar to its video by exact stem, and reads .forced/.sdh.
func TestSubtitleName(t *testing.T) {
	title := Title{Name: "Blade Runner", Year: 1982}
	tests := []struct {
		lang        string
		forced, sdh bool
		want        string
	}{
		{"eng", false, false, "Blade Runner (1982) {edition-Archive}.eng.srt"},
		{"eng", true, false, "Blade Runner (1982) {edition-Archive}.eng.forced.srt"},
		{"eng", false, true, "Blade Runner (1982) {edition-Archive}.eng.sdh.srt"},
		{"", false, false, "Blade Runner (1982) {edition-Archive}.und.srt"},
	}
	for _, tc := range tests {
		if got := title.SubtitleName("Archive", tc.lang, tc.forced, tc.sdh); got != tc.want {
			t.Errorf("SubtitleName(%q,%v,%v) = %q, want %q", tc.lang, tc.forced, tc.sdh, got, tc.want)
		}
	}
}

// A sidecar and its video must share a stem exactly, or Plex will not pair them.
func TestSidecarStemMatchesVideo(t *testing.T) {
	title := Title{Name: "Alien: Resurrection", Year: 1997}
	video := title.VideoName("Archive")
	sub := title.SubtitleName("Archive", "eng", false, false)

	stem := video[:len(video)-len(".mp4")]
	if len(sub) <= len(stem) || sub[:len(stem)] != stem {
		t.Errorf("sidecar %q does not share the video's stem %q", sub, stem)
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Alien: Resurrection", "Alien - Resurrection"},
		{"Face/Off", "Face-Off"},
		{"What?", "What"},
		{"Title ", "Title"},
	}
	for _, tc := range tests {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Braces and parentheses carry meaning to Plex and must survive.
func TestSanitizeKeepsPlexSyntax(t *testing.T) {
	got := sanitize("Blade Runner (1982) {edition-Archive}")
	if got != "Blade Runner (1982) {edition-Archive}" {
		t.Errorf("sanitize stripped Plex syntax: %q", got)
	}
}

func TestCleanDiscLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"THE_SHEEP_DETECTIVES", "The Sheep Detectives"},
		{"CRIME_101", "Crime 101"},
		{"IN_THE_GREY_BLU_RAY", "In the Grey"},
		{"THE_MATRIX_DISC_1", "The Matrix"},
		// Shouted, with the format tacked on and an en dash left behind.
		{"THE MANDALORIAN AND GROGU \u2013 BLU-RAY", "The Mandalorian and Grogu"},
		// Small words stay lowercase, except first and last.
		{"KING_OF_THE_HILL", "King of the Hill"},
	}
	for _, tc := range tests {
		if got := CleanDiscLabel(tc.in); got != tc.want {
			t.Errorf("CleanDiscLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

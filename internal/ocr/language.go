package ocr

import "strings"

// language is what a reader needs to know about a track's language: the
// short tag macOS and Windows name their languages by, the name Tesseract
// gives its language data, and a name for saying which one could not be read.
type language struct {
	tag  string // ISO 639-1, the start of a BCP 47 tag such as "en-US"
	tess string // Tesseract's, "+" between several that would each do
	name string
}

// languages maps the ISO 639-2 codes an original uses, in both the bibliographic
// and terminology forms where they differ. A code not here is read with the
// reader's own default language.
var languages = map[string]language{
	"ara": {"ar", "ara", "Arabic"},
	"bul": {"bg", "bul", "Bulgarian"},
	"cat": {"ca", "cat", "Catalan"},
	"ces": {"cs", "ces", "Czech"}, "cze": {"cs", "ces", "Czech"},
	"chi": {"zh", "chi_sim+chi_tra", "Chinese"}, "zho": {"zh", "chi_sim+chi_tra", "Chinese"},
	"dan": {"da", "dan", "Danish"},
	"deu": {"de", "deu", "German"}, "ger": {"de", "deu", "German"},
	"ell": {"el", "ell", "Greek"}, "gre": {"el", "ell", "Greek"},
	"eng": {"en", "eng", "English"},
	"est": {"et", "est", "Estonian"},
	"fin": {"fi", "fin", "Finnish"},
	"fra": {"fr", "fra", "French"}, "fre": {"fr", "fra", "French"},
	"heb": {"he", "heb", "Hebrew"},
	"hin": {"hi", "hin", "Hindi"},
	"hrv": {"hr", "hrv", "Croatian"},
	"hun": {"hu", "hun", "Hungarian"},
	"ice": {"is", "isl", "Icelandic"}, "isl": {"is", "isl", "Icelandic"},
	"ind": {"id", "ind", "Indonesian"},
	"ita": {"it", "ita", "Italian"},
	"jpn": {"ja", "jpn", "Japanese"},
	"kor": {"ko", "kor", "Korean"},
	"lav": {"lv", "lav", "Latvian"},
	"lit": {"lt", "lit", "Lithuanian"},
	"may": {"ms", "msa", "Malay"}, "msa": {"ms", "msa", "Malay"},
	"dut": {"nl", "nld", "Dutch"}, "nld": {"nl", "nld", "Dutch"},
	"nob": {"nb", "nor", "Norwegian"}, "nor": {"nb", "nor", "Norwegian"},
	"pol": {"pl", "pol", "Polish"},
	"por": {"pt", "por", "Portuguese"},
	"rum": {"ro", "ron", "Romanian"}, "ron": {"ro", "ron", "Romanian"},
	"rus": {"ru", "rus", "Russian"},
	"slk": {"sk", "slk", "Slovak"}, "slo": {"sk", "slk", "Slovak"},
	"slv": {"sl", "slv", "Slovenian"},
	"spa": {"es", "spa", "Spanish"},
	"srp": {"sr", "srp", "Serbian"},
	"swe": {"sv", "swe", "Swedish"},
	"tha": {"th", "tha", "Thai"},
	"tur": {"tr", "tur", "Turkish"},
	"ukr": {"uk", "ukr", "Ukrainian"},
	"vie": {"vi", "vie", "Vietnamese"},
}

// languageOf finds a track's language. ok is false for a code not known,
// "und" among them.
func languageOf(code string) (language, bool) {
	l, ok := languages[strings.ToLower(code)]
	return l, ok
}

// Tag is the short tag for a track's language, such as "en" for "eng", for
// naming a sidecar the way Plex reads it. A code not known is kept as it is.
func Tag(code string) string {
	if l, ok := languageOf(code); ok {
		return l.tag
	}
	return code
}

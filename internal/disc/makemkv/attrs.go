package makemkv

// Attribute IDs used by CINFO, TINFO and SINFO records.
//
// The numbering is shared across all three record types; which attributes
// actually appear depends on the record. Only the attributes ARFABIT reads are
// named here — see docs/ARCHITECTURE.md Appendix A for what has been observed
// in practice.
const (
	attrType          = 1  // "Video" / "Audio" / "Subtitles", or "Blu-ray disc"
	attrName          = 2  // disc or title name
	attrLangCode      = 3  // stream language, ISO 639-2 — see the trap below
	attrLangName      = 4  // stream language, human readable
	attrCodecID       = 5  // "V_MPEG4/ISO/AVC", "A_TRUEHD", "S_HDMV/PGS"
	attrCodecShort    = 6  // "Mpeg4", "TrueHD", "PGS"
	attrCodecLong     = 7  // "Mpeg4 AVC High@L4.1", "TrueHD Atmos"
	attrChapterCount  = 8  // title chapter count
	attrDuration      = 9  // "1:49:04"
	attrSizeHuman     = 10 // "31.1 GB"
	attrSizeBytes     = 11 // "33457569792"
	attrBitrate       = 13 // "128 Kb/s"
	attrAudioChannels = 14
	attrSourceFile    = 16 // "00001.mpls" or "00585.m2ts"
	attrSampleRate    = 17
	attrSampleSize    = 18
	attrVideoSize     = 19 // "1920x1080"
	attrAspectRatio   = 20 // "16:9"
	attrFrameRate     = 21 // "23.976 (120000/5005)"
	attrSegmentCount  = 25
	attrSegmentMap    = 26
	attrOutputFile    = 27 // suggested .mkv filename
	attrTreeInfo      = 30 // human summary, e.g. "PGS English  (forced only)"
	attrVolumeName    = 32 // "THE_SHEEP_DETECTIVES"
	attrMkvFlags      = 38 // flag characters, e.g. "d"
	attrMkvFlagsText  = 39 // "Default"
	attrChannelLayout = 40 // "7.1"
)

// Attributes 28 and 29 also hold a language code and name, but they carry the
// *title's* metadata language rather than the stream's. A French subtitle track
// reports attr 3 = "fra" while attr 28 = "eng". Reading 28/29 for a stream
// silently labels every track on every disc as English.
//
// They are deliberately not given constants, so the mistake is hard to make.

// Message codes emitted as MSG records.
//
// Error handling keys on these numbers, never on the rendered English text,
// which changes between versions and is localized.
const (
	msgVersion        = 1005 // version banner
	msgProfileMissing = 1009 // "default profile missing" — harmless, every run
	msgLibreDrive     = 1011 // "Using LibreDrive mode" — raw access engaged
	msgOSAccessMode   = 2010 // degraded access; usually another process holds the drive
	msgFailedToOpen   = 5010 // "Failed to open disc"
)

// noisyMessages are emitted on every successful run and carry no information a
// user would act on. They are dropped before logs reach the UI.
var noisyMessages = map[int]bool{
	msgVersion:        true,
	msgProfileMissing: true,
}

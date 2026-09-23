// Package disc models optical discs and the backends that read them.
//
// Nothing in this package knows about MakeMKV. Backends live in subpackages
// and translate their own output into these types.
package disc

import "time"

// Kind is the physical disc format, which drives profile matching (§8).
type Kind string

const (
	KindDVD     Kind = "dvd"
	KindBluray  Kind = "bluray"
	KindUHD     Kind = "uhd"
	KindAudioCD Kind = "audiocd" // phase 3
	KindUnknown Kind = "unknown"
)

// StreamKind distinguishes the three stream types ARFABIT handles.
type StreamKind string

const (
	StreamVideo    StreamKind = "video"
	StreamAudio    StreamKind = "audio"
	StreamSubtitle StreamKind = "subtitle"
)

// Drive is one optical drive on this node.
type Drive struct {
	Index  int    // backend's drive index
	Name   string // "BD-RE HL-DT-ST BD-RE BU40N 1.03"
	Device string // "/dev/rdisk8"
	Label  string // volume label of the loaded disc, empty when none
	Loaded bool   // a disc is present
}

// Disc is what a scan found.
type Disc struct {
	Kind   Kind
	Name   string // human-readable name, preferred for metadata lookup
	Label  string // raw volume label, e.g. "THE_SHEEP_DETECTIVES"
	Titles []Title
}

// Title is one playable item on the disc.
type Title struct {
	Index      int
	Name       string
	Duration   time.Duration
	SizeBytes  int64
	Chapters   int
	SourceFile string // "00001.mpls" — key to spotting playlist obfuscation
	OutputName string // filename the backend suggests
	Streams    []Stream
}

// Stream is one video, audio or subtitle track within a Title.
type Stream struct {
	Index     int
	Kind      StreamKind
	CodecID   string // "V_MPEG4/ISO/AVC", "A_TRUEHD", "S_HDMV/PGS"
	Codec     string // "TrueHD"
	CodecLong string // "TrueHD Atmos"
	Lang      string // ISO 639-2, from the stream's own attribute
	LangName  string
	Summary   string // backend's human summary

	// Video
	Width, Height int
	AspectRatio   string
	FrameRate     string

	// Audio
	Channels   int
	Layout     string // "7.1"
	SampleRate int
	BitDepth   int

	// Flags
	Default bool

	// Forced is inferred, not reported: no backend exposes a dedicated
	// attribute for it. Treat as a hint and show the user what was inferred
	// rather than asserting it (§10).
	Forced bool
}

// Backend reads discs. MakeMKV is the only implementation today; the interface
// exists so an audio-CD backend can be added without restructuring (§6).
type Backend interface {
	// Name identifies the backend in logs and the UI.
	Name() string

	// Drives lists the optical drives this backend can see.
	Drives() ([]Drive, error)

	// Scan enumerates the titles on the disc in the given drive.
	Scan(driveIndex int) (*Disc, error)
}

package store

import "time"

// A Project is what to make from an Original: its line items, and the
// containers to make them into, one file each.
//
// It is planned against what the Original holds, track by track. A blueprint
// can fill one in, but once filled in it is only line items, and the user can
// change any of them; nothing reads the blueprint again (§8).
type Project struct {
	// Containers are the files to make, one each, from the same line items.
	// "mkv" is the only one today.
	Containers []string `json:"containers"`

	// Edition names the files, as {edition-...}. Blank means none.
	Edition string `json:"edition"`

	// Blueprint records what filled the line items in, if anything. Nothing
	// reads it to decide anything.
	Blueprint string `json:"blueprint,omitempty"`

	// At and Length say which stretch of the Original. A Length of zero means
	// the whole of it.
	At     time.Duration `json:"at"`
	Length time.Duration `json:"length"`

	// Items are the line items. Within a kind, their order is the order the
	// tracks appear in the file, and the first sound and subtitle track are
	// the ones a player starts with.
	Items []Item `json:"items"`
}

// The kinds of line item, which are also the Project's sections.
const (
	KindVideo    = "video"
	KindAudio    = "audio"
	KindSubtitle = "subtitle"
)

// The things a line item can do with its track.
const (
	// ActionCopy keeps the track exactly as it is in the Original.
	ActionCopy = "copy"

	// ActionConvert makes a new track from it.
	ActionConvert = "convert"
)

// Item is one line item: a track from the Original, and what to do with it.
type Item struct {
	Kind   string `json:"kind"`
	Action string `json:"action"`

	// Source is the track's stream index in the Original.
	Source int `json:"source"`

	// What the track is, recorded so it can be shown, and found again in an
	// Original made after the Project was planned: a disc's Project is
	// planned from the scan, before its Original exists, and MakeMKV numbers
	// the Original's tracks its own way.
	Codec    string `json:"codec"`
	Lang     string `json:"lang,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Lossless bool   `json:"lossless,omitempty"`
	Label    string `json:"label"`

	// SourceBitrate is what the track runs at in the Original, in bits per
	// second, when known (Track.Bitrate).
	SourceBitrate int `json:"source_bitrate,omitempty"`

	// Conversion settings, used when Action is ActionConvert.
	//
	// To is the format made: "hevc" for a picture; "flac", "aac" or "eac3"
	// for sound.
	To string `json:"to,omitempty"`

	// CRF and Preset are for a picture.
	CRF    int    `json:"crf,omitempty"`
	Preset string `json:"preset,omitempty"`

	// Bitrate is for lossy sound. OutChannels of 2 makes a stereo downmix;
	// zero keeps every channel.
	Bitrate     string `json:"bitrate,omitempty"`
	OutChannels int    `json:"out_channels,omitempty"`
}

// ItemsOf returns a Project's line items of one kind, in order.
func (p Project) ItemsOf(kind string) []Item {
	var out []Item
	for _, it := range p.Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// Whole reports whether the Project makes something of all of its source,
// rather than of a stretch of it.
func (p Project) Whole() bool { return p.Length <= 0 }

// HasAV reports whether the Project makes a video or audio file: whether it
// has any video or audio line items. Without, it makes subtitle files alone.
func (p Project) HasAV() bool {
	return len(p.ItemsOf(KindVideo))+len(p.ItemsOf(KindAudio)) > 0
}

// Track is one track something holds, described the same way whether it was
// read from an Original by ffprobe or from a disc by MakeMKV, so one recipe and
// one editor serve both. Codecs are named as ffprobe names them.
type Track struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"` // KindVideo, KindAudio or KindSubtitle
	Codec    string `json:"codec"`
	Lang     string `json:"lang,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Lossless bool   `json:"lossless,omitempty"`
	Height   int    `json:"height,omitempty"`
	HDR      bool   `json:"hdr,omitempty"`
	Label    string `json:"label"`

	// Bitrate is what the track runs at, in bits per second, when known.
	// For lossy sound it is the most there is: a conversion to more only
	// makes a bigger file of the same sound.
	Bitrate int `json:"bitrate,omitempty"`

	// Note says what this track costs on the television it is being made
	// for, from what was tested (§4). Empty when it plays directly.
	Note string `json:"note,omitempty"`
}

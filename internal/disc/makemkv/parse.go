package makemkv

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
)

// ScanResult is everything a scan produced: the disc itself, the drives seen
// along the way, and the messages worth keeping.
type ScanResult struct {
	Disc     *disc.Disc
	Drives   []disc.Drive
	Messages []Message
}

// Message is one MSG record, kept for logging and error reporting.
type Message struct {
	Code int
	Text string
}

// ParseScan reads `makemkvcon -r info` output.
//
// It never fails on an unrecognised line. MakeMKV adds record types and
// attributes between versions, and a scan that found titles is useful even if
// part of the output was not understood.
func ParseScan(r io.Reader) (*ScanResult, error) {
	return ParseScanFunc(r, nil)
}

// ParseScanFunc is ParseScan with a callback invoked for each message as it is
// read, so the UI can show progress during a scan that takes minutes.
//
// onMessage may be nil. It is called from the parsing goroutine and should not
// block.
func ParseScanFunc(r io.Reader, onMessage func(Message)) (*ScanResult, error) {
	res := &ScanResult{Disc: &disc.Disc{}}

	// Titles and streams arrive interleaved and out of order, keyed by index,
	// so collect into maps and flatten once at the end.
	titles := map[int]*disc.Title{}
	streams := map[int]map[int]*disc.Stream{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // TreeInfo lines can be long

	for sc.Scan() {
		rec, ok := ParseLine(sc.Text())
		if !ok {
			continue
		}

		switch rec.Type {
		case recMSG:
			code, err := rec.intField(0)
			if err != nil || noisyMessages[code] {
				continue
			}
			m := Message{Code: code, Text: rec.field(3)}
			res.Messages = append(res.Messages, m)
			if onMessage != nil {
				onMessage(m)
			}

		case recDRV:
			if d, ok := parseDrive(rec); ok {
				res.Drives = append(res.Drives, d)
			}

		case recCINFO:
			applyDiscAttr(res.Disc, rec)

		case recTINFO:
			ti, err := rec.intField(0)
			if err != nil {
				continue
			}
			t := titles[ti]
			if t == nil {
				t = &disc.Title{Index: ti}
				titles[ti] = t
			}
			applyTitleAttr(t, rec)

		case recSINFO:
			ti, err := rec.intField(0)
			if err != nil {
				continue
			}
			si, err := rec.intField(1)
			if err != nil {
				continue
			}
			if streams[ti] == nil {
				streams[ti] = map[int]*disc.Stream{}
			}
			s := streams[ti][si]
			if s == nil {
				s = &disc.Stream{Index: si}
				streams[ti][si] = s
			}
			applyStreamAttr(s, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	res.Disc.Titles = flattenTitles(titles, streams)
	return res, nil
}

// MakeMKV's drive states, as reported in the second field of a DRV record.
//
// Sixteen DRV lines are always printed; the ones with no drive attached report
// 256, which is what separates a real drive from a phantom slot.
const (
	driveEmptyClosed = 0
	driveEmptyOpen   = 1
	driveInserted    = 2
	driveLoading     = 3
	driveNone        = 256
	driveUnmounting  = 257
)

// parseDrive converts a DRV record, reporting ok=false for unused slots.
//
// A drive with nothing in it reports no device path and no label, so neither
// can be required: doing so made the drive disappear from the page the moment
// a disc was ejected.
func parseDrive(rec Record) (disc.Drive, bool) {
	index, err := rec.intField(0)
	if err != nil {
		return disc.Drive{}, false
	}

	state, err := rec.intField(1)
	if err != nil || state == driveNone {
		return disc.Drive{}, false
	}

	name := rec.field(4)
	if name == "" {
		// No name and not marked absent: nothing useful to show.
		return disc.Drive{}, false
	}

	drive := disc.Drive{
		Index:  index,
		Name:   name,
		Label:  rec.field(5),
		Device: rec.field(6),
	}

	switch state {
	case driveInserted:
		drive.State = disc.DriveLoaded
		drive.Loaded = true
	case driveEmptyClosed:
		drive.State = disc.DriveEmpty
	case driveEmptyOpen:
		drive.State = disc.DriveOpen
	case driveLoading, driveUnmounting:
		drive.State = disc.DriveLoading
	default:
		drive.State = disc.DriveUnknown
		// An unrecognised state with a label still has a disc in it.
		drive.Loaded = drive.Label != ""
	}

	return drive, true
}

// attr returns the attribute id and value from a CINFO/TINFO/SINFO record.
// idField is the position of the attribute id, which varies by record type.
func attr(rec Record, idField int) (int, string, bool) {
	id, err := rec.intField(idField)
	if err != nil {
		return 0, "", false
	}
	return id, rec.field(idField + 2), true // id, code, value
}

func applyDiscAttr(d *disc.Disc, rec Record) {
	id, val, ok := attr(rec, 0)
	if !ok {
		return
	}
	switch id {
	case attrType:
		d.Kind = kindFromTypeString(val)
	case attrName:
		// Attr 2 is already human readable ("The Sheep Detectives") and is
		// preferred over the volume label for metadata lookup (§11).
		if d.Name == "" {
			d.Name = val
		}
	case attrVolumeName:
		d.Label = val
	}
}

func applyTitleAttr(t *disc.Title, rec Record) {
	id, val, ok := attr(rec, 1)
	if !ok {
		return
	}
	switch id {
	case attrName:
		t.Name = val
	case attrChapterCount:
		t.Chapters, _ = strconv.Atoi(val)
	case attrDuration:
		t.Duration = parseDuration(val)
	case attrSizeBytes:
		t.SizeBytes, _ = strconv.ParseInt(val, 10, 64)
	case attrSourceFile:
		t.SourceFile = val
	case attrOutputFile:
		t.OutputName = val
	}
}

func applyStreamAttr(s *disc.Stream, rec Record) {
	id, val, ok := attr(rec, 2)
	if !ok {
		return
	}
	switch id {
	case attrType:
		s.Kind = streamKindFromTypeString(val)
	case attrLangCode:
		// Attr 3, not 28: see the note in attrs.go.
		s.Lang = val
	case attrLangName:
		s.LangName = val
	case attrCodecID:
		s.CodecID = val
	case attrCodecShort:
		s.Codec = val
	case attrCodecLong:
		s.CodecLong = val
	case attrAudioChannels:
		s.Channels, _ = strconv.Atoi(val)
	case attrChannelLayout:
		s.Layout = val
	case attrSampleRate:
		s.SampleRate, _ = strconv.Atoi(val)
	case attrBitrate:
		s.Bitrate = parseBitrate(val)
	case attrSampleSize:
		s.BitDepth, _ = strconv.Atoi(val)
	case attrVideoSize:
		s.Width, s.Height = parseResolution(val)
	case attrAspectRatio:
		s.AspectRatio = val
	case attrFrameRate:
		s.FrameRate = val
	case attrMkvFlags:
		s.Default = strings.Contains(val, "d")
	case attrTreeInfo:
		s.Summary = val
		s.Forced = looksForced(val)
	}
}

// looksForced infers the forced flag from the backend's summary string.
//
// MakeMKV exposes no attribute for this; the only signal is the text
// "(forced only)" inside the human summary. That is prose, and prose is
// exactly what §15 says not to trust — so this is a hint the Plan shows the
// user, never an assertion made on their behalf.
func looksForced(summary string) bool {
	return strings.Contains(strings.ToLower(summary), "forced")
}

func kindFromTypeString(s string) disc.Kind {
	switch l := strings.ToLower(s); {
	case strings.Contains(l, "uhd"), strings.Contains(l, "ultra hd"):
		return disc.KindUHD
	case strings.Contains(l, "blu-ray"), strings.Contains(l, "bluray"):
		return disc.KindBluray
	case strings.Contains(l, "dvd"):
		return disc.KindDVD
	default:
		return disc.KindUnknown
	}
}

func streamKindFromTypeString(s string) disc.StreamKind {
	switch strings.ToLower(s) {
	case "video":
		return disc.StreamVideo
	case "audio":
		return disc.StreamAudio
	case "subtitles":
		return disc.StreamSubtitle
	default:
		return ""
	}
}

// parseDuration reads MakeMKV's "H:MM:SS" form. An unparseable value yields
// zero, which the main-feature heuristic treats as "no information" rather
// than "instant".
func parseDuration(s string) time.Duration {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
}

func parseResolution(s string) (w, h int) {
	x := strings.IndexAny(s, "xX")
	if x < 0 {
		return 0, 0
	}
	w, _ = strconv.Atoi(strings.TrimSpace(s[:x]))
	h, _ = strconv.Atoi(strings.TrimSpace(s[x+1:]))
	return w, h
}

// flattenTitles turns the index-keyed maps into ordered slices.
func flattenTitles(titles map[int]*disc.Title, streams map[int]map[int]*disc.Stream) []disc.Title {
	if len(titles) == 0 {
		return nil
	}

	out := make([]disc.Title, 0, len(titles))
	for i := 0; i < maxKey(titles)+1; i++ {
		t := titles[i]
		if t == nil {
			continue
		}
		if ss := streams[i]; len(ss) > 0 {
			t.Streams = make([]disc.Stream, 0, len(ss))
			for j := 0; j < maxStreamKey(ss)+1; j++ {
				if s := ss[j]; s != nil {
					t.Streams = append(t.Streams, *s)
				}
			}
		}
		out = append(out, *t)
	}
	return out
}

func maxKey(m map[int]*disc.Title) int {
	max := -1
	for k := range m {
		if k > max {
			max = k
		}
	}
	return max
}

func maxStreamKey(m map[int]*disc.Stream) int {
	max := -1
	for k := range m {
		if k > max {
			max = k
		}
	}
	return max
}

// parseBitrate reads MakeMKV's "640 Kb/s" or "24.5 Mb/s" as bits per second,
// or zero for anything else.
func parseBitrate(val string) int {
	fields := strings.Fields(val)
	if len(fields) != 2 {
		return 0
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(fields[1]) {
	case "kb/s":
		return int(n * 1000)
	case "mb/s":
		return int(n * 1_000_000)
	}
	return 0
}

// Package makemkv parses the output of `makemkvcon -r` and drives it as a
// DiscBackend. See docs/ARCHITECTURE.md Appendix A for the format field guide.
package makemkv

import (
	"fmt"
	"strings"
)

// Record is one parsed line of robot-mode output.
//
// Robot mode emits `TYPE:field,field,...` where strings are double-quoted and
// inner quotes are backslash-escaped. Fields arrive already unquoted.
type Record struct {
	Type   string
	Fields []string
}

// Line prefixes MakeMKV emits. Anything else is ignored rather than treated as
// an error: MakeMKV adds record types between versions, and an unknown line is
// never a reason to abandon a scan.
const (
	recMSG    = "MSG"    // code,flags,argcount,"rendered","format",args...
	recDRV    = "DRV"    // index,visible,?,flags,"name","label","device"
	recTCOUNT = "TCOUNT" // count
	recCINFO  = "CINFO"  // attr,code,"value"
	recTINFO  = "TINFO"  // title,attr,code,"value"
	recSINFO  = "SINFO"  // title,stream,attr,code,"value"
)

// ParseLine splits one line into its type and fields.
//
// Returns ok=false for blank lines and lines with no `TYPE:` prefix, which
// callers should skip.
func ParseLine(line string) (Record, bool) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return Record{}, false
	}

	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return Record{}, false
	}

	typ := line[:colon]
	// A type is always uppercase ASCII. This rejects stray output that happens
	// to contain a colon, such as a bare log line.
	for i := 0; i < len(typ); i++ {
		if typ[i] < 'A' || typ[i] > 'Z' {
			return Record{}, false
		}
	}

	return Record{Type: typ, Fields: splitFields(line[colon+1:])}, true
}

// splitFields splits a comma-separated field list, honouring double-quoted
// strings and backslash escapes.
//
// encoding/csv is not usable here: it treats a doubled quote as an escape,
// whereas MakeMKV uses a backslash.
func splitFields(s string) []string {
	var (
		fields []string
		cur    strings.Builder
		quoted bool
		escape bool
	)

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case escape:
			cur.WriteByte(c)
			escape = false
		case c == '\\' && quoted:
			escape = true
		case c == '"':
			quoted = !quoted
		case c == ',' && !quoted:
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	fields = append(fields, cur.String())

	return fields
}

// field returns the n-th field, or "" when the record is shorter than expected.
//
// Short records are tolerated rather than rejected: MakeMKV omits trailing
// fields in some versions, and a missing optional attribute should not fail a
// whole scan.
func (r Record) field(n int) string {
	if n < 0 || n >= len(r.Fields) {
		return ""
	}
	return r.Fields[n]
}

// intField parses the n-th field as an integer.
func (r Record) intField(n int) (int, error) {
	f := r.field(n)
	if f == "" {
		return 0, fmt.Errorf("field %d missing", n)
	}
	var v int
	if _, err := fmt.Sscanf(f, "%d", &v); err != nil {
		return 0, fmt.Errorf("field %d (%q) is not a number", n, f)
	}
	return v, nil
}

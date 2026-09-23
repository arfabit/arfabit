package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A deliberately small TOML reader.
//
// ARFABIT's configuration is a handful of sections of scalars and string
// lists, so this handles exactly that: [section] headers, key = value pairs,
// strings, integers, floats, booleans, and arrays of strings. It does not
// handle inline tables, arrays of tables, multi-line strings or dates, and
// says so plainly when it meets one rather than guessing.
//
// This exists because the build has no network access for a third-party
// parser. Swapping in a full implementation later means replacing this file.

// document is a parsed TOML file: section name to key to value.
type document map[string]map[string]value

// value is one parsed scalar or list.
type value struct {
	text string
	list []string
	line int
}

// parseTOML reads a configuration file.
func parseTOML(r io.Reader) (document, error) {
	doc := document{"": map[string]value{}}
	section := ""

	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(stripComment(sc.Text()))
		if text == "" {
			continue
		}

		if strings.HasPrefix(text, "[") {
			if !strings.HasSuffix(text, "]") {
				return nil, fmt.Errorf("line %d: section heading is missing its closing bracket", line)
			}
			if strings.HasPrefix(text, "[[") {
				return nil, fmt.Errorf("line %d: ARFABIT's settings files do not use [[table arrays]]", line)
			}
			section = strings.TrimSpace(text[1 : len(text)-1])
			if doc[section] == nil {
				doc[section] = map[string]value{}
			}
			continue
		}

		key, raw, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected a setting like name = value", line)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)

		v, err := parseValue(raw, line)
		if err != nil {
			return nil, err
		}
		doc[section][key] = v
	}

	return doc, sc.Err()
}

// stripComment removes a trailing # comment, ignoring one inside a string.
func stripComment(line string) string {
	var inString bool
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inString = !inString
		case '#':
			if !inString {
				return line[:i]
			}
		}
	}
	return line
}

func parseValue(raw string, line int) (value, error) {
	if strings.HasPrefix(raw, "[") {
		if !strings.HasSuffix(raw, "]") {
			return value{}, fmt.Errorf("line %d: a list must start and end on the same line", line)
		}
		var list []string
		for _, item := range splitList(raw[1 : len(raw)-1]) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			list = append(list, unquote(item))
		}
		return value{list: list, line: line}, nil
	}

	if strings.HasPrefix(raw, "{") {
		return value{}, fmt.Errorf("line %d: ARFABIT's settings files do not use inline tables", line)
	}

	return value{text: unquote(raw), line: line}, nil
}

// splitList splits on commas that are not inside a string.
func splitList(s string) []string {
	var out []string
	var cur strings.Builder
	var inString bool

	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inString = !inString
			cur.WriteByte(c)
		case c == ',' && !inString:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
		return s[1 : len(s)-1]
	}
	return s
}

// lookup finds a value by section and key.
func (d document) lookup(section, key string) (value, bool) {
	if s, ok := d[section]; ok {
		if v, ok := s[key]; ok {
			return v, true
		}
	}
	return value{}, false
}

func (v value) asString() string { return v.text }

func (v value) asInt() (int, error) {
	return strconv.Atoi(v.text)
}

func (v value) asBool() (bool, error) {
	return strconv.ParseBool(v.text)
}

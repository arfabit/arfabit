package pipeline

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/store"
)

// Entry is one line of a job's log.
//
// Lines carry a stable ID so the web view can append without re-rendering,
// which is what lets a filter or a text selection survive an update (§14).
type Entry struct {
	ID    int64       `json:"id"`
	Time  time.Time   `json:"time"`
	Stage store.Stage `json:"stage"`
	Text  string      `json:"text"`

	// Job and Disc say which disc a line belongs to. With several discs going
	// at once a stream of untagged lines is unreadable, which is exactly the
	// thing this project exists to do better.
	Job  string `json:"job"`
	Disc string `json:"disc,omitempty"`

	// Detail is raw output shown only on request, never summarised (§15).
	Detail string `json:"detail,omitempty"`
}

// Log records a job's progress to a file and to anyone watching.
type Log struct {
	mu      sync.Mutex
	file    *os.File
	entries []Entry
	nextID  int64
	watcher func(Entry)

	job  string
	disc string
}

// Describe says which disc this log belongs to, so its lines can be told apart
// from another disc's.
func (l *Log) Describe(job, disc string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.job, l.disc = job, disc

	// Lines written before the disc had a name are labelled retrospectively,
	// since they belong to it just as much.
	for i := range l.entries {
		l.entries[i].Job = job
		l.entries[i].Disc = disc
	}
}

// maxKeptEntries bounds what is held for the web view. The file on disk keeps
// everything.
const maxKeptEntries = 5000

// NewLog opens a job log, creating the file.
func NewLog(path string, watcher func(Entry)) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Log{file: f, watcher: watcher}, nil
}

// Printf records a line.
func (l *Log) Printf(stage store.Stage, format string, args ...any) {
	l.add(stage, fmt.Sprintf(format, args...), "")
}

// Detail records a line with raw output attached.
//
// The raw text is kept whole: a caller must never trim it to look tidy, since
// it is the only trustworthy account of what happened.
func (l *Log) Detail(stage store.Stage, text, detail string) {
	l.add(stage, text, detail)
}

func (l *Log) add(stage store.Stage, text, detail string) {
	l.mu.Lock()

	l.nextID++
	e := Entry{
		ID:     l.nextID,
		Time:   time.Now(),
		Stage:  stage,
		Text:   text,
		Detail: detail,
		Job:    l.job,
		Disc:   l.disc,
	}

	l.entries = append(l.entries, e)
	if len(l.entries) > maxKeptEntries {
		l.entries = l.entries[len(l.entries)-maxKeptEntries:]
	}

	if l.file != nil {
		line := fmt.Sprintf("%s  %-7s  %s\n", e.Time.Format("15:04:05"), stage, text)
		if detail != "" {
			line += indent(detail) + "\n"
		}
		_, _ = l.file.WriteString(line)
	}

	watcher := l.watcher
	l.mu.Unlock()

	// Called outside the lock so a slow watcher cannot stall the pipeline.
	if watcher != nil {
		watcher(e)
	}
}

// Entries returns the lines kept in memory, oldest first.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Close finishes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "        | " + line
	}
	return strings.Join(lines, "\n")
}

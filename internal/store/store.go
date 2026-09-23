// Package store keeps ARFABIT's state as plain files.
//
// Files are the source of truth. There is no database: settings and job
// records must survive on a NAS, be readable by a person, and be safe when
// several machines share one folder. The rule that makes that work is that a
// node only ever writes inside its own directory, so no locking is needed
// (§6).
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store is the on-disk state for one node.
type Store struct {
	// Root is the data directory, which may be on a NAS.
	Root string

	// NodeID scopes every write. A node never writes outside its own folder.
	NodeID string
}

// New opens a store, creating the directories it needs.
func New(root, nodeID string) (*Store, error) {
	if root == "" || nodeID == "" {
		return nil, fmt.Errorf("store: a data directory and node id are both needed")
	}
	s := &Store{Root: root, NodeID: nodeID}
	if err := os.MkdirAll(s.jobsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.logsDir(), 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) nodeDir() string { return filepath.Join(s.Root, "nodes", s.NodeID) }
func (s *Store) jobsDir() string { return filepath.Join(s.nodeDir(), "jobs") }
func (s *Store) logsDir() string { return filepath.Join(s.nodeDir(), "log") }

// JobPath is where a job record lives.
func (s *Store) JobPath(id string) string {
	return filepath.Join(s.jobsDir(), id+".json")
}

// LogPath is where a job's plain-text log lives.
//
// The log is plain text on purpose: it can be opened, searched and sent to
// somebody without ARFABIT running.
func (s *Store) LogPath(id string) string {
	return filepath.Join(s.logsDir(), id+".txt")
}

// SaveJob writes a job record.
func (s *Store) SaveJob(j *Job) error {
	if j.ID == "" {
		return fmt.Errorf("store: job has no id")
	}
	j.Node = s.NodeID
	j.Updated = time.Now()

	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.JobPath(j.ID), append(data, '\n'))
}

// LoadJob reads one job record.
func (s *Store) LoadJob(id string) (*Job, error) {
	data, err := os.ReadFile(s.JobPath(id))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("job %s could not be read: %w", id, err)
	}
	return &j, nil
}

// Jobs lists this node's jobs, newest first.
func (s *Store) Jobs() ([]*Job, error) {
	return jobsIn(s.jobsDir())
}

// AllJobs lists every node's jobs, newest first.
//
// This is how one machine shows what the others are doing: it reads their
// directories without ever writing to them.
func (s *Store) AllJobs() ([]*Job, error) {
	nodes, err := os.ReadDir(filepath.Join(s.Root, "nodes"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var all []*Job
	for _, n := range nodes {
		if !n.IsDir() {
			continue
		}
		jobs, err := jobsIn(filepath.Join(s.Root, "nodes", n.Name(), "jobs"))
		if err != nil {
			// One unreadable node must not hide the others.
			continue
		}
		all = append(all, jobs...)
	}

	sortJobs(all)
	return all, nil
}

func jobsIn(dir string) ([]*Job, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var jobs []*Job
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			// A half-written or hand-edited record must not stop the list.
			continue
		}
		jobs = append(jobs, &j)
	}

	sortJobs(jobs)
	return jobs, nil
}

func sortJobs(jobs []*Job) {
	sort.SliceStable(jobs, func(a, b int) bool {
		return jobs[a].Started.After(jobs[b].Started)
	})
}

// AppendLibrary records a delivered title in the shared library index.
//
// The index is append-only JSON Lines so that it stays readable, survives a
// partial write, and can be rebuilt from the job records if it is ever lost.
func (s *Store) AppendLibrary(entry LibraryEntry) error {
	dir := filepath.Join(s.Root, "library")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(data, '\n'))
	return err
}

// writeAtomic writes a file by writing a temporary one and renaming it.
//
// Rename is atomic on every filesystem worth using, so a reader sees either
// the old file or the new one and never a half-written record — which matters
// when the folder is on a NAS and another machine may be reading.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Any failure from here on leaves the original file untouched.
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

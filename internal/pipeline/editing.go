package pipeline

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// Each step of a disc's Plan stays open to change until the step starts
// (§2). The copy can start the moment the disc is read, since the scan
// already knows every track:
//
//   - the film's name, and which subtitle tracks to read, until the copy is
//     finished, when the original is named and its subtitles begin;
//   - the film to make from it, and whether to make one, until the film
//     starts, which is when it leaves the line for the processor.
//
// Changes are made under the runner's lock, which is also held where each
// step takes what the Plan says, so a change is either in the step or
// refused, never half in.

// Editing says what on a disc's Plan can still be changed.
type Editing struct {
	// Started is set once the disc is being copied.
	Started bool `json:"started"`

	// Name and Read are the film's name and the subtitles to read with the
	// copy; Film is the film made from it.
	Name bool `json:"name"`
	Read bool `json:"read"`
	Film bool `json:"film"`
}

// Editable returns the disc whose Plan can be changed: one waiting to be
// started, or the one last started while any of it can still change.
func (r *Runner) Editable() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending != nil && r.pending.State == store.StateWaiting {
		return r.pending
	}
	if j := r.planned; j != nil && r.editingLocked(j) != (Editing{Started: true}) {
		return j
	}
	return nil
}

// EditingOf says what on a disc's Plan can still be changed.
func (r *Runner) EditingOf(job *Job) Editing {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.editingLocked(job)
}

func (r *Runner) editingLocked(job *Job) Editing {
	if job == nil || job.Plan == nil {
		return Editing{}
	}
	if job.State == store.StateWaiting {
		return Editing{Name: true, Read: true, Film: true}
	}
	copying := job.State == store.StateRunning && !job.copied
	e := Editing{Started: true, Name: copying, Read: copying}
	switch film := job.film; {
	case film != nil:
		e.Film = film.State == store.StateRunning && film.Stage == store.StageQueued
	case copying:
		// No film yet, but one can be added while the copy runs.
		e.Film = true
	}
	return e
}

// errStarted says a step has begun, so what it was given is what it has.
func errStarted(what string) error {
	return errors.New(what + " has started, so it can no longer be changed")
}

// SetTitle names the film on the Plan being changed. A disc being copied
// takes the name when the copy is finished, and so does its film.
func (r *Runner) SetTitle(name string, year int) (*Job, error) {
	job := r.Editable()
	if job == nil {
		return nil, errors.New("there is no disc waiting")
	}
	r.mu.Lock()
	if !r.editingLocked(job).Name {
		r.mu.Unlock()
		return nil, errors.New("the copy is finished, so its name is settled")
	}
	job.Title, job.Year = name, year
	film := job.film
	if film != nil {
		film.Title, film.Year = name, year
		film.File = meta.Title{Name: name, Year: year}.VideoName(film.Project.Edition)
	}
	r.mu.Unlock()

	r.save(job)
	if film != nil {
		r.save(film)
	}
	return job, nil
}

// SetRead chooses which subtitle tracks are read with the copy, by the
// disc's number for each, keeping the disc's order.
func (r *Runner) SetRead(change map[int]bool) error {
	job := r.Editable()
	if job == nil {
		return errors.New("there is no disc waiting")
	}
	r.mu.Lock()
	if !r.editingLocked(job).Read {
		r.mu.Unlock()
		return errStarted("reading the subtitles")
	}
	var read []int
	for _, t := range job.Plan.Tracks {
		chosen, changed := change[t.Index]
		if !changed {
			chosen = slices.Contains(job.Plan.Read, t.Index)
		}
		if chosen && t.Kind == store.KindSubtitle {
			read = append(read, t.Index)
		}
	}
	job.Plan.Read = read
	r.mu.Unlock()

	r.save(job)
	return nil
}

// UpdateProject replaces the film on the Plan with one the user has
// changed, and the film waiting for its original with it.
func (r *Runner) UpdateProject(pkg store.Project) error {
	job := r.Editable()
	if job == nil || job.Plan == nil {
		return errors.New("there is no disc waiting")
	}
	r.mu.Lock()
	if !r.editingLocked(job).Film {
		r.mu.Unlock()
		return errStarted("the film")
	}
	film := job.film
	if film != nil {
		mine := pkg
		mine.Items = slices.Clone(pkg.Items)
		mine.Containers = slices.Clone(pkg.Containers)
		// Checked as it would be at Start, and pointed at the original if
		// that exists already.
		if err := checkProject(&mine, r.OCR != nil); err != nil {
			r.mu.Unlock()
			return err
		}
		if film.bound != nil {
			if err := bindToOriginal(&mine, film.discTracks, film.bound); err != nil {
				r.mu.Unlock()
				return err
			}
		}
		film.Project = &mine
		film.File = meta.Title{Name: film.Title, Year: film.Year}.VideoName(mine.Edition)
	}
	job.Plan.Project = &pkg
	job.Plan.Edition = pkg.Edition
	r.mu.Unlock()

	r.Reestimate(job)
	r.save(job)
	if film != nil {
		r.save(film)
	}
	return nil
}

// SetEdition changes the edition of the film on the Plan.
func (r *Runner) SetEdition(edition string) error {
	job := r.Editable()
	if job == nil || job.Plan == nil {
		return errors.New("there is no disc waiting")
	}
	r.mu.Lock()
	pkg := store.Project{Containers: []string{"mkv"}}
	if job.Plan.Project != nil {
		pkg = *job.Plan.Project
		pkg.Items = slices.Clone(pkg.Items)
	}
	r.mu.Unlock()
	pkg.Edition = strings.TrimSpace(edition)
	return r.UpdateProject(pkg)
}

// SetConvert says whether to make a film from the disc. While the disc is
// being copied, turning it on adds the film to the queue, waiting for the
// copy; turning it off takes a film that has not started off the queue.
func (r *Runner) SetConvert(parent context.Context, on bool) error {
	job := r.Editable()
	if job == nil || job.Plan == nil {
		return errors.New("there is no disc waiting")
	}
	r.mu.Lock()
	e := r.editingLocked(job)
	film := job.film
	if !e.Film || (on && film == nil && !e.Name) {
		r.mu.Unlock()
		return errStarted("the film")
	}
	if on && film == nil && e.Started {
		if job.Plan.Project == nil {
			r.mu.Unlock()
			return errors.New("there is no film planned")
		}
		check := *job.Plan.Project
		check.Items = slices.Clone(check.Items)
		if err := checkProject(&check, r.OCR != nil); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	job.Plan.Convert = on
	if !on {
		job.film = nil
	}
	r.mu.Unlock()

	switch {
	case !on && film != nil:
		r.stopJob(film)
	case on && film == nil && e.Started:
		r.followRip(parent, job)
	}
	r.Reestimate(job)
	r.save(job)
	return nil
}

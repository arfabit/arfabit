package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/store"
)

// A transcode planned with a disc is a job of its own, waiting behind the rip,
// and a rip that does not finish takes it off the queue and says why.
//
// Only the unfinished case is tried here. A rip that finishes ejects the disc,
// and a test has no business opening somebody's drive.
func TestTranscodeWaitsForItsRipAndGoesWithIt(t *testing.T) {
	r := runnerWithFolders(t)
	// A MakeMKV that is not there: the rip cannot finish.
	r.Backend = &makemkv.Backend{Path: filepath.Join(t.TempDir(), "no-makemkvcon")}

	rip := &Job{Job: store.NewJob("rip")}
	rip.State = store.StateWaiting
	rip.Title, rip.Year = "In the Grey", 2026
	rip.Plan = &store.Plan{Convert: true, Edition: "Archive", Project: &store.Project{
		Edition: "Archive",
		Items:   []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}},
	}}
	rip.Space = Space{Fits: true}
	rip.Log, _ = NewLog(r.Store.LogPath(rip.ID), nil)
	r.SetCurrentForTest(rip)

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var follow *store.Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := r.Store.Jobs()
		for _, j := range jobs {
			if j.From == rip.ID {
				follow = j
			}
		}
		if follow != nil && follow.State == store.StateStopped {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if follow == nil {
		t.Fatal("no package job was made for the rip")
	}
	if follow.Kind != store.KindConvert || follow.Project == nil || follow.Project.Edition != "Archive" {
		t.Errorf("the package did not carry the Plan's package: %+v", follow)
	}
	if follow.State != store.StateStopped || !strings.Contains(follow.Note, "was not copied") {
		t.Errorf("the transcode was left as %s: %q", follow.State, follow.Note)
	}
	if Resumable(follow) {
		t.Error("a transcode with no original was offered to start again")
	}
	waitUntilIdle(t, r)
}

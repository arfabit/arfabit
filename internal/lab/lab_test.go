package lab

import (
	"testing"
	"time"
)

func TestCompareExtrapolatesToTheWholeFilm(t *testing.T) {
	clips := []Clip{
		{Name: "crf20", Size: 100_000_000, Took: time.Minute},
		{Name: "crf24", Size: 50_000_000, Took: 50 * time.Second},
	}

	got := Compare(clips, 30*time.Second, 2*time.Hour)
	if len(got.Clips) != 2 {
		t.Fatalf("got %d rows, want 2", len(got.Clips))
	}

	// Smallest first.
	if got.Clips[0].Clip.Name != "crf24" {
		t.Errorf("rows are not smallest first: %s", got.Clips[0].Clip.Name)
	}

	// Thirty seconds scaled to two hours is 240 times.
	if want := int64(50_000_000 * 240); got.Clips[0].Whole != want {
		t.Errorf("Whole = %d, want %d", got.Clips[0].Whole, want)
	}
	if want := 240 * 50 * time.Second; got.Clips[0].EncodeTime != want {
		t.Errorf("EncodeTime = %v, want %v", got.Clips[0].EncodeTime, want)
	}

	// The best is 100%, and everything else is measured against it.
	if got.Clips[0].SizeShare != 100 {
		t.Errorf("the smallest result is %.1f%%, want 100", got.Clips[0].SizeShare)
	}
	if got.Clips[1].SizeShare != 50 {
		t.Errorf("twice the size should read 50%%, got %.1f%%", got.Clips[1].SizeShare)
	}
}

func TestCompareKeepsFailedSettingsInTheTable(t *testing.T) {
	clips := []Clip{
		{Name: "good", Size: 10_000_000, Took: time.Minute},
		{Name: "broken", Problem: "ffmpeg said no"},
	}

	got := Compare(clips, 30*time.Second, time.Hour)
	if len(got.Clips) != 2 {
		t.Fatalf("got %d rows, want both", len(got.Clips))
	}

	var broken *Row
	for i := range got.Clips {
		if got.Clips[i].Clip.Name == "broken" {
			broken = &got.Clips[i]
		}
	}
	if broken == nil {
		t.Fatal("the failed setting vanished from the table")
	}
	if broken.Clip.Problem == "" {
		t.Error("the failed setting lost its explanation")
	}
}

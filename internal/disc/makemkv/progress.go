package makemkv

import "strings"

// Progress records emitted during a rip.
//
// MakeMKV reports two nested operations: the current step (reading one title)
// and the overall job. Names arrive in separate records from the values, so
// both are tracked and combined.
const (
	recPRGC = "PRGC" // code,id,"name" — current operation
	recPRGT = "PRGT" // code,id,"name" — total operation
	recPRGV = "PRGV" // current,total,max
)

// Progress is one progress update during a rip.
type Progress struct {
	// Operation is the current step, e.g. "Saving to MKV file".
	Operation string

	// Title is the overall job description MakeMKV reports.
	Title string

	// Current and Total are fractions of Max. MakeMKV reports progress
	// against an arbitrary scale rather than bytes or seconds.
	Current, Total, Max int
}

// CurrentPercent is progress through the current step, 0-100.
func (p Progress) CurrentPercent() float64 {
	if p.Max <= 0 {
		return 0
	}
	return float64(p.Current) / float64(p.Max) * 100
}

// TotalPercent is progress through the whole job, 0-100.
func (p Progress) TotalPercent() float64 {
	if p.Max <= 0 {
		return 0
	}
	return float64(p.Total) / float64(p.Max) * 100
}

// progressTracker accumulates progress records, which arrive separately:
// PRGC and PRGT carry names, PRGV carries the numbers.
type progressTracker struct {
	operation string
	title     string
}

// apply folds one record in, returning a Progress and true when the record
// completed an update worth reporting.
//
// Only PRGV produces an update: the name records set context for the values
// that follow.
func (t *progressTracker) apply(rec Record) (Progress, bool) {
	switch rec.Type {
	case recPRGC:
		t.operation = strings.TrimSpace(rec.field(2))
	case recPRGT:
		t.title = strings.TrimSpace(rec.field(2))
	case recPRGV:
		cur, err1 := rec.intField(0)
		tot, err2 := rec.intField(1)
		max, err3 := rec.intField(2)
		if err1 != nil || err2 != nil || err3 != nil {
			return Progress{}, false
		}
		return Progress{
			Operation: t.operation,
			Title:     t.title,
			Current:   cur,
			Total:     tot,
			Max:       max,
		}, true
	}
	return Progress{}, false
}

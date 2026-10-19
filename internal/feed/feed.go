// Package feed holds the live events of the running Job for its viewers.
package feed

import (
	"context"
	"errors"
	"time"
)

// ErrBehind ends a viewer that read events slower than the Job made them.
var ErrBehind = errors.New("viewer fell behind")

// Event is one line of a Job's live view. A Bound event starts or ends a
// stage, or a step of a Verify gate, and outlives its stage.
type Event struct {
	Time  time.Time `json:"time"`
	Stage string    `json:"stage"`
	Text  string    `json:"text"`
	Bound bool      `json:"bound,omitempty"`
}

// Feed is the events of one Job: the bounds of its earlier stages, and its
// current stage from the start.
type Feed struct{}

func New() *Feed { return &Feed{} }

// Bound adds a line that starts or ends a stage; a new stage name drops the
// earlier stage's inner lines.
func (f *Feed) Bound(stage, text string) { panic("not implemented") }

// Line adds a line inside the current stage.
func (f *Feed) Line(stage, text string) { panic("not implemented") }

// Close ends every viewer once it has read every event.
func (f *Feed) Close() { panic("not implemented") }

// Follow sends the events so far, then each new one, until the Feed closes,
// ctx ends, send fails, or the viewer falls behind with ErrBehind.
func (f *Feed) Follow(ctx context.Context, send func(Event) error) error {
	return errors.New("not implemented")
}

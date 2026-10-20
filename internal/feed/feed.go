// Package feed holds the live events of the running Job for its viewers.
package feed

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrBehind ends a viewer that read events slower than the Job made them.
var ErrBehind = errors.New("viewer fell behind")

const (
	maxText  = 300
	maxStage = 2000
	// viewerBuffer is how many events a viewer may lag before it is ended.
	viewerBuffer = 1024
)

// Event is one line of a Job's live view. A Bound event starts or ends a
// stage, or a step of a Verify gate, and outlives its stage. Tool names the
// tool of a tool call, whose Text is its input; Sub marks a subagent's line.
type Event struct {
	Time  time.Time `json:"time"`
	Stage string    `json:"stage"`
	Text  string    `json:"text"`
	Bound bool      `json:"bound,omitempty"`
	Tool  string    `json:"tool,omitempty"`
	Sub   bool      `json:"sub,omitempty"`
}

// Line is the event as one line of text: the tool and its input, or the
// text, indented for a subagent.
func (e Event) Line() string {
	line := e.Text
	if e.Tool != "" {
		line = strings.TrimSpace(e.Tool + " " + e.Text)
	}
	if e.Sub {
		return "  " + line
	}
	return line
}

// Step is one tool call or text message of Claude inside a stage. Tool is
// empty for a text message.
type Step struct {
	Tool string
	Text string
	Sub  bool
}

// Feed is the events of one Job: the bounds of its earlier stages, and its
// current stage from the start.
type Feed struct {
	mu      sync.Mutex
	past    []Event
	current []Event
	viewers map[*viewer]struct{}
	closed  bool
}

type viewer struct {
	events chan Event
	behind bool
}

func New() *Feed { return &Feed{viewers: map[*viewer]struct{}{}} }

// Bound adds a line that starts or ends a stage; a new stage name drops the
// earlier stage's inner lines.
func (f *Feed) Bound(stage, text string) { f.add(Event{Stage: stage, Text: text, Bound: true}) }

// Line adds a line inside the current stage.
func (f *Feed) Line(stage, text string) { f.add(Event{Stage: stage, Text: text}) }

// Step adds Claude's tool call or text message inside the current stage.
func (f *Feed) Step(stage string, s Step) {
	f.add(Event{Stage: stage, Text: s.Text, Tool: s.Tool, Sub: s.Sub})
}

// Cut shortens text to the length an event holds, ending a cut text in "…".
func Cut(text string) string {
	if r := []rune(text); len(r) > maxText {
		return string(r[:maxText-1]) + "…"
	}
	return text
}

func (f *Feed) add(e Event) {
	e.Time = time.Now()
	e.Text = Cut(e.Text)
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.Bound && len(f.current) > 0 && f.current[0].Stage != e.Stage {
		for _, c := range f.current {
			if c.Bound {
				f.past = append(f.past, c)
			}
		}
		f.current = nil
	}
	f.current = append(f.current, e)
	if len(f.current) > maxStage {
		i := slices.IndexFunc(f.current, func(c Event) bool { return !c.Bound })
		f.current = slices.Delete(f.current, i, i+1)
	}
	for v := range f.viewers {
		select {
		case v.events <- e:
		default:
			v.behind = true
			delete(f.viewers, v)
			close(v.events)
		}
	}
}

// Close ends every viewer once it has read every event.
func (f *Feed) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for v := range f.viewers {
		delete(f.viewers, v)
		close(v.events)
	}
}

// Follow sends the events so far, then each new one, until the Feed closes,
// ctx ends, send fails, or the viewer falls behind with ErrBehind.
func (f *Feed) Follow(ctx context.Context, send func(Event) error) error {
	f.mu.Lock()
	replay := slices.Concat(f.past, f.current)
	v := &viewer{events: make(chan Event, viewerBuffer)}
	if f.closed {
		close(v.events)
	} else {
		f.viewers[v] = struct{}{}
	}
	f.mu.Unlock()
	defer f.leave(v)

	for _, e := range replay {
		if err := send(e); err != nil {
			return err
		}
	}
	for {
		select {
		case e, ok := <-v.events:
			if !ok {
				f.mu.Lock()
				behind := v.behind
				f.mu.Unlock()
				if behind {
					return ErrBehind
				}
				return nil
			}
			if err := send(e); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (f *Feed) leave(v *viewer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.viewers[v]; ok {
		delete(f.viewers, v)
		close(v.events)
	}
}

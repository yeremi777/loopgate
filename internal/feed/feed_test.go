package feed

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFollowReplaysEarlierStagesAsBoundsAndTheCurrentStageFromItsStart(t *testing.T) {
	f := New()
	f.Bound("implement 1", "start")
	f.Line("implement 1", "Read job.go")
	f.Bound("implement 1", "done $0.10")
	f.Bound("verify 1", "verify: go test ./...")
	f.Bound("verify 1", "FAIL verify command exited 1")
	f.Line("verify 1", "│ --- FAIL: TestX")
	f.Bound("implement 2", "start")
	f.Step("implement 2", Step{Tool: "Edit", Text: "job.go"})
	f.Step("implement 2", Step{Text: "Now the test.", Sub: true})
	f.Close()

	got := follow(t, f)
	want := []string{
		"implement 1 start", "implement 1 done $0.10",
		"verify 1 verify: go test ./...", "verify 1 FAIL verify command exited 1",
		"implement 2 start", "implement 2 Edit job.go", "implement 2   Now the test.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("replay %q, want %q", got, want)
	}
}

func TestFollowSendsNewEventsUntilTheFeedCloses(t *testing.T) {
	f := New()
	f.Bound("drill", "start")
	sent := make(chan string)
	done := make(chan error)
	go func() {
		done <- f.Follow(context.Background(), func(e Event) error {
			if e.Time.IsZero() {
				t.Errorf("event %q has no time", e.Text)
			}
			sent <- e.Stage + " " + e.Text
			return nil
		})
	}()
	if got := <-sent; got != "drill start" {
		t.Fatalf("first event %q", got)
	}
	f.Line("drill", "Read issue")
	if got := <-sent; got != "drill Read issue" {
		t.Fatalf("live event %q", got)
	}
	f.Close()
	if err := <-done; err != nil {
		t.Fatalf("follow after close: %v", err)
	}
}

func TestEventsAreCutAndTheCurrentStageKeepsItsLastLines(t *testing.T) {
	f := New()
	f.Bound("implement 1", "start")
	f.Line("implement 1", strings.Repeat("é", 500))
	for i := range 2100 {
		f.Line("implement 1", strconv.Itoa(i))
	}
	f.Close()

	var events []Event
	if err := f.Follow(context.Background(), func(e Event) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2000 || events[0].Text != "start" || events[1].Text != "101" || events[1999].Text != "2099" {
		t.Fatalf("kept %d events from %q to %q", len(events), events[1].Text, events[len(events)-1].Text)
	}

	g := New()
	g.Line("implement 1", strings.Repeat("é", 500))
	g.Close()
	got := follow(t, g)
	if text := strings.TrimPrefix(got[0], "implement 1 "); len([]rune(text)) != 300 || !strings.HasSuffix(text, "…") {
		t.Fatalf("long line kept as %d characters: %q", len([]rune(text)), text)
	}
}

func TestAViewerThatFallsBehindIsEndedAndTheJobNeverWaits(t *testing.T) {
	f := New()
	f.Bound("implement 1", "start")
	release := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- f.Follow(context.Background(), func(Event) error { <-release; return nil })
	}()
	added := make(chan struct{})
	go func() {
		for i := range 5000 {
			f.Line("implement 1", strconv.Itoa(i))
		}
		close(added)
	}()
	select {
	case <-added:
	case <-time.After(5 * time.Second):
		t.Fatal("adding events waited for a stuck viewer")
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrBehind) {
		t.Fatalf("follow of a stuck viewer: %v, want ErrBehind", err)
	}
}

func TestFollowEndsWithItsContext(t *testing.T) {
	f := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- f.Follow(ctx, func(Event) error { return nil }) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow after cancel: %v", err)
	}
}

func follow(t *testing.T, f *Feed) []string {
	t.Helper()
	var got []string
	if err := f.Follow(context.Background(), func(e Event) error { got = append(got, e.Stage+" "+e.Line()); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStepEventsNameTheToolAndTheSubagentForThePage(t *testing.T) {
	f := New()
	f.Bound("implement 1", "start")
	f.Step("implement 1", Step{Tool: "Bash", Text: "go test ./...", Sub: true})
	f.Step("implement 1", Step{Text: "Done."})
	f.Close()
	var got []string
	if err := f.Follow(context.Background(), func(e Event) error {
		data, err := json.Marshal(Event{Stage: e.Stage, Text: e.Text, Bound: e.Bound, Tool: e.Tool, Sub: e.Sub})
		got = append(got, string(data))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"time":"0001-01-01T00:00:00Z","stage":"implement 1","text":"start","bound":true}`,
		`{"time":"0001-01-01T00:00:00Z","stage":"implement 1","text":"go test ./...","tool":"Bash","sub":true}`,
		`{"time":"0001-01-01T00:00:00Z","stage":"implement 1","text":"Done."}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events %q, want %q", got, want)
	}
}

func TestLineKeepsTheTextOfAnEventThatIsNoToolCall(t *testing.T) {
	for _, e := range []Event{
		{Stage: "verify 1", Text: "│ x\r"},
		{Stage: "verify 1", Text: "│ "},
		{Stage: "verify 1", Text: "verify: go test\n", Bound: true},
	} {
		if got := e.Line(); got != e.Text {
			t.Errorf("line of %q is %q", e.Text, got)
		}
	}
	if got := (Event{Text: "  Done.  ", Sub: true}).Line(); got != "    Done.  " {
		t.Errorf("subagent text line %q", got)
	}
	if got := (Event{Tool: "TodoWrite"}).Line(); got != "TodoWrite" {
		t.Errorf("tool call without input %q", got)
	}
}

package github

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/job"
)

// recordingGH writes a gh stand-in that appends each call's args and stdin to log and prints out.
func recordingGH(t *testing.T, out string) (gh, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	gh = filepath.Join(dir, "gh")
	script := "#!/bin/sh\n{ printf '%s|' \"$@\"; echo; if [ \"$1 $2\" = 'issue comment' ]; then cat; echo; fi; } >> " + log + "\ncat <<'EOF'\n" + out + "\nEOF\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh, log
}

func TestListIssuesOfLabel(t *testing.T) {
	gh, log := recordingGH(t, `[{"number":12,"title":"B","body":"b"},{"number":3,"title":"A","body":"a"}]`)
	got, err := Issues{GH: gh}.List(context.Background(), "me/app", "agent")
	if err != nil {
		t.Fatal(err)
	}
	want := []job.Issue{{Number: 3, Title: "A", Body: "a"}, {Number: 12, Title: "B", Body: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v sorted by number", got, want)
	}
	if args := read(t, log); args != "issue|list|-R|me/app|--label|agent|--state|open|--limit|100|--json|number,title,body|\n" {
		t.Fatalf("gh args %q", args)
	}
}

func TestCommentAndLabel(t *testing.T) {
	gh, log := recordingGH(t, "")
	iss := Issues{GH: gh}
	if err := iss.Comment(context.Background(), "me/app", 7, "which db?"); err != nil {
		t.Fatal(err)
	}
	if err := iss.Label(context.Background(), "me/app", 7, "needs-drill"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(read(t, log)), "\n")
	want := []string{
		"issue|comment|7|-R|me/app|--body-file|-|",
		"which db?",
		"label|create|needs-drill|-R|me/app|--force|--description|loopgate needs a human decision before it can build this|",
		"issue|edit|7|-R|me/app|--add-label|needs-drill|",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("gh calls\n%q\nwant\n%q", lines, want)
	}
}

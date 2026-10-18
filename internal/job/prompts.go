package job

import (
	"fmt"
	"strings"
)

const unattended = `You run unattended inside loopgate. Nobody can answer a question during this run.
- Work only on the current branch in this directory. Commit every change; leave no uncommitted files.
- Never push, merge, or open a pull request. loopgate ships the work after its own checks.
- Settle every decision that a run can settle. List each choice you made on your own as a Flag in the summary.
- Report status blocked, with one question, only when the work cannot be undone, cannot be done safely (secrets, accounts, production systems, data deletion or migration, CI or Claude Code configuration), or has two or more very different readings.
- Write the summary as the tldr skill says.`

func implementPrompt(w Work, progress string) string {
	var b strings.Builder
	switch w := w.(type) {
	case Spec:
		fmt.Fprintf(&b, "/implement %s\n\n", w.Path)
	case Task:
		fmt.Fprintf(&b, "/implement %s\n\n", w.Text)
	}
	b.WriteString(unattended)
	if progress != "" {
		b.WriteString("\n\nEarlier runs on this branch did not pass loopgate's checks. Fix what this log shows:\n\n")
		b.WriteString(progress)
	}
	return b.String()
}

func rubricPrompt(defaultBranch string) string {
	return fmt.Sprintf(`/rubric defect pass, steps 1 to 3 only, on the diff of this branch against origin/%s.

You run unattended inside loopgate as a reviewer. Do not edit, stage, or commit any file.
Report status revise when any finding is act on, and put every act-on finding in the summary with its file, line, and the fix it needs.
Report status done when act on is empty.
Write the summary as the tldr skill says.`, defaultBranch)
}

func landPrompt(w Work) string {
	head := "/land PR"
	if s, ok := w.(Spec); ok {
		head = "/land PR, keep spec " + s.Path
	}
	return head + `

You run unattended inside loopgate, with no network. Nobody can answer a question during this run.
- Do not run git push or gh. loopgate pushes this branch and opens the draft pull request after its own checks.
- Change only Markdown files, and commit every change you make. loopgate refuses to ship any other change after the gate.
- Report status done with the summary set to the pull request text: the title on the first line, a blank line, then the body.
- Report status blocked, with one question, only when landing needs a decision that only the human can make.`
}

// parsePR splits land's summary into the PR title line and its body.
func parsePR(text string) (PR, error) {
	title, body, _ := strings.Cut(strings.TrimSpace(text), "\n")
	title = strings.TrimSpace(strings.TrimLeft(title, "# "))
	if title == "" {
		return PR{}, fmt.Errorf("land wrote no pull request title")
	}
	return PR{Title: title, Body: strings.TrimSpace(body)}, nil
}

// report opens the PR body with what loopgate checked, so a reviewer sees
// the evidence and the changed tests before the description.
func report(j Job, out Outcome, cost float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## loopgate\n\n")
	fmt.Fprintf(&b, "- Job %d of loop `%s` passed `%s` and a rubric review at %s.\n", j.ID, j.Loop.Name, j.Loop.Verify, out.Gated)
	fmt.Fprintf(&b, "- Cost so far: $%.2f.\n", cost)
	if len(out.TestChanges) == 0 {
		b.WriteString("- Changed or deleted tests: none.\n")
	} else {
		b.WriteString("- Changed or deleted tests, check that none was weakened:\n")
		for _, c := range out.TestChanges {
			fmt.Fprintf(&b, "  - `%s` (%s)\n", c.Path, c.Kind)
		}
	}
	fmt.Fprintf(&b, "\n### Implement summary\n\n%s", out.Summary)
	return b.String()
}

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

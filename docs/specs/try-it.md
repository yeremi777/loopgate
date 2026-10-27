# try-it

## Goal
Every done Job carries a Test guide, and `loopctl try <job>` names the Job's own worktree, with its needed local files in place, so that a human can open it, start the app and test the change by hand before accepting its draft PR.

## Non-goals
- loopgate never starts the user's app, never runs a database command, and never runs the Setup command for `try`.
- No second worktree: `try` uses the Job's own worktree.
- No copied file's name or contents reach a PR, a log or `loopctl` output.
- `try` takes only a done Job, and a done Job is never taken over, so no stage runs where `try` copied files.
- The Test guide is guidance, not a gate: it never decides pass or fail, and a missing one never stops a Job.
- No change to the Verify gate, the rubric stage, or `loopctl logs`.
- Depends on `docs/specs/job-ids.md`: ids are as that spec settles.

## Acceptance criteria
- AC-1: The land stage's result has an optional `try` field, and its prompt asks for the Test guide there as Markdown of at most 15 lines that a human reads on opening the Job's worktree root: the commands that start the app, then one line for each endpoint, screen or command the branch adds or changes, saying what is called or opened and what is expected, starting from the Spec's `Demo:` lines when the Job has a Spec. The Test guide names environment variables and never their values, holds no token, password or other secret, has no command that drops or resets data, and marks a migration or seed as the human's choice. The field stays empty when the branch adds nothing a human can run.
- AC-2: loopd stores the Test guide on the Job, cut to 4000 characters ending in `…`; a land result without one stores it empty.
- AC-3: The PR report has a `### Try it` section after its bullets and before the Drill section when the Job has a Test guide: the Test guide verbatim, then ``The worktree path is printed by `loopctl try <id>`.`` The section is absent when the Test guide is empty.
- AC-4: `loopctl try <job>` on a done Job copies each regular file of the Loop's checkout that the checkout's `.worktreeinclude` matches, in gitignore syntax, and that git ignores, to the same relative path in the Job's worktree, creating parent directories and overwriting an earlier copy.
- AC-5: `try` prints `worktree: <path>` with the home written as `~`, then, when the Job has a Test guide, a blank line and the Test guide with control characters replaced. It prints no other line, and with no Test guide it prints the `worktree:` line alone.
- AC-6: After `try`, the Job's worktree has the same commit and `git status --porcelain` prints nothing.
- AC-7: `try` refuses a Job that is not done, naming its state and a remedy: `loopctl logs -f <id>` for a queued or running Job, `loopctl handback <id>` for a manual one, `loopctl takeover <id>` for a Blocked, failed or exhausted Job with a worktree, and `loopctl rm <id>` for one that never got a worktree. It fails naming the Loop and `loopctl add <loop.yaml>` when the Job's Loop was removed, and naming the path, with the home written as `~`, and `loopctl rm <id>` when the Job's worktree is gone. It copies nothing when the Job's branch tracks a file it would copy, naming the file and the checkout's `.worktreeinclude`, or when the branch neither tracks nor ignores one, naming the file and the checkout's `info/exclude`.
- AC-8: `takeover` of a done Job fails naming `loopctl try <id>`, and of a Job that never got a worktree naming `loopctl rm <id>`.
- AC-9: `loopctl help` has a `loopctl try <job>` line, the README's commands table has a row for trying a done Job that names `.worktreeinclude`, `docs/specs/loopgate.md` AC-5 lists `try <job>`, and the loopgate skill names `loopctl try <job>` for testing a done Job.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/runner/ -run TestStageTryField` (AC-1)
- `go test ./internal/job/ -run TestLandPromptAsksForTheTestGuideInTry` (AC-1)
- `go test ./internal/job/ -run 'TestReportTryIt|TestReportWithoutATestGuide|TestTestGuideCut'` (AC-2, AC-3)
- `go test ./internal/daemon/ -run 'TestTry|TestTakeoverDoneNamesTry|TestTakeoverOfAJobWithoutAWorktreeNamesRm'` (AC-4, AC-6, AC-7, AC-8)
- `go test ./e2e/ -run 'TestTry|TestHelp'` (AC-5, AC-9)
- `grep -n 'try <job>\|worktreeinclude' README.md cmd/loopctl/main.go docs/specs/loopgate.md skills/loopgate/SKILL.md` (AC-9)
- Demo: a Job from a Spec with a `Demo:` line ends done; its PR shows `### Try it` with the Test guide; `loopctl try <prefix>` copies the `.worktreeinclude` files and prints the worktree path and the guide; starting the app from that worktree as the guide says serves the changed endpoints.

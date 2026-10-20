# track

## Goal
`loopd` serves a live tracking page for every Job on one localhost port, and `loopctl track <job>` opens a Job's page in a browser tab: a state-first card with a stage stepper, the current stage's tool calls and messages in Claude Code's style, and a table of every stage.

## Non-goals
- No address but `127.0.0.1`, no tunnel, and no page reachable from another machine; loopgate never sends a Job's events off the Mac.
- No change to how `loopctl` commands reach `loopd`: they keep the unix socket, and the web listener serves only the tracking pages.
- No state change from a page: tracking never stops, pauses or slows a Job.
- Tool calls and Claude's text stay in memory only and end with the Job; only stage rows are written to disk.
- No tool result in the feed: a tool call's dot does not show success or failure.
- No change to `loopctl logs` or `loopctl logs -f` output.
- No web font, CDN, or asset from outside `loopd`'s web listener.
- Depends on `docs/specs/job-ids.md`: ids are as that spec settles.

## Acceptance criteria
- AC-1: `loopd` serves the tracking pages on `127.0.0.1` at a port it picks free on its first start and keeps in `$LOOPD_HOME`; a later start reuses that port, and when it is taken `loopd` picks a free one, keeps it, logs it, and starts anyway.
- AC-2: `loopd` creates a token of at least 128 random bits on its first start and keeps it in `$LOOPD_HOME` with mode `0600`; every request without that token, or with a `Host` other than `127.0.0.1:<port>`, gets 403; the listener answers only `GET`, and its responses carry a Content-Security-Policy that lets a page load and connect only to the listener.
- AC-3: `loopctl track <job>` resolves the Job, prints `tracking job <id> at <url>`, opens the URL with `open` in a new tab, and exits 0; when `open` fails it prints the URL and exits 0; a second `loopctl track` on another Job opens that Job's page on the same port.
- AC-4: `/` lists each Loop with its Jobs as `loopctl list` does, each Job linking to its page at `/jobs/<id>`.
- AC-5: `loopd` stores a Job's start and end time, and a Job's page has them and its Loop's `max_iterations`.
- AC-6: `loopd` appends each stage row to `$LOOPD_HOME/jobs/<id>/stages.jsonl` as it ends: the stage name, start and end time, status, cost, and for a Verify gate its setup and verify commands with pass or fail, the exit code, and on failure the last 20 lines of its output; `loopctl rm` deletes the file with the Job's directory.
- AC-7: The page's card shows the state as a 48px icon, an uppercase label in the state's color and a headline, with the card tinted in that color: running names the current stage (`Drilling`, `Implementing, run N of M`, `Running verify N`, `Reviewing, rubric N`, `Landing`, `Opening the PR`), queued says `Queued`, done says `Draft PR opened` with an `Open PR #<n>` link and `loopctl try <id>`, Blocked says `Waiting for your answer` with the question, failed says `Job failed` with the error, exhausted says `Out of attempts` with the summary, and manual says `Taken over` with `loopctl handback <id>`.
- AC-8: The card shows Elapsed, Cost and Run (`N of M`), and a stepper of Drill, Implement, Verify, Rubric, Land and PR, each step done, current, failed, Blocked or pending, with its run count or its last failed gate under it, from the stage rows.
- AC-9: While the Job runs, the current stage's events show live as Claude Code shows them: a dot per entry, a tool call as `Tool(input)`, Claude's text as a line of its own, a subagent's calls nested under its task with an elbow, the time faint on the right, and the newest entry in the accent color.
- AC-10: The stages table lists every stage row with its status, stage, start time, duration and cost, and a failed Verify gate's row expands to its commands and output; the running stage is the last row while it runs.
- AC-11: When the Job ends, an open page moves to the final state without a reload; a page opened on an ended Job, or after `loopd` restarted, shows the card, stepper and stages table from the stored rows, without a live section.
- AC-12: When `loopd` stops, an open page says `loopd stopped`; after `loopd` starts again, reloading the same URL shows the page.
- AC-13: All text from a Job (tool input, Claude's text, gate output, question, error, summary, work) is inserted into a page as text, never as markup.
- AC-14: The pages follow `DESIGN.md`, read at a glance, and a Job's state is the first thing the eye lands on: judged by `rubric`.
- AC-15: `loopctl help` has a `loopctl track <job>` line, the README's commands table has a row for tracking a Job in the browser, `docs/specs/loopgate.md` AC-5 lists `track <job>`, the loopgate skill names `loopctl track <job>`, and `ARCHITECTURE.md` names the web listener and keeps the invariant that a viewer only reads.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./... -run 'TestTrackPort|TestTrackToken|TestTrackHost|TestTrackOnlyGet|TestTrackCSP'` (AC-1, AC-2)
- `go test ./internal/store/ -run 'TestJobTimes'` (AC-5)
- `go test ./internal/job/ -run 'TestStageRows'` (AC-6)
- `go test ./e2e/ -run 'TestTrack|TestHelp'` (AC-3, AC-4, AC-11, AC-12, AC-15)
- `grep -n 'track <job>' README.md cmd/loopctl/main.go docs/specs/loopgate.md skills/loopgate/SKILL.md` (AC-15)
- `rubric` (AC-7, AC-8, AC-9, AC-10, AC-13, AC-14)
- Demo: with a Loop added, `loopctl run <loop> --spec <path>` and `loopctl track <job>` open a tab that shows the stages and tool calls live and the done card with the PR link at the end; `loopctl track` on a second Job opens a second tab on the same port; reloading both after `loopctl install` restarts `loopd` shows them again.

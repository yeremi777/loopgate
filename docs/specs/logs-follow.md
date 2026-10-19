# logs-follow

## Goal
`loopctl logs -f <job>` shows a running Job live, stage by stage and tool call by tool call, and exits on its own when the Job ends.

## Non-goals
- No event written to disk: the live events of a Job live only in `loopd`'s memory while the Job runs, and its PR or Blocked question stays the lasting record.
- No change to plain `loopctl logs <job>`, to `progress.md`, or to the loopgate skill.
- No state change from the viewer: following a Job never stops, pauses, or slows it.
- No live view of a Trial gate.
- No line-by-line stream of setup or verify command output.

## Acceptance criteria
- AC-1: Each stage runs `claude -p` with `--output-format stream-json --verbose` in place of `--output-format json`, and its status, question, summary and `total_cost_usd` come from the final `result` line; a stage whose output has no `result` line fails with an error.
- AC-2: While a Job runs, `loopd` keeps its events in memory: a start and an end line for each stage (`drill`, `implement N`, `rubric N`, `land`), the end line carrying the reported status and the stage's cost; for each Verify gate a line when its setup and verify commands start, and an end line with pass or fail and the exit code, followed on failure by the last 20 lines of the gate's output; a `publish` line with the draft PR's URL; and inside a stage one line per tool call, naming the tool and its `file_path`, `command`, `pattern`, `path`, `url` or `description` input, and one line per assistant text message, its first non-empty line, with subagent lines indented and the `StructuredOutput` call left out.
- AC-3: Each event holds at most 300 characters, the current stage keeps at most its last 2000 events, earlier stages keep only their start and end lines, and the events are dropped when the Job ends.
- AC-4: `loopctl logs -f <job>` prints what `loopctl logs <job>` prints, then the running Job's earlier stages as their start and end lines, the current stage from its start, and each new event as it happens, each as `HH:MM:SS <stage>  <text>`.
- AC-5: `loopctl logs -f <job>` exits 0 when the Job reaches done, blocked, exhausted, failed or manual, after it prints the final state with the PR link, the question or the error; for a Job that is not queued or running it prints the snapshot and the final state and exits 0 at once; a queued Job is followed from when it starts.
- AC-6: Ending `loopctl logs -f` leaves the Job running to its outcome, and the events endpoint is a `GET` that changes no state.
- AC-7: A Job never waits for a viewer: a viewer that falls behind is ended with `viewer fell behind` and a non-zero exit, and a viewer whose `loopd` stops prints `loopd stopped` and exits non-zero.
- AC-8: On a terminal, `loopctl logs -f` cuts each line to the terminal width, ending a cut line in `…`; piped output is not cut.
- AC-9: `README.md` names `loopctl logs -f <job>`, and `docs/specs/loopgate.md` AC-5 lists `logs -f <job>` and AC-7 names `--output-format stream-json --verbose`.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/runner/ -run 'TestStage|TestRunRejectsBadResults'` (AC-1, AC-2 tool and text lines)
- `go test ./internal/job/ -run TestEvents` (AC-2 boundaries and gate lines)
- `go test ./internal/feed/` (AC-3, AC-7 viewer fell behind)
- `go test ./internal/daemon/ -run TestFollow` (AC-5, AC-6)
- `go test ./e2e/ -run TestLogsFollow` (AC-4, AC-5, AC-7 loopd stopped, AC-8)
- `grep -n 'logs -f' README.md docs/specs/loopgate.md` (AC-9)
- Demo: with a Loop added, `loopctl run <loop> --spec <path>` and then `loopctl logs -f <job>` print the stages and tool lines live and exit at the draft PR.

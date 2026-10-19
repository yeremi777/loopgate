# list-jobs

## Goal
`loopctl list` shows every id that another `loopctl` command takes, grouped by Loop: each Loop, and the id of each of its Jobs in progress and of the newest Jobs that ended, in a layout that reads at a glance.

## Non-goals
- No change to the store's schema, and no migration.
- No new `loopctl` command, and no change to any command's arguments.
- No change to which Jobs exist or how long they are kept.

## Acceptance criteria
- AC-1: `loopctl list` prints one block per Loop, sorted by name and separated by a blank line: a line with the Loop's name, its state (`active` or `paused`) and its trigger, the Loop names padded to one width; an indented `checkout:` line with the checkout path, the user's home written as `~`; then the Loop's Jobs as an indented table with the columns `JOB`, `STATE`, `WORK` and `RESULT`, newest first, or an indented `no jobs`.
- AC-2: The Jobs shown are every Job in progress (queued, running, Blocked or manual) and the 5 newest Jobs that ended done, failed or exhausted. A shown Job whose Loop was removed appears in a block for that Loop with the state `removed`.
- AC-3: `WORK` is the Job's stored work (`issue <n> <title>`, `spec <path>` or `task <text>`); `RESULT` is a done Job's PR link, a Blocked Job's question, a failed Job's error, an exhausted Job's summary, `taken over` for a manual Job, and empty for a queued or running Job.
- AC-4: The `WORK` and `RESULT` cells show only their first line, at most 60 characters, a cut cell ending in `…`, with control characters replaced.
- AC-5: When no Loop and no Job exists, `loopctl list` prints `no loops; add one with loopctl add <loop.yaml>`.
- AC-6: On a terminal, Loop names are bold, table headers are dim, and each state is colored: `active` and `done` green; `paused`, `queued` and `running` cyan; `Blocked` and `manual` yellow; `failed`, `exhausted` and `removed` red. Piped output, and output with `NO_COLOR` set to a non-empty value, has no escape codes; colors never change a column's alignment.
- AC-7: A Loop is named `loop` everywhere a user reads it: `<loop>` in `loopctl help` and in the README and in `docs/specs/loopgate.md` AC-5.
- AC-8: The `list` line of `loopctl help` and the README name the job ids that `list` shows; the README says that `<issue>` is the GitHub issue number, which an issue Job's work shows; the README's install check describes the line of AC-5, and the loopgate skill finds a Loop by its `checkout:` line.
- AC-9: When `loopd` answers a request with a status and a body that are not its own, as a `loopd` older than `loopctl` does, `loopctl` fails with that status and names `loopctl install`, and `list` prints nothing.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `GOOS=linux go vet ./cmd/loopctl` (the terminal check builds beyond macOS)
- `go test ./internal/store/ -run TestJobs` (AC-2)
- `go test ./internal/ipc/ -run TestFollowNamesTheStatus` (AC-9)
- `go test ./e2e/ -run 'TestList|TestInstall|TestHelp'` (AC-1, AC-2, AC-3, AC-4, AC-5, AC-6, AC-7, AC-9)
- `grep -n 'job id\|<issue>. is the GitHub\|no loops' README.md cmd/loopctl/main.go docs/specs/loopgate.md skills/loopgate/SKILL.md` (AC-8)
- `rubric` (AC-1 and AC-6 read at a glance)
- Demo: with a Loop that has a done and a Blocked Job, `loopctl list` on a terminal shows both in the Loop's block, colored, and piped shows the same text without escape codes.

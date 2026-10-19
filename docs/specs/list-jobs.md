# list-jobs

## Goal
`loopctl list` shows every id that another `loopctl` command takes: each Loop, and the id of each Job in progress and of the newest Jobs that ended.

## Non-goals
- No change to the store's schema, and no migration.
- No new `loopctl` command, and no change to any command's arguments.
- No change to which Jobs exist or how long they are kept.

## Acceptance criteria
- AC-1: When at least one Job exists, `loopctl list` prints, after the Loops table and a blank line, a Jobs table with the columns `JOB`, `LOOP`, `STATE`, `WORK` and `PR / QUESTION`, newest Job first.
- AC-2: The Jobs table holds every Job in progress (queued, running, Blocked or manual) and the 5 newest Jobs that ended done, failed or exhausted.
- AC-3: `WORK` is the Job's stored work (`issue <n> <title>`, `spec <path>` or `task <text>`); `PR / QUESTION` is a done Job's PR link or a Blocked Job's question, and empty for any other Job.
- AC-4: The `WORK` and `PR / QUESTION` cells show only their first line, at most 60 characters, a cut cell ending in `…`, with control characters replaced.
- AC-5: When no Job exists, `loopctl list` prints only the Loops table.
- AC-6: A Loop is named `loop` everywhere a user reads it: `<loop>` in `loopctl help`, `LOOP` in both `list` tables, and `<loop>` in the README and in `docs/specs/loopgate.md` AC-5.
- AC-7: The `list` line of `loopctl help` and the README name the job ids that `list` shows, and the README says that `<issue>` is the GitHub issue number, which an issue Job's work shows.
- AC-8: When `loopd` answers a request with a status and a body that are not its own, as a `loopd` older than `loopctl` does, `loopctl` fails with that status and names `loopctl install`, and `list` prints nothing.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/store/ -run TestJobs` (AC-2)
- `go test ./internal/ipc/ -run TestFollowNamesTheStatus` (AC-8)
- `go test ./e2e/ -run 'TestList|TestInstall|TestHelp'` (AC-1, AC-3, AC-4, AC-5, AC-6, AC-8)
- `grep -n 'job id\|<issue>. is the GitHub' README.md cmd/loopctl/main.go docs/specs/loopgate.md` (AC-7)
- Demo: after `loopctl run <loop>`, `loopctl list` shows the Job's id, and `loopctl logs <id>` opens it.

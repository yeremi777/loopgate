# list-jobs

## Goal
`loopctl list` shows every id that another `loopctl` command takes: each Loop's name, and the id of each Job the user can still act on.

## Non-goals
- No change to the store's schema, and no migration.
- No new `loopctl` command, and no change to any other command's arguments or output.
- No change to which Jobs exist or how long they are kept.

## Acceptance criteria
- AC-1: When at least one Job exists, `loopctl list` prints, after the Loops table and a blank line, a Jobs table with the columns `JOB`, `LOOP`, `STATE`, `WORK` and `PR / QUESTION`, newest Job first.
- AC-2: The Jobs table holds every Job that is not done, and the 5 newest done Jobs.
- AC-3: `WORK` is the Job's stored work (`issue <n> <title>`, `spec <path>` or `task <text>`); `PR / QUESTION` is a done Job's PR link or a Blocked Job's question, and empty for any other Job.
- AC-4: The `WORK` and `PR / QUESTION` cells show only their first line, at most 60 characters, a cut cell ending in `…`, with control characters replaced.
- AC-5: When no Job exists, `loopctl list` prints only the Loops table.
- AC-6: The `list` line of `loopctl help` and the README name the job ids that `list` shows, and `docs/specs/loopgate.md` AC-5 says that `list` shows them.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/store/ -run TestJobs` (AC-2)
- `go test ./e2e/ -run 'TestListShowsJobs|TestInstall'` (AC-1, AC-3, AC-4, AC-5)
- `grep -n 'job ids' README.md cmd/loopctl/main.go docs/specs/loopgate.md` (AC-6)
- Demo: after `loopctl run <loop>`, `loopctl list` shows the Job's id, and `loopctl logs <id>` opens it.

# sibling-worktrees

## Goal
A new Job's worktree is `<checkout>-worktrees/<id>`, beside its Loop's checkout, so a human reaches it without looking in `$LOOPD_HOME`.

## Non-goals
- A Job's logs, stage rows and progress log stay in `$LOOPD_HOME/jobs/<id>/`; only the worktree moves.
- A Trial gate's worktree stays under `$LOOPD_HOME/trials/`.
- A Job's stored worktree path is never rewritten.
- No setting for the folder name or place.
- Depends on `docs/specs/job-ids.md` for ids and `docs/specs/try-it.md` for `loopctl try`.

## Acceptance criteria
- AC-1: A Job's worktree is created at `<checkout>-worktrees/<id>`, with `<checkout>` as the Loop's `checkout` setting and `<id>` as the Job's id, and a start that fails leaves no `<checkout>-worktrees` folder behind.
- AC-2: `loopctl run` and `loopctl try` print that path, with the home written as `~` by `try`.
- AC-3: A Job's directory is `$LOOPD_HOME/jobs/<id>/`, and `logs`, `track`, `takeover` and `handback` read and write its progress log and stage rows there; a Job whose stored worktree is directly in `$LOOPD_HOME/jobs/<n>/` has that directory.
- AC-4: `loopctl rm <job>` removes the Job's worktree and its directory.
- AC-5: `loopctl rm <job>` removes `<checkout>-worktrees` when no other worktree is left in it.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/daemon/ -run 'TestRunCarriesSpecJob|TestRunThatFailsToStart|TestRemove|TestOldJobDir|TestSiblingWorktree'` (AC-1, AC-3, AC-4, AC-5)
- `go test ./e2e/ -run 'TestTry|TestPrefix|TestRemove'` (AC-2, AC-4, AC-5)
- `grep -n 'worktrees' README.md` (the `LOOPD_HOME` and `rm` rows)
- Demo: a Job runs on a checkout at `~/code/app`; `loopctl try <id>` prints `worktree: ~/code/app-worktrees/<id>`; `cd` there and the app starts as the guide says; `loopctl rm <id>` removes `~/code/app-worktrees` when it was the only Job.

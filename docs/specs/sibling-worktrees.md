# sibling-worktrees

## Goal
A new Job's worktree is `<checkout>-worktrees/<id>`, beside its Loop's checkout, so a human reaches it without looking in `$LOOPD_HOME`.

## Non-goals
- A Job's logs, stage rows and progress log stay in `$LOOPD_HOME/jobs/<id>/`; only the worktree moves.
- A Trial gate's worktree stays under `$LOOPD_HOME/trials/`.
- No existing Job's worktree is moved: an old Job keeps the worktree stored for it.
- No setting for the folder name or place.
- Depends on `docs/specs/job-ids.md` for ids and `docs/specs/try-it.md` for `loopctl try`.

## Acceptance criteria
- AC-1: A new Job's worktree is `<checkout>-worktrees/<id>`, with `<checkout>` as the Loop's `checkout` setting and `<id>` as the Job's id, and the folder is created when it is missing. `loopctl run` and `loopctl try` print that path, with the home written as `~` by `try`.
- AC-2: A new Job's directory stays `$LOOPD_HOME/jobs/<id>/`, and `logs`, `track`, `takeover` and `handback` read and write its progress log and stage rows there; an old Job's directory is still the parent of its stored worktree.
- AC-3: `loopctl rm <job>` removes the Job's worktree and its directory, and removes `<checkout>-worktrees` when no other worktree is left in it.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/daemon/ -run 'TestRunCarriesSpecJob|TestRemove|TestOldJobDir|TestSiblingWorktree'` (AC-1, AC-2, AC-3)
- `go test ./e2e/ -run 'TestTry|TestTakeover|TestRemove'` (AC-1, AC-2, AC-3)
- Demo: a Job runs on a checkout at `~/code/app`; `loopctl try <id>` prints `worktree: ~/code/app-worktrees/<id>`; `cd` there and the app starts as the guide says; `loopctl rm <id>` removes `~/code/app-worktrees` when it was the only Job.

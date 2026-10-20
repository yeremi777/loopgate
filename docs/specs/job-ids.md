# job-ids

## Goal
A Job's id is a random 12-character hex string that is never reused, every `loopctl` command takes it or any unique prefix of it, and `loopctl rm <job>` removes an ended Job's worktree, directory and row.

## Non-goals
- No id is ever given to a second Job, and no id is shortened or recycled when Jobs pile up.
- No automatic removal: a Job's worktree stays until `loopctl rm`.
- No change to a remote branch, a PR, or an issue: `rm` never deletes a remote branch, and an old Job's pushed branch name and PR text keep its integer id.
- No existing job directory or worktree is renamed or moved.
- No `loopctl try` and no test guide; `docs/specs/try-it.md` holds them.

## Acceptance criteria
- AC-1: A new Job's id is 12 lowercase hex characters from `crypto/rand`; an id already stored is never given to a new Job.
- AC-2: Every `<job>` argument of `loopctl` (`logs`, `logs -f`, `takeover`, `handback`, `rm`) takes a Job's full id or any unique prefix of it, matched in lowercase; a prefix that matches several Jobs fails with `job prefix <p> matches <id>, <id>`, and one that matches none fails as not found.
- AC-3: A new Job's branch is `loop/<loop>-<tag>-<id>`, and its directory is `$LOOPD_HOME/jobs/<id>/`.
- AC-4: Opening a state database whose Jobs have integer ids gives each Job a new id as AC-1 says, in one transaction, keeps every other stored field and the Jobs' creation order, and logs `job <n> is now <id>` for each; opening it again changes nothing.
- AC-5: A Job's directory is found from its stored worktree path, so an old Job keeps `jobs/<n>/`: `logs` shows its progress log, and `takeover` and `handback` work on it.
- AC-6: `loopctl list` and every other read that orders Jobs orders them newest first by creation, not by id.
- AC-7: When a Job ends, loopd stores the commit its worktree's branch points at.
- AC-8: `loopctl rm <job>` refuses a queued, running or manual Job, naming its state; for a done, failed, exhausted or Blocked Job it removes the worktree from the checkout's worktrees, its directory, and its row, and prints the PR link when the Job has one.
- AC-9: `rm` deletes the Job's local branch only when the branch points at the commit stored at the Job's end; otherwise it keeps the branch and prints `kept branch <branch>: it has commits the job did not end at`, and a Job with no stored end commit keeps its branch with the same line.
- AC-10: `rm` of a Job whose directory was deleted by hand still prunes the worktree and removes the row.
- AC-11: `loopctl help` has one `loopctl logs [-f] <job>` line and a `loopctl rm <job>` line; the README's commands table has an `rm` row; `docs/specs/loopgate.md` AC-5 lists `logs [-f] <job>` and `rm <job>`; the README and the loopgate skill say job id where they say job N or job 3.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./internal/store/ -run 'TestNewID|TestConvertIntegerIDs|TestJobsOrder|TestResolvePrefix'` (AC-1, AC-2, AC-4, AC-6)
- `go test ./internal/daemon/ -run 'TestRemove|TestOldJobDir|TestEndCommit'` (AC-5, AC-7, AC-8, AC-9, AC-10)
- `go test ./e2e/ -run 'TestHelp|TestRemove|TestPrefix'` (AC-2, AC-3, AC-8, AC-11)
- `grep -n 'logs \[-f\] <job>\|rm <job>' README.md cmd/loopctl/main.go docs/specs/loopgate.md` (AC-11)
- `grep -n 'job N\|job 3' README.md skills/loopgate/SKILL.md` prints nothing (AC-11)
- Demo: after upgrading on a `~/.loopd` with integer Jobs, `loopd.log` shows `job 1 is now <id>`, `loopctl logs <prefix>` shows that Job's progress, and `loopctl rm <prefix>` removes `~/.loopd/jobs/1/` and prints its PR link.

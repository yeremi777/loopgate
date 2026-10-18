# loopgate

## Goal
A local macOS service (`loopd`) and CLI (`loopctl`) that run Claude Code with the user's own `drill`, `implement` and `land` skills on GitHub issues, timers, or a handed-off Spec, gate each Job on a verify command's exit code, and stop at a draft PR that a human reviews, with Takeover and Handback between the loop and the manual harness at any time.

## Non-goals
- No merge of any kind, no force-push, no push to the default branch, and no change of a PR from draft to ready.
- No VPS, webhooks, web UI, chat bots, or Linux/Windows service install.
- No orchestration of subagents by `loopd`; subagents come only from the skills inside a Claude Code session.
- No stored tokens or credentials; GitHub access goes through the user's `gh` login.
- No database operations or migrations performed by a Job.
- No copy of the user's private skill definitions, company details, `.env`, or `*.db` in the repo or its history.

## Acceptance criteria

### Config, store, IPC
- AC-1: A Loop file that lacks `verify`, `bounds.max_iterations`, or `bounds.timeout`, or that has an unknown field, fails to load with an error that names the field.
- AC-2: A Loop file has exactly one trigger: `github` (`repo`, `label`, `every`) or `timer` (`every` or `cron`); a timer Loop has exactly one of `task` or `spec`.
- AC-3: A second `loopd` on the same `~/.loopd` exits non-zero and names the running pid; a lock left by a dead pid is taken over.
- AC-4: Loops, Jobs, Job states, and handled issue numbers survive a `loopd` restart.
- AC-5: `loopctl list | add <file> | remove <name> | pause | resume | stop <name> | run <name> [--spec <path>] | logs <job> | takeover <job> | handback <job> | requeue <name> <issue> | focus github <owner/repo> --label <label>` reach `loopd` over `~/.loopd/loopd.sock`; with `loopd` down, `loopctl` exits non-zero with `loopd not running`.
- AC-6: `loopd` refuses to start, with a message that names `gh auth login`, when `gh auth status` fails.

### Runner
- AC-7: Each stage runs `claude -p` in the Job worktree with `--permission-mode auto`, `--permission-prompts none`, a `--json-schema` for `{status: done|blocked, question, summary}`, and `--settings` with deny rules for `gh pr merge`, `gh pr ready`, `git merge`, `git push --force`, and a push to the default branch.
- AC-8: The child environment holds only allowlisted variables; a variable outside the allowlist, set in `loopd`'s environment, is absent in the child.
- AC-9: On timeout, `stop`, or Takeover, the child's whole process group ends, including grandchildren.
- AC-10: Each Job runs in its own worktree on branch `loop/<loop>-<issue|timestamp>`, cut from `origin/<default>`; uncommitted changes in the main checkout are absent from it.
- AC-11: `run --spec <path>` copies the Spec into the worktree at the same path.

### Verify gate and bounds
- AC-12: A Job reaches `land` only when the Loop's verify command exits 0 in its worktree; a stage that reports `done` while the verify command fails does not pass.
- AC-13: After a failed Verify gate, the next `implement` session is new and its prompt names `~/.loopd/jobs/<id>/progress.md`, which holds each earlier iteration's verify output.
- AC-14: A Job that fails the Verify gate `max_iterations` times, or exceeds `timeout`, ends as `exhausted` with no PR.
- AC-15: A stage that reports `blocked` ends the Job as Blocked with its question stored, and no later stage runs.
- AC-16: A diff that touches a path matching the Loop's `protected` globs makes the Job Blocked before `land`.
- AC-17: The PR report lists each file that matches the Loop's `tests` globs and that the diff deleted or changed.

### Land
- AC-18: `land` runs as `/land PR, keep spec`; afterwards `loopd` reads the PR through `gh pr view --json isDraft,headRefName`, and a PR that is not a draft is returned to draft and its Job is marked `failed`, as is a PR whose head is the default branch.
- AC-19: A Job that reaches a draft PR sends a macOS notification, and comments the PR link on its issue when it came from one.

### Timer trigger
- AC-20: A timer Loop fires on its `every` interval or `cron` expression.
- AC-21: A fire that comes due while the Loop has a running Job, or while the global running cap (1) is reached, is skipped and not counted toward bounds.

### GitHub trigger
- AC-22: A GitHub Loop polls `gh issue list --json` for its label every `every`, and starts at most one Job per issue number unless `requeue` names it.
- AC-23: An issue Job runs `drill` in away-from-keyboard mode before `implement`; the issue text reaches the model only as quoted data, and text in it cannot change the Loop's verify command, bounds, protected globs, or stages.
- AC-24: A self-drilled Job is Blocked only when the work touches protected paths, needs access outside the repo (secrets, accounts, production systems), has two or more very different readings, or deletes or migrates data; every other choice is built and listed as a Flag at the top of the PR body.
- AC-25: A Blocked issue Job comments its question on the issue and adds the `needs-drill` label.

### Takeover and Handback
- AC-26: `takeover <job>` stops the Job, sets it to `manual`, and prints its worktree path and branch.
- AC-27: `handback <job>` starts a new `implement` session in the same worktree; commits made during the Takeover stay.
- AC-28: A `manual` or Blocked Job does not hold its Loop's slot or the global running cap.

### Service and harness
- AC-29: `loopctl install-service` writes `~/Library/LaunchAgents/<label>.plist` that passes `plutil -lint` and has `RunAtLoad` true.
- AC-30: The repo ships a Claude Code skill that drives `loopctl` for list, run with a Spec, logs, Takeover, and Handback; `rubric` judges it.

### Public repo
- AC-31: `README.md` opens with what loopgate is and the rule that it never merges and stops at a draft PR; `rubric` judges it.
- AC-32: `.gitignore` covers `.env`, `*.db`, `.loopd/`, and local config; the repo has an MIT `LICENSE`.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `plutil -lint ~/Library/LaunchAgents/<label>.plist` (AC-29)
- `rubric` (AC-30, AC-31)
- Demo: in a scratch GitHub repo, `loopctl add examples/fix-issues.yaml`, label an issue `agent`, and `loopctl logs <job>` shows drill, implement, the Verify gate, and a draft PR link.

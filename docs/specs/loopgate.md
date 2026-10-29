# loopgate

## Goal
A local macOS service (`loopd`) and CLI (`loopctl`) that run Claude Code with the user's own `drill`, `implement` and `land` skills on GitHub issues, timers, or a handed-off Spec, gate each Job on a verify command's exit code, and stop at a draft PR that a human reviews, with Takeover and Handback between the loop and the manual harness at any time.

## Non-goals
- No merge of any kind, no force-push, no push to the default branch, and no change of a PR from draft to ready.
- No VPS, webhooks, web UI, chat bots, or Linux/Windows service install.
- No orchestration of subagents by `loopd`; subagents come only from the skills inside a Claude Code session.
- No network path from any Claude Code stage to GitHub; every GitHub read and write goes through `loopd`.
- No stored tokens or credentials; GitHub access goes through the user's `gh` login.
- No database operations or migrations performed by a Job.
- No copy of the user's private skill definitions, company details, `.env`, or `*.db` in the repo or its history.

## Acceptance criteria

### Config, store, IPC
- AC-1: A Loop file that lacks `name`, `checkout`, `verify`, `bounds.max_iterations`, or `bounds.timeout`, or that has an unknown field, fails to load with an error that names the field; `checkout` is the path of the user's own clone that the Loop's worktrees come from.
- AC-2: A Loop file has exactly one trigger: `github` (`repo`, `label`, `every`) or `timer` (`every` or `cron`); a timer Loop has exactly one of `task` or `spec`.
- AC-3: A second `loopd` on the same `~/.loopd` exits non-zero and names the running pid; a lock left by a dead pid is taken over.
- AC-4: Loops, Jobs, Job states, and handled issue numbers survive a `loopd` restart.
- AC-5: `loopctl list | add <file> | remove <loop> | pause <loop> | resume <loop> | stop <loop> | run <loop> [--spec <path>] | logs [-f] <job> | track <job> | takeover <job> | handback <job> | try <job> | rm <job> | requeue <loop> <issue>` reach `loopd` over `~/.loopd/loopd.sock`; with `loopd` down, `loopctl` exits non-zero with `loopd not running`; `list` shows each Loop and the job id of each Job in progress (queued, running, Blocked, manual) and of the 5 newest that ended.
- AC-6: `loopd` refuses to start, with a message that names `gh auth login`, when `gh auth status` fails.
- AC-39: `loopctl add` refuses a Loop, naming the cause, when its `checkout` is not the top level of a git work tree, when the checkout has no `refs/remotes/origin/HEAD`, with an error that names `git -C <checkout> remote set-head origin --auto`, or when a GitHub Loop's `repo` differs from the `owner/repo` of the checkout's `origin` remote.
- AC-40: `loopctl add` adds a Loop only after its Trial gate passes: the Loop's Verify gate, run after a fetch on a new detached worktree of `origin/<default>` and bounded by `bounds.timeout`; a failing Trial gate adds nothing and prints the gate's output, and one that runs past the timeout adds nothing and says so, both naming each host the proxy refused; ending the `add` call stops the Trial gate and adds nothing; and the worktree is removed after every outcome, `loopd` stopping included, or at `loopd`'s next start when `loopd` was killed.

### Runner
- AC-7: Each stage runs `claude -p` in the Job worktree with `--output-format stream-json --verbose`, `--permission-mode auto`, `--permission-prompts none`, a `--json-schema` for `{status: done|blocked|revise, question, summary}`, `--setting-sources user`, `--strict-mcp-config`, and `--settings` that enable the sandbox with no unsandboxed escape, allow network only to the Loop's `network` domains, deny reads of credential directories and writes to the repository's git config, hooks and `.git` pointer, and deny commands that start with `gh`, `git push`, or `git merge`; each stage's `total_cost_usd` is recorded on its Job.
- AC-8: The child environment holds only allowlisted variables; a variable outside the allowlist, set in `loopd`'s environment, is absent in the child.
- AC-9: On timeout, `stop`, or Takeover, the child's whole process group ends, including grandchildren.
- AC-10: Each Job runs in its own worktree on branch `loop/<loop>-<issue>-<job id>` or `loop/<loop>-<timestamp>-<job id>`, cut from `origin/<default>`; uncommitted changes in the main checkout are absent from it.
- AC-11: `run --spec <path>` copies the Spec into the worktree at the same path and commits it on the Job branch when the branch lacks it.
- AC-33: A Loop's optional `network` lists the domains its stages and its setup command may reach; a domain that is or ends in `github.com` or `githubusercontent.com` fails to load.
- AC-38: Every stage, setup command and verify command keeps its npm, pnpm, uv and pip caches under `~/.loopd/cache`.

### Verify gate and bounds
- AC-12: A Job reaches `land` only when the Loop's verify command, run after its setup command when one is set, under Seatbelt with no network but localhost, no keychain, and writes only to the worktree, cache and TMPDIR, exits 0 on a fresh checkout of its worktree's HEAD, which must be clean and committed; a stage that reports `done` while the verify command fails, or that leaves uncommitted changes, does not pass.
- AC-36: A Loop's optional `setup` command runs in the Verify gate's fresh checkout of the Job worktree's HEAD, after the clean-tree check and before the verify command, under the verify command's Seatbelt profile, with `HTTPS_PROXY` and `HTTP_PROXY` set to a proxy in `loopd` that tunnels `CONNECT` to port 443 for the Loop's `network` domains and refuses every other request; a connection that bypasses the proxy reaches no remote host.
- AC-37: A setup command that exits non-zero, or that leaves a tracked file changed or a file untracked and not ignored, fails the Verify gate with its output; every Verify gate that fails after the setup command ran, and a setup command that times out, names each host the proxy refused; the proxy's tunnels end when the setup command ends.
- AC-13: After a failed Verify gate or a `rubric` revision, the next `implement` session is new and its prompt carries the progress log: each earlier iteration's verify output or findings, also kept in `~/.loopd/jobs/<id>/progress.md`.
- AC-14: A Job whose `max_iterations` `implement` runs all end without a passing Verify gate and `rubric` review, or that exceeds `timeout`, ends as `exhausted` with no PR.
- AC-15: A stage that reports `blocked` ends the Job as Blocked with its question stored, and no later stage runs.
- AC-16: After every stage, a change since the Job's base commit to a path matching the default or the Loop's `protected` globs in any letter case, committed or in the working tree, ignored files included except those under a directory named `node_modules` or `.venv` outside `.claude/` and `.github/`, makes the Job Blocked before the next stage.
- AC-17: The PR report lists each file that matches the Loop's `tests` globs and that the diff deleted or changed.
- AC-34: After the Verify gate passes, a `rubric` defect-pass stage reviews the branch diff on every Job; a `revise` report sends its findings to the next `implement` run, and a `rubric` stage that changes the tree fails the Job.

### Land
- AC-18: `land` runs as `/land PR, keep spec` with no network and returns the PR title and body as its summary instead of pushing; `loopd` then pushes the Job branch and opens the PR with `gh pr create --draft`.
- AC-35: `loopd` pushes only when the branch head differs from the commit that passed the Verify gate and `rubric` in unprotected `.md` files alone, renames counted as deletions; otherwise nothing is pushed.
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
- AC-29: `loopctl install` writes `~/Library/LaunchAgents/<label>.plist` that passes `plutil -lint` and has `RunAtLoad` true, with a `PATH` that is the installing shell's `PATH` with each link to a link replaced by its target and each relative or missing entry dropped; it starts the agent with `launchctl bootstrap`, replacing a loaded copy only after launchd has stopped it, installs the loopgate skill into `~/.claude/skills/loopgate`, prints where `claude`, `gh`, `git`, `node` and `go` resolve on that `PATH`, and exits non-zero when `loopd` does not answer on its socket, with the error `loopd` logged when it logged one; `loopctl uninstall` stops the agent and removes the plist and the skill.
- AC-30: The binaries carry a Claude Code skill, installed by `loopctl install`, that drives `loopctl` for list, run with a Spec, logs, Takeover, and Handback; `rubric` judges it.

### Public repo
- AC-31: `README.md` opens with what loopgate is and the rule that it never merges and stops at a draft PR; `rubric` judges it.
- AC-32: `.gitignore` covers `.env`, `*.db`, `.loopd/`, and local config; the repo has an MIT `LICENSE`.

## Verification
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- `golangci-lint run`
- `go test ./e2e/ -run TestInstall` (AC-29)
- `go test ./e2e/ -run TestAdd` (AC-40)
- `go test ./internal/daemon/ -run 'TestAdd|TestClose|TestNew'` (AC-39, AC-40)
- `rubric` (AC-30, AC-31)
- Demo: in a scratch GitHub repo, `loopctl add examples/fix-issues.yaml`, label an issue `agent`, and `loopctl logs <job>` shows drill, implement, the Verify gate, and a draft PR link.
- Demo: in a scratch GitHub repo with a TypeScript package, a Loop with `setup: npm ci`, `verify: npm test` and `network: [registry.npmjs.org]` takes a labeled issue to a draft PR.

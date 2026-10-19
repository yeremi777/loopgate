# Architecture

## What this is
loopgate is a local macOS service, `loopd`, and its CLI, `loopctl`. It runs Claude Code unattended on a Loop's work, gates every Job on the Loop's verify command, and stops at a draft PR that a human reviews. One developer runs it on their own machine, next to their manual Claude Code sessions.

## Modules
- **config**: parses a Loop file into a typed Loop, and refuses anything incomplete or ambiguous at load time.
- **store**: keeps Loop, Job and claimed-issue state in SQLite, so it survives restarts.
- **daemon**: owns the registered Loops and the single running slot: starts, stops, takes over and hands back Jobs, fires timers, and polls issues.
- **job**: carries one Job through drill, implement, the Verify gate, rubric and land, within the Loop's bounds.
- **verify**: decides whether a tree may ship: the Verify gate with its setup command, protected paths, test changes, and the after-land check.
- **runner**: spawns every child, whether a Claude Code stage, the setup or verify command, or git, sandboxed and in its own process group, and creates Job worktrees.
- **github**: loopd's only path to GitHub, through the user's `gh` login: push, draft PR, issue list, comment, label.
- **trigger/timer**: computes when a timer Loop fires next.
- **ipc** and **service**: carry `loopctl` commands to `loopd`, and install `loopd` as a login agent.

## Seams
- `job.Stager`: one Claude Code stage. `runner.Runner` runs real `claude -p`; tests script the model.
- `job.Publisher` and `daemon.IssueTracker`: everything that writes to GitHub, implemented by `github`.
- The user's skills, called by name in stage prompts: `drill`, `implement`, `rubric`, `land`, `tldr`.
- Loop files in `~/.loopd/loops/`: a new Loop is configuration, not code.

## Invariants
- No Claude Code stage has a network path to GitHub; only `loopd` pushes, without force, and only opens draft PRs.
- Pass or fail comes only from the setup and verify commands' exit codes on a clean, committed tree that setup leaves unchanged, never from a model's report.
- The setup and verify commands run sandboxed, because they execute code the model wrote; setup reaches the network only through `loopd`'s proxy to the Loop's `network` domains.
- A protected path changed by any stage or Verify gate blocks the Job before the next stage starts.
- After the gate, only unprotected Markdown may change before the push.
- One Job holds the running slot at a time; a manual or Blocked Job holds none.
- Issue text reaches a model only as fenced, untrusted data.

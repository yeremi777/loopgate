---
name: loopgate
description: Hand work to the loopgate loop, check on it, or take it back into this session. Use when the user says "hand this to the loop", "let the loop do it", "I'm leaving my desk", "loop status", "take over job <id>", or "give it back to the loop". Use it also when a settled spec should run while the user is away.
---

`loopctl` controls `loopd`, which runs Claude Code on a spec or task without the user, gates it on the loop's verify command and a `rubric` review, and stops at a draft PR. `loopctl help` lists every command.

1. Find the loop of this repository: run `loopctl list` and pick the loop whose `checkout:` line is this repository's root, where `~` is the user's home. When none matches, say so and stop. Adding a loop is the user's decision.
2. Hand over a settled spec with `loopctl run <loop> --spec docs/specs/<slug>.md`. A spec that still has `## Open decisions` is a draft: run `drill` on it first. The spec need not be committed; loopd commits it on the job branch.
3. Tell the user the job id and branch, and that the result arrives as a draft PR and a macOS notification. Show `loopctl logs <job>` for status.
4. To take a job back, run `loopctl takeover <job>`. Continue in the worktree path that it prints, with the usual `implement` and `verify` flow. To return it, commit, then run `loopctl handback <job>`.
5. To test a done job by hand before the user accepts its PR, run `loopctl try <job>`. Start the app from the worktree it prints, as the test guide it prints says.

Rules:

- `loopctl stop <loop>` ends only the running job. The loop keeps its timer or issue polling; `loopctl pause <loop>` stops those.
- One job runs at a time. `a job is already running` means wait, or take that job over.
- Never merge a loopgate PR or mark it ready. The user reviews the draft.

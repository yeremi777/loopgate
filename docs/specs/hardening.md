# hardening

## Goal
A Verify gate passes only on committed files, and protected paths, Handback, the order of Jobs, the worktree folder check and two tests have no known gap.

## Non-goals
- No change to how a stage calls Claude Code, to the stage prompts, or to the job page (`/jobs/<id>`).
- The Trial gate keeps its own worktree under `$LOOPD_HOME/trials/`.
- No new setting in a Loop file.
- No change to which Jobs `loopctl list` and the index page show; only their order changes.
- The Job's worktree gets no dependencies from the Verify gate; a stage that needs them installs them through the Loop's network.

## Acceptance criteria
- AC-1: A change to a path that matches a default or Loop protected glob in any letter case, such as `.CLAUDE/settings.json`, `.Github/workflows/ci.yml` or `docs/claude.md`, blocks the Job, also when it sits under a `node_modules` or `.venv` folder inside `.claude/` or `.github/` in any letter case; test globs still match case-sensitively.
- AC-2: The Verify gate runs the setup and verify commands in a new detached worktree of the Job's committed HEAD, inside the Job's directory, and removes that worktree after every outcome, read-only folders in it included; a gitignored file in the Job's worktree is absent there, so a verify command that needs it fails the gate.
- AC-3: Neither gate command can write, rename or replace the gate checkout's `.git` pointer, nor write the Job's worktree.
- AC-4: A Job handed back runs with no question, summary, error, end commit, Test guide or end time from its earlier run; its cost keeps adding up, and `loopctl logs` shows no earlier error while it runs.
- AC-5: The index page loads itself again every 5 seconds.
- AC-6: In each Loop block of the index page and of `loopctl list`, Blocked, failed and exhausted Jobs come first, then the other Jobs; each group is newest first.
- AC-7: `TestGateLetsSetupReachTheLoopsNetwork` gives curl more time than the proxy's 10-second dial bound, so it reads the proxy's answer, 200 or 502, and never a curl timeout.
- AC-8: `TestVersionPrintsTheVersionGoStamped` passes in a detached or outside worktree, where Go stamps `(devel)`, and still requires a `v` version in a branch checkout.
- AC-9: `loopctl add` refuses a Loop whose `<checkout>-worktrees` folder, or the checkout's parent folder when that folder does not exist, is not writable, and a Job start on such a checkout fails; both errors name `<checkout>-worktrees` and the folder to make writable, and neither leaves a file behind.
- AC-10: `try`'s reply over IPC carries no list of copied files.
- AC-11: `CONTEXT.md`, `ARCHITECTURE.md`, the README and `docs/specs/loopgate.md` say that the gate runs on a fresh checkout of the Job's committed HEAD and writes only that checkout; the README and `docs/specs/loopgate.md` also say that protected paths match in any letter case.

## Verification
- `go build ./...`
- `go vet ./...`
- `golangci-lint run`
- `go test -race ./...`
- `go test ./internal/verify/ -run 'TestProtected|TestTouched|TestGate'` (AC-1, AC-2, AC-3, AC-7)
- `go test ./internal/runner/ -run 'TestRemoveWorktree'` (AC-2)
- `go test ./internal/job/ -run 'TestGateCommandsLeaveNothing'` (AC-3)
- `go test ./internal/daemon/ -run 'TestHandback|TestAdd|TestRun|TestTry'` (AC-4, AC-9, AC-10)
- `go test ./internal/store/ -run 'TestJobs'` (AC-6)
- `go test ./internal/web/ -run 'TestIndex'` (AC-5, AC-6)
- `go test ./e2e/ -run 'TestVersion'` (AC-8), also from a detached worktree
- `grep -rn 'Copied' internal/daemon/try.go internal/ipc` prints nothing (AC-10)
- `grep -n 'fresh checkout\|letter case' CONTEXT.md ARCHITECTURE.md README.md docs/specs/loopgate.md` (AC-11)
- Demo: in a repo whose `.gitignore` lists `secret.txt`, with `secret.txt` on disk and a verify command `test -f secret.txt`, the Verify gate fails naming the missing file.

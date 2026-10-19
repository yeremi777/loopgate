# loopgate

Runs Claude Code unattended on settled work, gates the result on a verify command, and stops at a draft PR for a human to review.

## Language

**Loop**:
A named, configured source of work with one trigger, one verify command, and fixed bounds.
_Avoid_: pipeline, workflow, agent

**Job**:
One attempt by a Loop to carry one work item from its branch to a draft PR.
_Avoid_: run, task, execution

**Spec**:
A settled work-item description at `docs/specs/<slug>.md`, written through `drill` by a human or by a Job, that a Job implements against.
_Avoid_: plan, ticket, design doc

**Obvious plan**:
A work item that one sentence states, with its check, and that needs no Spec.
_Avoid_: quick fix, small task

**Verify gate**:
The exit codes of a Loop's Setup command, when set, and its verify command on a Job's worktree, which alone decide pass or fail.
_Avoid_: check, test step

**Setup command**:
A Loop's optional command that installs the worktree's dependencies through `loopd`'s proxy at the start of each Verify gate, so the verify command can run offline.
_Avoid_: install step, bootstrap, prepare

**Takeover**:
A human taking a Job from the Loop to continue it by hand in its worktree.
_Avoid_: pause, interrupt

**Handback**:
A human returning a taken-over Job to its Loop, which continues from the files on disk.
_Avoid_: resume, release

**Blocked**:
The state of a Job that stopped before coding because its work cannot be undone, cannot be done safely, or has no clear target.
_Avoid_: stuck, failed

**Flag**:
A choice a Job made on its own and reported at the top of its draft PR for the human to accept or reject.
_Avoid_: warning, note, assumption

## Relationships

- A **Loop** has any number of **Jobs**; at most one **Job** of a **Loop** runs at a time
- A **Job** implements exactly one **Spec** or one **Obvious plan**
- A **Job** ends at a draft PR, **Blocked**, or bounds exhausted; only the **Verify gate** lets it reach a draft PR
- A **Takeover** and a **Handback** move one **Job** between the human and its **Loop**
- A **Job** that reaches a draft PR carries zero or more **Flags**; a **Blocked** **Job** has no PR

## Example dialogue

> **Dev:** "Issue #42 has the `agent` label but no **Spec**, and the fix needs a new dependency. Does the **Job** go **Blocked**?"
> **Owner:** "No. The **Job** drills it, adds the dependency, and raises a **Flag** in the draft PR. It goes **Blocked** only if the fix needs a production secret, and then I do a **Takeover**."

## Flagged ambiguities

- "spec" meant both the loopgate design document and a work-item description. Resolved: **Spec** is only a work-item description in `docs/specs/`; the loopgate design lives in `docs/specs/loopgate.md` as one such Spec.
- "resume" meant both un-pausing a Loop and returning a Job. Resolved: `resume` applies to a **Loop**; **Handback** applies to a **Job**.

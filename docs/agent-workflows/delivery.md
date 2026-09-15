# Delivery and Worktree Lifecycle

Read this document when planning isolated or parallel work, integrating, handing off, or parking a slice.
[Agent Execution Policy](execution-policy.md) owns execution, review selection, and check scope.

## 1. One Delivery Cycle Per Approved Slice

- Define the reviewable outcome and acceptance criteria before implementation begins.
- Keep tests, implementation, repairs, documentation, and review on one branch, worktree, and PR.
- Finish all applicable acceptance criteria, review, and documentation before integration.

## 2. Sequential Branch Lifecycle

1. Inspect `git worktree list`. Fetch `origin/main`. To create an isolated slice, run:
   ```bash
   git worktree add /home/jeffhuen/herdr/workspaces/tether-browser/<slice-id> -b <branch-name> origin/main
   ```
   Herdr (`herdr worktree create`) and OMP wrappers are also accepted. Never branch from another feature branch. Never create worktrees inside the repository tree. Put the stable task or Bead ID in the branch name. For read-only work, create no worktree.
2. Keep all owned fixes on that branch. Before integration, refresh against current `origin/main`. Inspect the complete candidate diff against scope and acceptance. Rerun only checks affected by new changes or evidence gaps.
3. Under existing Git authority, stage specific files, inspect staged status, and ensure no intended new file was omitted. Keep commits atomic.
4. Merge the PR. Fetch and prove `git merge-base --is-ancestor <slice-tip> origin/main`. An open PR, commit, push, or successful check does not prove integration.
5. Fast-forward the clean primary checkout on `main` with `git pull --ff-only origin main`. If the primary checkout has commits ahead, is on another branch, or has dirty files, leave it untouched and report the pending update.
6. After verified integration, remove the owned merged worktree. In Herdr, run:
   ```bash
   herdr worktree remove --workspace <workspace-id>
   ```
   In a bare shell, run:
   ```bash
   git worktree remove /home/jeffhuen/herdr/workspaces/tether-browser/<slice-id>
   git worktree prune
   ```
   Delete the merged local branch and the merged remote branch. Never run `omp worktree clear --all` in this repository because it forces removal of live checkouts across workspaces. Inspect `git worktree list` at cleanup. Only then start the next sequential slice from fresh `origin/main`.

Use native Git, Herdr, and OMP commands. Never switch the primary checkout. Never reset the primary checkout. Never hide dirty state in another worktree. Never force cleanup of unproven resources. Never delete another writer's work. To park unfinished work, push its branch. Record the branch on the owning Bead. Then remove the worktree.

## 3. Independent Review Protocol

For changes requiring independent review:

1. **Isolation over snapshot**: From the candidate checkout root, record HEAD, the stash ref, the reflog count, and the worktree status. Then create an ephemeral detached review worktree:
   ```bash
   HEAD=$(git rev-parse HEAD); REFN=$(git reflog HEAD | wc -l)
   STASH=$(git rev-parse --verify -q refs/stash || echo none)
   git status --porcelain -uall > "/tmp/candidate-$HEAD.status"
   RT=$(mktemp -d)
   git worktree add --detach "$RT" "$HEAD"
   ```
   If untracked candidate files exist, run the copy from the repository root. Copy them into `$RT`:
   ```bash
   git ls-files --others --exclude-standard -z | tar --null -T - -cf - | tar -C "$RT" -xf -
   ```
2. **Review execution**: Direct the reviewer to inspect `$RT`. As an alternative for small diffs, pass the candidate diff `git diff origin/main...HEAD` directly. Reviewers work strictly read-only. Reviews in `$RT` evaluate static code and diffs. Executable verification remains with the coordinator. Do not review inside the live candidate checkout.
3. **Receipt and verification**: From the candidate checkout root, verify the candidate checkout before you acknowledge the report:
   ```bash
   [ "$(git rev-parse HEAD)" = "$HEAD" ] \
     && git status --porcelain -uall | diff -q "/tmp/candidate-$HEAD.status" - >/dev/null \
     && [ "$(git rev-parse --verify -q refs/stash || echo none)" = "$STASH" ] \
     && [ "$(git reflog HEAD | wc -l)" = "$REFN" ] \
     && echo REVIEW_TARGET_INTACT || echo REVIEW_INVALID_INVESTIGATE
   git worktree remove --force "$RT"
   git worktree prune
   ```
4. **Taxonomy and approval**: Reports classify findings as `REVIEW_PASS` or `CHANGES_REQUIRED`. Each finding carries `BLOCKING`, `FOLLOW_UP`, or `NOTE`. The coordinator validates the findings. Approval requires `REVIEW_PASS` and an intact target. Never approve with an unresolved validated blocker. Record accepted follow-ups as Beads. A completed diagnosis is not `REVIEW_PASS`.

## 4. Parallel Slice Execution Protocol

- Parallel writers require a durable manifest before dispatch, preferably under `docs/plans/parallel-manifests/`. A single executor in an isolated checkout does not need a parallel manifest.
- Record owned and forbidden files, the common `main` baseline, migration names, affected checks, documentation surfaces, merge order, and the reconciliation owner. A shared seam must have its contract landed first.
- Default to at most two concurrent code-writing worktrees. Each starts from the recorded `main` baseline and refreshes before its ordered merge.
- Bind one writer to each checkout, Bead, and branch. Treat a checkout that is open in a Herdr workspace as read-only to other agents.

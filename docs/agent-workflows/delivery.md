# Delivery and Worktree Lifecycle

Read this document when planning isolated or parallel work, integrating, handing off, or parking a slice.
[Agent Execution Policy](execution-policy.md) owns execution, review selection, and check scope.

## 1. One Delivery Cycle Per Approved Slice

- Define the reviewable outcome and acceptance criteria before implementation begins.
- Keep tests, implementation, repairs, documentation, and review on one branch, worktree, and PR.
- Finish all applicable acceptance criteria, review, and documentation before integration.

## 2. Integration and resource ownership

Follow `skill://herdr-workflow` for worktree creation, resume, parking, removal, and independent review mechanics. Use only owned or explicitly authorized resources. Review and cleanup do not grant Git authority.

- Start new isolated slices from verified `origin/main`, never another feature branch. `'/home/jeffhuen/.agents/skills/herdr-workflow/SKILL.md'` section 2 sets the branch and checkout names. Read-only work needs no worktree.
- Keep repairs on the same branch and PR. Refresh against current `origin/main` before integration and rerun only checks affected by changes or evidence gaps.
- Stage only intended files under existing Git authority, including intended new files. Keep commits atomic.
- After a PR merge, fetch and prove integration. For a true merge, prove `git merge-base --is-ancestor <slice-tip> origin/main`. A squash merge rewrites the commit. Rebase onto current `origin/main`, confirm that `origin/main` has not moved, and merge with the reviewed head pinned (`gh pr merge <pr> --squash --match-head-commit <slice-tip>`). Then prove `git merge-base --is-ancestor <merge-commit> origin/main` and `git diff --quiet <slice-tip> <merge-commit>`. Record the reviewed head and the merge commit. A commit, push, passing check, or worker report does not prove integration.
- Fast-forward the primary checkout only when it is clean and on `main`. If it is dirty, on another branch, or ahead, leave it untouched and report the pending update. Never switch or reset the primary checkout.
- Before authorized cleanup, verify integration and account for helpers, descendants, processes, and needed files. Park unfinished work only with a preserved branch and checkpoint. Without savepoint authority, leave the checkout intact.

## 3. Independent review

Use the risk criteria in [execution policy](execution-policy.md). Reviewers remain read-only and executable verification stays with the coordinator. Do not review inside the live candidate checkout: use a complete candidate diff or an authorized isolated review checkout. Include intended uncommitted and new files in the review target.

Follow `skill://herdr-workflow` for target capture, reviewer instructions, report taxonomy, and safe cleanup. Approval requires `REVIEW_PASS`, an intact reviewed target, and no unresolved validated blocker. The coordinator validates findings and records accepted follow-ups in Beads.


## 4. Parallel Slice Execution Protocol

- Parallel writers require a durable manifest before dispatch, preferably under `docs/plans/parallel-manifests/`. A single executor in an isolated checkout does not need a parallel manifest.
- Record owned and forbidden files, the common `main` baseline, migration names, affected checks, documentation surfaces, merge order, and the reconciliation owner. A shared seam must have its contract landed first.
- Default to at most two concurrent code-writing worktrees. Each starts from the recorded `main` baseline and refreshes before its ordered merge.
- Bind one writer to each checkout, Bead, and branch. Treat a checkout that is open in a Herdr workspace as read-only to other agents.

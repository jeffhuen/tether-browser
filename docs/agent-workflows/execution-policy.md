# Agent Execution Policy

Policy version: `1.2.0`

`AGENTS.md` and `CLAUDE.md` require a source read of this policy. Read this document before choosing an execution method, running project commands, or changing files.

## 1. Default: Finish the Bounded Change Directly

State the intended result and the smallest check that can observe it. Reuse the existing owner, make the change, run affected checks, and report the result. One executor owns the result through local repairs. An edit or failed assertion does not require another task, brief, reviewer, or worktree.

Assign implementation and affected tests together. Add an intermediate checkpoint only for an explicit requirement, real dependency, or identified risk.

Choose isolation, delegation, and review separately:

| Decision | When it is needed |
|---|---|
| **Isolated checkout** | Concurrent or user-owned work, dependency changes, new component boundaries, or meaningful protocol/concurrency risk |
| **Delegation** | Explicit user request, useful parallelism, ownership transfer, or dependency coordination |
| **Independent review** | Wire protocols, cryptographic operations, cross-platform compatibility, concurrency and recovery; also when explicitly requested |

The primary checkout is user-owned. Start direct work there only when it is clean, on `main`, and equal to freshly fetched `origin/main`. Otherwise use an owned isolated checkout on the NVMe workspace root (`/home/jeffhuen/herdr/workspaces/tether-browser/<slice-name>`).

## 2. Keep the Scope Fixed

Separate required behavior from implementation preferences. A discovered bug or related task does not authorize a new subsystem. Necessary prerequisites stay narrow. Independent outcomes get named follow-up Beads.

## 3. Codebase Discovery

Use codebase-memory for code structure: definitions, relationships, and call paths. Check coverage for cited and changed files, then read source code directly where coverage is incomplete.

Use direct text search for literals, configuration, scripts, and documentation.

## 4. Verification Follows Changed Behavior

Use the smallest relevant check that can observe the changed behavior:
- **Prose or documentation only**: review diffs, links, and markdown formatting with `git diff --check`.
- **Scripts and CLI tools**: run affected test cases directly.
- **Protocol definitions**: run schema validation and type checks.

Do not run full repository test suites for localized prose or documentation fixes.

## 5. Repair Strategy Checkpoint

After two completed unsuccessful repair attempts at the same acceptance failure, reassess the strategy before another edit or dispatch. Record once: unmet outcome, why the attempts failed, and the next verified correction or changed strategy.

Continue toward the approved outcome with a smaller correct implementation, removal of optional behavior, or an authorized tool change. Do not repeat the same failed approach.

## 6. Track Durably Without Creating Another Delivery Loop

For actionable task tracking, read `docs/agent-workflows/beads.md`. Start with `bd ready`, claim with `scripts/bd-claim <id>`, and close with `bd close <id>`. Pure repository-instruction or local maintenance edits remain exempt. `delivery.md` owns closeout timing and worktree cleanup.

## 7. Load Detailed Procedures Only When Selected

Read [Delivery and Worktree Lifecycle](delivery.md) when planning isolated or parallel work, integrating, handing off, or parking a slice. It owns the native Git worktree lifecycle at `/home/jeffhuen/herdr/workspaces/tether-browser/` and the independent review isolation protocol. Follow `skill://herdr-workflow` for cross-repository Herdr and OMP coordination.

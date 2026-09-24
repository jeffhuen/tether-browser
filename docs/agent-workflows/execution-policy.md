# Agent Execution Policy

Policy version: `1.2.0`

`AGENTS.md` and `CLAUDE.md` require a source read of this policy. Read this document before choosing an execution method, running project commands, or changing files.

## 1. Default: Finish the Bounded Change Directly

State the intended result and the smallest check that can observe it. Reuse the existing owner, make the change, run affected checks, and report the result. One executor owns the result through local repairs. An edit or failed assertion does not require another task, brief, reviewer, or worktree.

Assign implementation and affected tests together. Add an intermediate checkpoint only for an explicit requirement, real dependency, or identified risk.

Choose isolation, delegation, and review separately:

| Decision | When it is needed |
|---|---|
| **Isolated checkout** | Follows the level in `skill://herdr-workflow` section 6. L0 and L1 changes work in the primary checkout under section 8. L2 and L3 changes, dependency changes, new component boundaries, and protocol or concurrency risk use a worktree |
| **Delegation** | Explicit user request, useful parallelism, ownership transfer, or dependency coordination |
| **Independent review** | Wire protocols, cryptographic operations, cross-platform compatibility, concurrency and recovery; also when explicitly requested |

The primary checkout is shared. `skill://herdr-workflow` section 8 governs work there, including the standing authority to push L0 and L1 changes. Section 2 governs worktrees.

## 2. Keep the Scope Fixed

Separate required behavior from implementation preferences. A discovered bug or related task does not authorize a new subsystem. Necessary prerequisites stay narrow. Independent outcomes get named follow-up Beads.

## 3. Codebase Discovery

Choose the tool that fits the scope of your question. Do not query both graphs for one question.

- **Code structure and call paths**: Use `codebase-memory-mcp`. Search for symbols with `search_graph`, trace callers and callees with `trace_path`, and read source code with `get_code_snippet`. Check index coverage for cited files before you make negative claims.
- **System architecture and concepts**: Use Graphify. Read artifacts in `graphify-out/` to inspect component boundaries, documentation relationships, and design decisions. Run `graphify update .` only after you make structural code changes.
- **Exact text and configuration**: Use direct file search for string literals, configuration values, scripts, and documentation files.
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

Follow `.beads/PRIME.md` for durable tracking and `docs/agent-workflows/beads.md` for local constraints. Use native `bd --sandbox update <id> --claim` only with claim authority. Keep execution todos in the harness. Pure repository-instruction or local maintenance edits remain exempt from Beads. The coordinator owns governing-bead updates and verified closure; helpers report. Worktree cleanup is a separate authorized operation.

## 7. Load Detailed Procedures Only When Selected

Read [Delivery](delivery.md) for repository integration and review gates when planning isolated or parallel work, integrating, handing off, or parking a slice. Follow `skill://herdr-workflow` for worktree operations, independent review mechanics, and native Herdr and OMP coordination.

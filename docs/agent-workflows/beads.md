# Active Queue (Beads)

Beads (`bd` CLI) is the active-queue authority for in-flight tasks, blocked items, named follow-ons, and dependency state.

## 1. Identity and Records

- Issues use the `tb-` prefix (e.g. `tb-a1b2c3`).
- Issue titles summarize the goal and scope in one line.
- Descriptions contain the target files, acceptance criteria, and plan reference.
- Provenance and phase labels track work streams.

## 2. Canonical Workflow

1. **Pick next work**: Run `bd ready` to see unblocked actionable tasks. Inspect specific items with `bd show <id>`.
2. **File new work**: Run `bd create "<title>" -p <priority> -d "<description>"`. Wire real dependencies with `bd dep add <child> <parent>`.
3. **Claim work**: Run `scripts/bd-claim <id>` to claim the task. The wrapper records the active agent runtime (`agent:omp`, `agent:herdr`, `agent:claude`, etc.) and session/pane identity for crash recovery.
4. **Close work**: After verified integration and delivery, run `bd close <id>`.

## 3. Concurrency and Synchronization

- Beads uses embedded Dolt storage with file locking.
- Serialize mutating commands (`create`, `claim`, `close`) through one coordinating checkout.
- Remote synchronization uses Git remote sync: `git+https://github.com/jeffhuen/tether-browser.git`.

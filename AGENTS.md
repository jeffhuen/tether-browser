# tether-browser Agent Contract

This repository owns `tether-browser`, a high-performance remote browser automation bridge for AI coding agents across Herdr and tmux sessions.

## Execution Surface and Tool Compass

Follow `skill://herdr-workflow` for worktree lifecycle, terminal ownership, and native agent coordination. Manage only resources you own or have explicit authority to control.

Worktrees, when needed, belong under `/home/jeffhuen/herdr/workspaces/tether-browser/<slice-name>`. Choose isolation separately from Beads tracking and delegation.

Read [Agent execution policy](docs/agent-workflows/execution-policy.md) before project commands or edits. [Delivery](docs/agent-workflows/delivery.md) records repository-specific integration and review gates. [Beads](docs/agent-workflows/beads.md) records local tracking and synchronization constraints.

- **Architecture and Roadmap**: Read [README.md](README.md) before implementing components.

## Codebase Discovery

Use the tool that matches the scope of your question. Do not query both graphs for one question.

<!-- codebase-memory-mcp:start -->
### Code Intelligence with codebase-memory-mcp

Use `codebase-memory-mcp` for code structure, definitions, and call paths. Prefer MCP graph tools over text search for code symbols:

1. `search_graph`: Find functions, classes, routes, and variables.
2. `trace_path`: Trace callers, callees, and data flow.
3. `get_code_snippet`: Read source code for a specific symbol.
4. `check_index_coverage`: Verify file indexing before you make negative claims.
5. `query_graph`: Run Cypher queries for multi-hop code patterns.
6. `get_architecture`: Inspect project structure and entry points.
<!-- codebase-memory-mcp:end -->

### Architecture and Concepts with Graphify

Use Graphify for high-level architecture, concepts, and documentation relationships.

1. Inspect `graphify-out/` to review community clusters, system boundaries, and document links.
2. Read `graphify-out/GRAPH_REPORT.md` only for broad project review.
3. To refresh the architectural graph after you make structural changes, run `graphify update .`. Do not run updates for documentation or configuration edits.

### Exact Text and Configuration

Use direct text search for string literals, configuration values, scripts, and documentation.

## Architecture Invariants

1. **Deterministic Core**: Keep business logic pure. Isolate side effects at the call boundary.
2. **Surgical & Debloat**: Touch only files required for the task. Keep diffs minimal.
3. **No Dead Scaffolding**: Never create speculative abstractions or unused helpers.

<!-- BEGIN PI-BEADS-COMPANION -->
## Beads companion

Use native harness plans, todos, memory, and subagents for execution. Beads holds durable outcomes, acceptance criteria, high-level plans, and recovery checkpoints, not every execution step. Small bounded work needs no new bead unless project rules require one. Choose worktrees separately for writer or branch isolation. Read the project workflow with `bd prime`. One coordinator owns bead updates and closure unless ownership transfers explicitly. For assigned bead-scoped work, verify acceptance, record final evidence, and close the bead within your authority before reporting it complete. Completed todos alone do not prove acceptance. Helpers report back; lifecycle events do not close issues. Keep official Herdr integrations separate.
<!-- END PI-BEADS-COMPANION -->

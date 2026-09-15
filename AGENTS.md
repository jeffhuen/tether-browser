# tether-browser Agent Contract

This repository owns `tether-browser`, a high-performance remote browser automation bridge for AI coding agents across Herdr and tmux sessions.

## Execution Surface and Tool Compass

Follow `skill://herdr-workflow` for cross-repository worktree lifecycle, terminal coordination, and agent execution.

When `${HERDR_ENV:-0}` equals `1`, you run inside a Herdr pane. Use the `herdr` CLI (`skill://herdr`) for layout, worktrees, and interactive supervision:

- **Worktree root**: Store worktrees at `/home/jeffhuen/herdr/workspaces/tether-browser/<slice-name>`.
- **Worktree lifecycle**: Herdr owns creation and teardown. To create a worktree, run `herdr worktree create`. To remove a worktree, run `herdr worktree remove --workspace <ID>`. Outside Herdr, use native `git worktree add` and `git worktree remove`.
- **Interactive panes and supervision**: Herdr owns panes, tabs, splits, and observable agent runs. Use `herdr pane split` to arrange panes. Use `herdr agent start`, `herdr agent prompt`, and `herdr agent wait` to run and observe workers.
- **In-turn tools and subagents**: OMP owns the cognitive tool loop. Use OMP for file tools (`read`, `edit`, `write`), in-kernel execution (`eval`), turn-scoped headless subagents (`task`), and background services (`hub start`, `hub ps`, `hub logs`).
- **Delivery and review**: `docs/agent-workflows/delivery.md` owns the branch lifecycle, parallel manifests, and independent review. Read [Agent Execution Policy](docs/agent-workflows/execution-policy.md) before you run project commands or change files.
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

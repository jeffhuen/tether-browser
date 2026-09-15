# tether-browser Agent Contract

This repository owns `tether-browser`, a high-performance remote browser automation bridge for AI coding agents across Herdr and tmux sessions.

## Execution Surface and Tool Compass

Follow `skill://herdr-workflow` for cross-repository worktree lifecycle, terminal coordination, and agent execution.

When `${HERDR_ENV:-0}` equals `1`, you run inside a Herdr pane. Use the `herdr` CLI (`skill://herdr`) for layout, worktrees, and interactive supervision:

- **Worktree root**: Store worktrees at `/home/jeffhuen/herdr/workspaces/tether-browser/<slice-name>`.
- **Worktree lifecycle**: Herdr owns creation and teardown. To create a worktree, run `herdr worktree create`. To remove a worktree, run `herdr worktree remove --workspace <ID>`. Outside Herdr, use native `git worktree add` and `git worktree remove`.
- **Interactive panes and supervision**: Herdr owns panes, tabs, splits, and observable agent runs. Use `herdr pane split` to arrange panes. Use `herdr agent start`, `herdr agent prompt`, and `herdr agent wait` to run and observe workers.
- **In-turn tools and subagents**: OMP owns the cognitive tool loop. Use OMP for file tools (`read`, `edit`, `write`), in-kernel execution (`eval`), turn-scoped headless subagents (`task`), and background services (`hub start`, `hub ps`, `hub logs`).
- **Architecture and Roadmap**: Read [README.md](README.md) before implementing components.

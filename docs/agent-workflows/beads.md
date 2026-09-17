# Beads repository constraints

Follow the companion policy in `.beads/PRIME.md`, loaded by native `bd prime`. It owns durable outcomes, high-level plans, acceptance criteria, and checkpoints. Keep execution todos and native memory in the harness.

- Issues use the `tb-` prefix. Preserve provenance and phase labels.
- Claim authorized work with `bd update <id> --claim`. Do not attach runtime labels or synthesize session actors.
- Serialize Beads mutations through one coordinating checkout. Sessions sharing a native actor do not have independent claim identities.
- The coordinator owns governing-bead updates, verification, and closure. Helpers report results without claiming or closing that bead.
- Beads uses embedded Dolt storage with file locking. The configured remote is `git+https://github.com/jeffhuen/tether-browser.git`.
- Tracking does not authorize Git operations, remote synchronization, or worktree cleanup. Follow [delivery](delivery.md) and `skill://herdr-workflow` under existing authority.

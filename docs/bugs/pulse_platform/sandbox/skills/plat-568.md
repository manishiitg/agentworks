[← sandbox / skills](index.md)

# PLAT-568: Custom skills can be installed again; only our policy files are protected

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | sandbox |
| Area | skills |
| Summary | Users could not install custom skills: the sandbox made the whole .agents/.claude/.codex/.cursor/.gemini/.pi folders read-only |

## What happened

## Fix

## Left

## What happened

Excellence, 2026-10-06: an agent running as a user's slot tried to install a skill (the `gws` installer writing `.agents/skills/gws-shared`) and got "Read-only file system". `pkg/common/managed_projection_guard.go` made the whole `.agents`, `.claude`, `.codex`, `.cursor`, `.gemini` and `.pi` folders read-only for the coding agent's shell, so nothing could be installed under their `skills/` folders (`npx skills add` writes `.agents/skills`). The folder permissions themselves were fine.

## Fix

Owner decision: protect our system files, not the whole folders. The guard now blocks the root instruction files (AGENTS.md, CLAUDE.md, GEMINI.md) and each CLI's policy files by name (settings, hooks, rules, commands, prompts, config, mcp.json, ...) plus whatever else already sits in those folders, and leaves the folders and their `skills/` alone. A projected skill carries an ownership marker and only those are removed by the adapters, so an installed skill survives.

## Left

- Not verified in a real sandbox: this Mac's Docker cannot create the namespaces, and the existing sandbox tests skip here. After the next deploy, as a slot user, check that `mkdir .agents/skills/x` works and that writing `.claude/settings.json` and `AGENTS.md` fails.
- A policy file that does not exist yet (for example `.claude/settings.local.json`) is not protected on the Linux backends, which skip a missing path. Before this change the whole folder was protected. If that matters, protect it from the launcher (create it empty first).
- Not deployed.

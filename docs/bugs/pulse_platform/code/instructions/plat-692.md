[← code / instructions](index.md)

# PLAT-692: Project instructions appended to the generated AGENTS.md

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | instructions |
| Summary | Code and Crew projects can keep their own PROJECT_INSTRUCTIONS.md; it is appended below the platform's generated instructions on every turn |

## What happened

The platform writes AGENTS.md, CLAUDE.md, GEMINI.md and each CLI's own instruction file every turn and the managed
projection guard stops tools from changing them. Owner (2026-10-07): a person should be able to add their own
section at the bottom without the platform replacing it.

## Fix

- `agent_go/pkg/projectinstructions`: renders `PROJECT_INSTRUCTIONS.md` (project root) under
  "## Project instructions (from PROJECT_INSTRUCTIONS.md, written by the project's owner)", capped at 32 KB with a
  note, neutralising the `agentworks-session-instructions` marker so user text cannot end the managed block early.
- `cmd/server/agent_profile_runtime.go`: Code and Crew turns read the file (also for Crew readers); its hash joins
  `agentProfileSessionKey`, so a change relaunches a retained CLI with resume (same as an identity change).
- `cmd/server/server.go`: added as the last `AddInstructions` section, after the profile prompt and secret names.
  All adapters project that one prompt, so every carrier (AGENTS.md block, Pi APPEND_SYSTEM.md, Cursor rules,
  `--system-prompt-file`) gets it. mcpagent's runtime tool routing and tool manifest are appended after it.
- Not in the managed guard: the person and the agent may edit it. Code and Crew Builder prompts say to put
  "remember this" project rules there, never in AGENTS.md.
- UI: Identity → General → "Project instructions" card (`ProjectInstructionsCard.tsx`), owners only.
- Test: `pkg/projectinstructions` `TestProjectInstructionsAppendAfterPlatformAndStayEditable` (order after the
  platform part across regeneration with the real `projectfile`, cap, marker, guard).

## Left

- Not verified live on a real Code/Crew chat (no local servers); check on the next deploy that an edit relaunches
  the retained CLI and the section shows in the session's AGENTS.md.

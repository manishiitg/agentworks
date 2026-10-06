[← goals / pulse / reviews](index.md)

# PLAT-046 — reviewer completion can reach the tool boundary with an empty verdict

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | pulse/reviews |
| Summary | typed Pulse reviewer tools |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P2
- **Owner:** typed Pulse reviewer tools
- **Source workflow:** Upwork

## Finding correction

The current storage implementation already trims and rejects an empty verdict,
so the claim that a blank verdict can be durably accepted is not reproducible
on current source. The public tool boundary did not state or validate this
clearly before invoking storage, however, which made failures harder for an
agent to diagnose and could differ on an older running binary.

## Hardening

`complete_pulse_review` now trims and rejects a blank verdict immediately with
an actionable error, then passes only the normalized value to storage. The
focused typed-tool test proves whitespace-only input is rejected before a valid
completion succeeds.

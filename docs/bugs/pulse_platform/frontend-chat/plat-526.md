[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-526 — The voice "Loading the voice model" bar came back on every workflow or Crew switch

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed; owner to check locally |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

Owner, local app: "when we change workflows or go workflow -> crew via ctrl + k... it keeps showing me the voice loading again and again".

## Cause

The app fetches `/api/capabilities` once at start. The server warms the voice model right after it starts (log 18:55:05 `[VOICE] engine ready in 1.511s`), so the snapshot can say `loading: true` for the
whole session. `MicButton` took its first state from that snapshot, so each remount (each workflow or Crew switch) put the "Setting up / Loading the voice model" bar up until its own 1 s poll saw ready.

## Done

- `frontend/src/voice/MicButton.tsx`: an in-flight snapshot is only a hint: the button starts `unknown` and asks `/api/voice/status` once; every status it reads is written back into the capabilities store,
  so the next mount starts from the truth. Type-check clean; no test (none existed; behaviour read and reasoned from the log).

## Left

- Owner check after restart: switch workflows / Crews; no voice loading bar once the model is ready.

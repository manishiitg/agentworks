[← platform / integrations](index.md)

# PLAT-122 — real microphone reads digital silence through every app on a dev machine

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | integrations |
| Area | integrations |
| Summary | P1 — blocks live verification of PLAT-120 (Video Studio voice |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — environment-level, not yet root-caused |
| Last synchronized | `2026-08-16` |

- **Priority:** P1 — blocks live verification of PLAT-120 (Video Studio voice
  dictation); not reachable from workflow code, external action required.
- **Owner:** none (OS/hardware/driver layer) — see
  [`voice_dictation_mic_captures_silence.md`](../../../voice_dictation_mic_captures_silence.md)
  for the full investigation this ticket indexes.

## Problem

On the reporter's dev machine, `agent_go/pkg/voicestt`'s server-side RMS
telemetry (`pcmRMS` in `cmd/server/voice_stt_routes.go`) shows every audio
chunk at `rms=0.0000` during real mic dictation — genuine digital silence
reaching the backend, not a transport or model defect.

## What was ruled out (full detail in the linked doc)

- The STT pipeline itself: proven correct via a raw WAV file streamed through
  the live WebSocket endpoint (correct transcripts), and a synthetic 440Hz
  tone fed through the actual browser capture code (real, varying PCM
  samples, decoded RMS ≈ 0.71).
- Wrong input device: a bare `getUserMedia` call in the reporter's own tab was
  granted the correct device (`MacBook Air Microphone (Built-in)`), returned
  `muted=false enabled=true`, and still read `maxPeak=0` over 2+ seconds of
  real speech.
- Browser-automation tooling serving a fake device: reproduced in a second
  browser window not under any automation control.
- SparkQuill's Electron app holding the input device: reproduced with it
  fully quit (`Cmd+Q`).
- A third-party noise-suppression HAL driver (e.g. Krisp): none present;
  `/Library/Audio/Plug-Ins/HAL/` has only Apple's own `ParrotAudioPlugin` and
  the (unused-here) Zoom virtual device.
- macOS Privacy & Security → Microphone: Chrome is enabled there.

## Remaining candidate, unconfirmed

macOS System Settings → Sound → Input has a level *meter* and a separate
*Input volume* slider. The meter can reflect raw hardware signal presence
even when the volume slider — the actual gain handed to apps via CoreAudio —
is near zero, which would explain real signal at the hardware level and
exact silence in every application. Asked the reporter to check; unconfirmed
as of this ticket.

## Acceptance

- Either the Input-volume slider is confirmed as the cause and the ticket
  closes with that as the documented resolution (a config/environment fix,
  not a code change), or
- a fresh elimination pass (see the linked doc's "Follow-up if this recurs"
  section) locates a real code-owned cause, and this ticket re-opens against
  the owning component instead of environment.

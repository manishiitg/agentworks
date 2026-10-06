[← sparkquill / integrations](index.md)

# PLAT-120 — Video Studio voice dictation (SparkQuill-parity streaming STT)

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | sparkquill |
| Area | integrations |
| Summary | `agent_go/pkg/voicestt`, `cmd/server/voice_stt_routes.go`, |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented_pending_live_reverify` — shipped, not yet confirmed against real speech |
| Last synchronized | `2026-08-16` |

- **Priority:** P2
- **Owner:** `agent_go/pkg/voicestt`, `cmd/server/voice_stt_routes.go`,
  `frontend/src/voice/*`, `agentprofiles.RuntimeCapabilities.Voice`

## What shipped

A shared streaming speech-to-text capability, generic across products rather
than hardcoded to Video Studio:

- **Engine**: `pkg/voicestt` wraps sherpa-onnx-go + a Nemotron-3.5 streaming
  ASR model (int8 ONNX, cache-aware FastConformer). One process-wide
  `Engine` (lazily loaded, pre-warmed at route registration so the first real
  click isn't blocked on model load), cheap per-connection `Stream`s.
- **Transport**: `GET /api/voice/stream?profile_id=...&token=...` — a
  WebSocket accepting raw PCM16 mono 16kHz binary frames, streaming back
  partial/final JSON transcripts. Query-param JWT auth (browsers can't set
  custom headers on a WS upgrade), same pattern as SSE and terminal
  live-attach.
- **Capability gating**: `agentprofiles.RuntimeCapabilities.Voice`, the same
  shape as `Browser`/`Secrets`. Video Studio opts in via `voice: preferred`
  in its `product.yaml`; the frontend loads capability state generically
  (`utils/agentProfileCapabilities.ts`) rather than special-casing any
  product.
- **Frontend**: `frontend/src/voice/pcmCapture.ts` (AudioWorklet-based raw PCM
  capture at 16kHz, ported from SparkQuill's `nativePcm.ts`),
  `useVoiceDictation.ts` + `MicButton.tsx` (ported to match SparkQuill's UX
  exactly per explicit user spec — level-reactive ring, floating "Listening"
  banner with live partial text, push-to-talk toggle that flushes the final
  transcript into the composer on stop).

## Bugs found and fixed while building this

- `websocket.Upgrader{}`'s default `CheckOrigin` rejected the frontend's
  cross-port dev requests (403).
- `statusCapturingResponseWriter` embeds `http.ResponseWriter` as an
  interface field, which only promotes methods that interface declares —
  `Hijack()` was missing, breaking any websocket route wrapped by the
  response-logging middleware whenever `API_REQUEST_LOG` is on (default).
  Latent bug affecting every websocket route, not just this one; fixed with
  an explicit forwarding `Hijack()`.
- Lazy `sync.Once` model load caused a 60+ second silent hang on the first
  real click; fixed with a pre-warm goroutine at route registration.

## Verification

- `pkg/voicestt/engine_live_test.go` (opt-in, real model): transcript text
  grows across chunks against a real `.wav` — passed.
- A raw Go WebSocket client streamed a real `.wav` through the live server
  end-to-end and got correct partial/final transcripts.
- A synthetic 440Hz tone through the actual browser capture code produced
  real, varying PCM (decoded RMS ≈ 0.71) — proves the JS capture → encode →
  transport path independent of any physical microphone.
- The rebuilt banner/level-ring UI was visually confirmed rendering correctly
  (pulse dot, "Listening" label, ring scaling with live level).
- `tsc --noEmit` and `npm run build` clean.

## Not verified yet

- **No confirmed end-to-end pass with real human speech.** Every real-mic
  attempt on the reporter's machine has returned `rms=0.0000` — tracked
  separately as [PLAT-122](../../platform/integrations/plat-122.md), which blocks this ticket's own
  closure.
- Live text appearing in the banner as real speech streams, and the final
  transcript landing in the chat composer on stop, are both implemented per
  the SparkQuill port but unconfirmed against real audio for the same reason.

## Acceptance

- A user speaks into a real microphone; partial text appears live in the
  banner as they talk; stopping delivers the final transcript into the
  composer input. Currently blocked on PLAT-122.

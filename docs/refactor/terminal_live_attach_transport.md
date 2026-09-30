# Design: Live-attach terminal transport (replace snapshot/replay mirror)

**Status:** ✅ **SHIPPED (on `main`), sole transport.** Control mode chosen (§3). The
snapshot/replay mirror is replaced for the selected live tmux pane. §§1–3 (why, target,
control-mode-vs-PTY-attach decision) remain accurate. §§4–9 are the *original* migration
plan and contain decisions that were **superseded during implementation** — read §0 first;
it lists exactly what changed.
**Author/driver:** terminal-stability refactor.
**Related:** `cli_live_input_unification.md` (chat input routing). This document now also covers native Terminal input. Debug/incident journal: `live_attach_app_vs_demo_debug.md`.

---

## 0. As-built (2026-07-02) — READ THIS FIRST

The shipped transport (`agent_go/cmd/server/terminal_live_attach.go`,
`agent_go/internal/liveattach/`, `frontend/src/components/TerminalCenter.tsx →
LiveAttachXtermPane`) matches §§1–3 but differs from the §§4–9 plan on these points:

1. **In-band control channel (the defining change; not in the original plan).** Every
   tmux command the transport needs — `resize-window`, the `capture-pane` seed/backfill,
   `#{history_size}` and cursor queries — is written to the control client's **own stdin**
   and answered in-stream between `%begin`/`%end` guards. `liveattach.Protocol` frames
   those replies (FIFO by command number; tmux serializes them with `%output`, so a reply
   is an exact ordering barrier). A viewer's byte channel is spliced into the broadcast
   **inside the scanner goroutine at its seed's `%end`**, so the seed and the live stream
   can neither overlap (duplicate frames) nor gap. There are **no out-of-band `tmux`
   subprocesses on the connect path** and **no drain timers**.

2. **`window-size manual` + explicit `resize-window`, NOT `window-size latest`.** §4.2 is
   **superseded.** The browser xterm grid is authoritative, but geometry is applied by
   pinning `window-size manual` at attach and issuing an in-band `resize-window` to the
   fitted grid (deduped). `window-size latest` (client-driven PTY size) was not used — the
   control client's PTY size is not authoritative enough across CLIs/macOS.

3. **Resize is in-band `resize-window`, not `pty.Setsize`.** The §2 diagram's
   `pty.Setsize(cols,rows)` note is superseded by the in-band command above.

4. **Manual `term.write`, not `AttachAddon`.** The frontend writes WS bytes to xterm
   directly. The first frame of every (re)connect is the backend's in-band **seed** (RIS
   reset + bounded scrollback history + current screen + cursor); it is run through
   `normalizeAnsiForEmbeddedXterm` (strips Claude Code's neutral bg-237 canvas fill, which
   otherwise renders as grey panels/bars); live `%output` after it is written verbatim.
   `FitAddon.fit()` is the **sole** sizing authority (no manual DOM-ruler override — that
   caused a double resize per layout tick). No `SerializeAddon`: reconnect re-seeds from
   the backend instead.

5. **Feature flag removed.** `RUNLOOP_TERMINAL_LIVE_ATTACH` (§6) no longer exists;
   live-attach is always on for the selected tmux terminal, gated only by a minimum tmux
   version check (2.9). The phased/flagged rollout in §6 is historical.

6. **History capture omits `-J`** (joining preserves trailing spaces → background fills
   become full-width grey bars) and queries `#{history_size}` first (tmux clamps
   `capture-pane -S … -E -1` into the visible screen when scrollback is empty, which would
   seed row 0 twice). **Slow viewers are dropped whole** (WS close → reconnect → fresh
   seed), not fed a holed stream.

7. **One live geometry owner per tmux pane.** A newly seeded viewer supersedes the
   incumbent and the backend closes the old viewer with WebSocket code `4001`. The old
   frontend does not reconnect automatically (that would create a resize/ownership
   ping-pong) and does not fetch the new owner's snapshot (it was captured at a different
   grid and would corrupt local wrapping). It keeps its existing frame until the user
   explicitly chooses **Take over**.

8. **List metadata and detail bodies are intentionally asymmetric.** The frontend's
   terminal-list poll owns lifecycle/process state. Detail and history fetches contribute
   body content only. Interactive main-agent panes continue streaming after a turn is
   `completed` while their process remains `live`; conflating turn completion with process
   closure caused the live WebSocket to close and reopen every polling cycle.

### Native main-agent Terminal mode (2026-09-30)

Main-agent live panes accept input directly. Static snapshots, workflow children,
and read-only run views retain their display-only behavior. The chat composer is
replaced with a Terminal toolbar while this mode is selected; its draft remains
in component state when the user returns to Chat.

- xterm preserves the CLI's alternate screen, mouse modes and application cursor
  keys. Its `onData` UTF-8 bytes and `onBinary` bytes travel as WebSocket binary
  frames. `send-keys -H` uses the existing control client's stdin, so typing does
  not spawn a tmux process for each key. Slash commands and Up/Down go to the CLI.
- Browser paste uses a `paste` control frame and tmux `paste-buffer -p -r`.
  tmux supplies bracket markers only when the native CLI requested them; this
  also works when the CLI enabled paste mode before the browser attached.
- Input remains disabled until the connection seed arrives and while
  reconnecting. Failed writes disable input and show a reconnect action. No
  keyboard bytes are queued or replayed after a disconnect. A superseded viewer
  cannot write queued input once it loses ownership.
- The seed restores alternate-screen, cursor, keypad and mouse flags from tmux.
  The original display-only panes suppress the same flags as before.
- A per-process native transcript observer survives browser view switches. It
  records actual accepted user rows, preserving repetitions and distinguishing
  them from `/query` inputs still waiting in the deferred-user hold. Enter itself
  can choose a menu item and therefore does not create a chat message.
- `Session.ObserveNativeInput` observes the already accepted turn without sending
  it again. Claude/Codex/Pi use transcript timestamps; Cursor pins the accepted
  blob boundary, Muse its accepted sequence, and AGY its accepted step index.
  Retained observers publish narration, thinking and paired tool events through
  the normal Chat stream. AGY's tool trail is published after it settles.
- Raw typing reserves the native draft against programmatic paste. A durable
  user row releases only its own submission version; a newer draft is preserved.
  Ctrl+C clears the reservation. Ctrl+U cannot prove that text after the cursor
  was cleared. Chat sends during a reserved draft return `423 terminal_draft_active`.
  Native Ctrl+C with no reserved draft also settles its current retained host
  watcher; a captured generation prevents a late acknowledgement from settling
  a newer turn. Normal foreground Runs retain their existing interruption path.

Validation uses real xterm key handling, a scratch tmux/WebSocket byte receiver,
provider transcript fixtures, native repeated-prompt/adoption tests, and race
tests for the broker and observation lifecycle. These tests do not contact live
coding-provider accounts. Terminal mode controls the coding CLI's pane; tmux
client prefix commands and window-management UI are outside this transport.

### Renderer robustness review (2026-09-30)

Live output goes through a bounded, ordered queue in front of xterm's async
parser (16 MiB and 4,096 queued frames). Overflow closes the connection and
re-seeds; bytes are never silently dropped while continuing the stream. A
column resize reseeds over the same socket, discards unparsed old-width frames,
and waits for the active parse before fitting the new grid. Late socket events, snapshot responses and seed
callbacks cannot alter a replacement connection, and input stays disabled until
the seed's terminal modes are parsed. Seeds cancel incomplete escape sequences
and discard incomplete UTF-8 from a disconnected stream.

The backend seed restores scroll regions, origin, wrap and insert modes after
painting cells. The server also bounds each viewer's queued output to 16 MiB.
Keepalive pings, attached-session access checks, and final close reasons are
preserved. Every WebSocket output/error write has a ten-second deadline;
writer failure closes the socket and cancels its input context. Font loading
forces a new fit even when the pane's box is unchanged. Metadata requests belong
to their session, and a transient metadata failure keeps the current pane mounted.
Saved snapshot refreshes use the same parser barrier and discard superseded
refreshes before resetting, so pending old output cannot mix with a new snapshot.

Regression coverage includes component lifecycle races, real xterm parsing of
fragmented Unicode/ANSI and partial-stream recovery, scrolling regions, output
overflow, a blocked WebSocket peer, and the existing scratch tmux transport tests.

Everything else below is the original design record, kept for rationale. Its
display-only input assumptions are superseded by the native-mode section above.

---

## 1. Why (the root cause)

The CLI runs inside a **detached** tmux session (`tmux new-session -d`, **no client ever attaches**). Because nothing is attached, the pane's live output is *reconstructed* for the browser **three separate times**:

1. **Adapter** `capture-pane` polling (200–250ms) → `StreamChunkTypeTerminal` snapshots.
2. **Backend** `pipe-pane` append-log + a screen-mode-aware prologue, repaint hacks, trim loop.
3. **Frontend** delta-vs-reset heuristics (`computeXtermWrite`), 3s re-probe polling, remount-on-switch, RIS reseed on resize.

Every terminal bug this cycle — litter, duplicated frames, stacked spinners, wrapped tables on resize, blank-on-resume — is a failure of that reconstruction. The fix is to **stop reconstructing** and let a real attached client + xterm render the live byte stream natively.

> Empirically confirmed by the end-to-end map (see "Reconstruction eliminated" below): ~10 distinct mechanisms exist solely to compensate for the detached session.

---

## 2. Target architecture

```
 tmux session (created -d, as today)            ── reused: creation/lifecycle/input
      │  PTY bytes
      ▼
 backend ATTACH STREAMER  (per SELECTED terminal)        ── NEW
   pty.Start("tmux -CC attach -t <session>")  (control mode; PTY needed for the client's own stdio)
   resize: in-band `resize-window -x C -y R` on the control channel   [as-built; NOT pty.Setsize — see §0]
      │  raw terminal bytes (live, in order, at the xterm's geometry)
      ▼
 WebSocket (binary, 1:1)                                  ── NEW
      ▼
 xterm.js  (AttachAddon writes the stream; FitAddon for size; SerializeAddon/capture for reconnect)

 INPUT (unchanged): chat live-input / debug keys → send-keys / paste-buffer  (xterm stays display-only)
```

**Core idea:** attach one real (read-only) tmux client per *selected* terminal, stream its PTY bytes over a WebSocket straight into xterm. xterm handles cursor motion, alt-screen, colors, spinners, wrapping natively — because it's receiving the actual terminal stream, in order, at a stable geometry. No log, no capture-poll, no delta/reset, no reseed.

Input keeps the **existing** `send-keys`/`paste-buffer` path (the CLI-live-input unification). xterm remains `disableStdin` — we never write to the attach client's stdin, which also sidesteps tmux prefix-key / interactive-client concerns.

---

## 3. Key decision: PTY-attach (primary) vs control-mode `-CC` (fallback)

| | PTY-attach (`tmux attach` in a PTY) | Control mode (`tmux -CC attach`) |
|---|---|---|
| Mechanism | Read the attached client's rendered PTY output | Parse a structured protocol (`%output`, `%layout-change`) |
| Bytes to xterm | The rendered terminal stream (what we want) | `%output` per-pane (also what we want) |
| Chrome/prefix | Tame with `status off` + single pane + **read-only** (never write its stdin) | None (protocol, not an interactive client) |
| Resize | `pty.Setsize` → client follows | size commands in protocol |
| Code we write | Minimal (ttyd is a near-blueprint; creack/pty does the PTY) | **A control-mode protocol parser** (no mature Go lib; iTerm2 is GPL → design-only) |
| OSS leverage | High (ttyd, GoTTY, creack/pty — all MIT) | Low (write our own parser) |

**Decision (revised after the Phase 0 PoC): CONTROL MODE is primary.** The PoC (tmux 3.6a, macOS) found read-only PTY-attach fails the two cases that matter most for this app:
1. **Dead-pane banner leak** — with `remain-on-exit` on (workflow-step / completed terminals), tmux injects `Pane is dead (status N, <timestamp>)` into the pane bytes. We want the final frame, not tmux chrome.
2. **Alt-screen wrap kills scrollback** — a read-only attach wraps the whole stream in tmux's OWN alternate buffer, so our inline/normal-buffer CLI output lands in xterm's *alternate* buffer → **no xterm scrollback** (≈ the snapshot behavior this refactor escapes).

Control mode delivers the app's **exact bytes with zero chrome**, reports exit/resize as clean structured events (`%window-renamed … dead`, `%layout-change`), and preserves the normal/alt-screen distinction → native scrollback — all for a **~40-line `%output` parser** (octal `\ooo` decode + line dispatch). Its one tradeoff (no free repaint on resize) is a non-issue: apps repaint on SIGWINCH and we use capture-pane/SerializeAddon for (re)connect backfill (§4.3). **PTY-attach stays the documented fallback** (ttyd-blueprint, free `Setsize` repaint) for any future case where chrome doesn't matter.

---

## 4. Constraints / assumptions (locked with product owner)

1. **Single viewer per terminal** → 1 attach client ↔ 1 WebSocket ↔ 1 xterm. No fanout, no multi-client size negotiation.
2. **xterm grid is the authoritative size.** ⚠️ **SUPERSEDED (see §0.2):** the as-built
   transport does **not** use `window-size latest`. It keeps **`window-size manual`** and
   applies the browser grid with an explicit in-band **`resize-window`** (the control
   client's own PTY size is not authoritative enough). This bullet's original claim — flip
   to `window-size latest` — was not adopted. (Note: control mode still needs a **PTY for
   the client's own stdio** — plain pipes fail `tcgetattr` — so creack/pty / go-pty is used
   regardless of transport.)
3. **Live stream renders; `capture-pane` (or xterm `SerializeAddon`) is used only for (re)connect backfill** — never for live rendering.
4. **Platforms:** macOS + Linux first-class. **Windows via WSL2** (= the Linux path; tmux already required, so no new dependency). **Native Windows out of scope** — blocker is tmux (no native equivalent), not the PTY. Use a **portable PTY lib** (e.g. `aymanbagabas/go-pty`: creack/pty on Unix, ConPTY on Windows) so the PTY layer keeps ConPTY optionality cheaply.
5. **Attach only the SELECTED terminal.** A chat/session can have several terminals (`main:<session>`, `workflow-step:<…>`), each its own tmux session. Only the focused one gets a live attach+WS; the rail/others stay low-freq `capture-pane` thumbnails/metadata. Switch → detach old, attach new.
6. **Minimum tmux version** pinned + checked at startup (control-mode/attach behavior varies by version; we already cope with version variance today).

---

## 5. OSS leverage (all MIT unless noted)

- **ttyd (MIT)** — near-blueprint for PTY→WS→xterm (incl. readonly); borrow its WS message framing (input/resize/output). Not drop-in (no session/cost/workflow integration).
- **creack/pty / aymanbagabas/go-pty (MIT)** — Go PTY primitive; `pty.Start`, `Setsize`. go-pty for the portability hedge.
- **xterm addons (MIT)** — **AttachAddon** (wire xterm ⇄ WS), **FitAddon** (size, already used), **SerializeAddon** (snapshot xterm buffer for reconnect).
- **GoTTY (MIT)** — Go-specific reference for the WS↔PTY relay; documents tmux for shared sessions.
- **WebSocket lib** — `coder/websocket` or `gorilla/websocket`.
- **iTerm2 (GPL-2)** — gold-standard *design* reference for control-mode (fallback). Study, **do not copy**.
- **node-pty / WeTTY** — conceptual only (Node).

---

## 6. Phased migration (feature-flagged, parallel to the current transport)

The current snapshot/replay transport stays the **default** until the new one is proven. Flag: `RUNLOOP_TERMINAL_LIVE_ATTACH` (off by default).

- **Phase 0 — Spike (throwaway): ✅ DONE.** Compared read-only PTY-attach vs control-mode on tmux 3.6a/macOS (scratchpad `ptypoc/`). Result: control mode chosen (§3) — PTY-attach leaked the dead-pane banner and wrapped output in its own alt-screen (no scrollback). Re-verify the same on Linux during Phase 1.
- **Phase 1 — Backend attach streamer + WS endpoint (behind flag):** per-selected-terminal attach streamer (portable-pty), read-only; `GET/WS /api/terminals/{id}/stream`; lifecycle (open on select, close on deselect/disconnect); `pty.Setsize` on resize; reconnect backfill via `capture-pane` seed then live. Reuse `tmuxsize`, session registry, `tmuxexec`.
- **Phase 2 — Frontend xterm over WS (behind flag):** xterm + AttachAddon bound to the WS; FitAddon → `Setsize`; reconnect → SerializeAddon/capture seed. Keep `disableStdin`. Behind the flag, side-by-side with the existing polling path.
- **Phase 3 — Input + selection + rail:** confirm existing `send-keys`/`paste-buffer` input works unchanged with an attached session; switch = detach/attach; rail thumbnails via low-freq capture.
- **Phase 4 — Verify all cases** (flag on, internal): new chat (pi-cli + codex), resume, busy steer, idle follow-up, completed→new turn, terminal switch, server restart/reconnect, workflow-step terminals, resize/wide-table. macOS + Linux + WSL.
- **Phase 5 — Flip default + soak.**
- **Phase 6 — Delete the old transport:** remove `terminal_pipe_recorder.go`, adapter capture-poll streaming, `terminalReplay.ts` + `applyContent` delta/reset, the 3s re-probe + dual-source merge + `ResetForResize`/repaint hacks, `[SPINNER_DEBUG]`.

Each phase ships independently; the flag means a regression can't reach users mid-build.

---

## 7. What gets replaced vs reused (from the end-to-end map)

**Replace / delete:**
- `agent_go/cmd/server/terminal_pipe_recorder.go` (entire byte-mirror: prologue, pipe-pane, `forceTerminalPaneRepaint`, trim, `ResetForResize`).
- Adapter snapshot streaming + capture-poll bodies: `streamCodex/PiTerminalSnapshot`, `captureCodex/PiPaneForDisplay`, the `waitFor…` capture loops (snapshot portions only).
- `frontend/src/components/terminalReplay.ts` + `applyContent` delta/reset branches; the 3s re-probe + rail/detail polling; dual-source merge (`terminal_routes.go` detail `:218-259`); resize reseed/repaint.
- `[SPINNER_DEBUG]` / `inspectTerminalPaneRuntimeStats` geometry diagnostics.

**Reuse (transport-agnostic):**
- tmux **session creation/lifecycle** (`startCodex/PiTmuxSession`, `remain-on-exit`, kill, owner→tmux registry). `window-size manual` likely flips to client-driven.
- `coding_agent_contract.go` capability registry + `coding_agent_live_input.go` dispatch (add a transport flag).
- **Input path** (`send-keys`/`paste-buffer`, `/input`, `/key`, `tmuxKeyName`).
- `internal/terminals/store.go` Snapshot model + `terminal_id` keying (`main:`/`workflow-step:`) + `Active/State/Status` + `SessionHasBusyCodingTmux` + live-input routing.
- `internal/tmuxsize`, `tmuxexec`, `tmuxlaunch`.
- xterm shell in `TerminalCenter.tsx` (FitAddon, `key={terminal_id}` remount, theme) — fed by the stream instead of polls.
- `services/api.ts` `resize`/`size-hint`/`input`/`key`; `useSessionTerminals` presence (useful at connect).

---

## 8. Risks / open questions

1. ~~Read-only PTY-attach leakage~~ **RESOLVED (Phase 0):** it leaks — dead-pane banner + alt-screen-wrap (no scrollback). → control mode is primary (§3). Remaining: re-verify dead-pane wording / alt-screen behavior / DA-OSC probe set on Linux + pin a minimum tmux version.
2. **Reconnect backfill fidelity:** capture-pane vs xterm SerializeAddon for restoring the exact pre-disconnect screen, then seamless resume of the live stream without a doubled frame.
3. **tmux version variance** across brew (macOS) / distro (Linux) / WSL — pin a minimum + startup check.
4. **WS lifecycle** under the existing auth/session model (the app's API auth, session ownership).
5. **Selection churn cost:** attach/detach on every terminal switch — ensure it's cheap (it is: attach is fast; rail stays capture-based).
6. **Workflow-step terminals** that complete/exit (`remain-on-exit`) — attach to a dead-but-retained pane shows the final frame; confirm that's the desired read-only behavior.

---

## 9. Recommended stack (summary)

**Control mode** (`tmux -CC attach` per selected terminal) + a ~40-line `%output` parser (octal `\ooo` decode + line dispatch; route `%layout-change`/`%window-renamed`/`%exit` to events) + `go-pty`/creack-pty for the client's own stdio + `coder/websocket` + xterm `AttachAddon`/`FitAddon`/`SerializeAddon`; session flipped to `window-size latest`. **PTY-attach (ttyd-blueprint) is the documented fallback.** Phased behind `RUNLOOP_TERMINAL_LIVE_ATTACH`, current transport stays default until Phase 5.

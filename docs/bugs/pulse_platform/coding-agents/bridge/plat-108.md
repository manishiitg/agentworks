[← platform / coding-agent-bridge](index.md)

# PLAT-108 — coding-agent transcripts are located by proximity instead of identity

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | coding-agents |
| Area | bridge |
| Summary | was one instance of a recurring class, not a one-off. |

| Field | Value |
|---|---|
| Status | `partially implemented` — layer 1 complete for the Codex interactive transport 2026-08-15 (retained, wrapped, completion detection, and structured streaming all bound to the session's own rollout); layers 2 and 3 pending |
| Priority | P1 |
| Owner | coding-agent transcript identity contract and certification |
| Reported | 2026-08-15 |
| Related | [PLAT-102](plat-102.md), [PLAT-103](plat-103.md), [PLAT-105](plat-105.md), [PLAT-106](../../app/ui/plat-106.md)), [PLAT-116](plat-116.md) |

## Problem

PLAT-106 was one instance of a recurring class, not a one-off. The same defect
has now been found and fixed independently in two adapters, and the rule that
produced it is still reachable in a third place.

**The class:** a conversation's transcript is located by *proximity* — working
directory plus newest modification time — rather than by the conversation's own
*identity*. Whenever two sessions share a directory, the lookup can return the
other conversation's transcript.

A workflow's interactive Chat and its scheduled run always share a directory.

## Evidence that this is a class, not an incident

Every provider contract **already declares** that its transcript is addressed by
a session identity:

| Provider | `TranscriptPathTemplate` |
|---|---|
| claude-code | `~/.claude/projects/*/<session-id>.jsonl` |
| codex | `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<session-uuid>.jsonl` |
| cursor | `~/.cursor/chats/<md5(cwd)>/<agentId>/store.db` |
| pi | `.../*_<session-id>.jsonl` |

Yet the adapters diverged:

- **claude-code** passes `nativeSessionID` into its reader — bound;
- **pi** passes `nativeSessionID` — bound, and does not even need the directory;
- **cursor** uses `knownNativeSessionID` directly, falling back to a directory
  guess only on a genuinely first turn. Its code carries a comment describing
  this exact bug found live: a nested one-shot session's `store.db` was selected
  because it "happened to commit its async write more recently", which
  "permanently glu[ed] the conversation's persisted native_session_id onto an
  unrelated one-shot session, so every LATER turn kept resuming the wrong one";
- **codex** used working directory + recency only, and its session struct stored
  no native identity at all until PLAT-106.

So the correct rule was known, written down in the contract, and learned the
hard way in Cursor — and none of that prevented Codex from shipping without it.
Nothing enforced the invariant, so it held only where someone remembered.

## Codex interactive transport — bound 2026-08-15

All four paths in the interactive transport now resolve **this session's own**
rollout:

| Path | Before | Now |
|---|---|---|
| retained-turn final answer | cwd + newest mtime | bound (PLAT-106) |
| wrapped-turn final answer | cwd + newest mtime | bound (PLAT-106) |
| usage + thread ID | independent cwd re-scan | reads the same bound rollout |
| completion detection | cwd + newest mtime | bound resolver |
| live structured streaming | cwd + newest mtime | bound resolver |

**Completion detection and streaming were the more serious two.** A tracker on
the wrong rollout can declare this turn complete from *another* conversation's
`task_complete`; a stream on the wrong rollout tails another conversation's
assistant text and tool calls into this session.

### A deadlock this work had to avoid

`session.mu` is held for the entire `GenerateContent` body — acquired when the
session is claimed, released by `releaseCodexInteractiveSession` in a defer. Go
mutexes are not reentrant, so any binding helper that locks would deadlock the
live path. **Unit tests do not reach it** (they never drive a real tmux Codex
session), so neither the compiler nor the suite reports it. This was hit and
fixed during implementation.

The resolution is two-part:

- `resolveCodexRolloutPathLocked` for callers already holding `session.mu`;
- `codexRolloutResolverForSession` snapshots the thread ID and the set of
  rollouts other sessions have claimed, returning a **lock-free** closure the
  tracker and stream can call from inside the poll loop.

This is a general hazard for anything else that reaches for session state from
inside a turn, and layer 2 should encode it in the interface rather than leave
it to each caller to rediscover.

### Still unbound

`newCodexTurnCompletionTracker(..., nil)` in the **structured** adapter: a
one-shot `--json` process has no persistent-registry entry and therefore no
thread identity to bind. Still exposed if two structured runs share a working
directory. `findCodexRolloutByWorkingDirUnsafe` remains for that caller and for
the status line, so it cannot yet be deleted.

> **Relation to [PLAT-105](plat-105.md).** A completion tracker bound to the
> wrong rollout was a live candidate explanation for "provider captured its
> final response but the turn never settled". That candidate is now removed for
> the interactive transport, which usefully narrows PLAT-105 rather than
> confounding it — but PLAT-105 must still be confirmed against a real run
> before this is claimed as its cause.

## Required repair

### Layer 1 — make the ambiguous primitive unusable by accident

*Partially landed 2026-08-15.* `findCodexRolloutForTurn` is renamed to
`findCodexRolloutByWorkingDirUnsafe` and documents that working directory is not
an identity, so every remaining call site announces itself. The false comment on
`codexTurnCompletionTracker` claiming interactive sessions have unique working
directories is corrected.

Remaining: bind the three call sites above, then delete the unsafe function so
the rule cannot be reintroduced.

### Layer 2 — promote transcript location from metadata to an enforced interface

`TranscriptPathTemplate` and `AdapterReadsTranscript` are currently descriptive
metadata. Replace the per-adapter lookups with one contract capability:

```text
Bind(ownerSessionID) -> transcriptHandle     // claim identity once
Read(handle, turnStart) -> []MessageContent  // no search, no recency, no cwd
```

Rules:

- a handle may only be claimed when no other live session holds it;
- a provider with a genuine first-turn discovery window (Cursor documents one)
  uses a single shared, audited fallback that takes an exclusion set of
  already-claimed transcripts — not four independent directory scans;
- a new adapter cannot compile without answering "how do you identify your own
  conversation?", which is the question Codex never had to answer.

### Layer 3 — certify it

Add a required certification, `CertTranscriptIsolation`: two concurrent sessions
in the **same working directory** each read back their own final answer.

Gate it on `AdapterReadsTranscript`, **not** on transport.
`RequiredP0CodingAgentCertificationIDs` currently returns `nil` for any non-tmux
contract, so structured-transport providers have no P0 floor at all; scoping
this cert to tmux would repeat the very mistake of covering only the case in
front of us.

This is the highest-leverage layer: it is the only one that fails for code
nobody has written yet. It would have caught Cursor's instance and Codex's, and
it will catch the next adapter's.

## Acceptance

1. No Codex code path selects a transcript by working directory alone;
   `findCodexRolloutByWorkingDirUnsafe` no longer exists.
2. Completion detection and structured streaming are bound to the session's own
   rollout, verified by a test with two sessions in one directory.
3. Transcript location is a contract capability implemented by every adapter
   with `AdapterReadsTranscript`, not per-adapter ad-hoc lookup code.
4. `CertTranscriptIsolation` is a required certification for every such adapter
   and is a real E2E.
5. A regression proves a session never adopts another session's native/thread ID
   — the "permanently glued to the wrong session" failure Cursor recorded.

## Note on verification

Every instance of this class passed its tests while broken. The invariant worth
carrying (already drafted in PLAT-105's IC-11) is that **a test must reach the
state under test through the product path, never by constructing it**. A test
that builds the state directly, or that asserts current behaviour because it is
current, will certify the bug.

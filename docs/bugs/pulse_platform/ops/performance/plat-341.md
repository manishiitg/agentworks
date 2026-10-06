[← platform / performance](index.md)

# PLAT-341 — Native transcript recovery replay saturated the RTS agent CPU

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | ops |
| Area | performance |
| Summary | records the RTS performance regression where 13 durable transcript-recovery markers were reconciled every 30 seconds forever and the LCS merge repeatedly normalized full message text inside every matrix comparison. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `CPU containment deployed; first structured-stream cutover slice verified locally, deployment pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P0 production performance / chat durability` |

## Report and production evidence

The operator reported that Ctrl+K and workflow switching on RTS had become
slow. Read-only inspection of `video.realtrainingsys.com` found the agent using
roughly 90% CPU over its lifetime and 147% CPU during a five-second sample on a
two-vCPU host. Memory and disk capacity were healthy.

A 15-second production CPU profile attributed 55.31% of all samples to
`replayPendingNativeTranscriptRecovery` and its reconciliation worker. Within
that path, `builderConversationMessageKey` accounted for 50.66% cumulative CPU,
`strings.Fields` for 33.80%, and garbage-collection work for 12.22%. RTS had 13
durable native-recovery demands, with the oldest dating to 2026-09-17.

The same inspection found no Video Studio-specific log rotation. At the time of
inspection, `agent.log` was approximately 821 MB and `workspace.log` was
approximately 1.8 GB. This log volume increased I/O and disk usage but was not
the primary CPU hotspot in pprof.

## Cause

Native transcript recovery is the durability fallback that repairs structured
AgentWorks chat history from a coding CLI's native transcript after a missed
completion, disconnect, or restart. The recovery feature is required; removing
it would reintroduce disappearing completed replies.

Two implementation details combined into the production regression:

1. Durable demands intentionally remained `unresolved` because native adapters
   do not expose the application turn ID needed for a strict delivery receipt.
   The fallback scanner consequently replayed every historical marker every 30
   seconds forever. Four recovery workers could perform expensive transcript
   reconciliation concurrently.
2. The LCS history merge normalized both messages inside every dynamic-
   programming matrix comparison. Normalization walks and allocates for all
   message text, changing the practical cost from O(n*m) comparisons to
   O(n*m*message-size) repeated string work. Long builder transcripts made a
   single replay expensive; perpetual concurrent replay kept the server hot.

Webhook bursts increased concurrent transcript activity and exposed the defect,
but they were not the underlying cause.

## Fix

- Precompute the normalized key of every persisted and native message once
  before the LCS pass. Matrix and reconstruction comparisons now use those
  cached keys. Equality checks use the same precomputed-key path.
- Persist attempt count, last-attempt time, and next-attempt time on durable
  recovery markers.
- Retry with bounded backoff: 1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours,
  and 12 hours, with at most seven periodic attempts and a 24-hour maximum age.
  Legacy markers older than the age limit are marked exhausted without another
  transcript merge.
- Mark unsupported providers terminal instead of retrying them.
- Limit the low-priority historical recovery batch to one worker. The immediate
  post-completion reconciliation window remains unchanged.
- Prevent an older in-flight retry from overwriting a newer demand for the same
  owner/session/workspace identity.

The policy remains fail-safe for chat durability: newly completed turns still
receive the existing immediate bounded reconciliation window, and durable
fallback survives restarts. The change bounds only repeated historical work.

## Remaining architectural problem

The deployed fix bounds the production cost, but it does not make native
transcript recovery a simple or authoritative protocol. The recovery path still
has to coordinate all of the following concerns:

- an immediate post-completion retry window and a separate restart-safe
  periodic scanner;
- a filesystem journal plus process-local `pending` and `inFlight` state;
- provider-specific native transcript discovery and parsing;
- ownership, session and workspace validation before recovered content can be
  published;
- concurrent canonical-history writes and protection against an older attempt
  overwriting a newer recovery demand; and
- an ordered LCS merge based on normalized role and message text because the
  native adapters do not expose an application turn ID.

That last constraint is the fundamental source of fragility. Normalized text is
useful recovery evidence, but it is not a delivery identity. Legitimately
repeated messages, provider rewrites, partial streaming content and structured
tool rows all make text-based reconciliation heuristic. A durable demand
therefore cannot be marked `resolved` from an exact provider receipt; after the
bounded fix it eventually becomes `exhausted` instead. The implementation is
now operationally safe, but it remains a medium-to-high maintenance-risk
fallback and should not become another routine source of Formatted Chat truth.

## Long-term design

Formatted Chat should have one canonical ingestion path:

```text
provider/CLI adapter
  -> structured bridge event with stable owner, session and turn/message IDs
  -> append-only canonical AgentWorks chat history
  -> Formatted Chat projection
```

Tmux remains the required CLI process host and raw Terminal surface, but its
screen transcript does not feed the frontend directly. Provider adapters own
the conversion of native activity into structured events. The structured
bridge must persist an acceptance checkpoint and a completion checkpoint with
stable identities, so an interrupted or restarted server can determine exactly
which turn is missing without comparing whole conversations.

Native transcript recovery remains available only as an emergency repair path:

```text
missing structured completion checkpoint
  -> read the bounded native transcript tail for that provider turn
  -> normalize it once into structured events
  -> append idempotently using the stable turn/message IDs
  -> persist `resolved` and stop retrying
```

The recovery worker should consume explicit durable jobs rather than rescan a
directory of unresolved history markers. A job has a terminal state
(`resolved`, `unsupported`, `exhausted` or `corrupt`), an attempt lease, bounded
backoff and a recorded reason for every transition. Only one worker may own a
job at a time, including across processes. Reconciliation should operate on the
missing turn or bounded transcript tail, not perform an O(n*m) whole-history
merge during routine recovery.

The cutover is complete when:

1. every supported retained CLI emits stable acceptance and completion IDs;
2. Formatted Chat reads only canonical structured history;
3. a native recovery job can prove and persist `resolved` for an exact turn;
4. repeated user text and structured tool rows are recovered idempotently;
5. restart, disconnect and late-transcript-flush tests require no whole-history
   polling or frontend-native transcript merge; and
6. production telemetry distinguishes normal structured delivery from rare
   native repair, including queue depth, attempt count, terminal reason and
   repair duration.

Until those conditions hold, the bounded implementation remains necessary and
must not be removed: it protects completed replies from disappearing after a
missed completion, disconnect or restart.

## 2026-09-21 structured-stream cutover implementation

The first cutover slice now replaces two important pieces of the routine
reconciliation path:

- Every accepted semantic structured event is appended to
  `structured-chat-events.sqlite` before it is published to the in-memory store
  or SSE subscribers. Live-only `streaming_start`, `streaming_chunk` and
  `streaming_end` events retain ordered in-memory sequences without paying a
  synchronous SQLite transaction per token; the next durable boundary
  preserves the resulting sequence gap. A unique `(session_id, event_id)`
  constraint makes durable replay idempotent. A new server process hydrates the
  bounded durable tail and continues the sequence instead of starting an
  unrelated stream at one.
- A retained turn whose canonical `unified_completion` contains a final reply
  now appends that reply directly to the AgentWorks conversation, records an
  exact `structured_turn_checkpoints[turn_id]`, and persists the completion in
  `ui_events`. Replaying the same turn is a no-op; an identical reply produced
  by a different turn is preserved. Whole-history native reconciliation runs
  only if the structured completion has no final response or this direct append
  fails.

Focused tests cover journal sequencing, restart hydration, duplicate event
replay, bounded chronological tail loading, exact-turn completion idempotency,
legitimate repeated replies across different turns, live-only chunk bypass,
sequence gaps across transient events, status reads without disk hydration,
explicit removal without resurrection, and journal size/count telemetry.

The bridge keeps an upstream `event_id` when a producer supplies one, otherwise
uses stable turn/correlation/span identities where their semantics are safe,
and only then falls back to a hash of the structured envelope. That hash is
best-effort for byte-identical legacy replay, not a guarantee for a producer
that reconstructs an event with a new timestamp. Current `mcpagent` constructors
do not all assign `BaseEventData.EventID`; universal upstream IDs remain a
follow-up. An event is never published to SSE when its required journal append
fails. Tmux remains unchanged as the CLI process host and raw Terminal source;
this cutover only removes it from the normal Formatted Chat persistence path.

Durable append latency at or above 50 ms is logged with session and event type.
Every five minutes the server logs journal bytes (including WAL/SHM) and event
count, elevating the record to `warning` at 512 MiB. Token chunks no longer
drive journal growth. Destructive compaction remains deferred until canonical
conversation projection exposes a safe checkpoint watermark; arbitrary age or
size deletion would weaken the durability guarantee.

Lazy hydration is limited to explicit history/terminal restoration and active
event paths. `GetSessionStatus` is memory-only. `RemoveSession` now leaves an
in-process tombstone so a subsequent poll or new event cannot resurrect the old
tail; cleanup follows the same non-resurrection behavior.

This is not yet the entire cutover. Some retained CLI adapters still obtain the
final response used by `unified_completion` from a provider-native sidecar, and
legacy/read-time transcript catch-up remains for conversations created before
structured checkpoints. Follow-up work must propagate one stable turn ID into
each provider-native record, convert emergency recovery into an exact-turn job
with a terminal `resolved` state, and define journal compaction after canonical
conversation projection. Until then, the bounded native fallback stays enabled.

## Verification and operational follow-up

- Focused native transcript, builder conversation, and recovery concurrency
  tests pass.
- Structured-journal, event-bridge, retained-completion, restart replay and
  publish-after-persist tests pass. The broader server suite still has one
  unrelated existing prompt-budget failure: the assembled interactive workshop
  prompt is 24,412 bytes against a 24,000-byte ceiling.
- Regression coverage verifies backoff, expiry, unsupported-provider terminal
  state, bounded worker concurrency, and preservation of newer demands.
- `git diff --check` passes.
- Deployed to RTS in release `6c47129-20260921082037`. All three services and
  the public HTTP endpoint passed health checks. The newly restarted agent was
  at 18.4% CPU in the initial process sample, versus the prior sustained
  saturation; a longer steady-state profile remains useful follow-up evidence.
- Rootless Video Studio now installs a low-priority user timer that checks logs
  every ten minutes, rotates at 100 MB with `copytruncate`, and retains seven
  compressed generations. Production-debug workspace messages are opt-in, shell
  diagnostics persist only command length plus a short SHA-256 fingerprint, and
  the unused agent `--log-file` flag is removed so systemd owns one clear log
  path. The registry-side hot-path suppression shipped in `mcpagent` commit
  `22ff53a` (included on `main` by merge `ee12433`).
- The first production rotation completed successfully during deployment. The
  active logs dropped to approximately 21 KB (agent) and 94 KB (workspace);
  the two original generations are retained intact for delayed compression.

## Register notes

[PLAT-341](plat-341.md) records the RTS
performance regression where 13 durable transcript-recovery markers were
reconciled every 30 seconds forever and the LCS merge repeatedly normalized
full message text inside every matrix comparison. Production pprof attributed
55% of agent CPU to that recovery path. Message keys are now precomputed,
historical retries use bounded backoff/age/attempt limits with one worker, and
older attempts cannot overwrite newer recovery demands. Focused regressions
pass. Production logging now also has bounded rotation, opt-in debug chatter,
safe shell-command fingerprints, and deduplicated registry diagnostics. The
runtime is protected by these bounds, but the ticket now records the remaining
architectural risk and long-term cutover: stable structured turn/message IDs as
the sole Formatted Chat source, with native transcripts retained only as an
idempotent, terminal-state emergency repair path rather than a whole-history
polling and text-matching mechanism. RTS
release `6c47129-20260921082037` is healthy and completed its first rotation;
the active agent/workspace logs are now small. Longer steady-state profiling
remains pending. The first local cutover slice now durably journals semantic
structured boundaries before SSE publication and persists retained structured
completions by exact turn checkpoint; token streaming stays live-only and does
not synchronously commit to SQLite. Slow-append and journal-size telemetry are
included, session removal no longer resurrects a durable tail, and status reads
do not trigger hydration. Native whole-history recovery is skipped when the
structured append succeeds. Universal provider event IDs, exact-turn emergency
jobs, checkpoint-safe compaction, deployment and live acceptance remain
pending.

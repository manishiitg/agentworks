[← coding-agents / sessions](index.md)

# PLAT-334 — Busy multiline tmux input can become a false durable 409

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | records the Claude Code incident where a multiline paste chip was already visible during an active repainting turn, but whole-pane stability never occurred, Enter was never sent, and the durable submission remained a 40… |

| Coordination | Value |
|---|---|
| Priority | P0 |
| Assigned agent | Codex |
| Ticket state | Claude fix `36f1e19` pushed and focused P0 locally verified; app deployment, live acceptance, and all-provider P0 matrix pending |
| Last synchronized | 2026-09-19 |
| Affected boundary | Claude Code tmux live input, durable chat-submission reconciliation, coding-CLI P0 certification |

## Incident

The `salesoutreach` Builder chat rejected this follow-up:

```text
for this
Manish Prakash <manish@excellencetechnologie.com>
Sat, Sep 12, 6:42 PM (7 days ago)
to herath.veloxdy
```

The UI reported:

```text
Request failed with status code 409
Submission ab23b1f9-45e2-42aa-a04f-7b1d2147df31 is durable but
delivery is uncertain. Reconcile this submission before sending it again.
```

Incident identity:

- application session: `c7e88c62-f59a-4b37-9ff3-57c1366c5df5`;
- native Claude session: `8826ba03-e3aa-43a7-a267-7f3115836694`;
- tmux session: `mlp-claude-code-1789811050282315000-6570bffa`;
- durable receipt time: `2026-09-19T10:41:36.392462Z`;
- local request window: `2026-09-19 16:11:31–16:11:36 Asia/Kolkata`.

This was not the retained-tmux reaper failure fixed immediately before it. The
tmux session was alive and Claude was running a tool. The pane already showed:

```text
✳ Billowing… (45s · thinking with high effort)
❯ [Pasted text #1 +3 lines]
  paste again to expand
```

The adapter nevertheless returned:

```text
Claude Code multiline live input did not settle before submit:
timed out waiting for Claude Code prompt paste
```

Claude's native transcript subsequently advanced through
`2026-09-19T10:43:20Z`, but it never contained the exact follow-up. That is
provider-native evidence that the prompt was pasted into the composer but Enter
was never sent.

## Root cause

Claude converts bracketed multiline input into a `[Pasted text #N +M lines]`
attachment chip before it can be submitted safely. The adapter correctly waited
for that conversion, but its settlement check required the entire captured pane
to remain byte-for-byte stable for 900 ms.

During an active turn, Claude continuously repaints its activity spinner and
elapsed time. The whole pane therefore never stabilizes even though the paste
chip itself is already complete. The five-second wait expired before the code
reached `tmux send-keys ... Enter`.

The backend could not distinguish this known pre-submit failure from a failure
after Enter, so it persisted a conservative `delivery_uncertain` receipt and
returned 409. The original reconciliation logic only reopened a receipt when
the final transcript ended before the receipt timestamp. It did not accept the
stronger evidence present here: a closed transcript continued beyond the
receipt while the exact human prompt remained absent.

## Why P0 missed it

The provider certification suite had separate proof for:

- large or multiline prompt paste (`CertPromptPaste`);
- live input during a busy turn (`CertBusyLiveInput`);
- slow-tool activity and false-idle protection.

The paste contract used an otherwise stable prompt, while the busy-live-input
contract used a short single-line follow-up. No required test combined all four
conditions:

```text
active tool call
+ repainting TUI
+ multiline/attachment paste
+ verified submit and exactly-once processing
```

`CertPromptPaste` is also part of the full tmux promotion bar but not the
required P0 certification list. A provider could therefore pass P0 with
independent busy-input and paste tests while their composition remained broken.

## Local correction

### Claude transport

Once `[Pasted text` is visible, the attachment conversion is complete. The
Claude adapter now treats that chip as prompt-local settlement evidence and
continues to Enter and the existing post-submit verification without waiting
for unrelated activity rows to stop repainting.

Focused regression:

```text
TestClaudePromptPasteChipSettlesWhileActiveTurnRepaints
```

The deterministic fixture exposes an active spinner and visible multiline
paste chip through a fake tmux pane. It fails if settlement waits for the whole
pane stability window or returns a pre-submit timeout.

### Durable receipt reconciliation

For a closed provider session with no active turn or live tmux, the final native
transcript now proves non-delivery whenever the exact human prompt is absent.
That proof is valid whether the transcript ended before the receipt or continued
beyond it. Missing transcript evidence, a live tmux, an active turn, or an exact
matching human message keeps the receipt uncertain and prevents a duplicate.

Focused regressions cover:

- final transcript predating the receipt with the message absent;
- final transcript continuing beyond the receipt with the message absent;
- final transcript containing the exact message;
- no proof remaining a 409;
- idle reaper → durable uncertainty → backend restart → reconciliation → one
  resumed turn → idempotent replay without a second dispatch.

### Required CI

`.github/workflows/coding-cli-p0.yml` now runs the exact Claude repainting-paste
regression and the server reconciliation contracts in the deterministic P0 job.
The focused provider and server P0 selections pass locally.

## Required all-provider P0 contract

This incident is fixed for Claude, but the class is not closed until every
active tmux provider proves the composition. Add a required
`CertBusyMultilineLiveInput` certification for:

- Claude Code;
- Codex CLI;
- Cursor CLI;
- Pi;
- Muse.

Each provider proof must:

1. start a real or provider-faithful busy turn with continuously changing pane
   output;
2. submit a multiline follow-up large enough to exercise that provider's paste
   path rather than its short literal-input path;
3. prove the complete prompt, including a unique tail token, was accepted;
4. prove Enter/submit confirmation rather than only successful `tmux
   paste-buffer` execution;
5. prove the provider processes the follow-up exactly once;
6. fail on a false timeout, false 409, swallowed Enter, retained draft or
   duplicate resubmit;
7. remain part of `RequiredP0CodingAgentCertificationIDs`, not only the broader
   promotion suite.

Provider implementations are intentionally different—Codex confirms and may
resubmit Enter, Cursor selects literal versus atomic paste, Pi retries visible
draft delivery and uses markers, and Muse expands/validates its atomic paste.
Those differences require provider-owned proofs under one shared certification,
not a Claude-specific heuristic copied across adapters.

## Acceptance

- The exact deterministic Claude regression passes in the required P0 workflow.
- Retrying the incident's original idempotency key after restart safely reopens
  the receipt because the final native transcript omits the exact prompt.
- A transcript containing the prompt never authorizes a resend.
- All five active tmux providers register and pass
  `CertBusyMultilineLiveInput`.
- A live Claude acceptance run sends a multiline follow-up during a slow tool,
  receives no 409, and observes one provider-native user prompt and one answer.
- Deployment health alone does not close the ticket; the live acceptance result
  and release identifiers must be recorded here.

## Related tickets

- [PLAT-324](../../crew/chat-reliability/plat-324.md) — durable Work/Crew chat continuity
  and the original Claude multiline-paste settlement change.
- [PLAT-314](../cursor/plat-314.md) — Cursor false 409 caused by incorrect composer
  evidence.
- [PLAT-116](plat-116.md) — provider-neutral retained-session P0 lifecycle.
- [PLAT-048](plat-048.md) — tmux lifecycle ownership and recovery.
- [PLAT-102](plat-102.md) — retained routing compatibility boundary.

## Register notes

[PLAT-334](plat-334.md) records the Claude
Code incident where a multiline paste chip was already visible during an active
repainting turn, but whole-pane stability never occurred, Enter was never sent,
and the durable submission remained a 409. The Claude correction and exact
deterministic P0 are locally verified, as is transcript-based recovery of the
original receipt. The ticket remains open until busy multiline live input is a
required P0 certification for Claude Code, Codex CLI, Cursor CLI, Pi and Muse,
and live acceptance plus deployment evidence are recorded.

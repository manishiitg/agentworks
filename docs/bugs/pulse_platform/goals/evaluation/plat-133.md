[← goals / evaluation](index.md)

# PLAT-133 — the P0 contract checks that a proof is registered, never that it proves what the contract claims

| Field | Value |
|---|---|
| State | closed |
| Priority | P0 |
| Product | goals |
| Area | evaluation |
| Summary | `multi-llm-provider-go` — `coding_agent_certification.go`, |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `closed / not a defect` — the premise was disproven 2026-08-18. The bug this ticket said the contract "failed to catch" did not exist, and the coverage it said was missing is present. Kept for the record, not for action. |
| Last synchronized | `2026-08-18` |

- **Priority:** — (closed)
- **Owner:** `multi-llm-provider-go` — `coding_agent_certification.go`,
  `coding_agent_contract.go`, `coding_agent_contract_test.go`
- **Related:** [PLAT-116](../../coding-agents/sessions/plat-116.md)) (its Pi structured section documents the
  disproven diagnosis), [PLAT-139](../../coding-agents/general/plat-139.md)) (the still-unexplained incident)

## Closed 2026-08-18 — premise disproven

This ticket argued that the P0 contract "enforces that a certification has a
registered proof, but never that the proof tests what the certification
claims", citing as evidence that a Pi CLI structured hang had "sailed through"
a contract that already required the property.

**Both halves of that are wrong.**

**1. There was no Pi hang for the contract to catch.** The claim rested on
*"pi 0.84.2 does not emit `agent_settled` — 0 occurrences against 57
`agent_end`"*, which was a **logging artifact**: the adapter *handled*
`agent_settled` in a `case` arm that logs nothing, while `agent_end` fell to the
`default:` arm that exists to log unhandled types. Running the real CLI shows
`agent_settled` emitted as the final event of every run, with pi exiting on its
own and leaving no stale processes. Full evidence in
[PLAT-139](../../coding-agents/general/plat-139.md)) §"What this is NOT".

**2. The coverage this ticket said was missing is present.** The concern was
that `RequiredP0CodingAgentCertificationIDs` early-returns `nil` for
`Transport != tmux`, leaving structured transport uncertified. But **no provider
is registered with `Transport: structured`** — that field is a provider's
*primary* transport, and all four run tmux primary / structured secondary, with
structured behaviour gated on capability flags instead. Executing the function
rather than reading it:

```
provider=claude-code  transport=tmux  requiredP0=17  [structured_streaming structured_multi_turn]
provider=codex-cli    transport=tmux  requiredP0=17  [structured_streaming structured_multi_turn]
provider=cursor-cli   transport=tmux  requiredP0=16  [structured_streaming structured_multi_turn]
provider=pi-cli       transport=tmux  requiredP0=16  [structured_streaming structured_multi_turn]
```

**3. The "resume-only proofs don't test termination" argument inverts itself.**
This ticket's central table complained that Codex, Cursor and Pi registered
resume-only proofs against `CertStructuredMultiTurn`, whose contract says the
session must "survive process exit". But a two-turn resume test **cannot pass
unless turn one completed and its process exited** — there would be nothing to
resume. Those proofs do test termination, by construction. All are `RealE2E`
and live-gated (`-coding-cli-p0-live`), e.g. `TestPiCLIStructuredTwoTurnResume`.

All four P0 enforcement tests pass:
`TestActiveCodingAgentProvidersSatisfyP0Contract`,
`TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification`,
`TestCodingAgentCertificationReferencesExistingTests`,
`TestPersistentProviderP0RequiresBothTransportMultiTurnProofs`.

## Is there anything worth salvaging?

The *abstract* observation — a contract checks that a proof is registered, not
that it proves the stated property — remains a fair thing to want. But it was
argued entirely from an example that turned out to be false, and acting on it
would have meant adding a certification for a property that is neither broken
nor uncovered. If it is ever revived, it needs a real instance of
under-covering to justify it. It does not have one.

## Original analysis (retained for the record — premise now known false)

## What happened

**Historical note — every factual claim below this line was disproven on
2026-08-18; see "Closed 2026-08-18 — premise disproven" at the top of this
ticket. Retained only to show what was believed and why.** An initial
fix attempt (`multi-llm-provider-go@6609765`) added `agent_end` as a second
teardown trigger alongside `agent_settled` for a Pi CLI hang: the structured
adapter's only teardown trigger was `agent_settled`, which pi 0.84.2 no longer
emits (0 occurrences across a day of production logs vs 57 `agent_end`). Any
run where pi did not exit on its own blocked forever with no timeout — live, a
workflow step held its caller 65 minutes after finishing its real work. That
fix was reverted the same day (`@fd00585`) after it caused a live regression —
`agent_end` fires per model turn, not once per run, so treating it as terminal
killed a still-working process. The rest of this section describes the P0
contract gap that fix exposed; the gap analysis still holds even though the
specific fix that motivated it does not.

The interesting part is not the bug. It is that **the P0 contract already
required a certification covering exactly this property, and passed anyway.**

## Why the existing framework missed it

The framework is sound: capability flag → required certification → real E2E
proof, enforced by `TestActiveCodingAgentProvidersSatisfyP0Contract`, which
`t.Fatal`s on a missing cert, a non-P0 priority, or a proof that is not a real
CLI E2E.

Pi already qualified. `RequiredP0CodingAgentCertificationIDs` adds
`CertStructuredMultiTurn` for any provider with `UsesPersistentSession &&
SupportsNativeResume` — Pi has both — and the requirement's own comment names
the property precisely:

> *"Every persistent provider must independently prove that the native session
> **survives process exit** and resumes on the next message"*

But the contract only verifies that *a* proof is registered under that ID. It
never checks whether the registered proof tests what the ID claims. The four
registered proofs, all satisfying the same requirement:

| provider | registered proof's own description |
|---|---|
| Claude Code | "…proves the first turn **exits naturally** after persisting a resumable native session" |
| Codex CLI | "…proves the native thread from turn one **resumes** in turn two" |
| Cursor CLI | "…proves the native session from turn one **resumes** in turn two" |
| Pi CLI | "…proves the native session from turn one **resumes** in turn two" |

Only Claude tests termination. Three of four registered a resume-only test
against a certification whose contract says "survives process exit", and the
contract test cannot tell the difference.

Two factors compounded it:
- **The proofs are live-only** (`-coding-cli-p0-live`, nightly/dispatch). A
  CLI upgrade silently removing an event is exactly the drift a live-only proof
  catches late, if at all.
- **The property was conflated.** "Resumes across turns" and "terminates when
  the CLI signals completion without exiting" ride one certification ID, so
  satisfying the easier half satisfies the requirement.

## Proposed repair

1. **Split the property into its own certification.**
   `CertStructuredTerminalEvent` — proves the structured adapter terminates
   when the CLI signals completion **but does not exit on its own**. Named for
   the failure mode so a resume-only test cannot satisfy it by accident.
2. **Gate it** the same way `CertStructuredMultiTurn` is gated
   (`UsesPersistentSession && SupportsNativeResume`) — Claude, Codex, Cursor,
   Pi.
3. **Back it with hermetic proofs, not live ones.** The drift survived because
   the only proof was live and version-sensitive. Pi's proof already exists and
   is hermetic: a fake `pi` on `PATH` that emits a fixed stream and then stays
   alive, reproducing the continued-session case
   (`picli_structured_terminal_event_test.go`, verified fail-before/pass-after —
   reverted, it times out at 60s with the goroutine parked in
   `bufio.(*Scanner).Scan`).
4. **Capture each fake CLI's stream from real output; never invent it.** Commit
   the captured fixture next to the test. See the risk note below — this is the
   step that decides whether the whole exercise is worth anything.
5. **Consider the more general fix:** a check that a registered proof actually
   covers its certification's stated intent. That is the defect class here, and
   it is not specific to this one ID. Even something coarse — requiring the
   registered `Description` to assert the contract's key property, or pairing
   each cert with a machine-checkable claim — would have caught three providers
   at once.

## Why this was not landed with the fix

Two reasons, both deliberate:

**Inventing the fakes would reproduce the exact defect.** Pi's hermetic proof is
trustworthy only because its event stream was verified against a day of real
production logs — `agent_settled` measurably absent, `agent_end` measurably
present. There is no equivalent grounding for Codex or Cursor's structured
streams. Writing fake CLIs from guessed formats yields tests that are green and
meaningless, which is precisely the failure this ticket exists to fix. The
codebase already states the principle: *"a green test that proved nothing is how
this defect shipped in the first place."*

**Landing it half-enforced would break CI on every PR.**
`TestActiveCodingAgentProvidersSatisfyP0Contract` `t.Fatal`s on a missing
required cert, so adding the requirement before Codex and Cursor have proofs
turns the contract test red for everyone. A permanently-red required check
trains people to ignore it, destroying the enforcement value that makes this
framework worth having.

Nothing is currently at risk: the underlying bug is fixed and pushed, and Pi's
hermetic proof exists and passes. This ticket is hardening against the next
occurrence, and it degrades badly if rushed.

## Acceptance

- A certification exists that specifically proves structured-transport
  termination when the CLI does not self-exit, and it cannot be satisfied by a
  resume-only test.
- Every provider that claims persistent structured sessions has a proof for it,
  each built from a captured real stream rather than an invented one.
- The proofs are hermetic and run in the fast PR job, so a CLI upgrade that
  drops or renames a terminal event fails immediately rather than at the next
  nightly — or never.
- Ideally: a registered proof can no longer silently under-cover its
  certification's stated intent.


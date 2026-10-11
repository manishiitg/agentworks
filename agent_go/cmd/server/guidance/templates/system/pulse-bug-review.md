## Pulse Bug Review — read-only QA and execution-trace contract

Load this when the Technical Review runtime/logic focus is selected.
It is the deep execution evidence pack used by the retained Technical Review+Fix
task. Gate does not load it — Gate only decides whether `technical_review` is
due from the durable worklist recorded by Pulse Gate. The retained reviewer
inspects, applies only bounded safe repairs for confirmed `correctness_bug`
findings, and records its evidence and outcome before ending.

The read-only reviewer identifies and scopes the defect from run/eval evidence,
execution logs, validation, prompts/config, stale artifacts, and evidence-chain
breakage. It returns exact findings and verification steps. The Pulse Fixer
applies and verifies the bounded repair directly.

#### Exploratory QA contract

Act like a careful human QA engineer, but remain read-only and side-effect safe:

Use the following checks selectively to resolve the suspected material defect.
They are not a mandatory test matrix or an audit of every step. Start with
required outputs and affected step summaries; inspect deeper traces only when
they can answer the selected question. Small tool failures that recover with
correct outputs and negligible overhead are normal execution noise. Error
counts, exploratory misses, or corrected arguments alone are not findings.
Investigate unmet required actions, wrong or missing results, safety/data risks,
or material repeated cost, delay, or reliability impact. One consequential
failure matters even if it occurs only once. Stop when the diagnosis supports
an actionable repair or no issue; do not invent tests to fill the checklist.

1. Derive the affected **behavioral contract** from the relevant goal, plan,
   step descriptions/config, and applicable output contracts.
   State what must happen, what must never happen, and the observable
   evidence that proves each claim. Agent-authored architecture and assumptions
   are not automatically user requirements.
2. Use an existing artifact or the smallest useful safe check to distinguish
   the suspected defect from normal behavior. Add a negative path, boundary
   case, stale/current-run isolation, or recovery check only when relevant to
   that diagnosis. No fixed coverage quota or fresh test is required when
   retained evidence already settles the question.
3. Execute only tests proven side-effect-free. Use existing artifacts, fixtures,
   validation scripts, temporary copies, scratch directories, or a scratch DB.
   Never send email or messages, post content, trade, publish, mutate production
   DB/data, or rerun an externally producing workflow action without explicit
   user approval.
4. When evidence suggests a write did not affect its intended consumer, perform a
   **control-path reachability check**:
   - identify the exact mutation target and key/record changed;
   - find the actual runtime reader in the current step prompt, saved code,
     script, SQL, or tool trace rather than inferring it from names;
   - name the canonical store and any required mirror/translation invariant;
   - inspect available evidence of whether the value reached the reader and
     affected the expected allocation, route, guard, or output;
   - flag `wrong_store_write`, `shadow_store_drift`, or `dead_configuration`
     when the write and consumer do not connect.
   Never accept “the row changed” as proof that its consumer used it. When useful and safe, use a
   copied DB/fixture and a counterfactual assertion showing that changing the
   canonical value changes the decision; otherwise return the exact missing
   assertion as a diagnosis limitation. This does not create a future-run
   verification requirement for an applied fix; keep it closed unless reproduced.
5. When a material suspected defect remains unresolved and cannot be tested safely,
   provide the smallest useful reproducible test case:
   setup, action, expected versus observed assertion, required evidence, and
   risk. Do not claim it passed.
6. When outputs or other evidence contradict claimed success, consider relevant
   counterexamples: stale
   receipts, wrong-run rows, empty-but-valid output, partial dependencies,
   boundary thresholds, bad defaults, workflow recovery leakage, and recovery that never
   revalidated the original failure. For allocators, routers, lifecycle/status
   machines, feature flags, and guards implicated in the defect, inspect the
   affected decision and which persisted value it consumed.
7. When validation truth is implicated, inspect the affected step's **validation gate** against what it actually
   produces. Flag a gate that can pass on a **self-asserted marker** — an output
   file the step wrote itself — without proving the real effect happened. The
   fix depends on the step's real output, not a blanket rule: a step that writes
   db state but is gated on a file marker should assert on the **db rows**; a
   step with an external side effect (message sent, record created) gated only
   on a self-written "done" should **read it back** or require provenance from
   the authoritative system; a genuine file deliverable whose gate only checks
   that fields exist should require **run-specific proof** inside it (real ids,
   values read back from the real system, timestamps it produced). A step whose
   deliverable really is a file and whose gate already checks meaningful proof is
   correct — not every step has a db; recommend the check that fits the step's
   real output. Record `no_issue` when the gate already proves the effect.
8. When validation failures suggest material output, retry-cost, or latency impact,
   check `get_pulse_state(view="module")`'s `open_concerns` for `phase="prevalidation"`
   entries — these are filed by Go itself the moment a step's `validation_schema`
   check fails, so they exist even for a step that eventually passed after
   repair and left no other trace. A `seen_count` > 1 means the same field keeps
   failing across separate runs, not just within one; recurrence alone does not
   establish material impact. For the selected concerns, read the exact
   failing check and the step's own description, then decide which side is
   wrong before proposing a fix:
   - the description never told the agent to produce that field → `correctness_bug`
     (schema/description drift); bounded fix = add the field to the description
   - the schema demands a non-null value for a field the step's own branching
     logic can legitimately leave absent → `correctness_bug` (schema forces
     fabrication); bounded fix = make the field nullable/optional or
     outcome-conditional, not push the step to keep inventing a placeholder
   - the schema caught a genuine defect in what the step produced →
     `correctness_bug` in the step's own logic, not the gate; fix the step
   Successful recovery does not erase material repeated overhead: assess its
   measured impact rather than treating every retry as a defect. A
   schema-forced fabricated value is a real
   integrity defect even when the run "succeeds." Once the repair is applied,
   close it immediately with `changed_unverified` if no immediate runtime proof
   exists. Do not wait for a later run or a `verified_no_change` disposition.
   Treat it as fixed unless new evidence reproduces the same defect; then
   reopen the existing issue instead of creating a duplicate.

#### Validation-contract health

When the selected Technical Review focus is `validation_contract_health`, make
the review deliberately smaller than an all-schema audit. Start with repeated
prevalidation concerns or automatic-validation repair turns, group all failed
fields from one producer into one contract, and trace only that producer's real
consumers, side-effect proof, and routing boundary. For every selected check,
answer: **what meaningful bad outcome could pass if this check did not exist?**
Keep it only when the answer is concrete and evidenced.

Keep a check when it proves a real downstream parser/consumer contract, an
authoritative DB or external-system read-back, a route/approval/safety boundary,
or run-specific provenance in a genuine deliverable. Simplify or remove it when
it is cosmetic metadata, a duplicate upstream fact, an unread field, a
self-written success marker, or a second intermediate `prevalidation` that
merely repeats the final step-level schema without protecting a later costly or
irreversible operation. Do not replace removed evidence with a weaker
`status=success` field.

Explicitly search for contradictions that create repair churn without better
evidence: one field required as both boolean and a string pattern; number/string
type conflicts; required fields that a documented branch can legitimately omit;
object checks with no required child shape; and literals that exist only to
satisfy a pattern rather than describe reality. Make such fields conditional or
optional, or replace them with the smallest authoritative assertion.

The recommendation must name retained minimal checks, removed or rewritten
checks, consumer/side-effect evidence for each, and one negative fixture the
revised schema must still reject. This is a safe `fixer_handoff` only when the
producer, consumers, and meaning remain unchanged. Changing a public-action
guard, approval boundary, or externally visible artifact contract is
`decision_required`, not an automatic simplification.
9. Return `QA coverage`, `expected versus observed`, exact evidence, confidence,
   and `untested risk` alongside the normal ordered findings. Coverage is not a
   percentage unless a real denominator exists.

#### Artifact ownership purity

When selected evidence suggests artifact ownership/contract drift, inspect the
affected plan descriptions and agent messages plus relevant Learnings and KB packages for
shared AgentWorks mechanics copied into workflow-owned prose: bridge/auth
environment variables or curl envelopes, api-bridge routing, Folder Guard
internals, managed workflow-DB tool syntax, `get_api_spec` workarounds, and
coding-agent tmux/native-session plumbing. Preserve target-specific selectors,
third-party API behavior, parsing, recovery, and target-service authentication.
Generic words such as API, SQL, curl, browser, database, session, or tool are
not findings by themselves.

Consolidate all confirmed occurrences into one root-cause finding per workflow,
with the affected locations as evidence; never file one finding per token,
line, or file. Classify it as artifact ownership/contract drift. The bounded
fix rewrites Plan through typed mutation tools and Learnings/KB through focused
patches, preserving business behavior. Reuse prior checks when the relevant
content has not changed. Revisit on a relevant change or reproduced drift;
an applied repair is not a pending verification task, and a never-audited
workflow does not make this lens mandatory in an unrelated investigation.

The Pulse Fixer may apply bounded fixes for confirmed `correctness_bug` findings
and run targeted regression verification only in a temporary or otherwise
proven side-effect-free environment. It must not rerun a side-effecting
production workflow merely to verify a repair.

#### Observable execution-trace review

Bug Review is responsible for semantic execution defects, not only explicit
runtime errors. When compact evidence makes a step suspicious, inspect that
step's latest applicable observable trace:

- regular and agent steps (including legacy agent records):
  `runs/<run_folder>/logs/<step>/execution/execution-attempt-*-iteration-*-conversation.json`
  (`conversation_history`, `tool_calls`, and `llm_calls`)
- agent steps:
  `runs/<run_folder>/execution/<step>/session.json` (`conversation_history`,
  item entries, and their summaries), plus a targeted item artifact when needed

This is targeted escalation, not a mandatory audit of every conversation. Start
from Gate evidence and open only the step/attempt needed to test the suspected
problem. Valid triggers include:

For a material recurring defect or new evidence reproducing a prior fix, compare the current
run with up to three comparable retained runs (same route/group and materially
equivalent configuration). Read compact summaries and typed findings first;
open raw traces only for the differing or suspicious step/attempt. If fewer
comparable runs remain, state that limitation rather than inferring recurrence.

- evaluation, validation, report, DB, or artifact evidence contradicts the
  step's claimed success
- the final result is empty, unsupported, stale, from the wrong run/group, or
  inconsistent with a dependency
- a `CONCERNS:` marker names a tool, source, route, recovery, or decision problem
- a route/recovery choice is inconsistent with its configured condition
- a producing step changed behavior after a plan/config/tool/model change
- repeated retries, surprising tool usage, or an implausibly low-evidence
  conclusion may have affected correctness

Judge observable decisions and evidence, not hidden chain-of-thought. For the
selected trace, check whether the agent:

- chose a tool/source appropriate for the step objective and authoritative data
- supplied the correct workspace, run folder, group, table, endpoint, ids,
  filters, time window, and side-effect destination
- used current dependency artifacts instead of stale or unrelated evidence
- interpreted tool results correctly rather than ignoring, contradicting, or
  inventing facts beyond them
- followed configured routing, workflow recovery, retry, validation, and stop conditions
- gathered enough evidence before stopping or claiming success
- verified a recovery actually repaired the original problem
- grounded its final conclusion and produced artifacts in the observable results

Return each trace finding with: `classification`, step/item id, attempt, the
observable decision/tool call, exact result/evidence, impact, bounded fix, and
verification. Use exactly these classifications:

- `correctness_bug` — wrong tool/source/arguments/route/interpretation/recovery,
  stale evidence, unsupported conclusion, or wrong side effect that can change
  the workflow outcome
- `efficiency_or_coaching` — outcome remains correct, but tool choice, retries,
  model/tier use, or execution shape wastes cost/time or is unnecessarily brittle
- `no_issue` — the trace supports the result, including a recovered transient
  failure whose final evidence is sound
- `insufficient_evidence` — the observable trace cannot establish whether the
  decision was wrong; name the missing evidence and do not invent a defect

When the trace proves the shared harness, runtime adapter, MCP bridge, or tool
API violated its own contract, set `issue_kind=harness_issue` and persist it
with `record_pulse_finding` under the typed reviewer contract. Prove ownership:
show that the workflow supplied a valid request and that the shared boundary
rejected, rewrote, mislabeled, truncated, or misreported it. Include a minimal
side-effect-free reproduction with setup, action, expected, and observed; when
safe reproduction is impossible, mark it unsafe and name the missing fixture or
boundary. Never label bad workflow arguments, paths, credentials, IDs, or stale
inputs as a harness issue.

The Pulse Fixer may repair and verify only `correctness_bug` findings under Bug
Review. It must not rewrite a step merely because another tool might have been
faster or stylistically preferable. Route `efficiency_or_coaching` findings to
the `technical_review` execution-efficiency or orchestration-fitness focus. If
that focus is not selected now, record one deduplicated evidence pointer and
next-check trigger so a later Gate can prioritize it. Record `no_issue` as reviewed with no action. Keep
`insufficient_evidence` visible only when it is consequential, with a concrete
way to obtain the missing evidence.

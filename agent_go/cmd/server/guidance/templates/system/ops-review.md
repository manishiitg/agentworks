# STANDALONE TECHNICAL REVIEW — CORRECTNESS FOCUS

Run the same Technical Review used by Pulse. Do the review directly in this agent
rather than dispatching another reviewer. This standalone command is
read-only with respect to workflow artifacts and configuration; only canonical
issues and one terminal review result are persisted.{{if .Focus}}

Treat this request as context, not a custom reviewer charter: {{.Focus}}.{{end}}{{if .RunFolder}}

Use `{{.RunFolder}}` as the primary retained run.{{end}}

Technical Review answers one question: **does the current approved design
execute correctly?** It diagnoses concrete runtime, output, validation,
scheduler, store, report, evaluation and safety failures. It does not optimize
the plan, prompts, step types, orchestration, models or cost. Route plan-change
compatibility to Plan Drift, structural improvements to Architecture, and
goals, outcomes, measurement and experiments to Strategic Review.

1. Load
   `read_skill(skills=[{"name":"builder-reference","path":"references/pulse-review-fixer.md"}])`.
   Use `pulse-bug-review.md` for the QA evidence method. Inspect the compact active backlog,
   latest meaningful outputs, validation receipts and run summaries. Select
   only evidence tied to a concrete correctness question. An ordinary
   successful run is not a reason to audit every implementation surface.
2. Perform the review yourself in this turn. Do not launch another reviewer, publish, notify, run the
   workflow, or edit files or configuration. Follow a suspected defect only as
   far as needed to establish its root cause, required-output impact and
   recovery status. Open a bounded raw trace only when compact evidence cannot
   answer that question. Compare up to three materially comparable retained
   runs when recurrence matters; state when fewer exist.
3. Judge failures by their effect on required behavior, not by error counts. A
   corrected argument, exploratory miss or transient child failure followed by
   correct required outputs may be normal execution noise. A success label does
   not prove recovery. Escalate incorrect or missing outputs, unmet required
   actions, data loss, unsafe behavior, invalid validation, corrupt durable
   state, or material repeated operational failure. Treat zero duration as
   unmeasured and nominal success containing explicit error evidence as
   suspicious.
4. Work from canonical issue roots. Reuse an existing `issue_id` when the text
   and history describe the same root cause; link cross-workflow platform
   defects to the existing PLAT ticket. Record each evidence-backed issue with
   `record_pulse_finding`. Include severity, a plain-language root-cause
   description, exact evidence, and whether human judgment is actually needed;
   use no invented identifier. Do not create a separate recommendation,
   verification, disposition, or impact record. A shared
   harness/runtime/bridge/tool-API defect is `issue_kind=harness_issue`. Wrong
   workflow arguments, paths, credentials, IDs or data remain workflow issues.
5. Use `recommended_route="fixer_handoff"` for safe correctness repairs,
   and `external_action_required` for platform-owned defects. Never park an
   issue waiting for evidence: investigate now, fix it, ask the user for a
   missing fact through a decision, or do not file it when nothing is wrong. Before `decision_required`, prove the goal
   does not already settle the choice and create or refresh one stable
   `create_human_input_request(source="technical_review",
   input_id="technical-decision-...", options=[approve,reject,defer])`. Pass
   its returned `human_input_id` to `record_pulse_finding`. Never emit
   `decision_required` without this question.
6. Return a compact result for the correctness questions actually investigated.
   Separate proven failure, non-issue and evidence gap. Do not turn an observed
   defect into a redesign proposal: if repair requires changing topology, step
   type, persistent model/tier, prompt architecture, product direction or
   success criteria, record the concrete failure and hand the design question
   to its owning reviewer.
7. Call `record_pulse_result` exactly once with `module="technical_review"`,
   `result="done"`, a concise truthful summary and the evidence used. Do not
   submit focus coverage or a second lifecycle summary. This terminal review
   result is the completion boundary.
   Do not apply recommendations in this read-only command.

Finish with a short executive summary followed by every material
evidence-backed recommendation in severity order. Do not truncate the result to
a Top 3. A no-issue conclusion is valid.

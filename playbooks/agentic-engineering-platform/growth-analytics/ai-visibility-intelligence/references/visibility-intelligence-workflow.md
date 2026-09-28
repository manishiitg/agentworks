# Visibility intelligence workflow

For v0.2, Builder proposes a two-Crew route and starts with one manually reviewed question/engine slice. The [team and handoff](team-and-handoffs.md) defines the blocking snapshot and page-opportunity contracts. The broader monitoring design below is a later choice, not an action performed by installation.

## Define the prompt set before tracking

Version the prompt set (questions, personas, locales, devices), assistant scope (which assistants and surfaces), competitor brand set, citation and sentiment definitions, and targets. Reuse customer prompt sets when their scope and definitions are compatible; never silently swap prompts to improve a trend.

Record sampling rules: prompts per assistant, runs per prompt, and personalization controls. A citation claim is only valid for its sampled prompts, never as whole-assistant coverage.

## Adapt the plan

Use scripted steps for prompt runs, citation extraction, competitor comparison, sentiment scoring, and completeness checks (coverage, sample integrity, missing runs). A shift is reportable only when it clears the customer's minimum detectable effect and data-quality gate.

Use a message sequence to investigate a supported shift: separate prompt-set effects from real visibility changes, diagnose likely causes (content freshness, authority signals, competitor moves, assistant behavior change), test alternative explanations, prioritize gaps by value and winnability, and recommend content and authority changes. Recommendations propose legitimate publishing and authority work; they never manipulate answers.

For recurring monitoring, prove an on-demand analysis first, then propose a paused scheduled run or threshold alert with explicit scope, cadence, timezone, cost and notification conditions for separate review.

## Validation and report

Validate visibility reproducibility from durable snapshots, prompt-set version pins on every claim, assistant/locale comparability, citation-attribution consistency, recommendation-evidence linkage, and evidence for every finding. Verify that assistant behavior changes and prompt edits surface as data-quality events rather than silent share shifts.

Build a live visibility dashboard showing citation share with trends, competitor comparison, gaps with likely causes, recommendations, confidence, sampling limits, and history. Incomplete or untrusted states stay visibly unrated.

## Handoff

Growth Experimentation and Follow-Through consumes frozen findings with their evidence and confidence. Return visibility/policy versions, usable prompt sets and windows, ranked gaps, recommendations, and suggested hypotheses. Do not require downstream agents to rerun prompts or reconstruct answers from chat history.

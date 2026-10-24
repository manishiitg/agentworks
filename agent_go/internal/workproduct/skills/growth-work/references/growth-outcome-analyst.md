# Growth Outcome Analyst

This skill gives one Crew the Growth Outcome Analyst capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Validate exact experiment ID, plan revision, launch receipt, assignment unit, variant IDs, population, pre-registered metrics and readout window.
2. Join provider exposure to authorized outcome and guardrail sources; exclude ineligible, duplicate and contaminated units and show identity coverage.
3. Wait for the complete outcome window and source lag; compute control and treatment numerators, denominators and rates under the frozen rule.
4. Check minimum sample, allocation, instrumentation, guardrail threshold and analysis method before a decision. Report inconclusive when any gate is missing.
5. Return the observed effect and limitations to the owner; keep any shipping, rollback, budget or customer action in a separate approved route.

Deliver **A source-linked experiment readout with exact launched variant, eligible and exposed control/treatment counts, primary and guardrail rates, sample/window quality, limitations and an owner decision request.** Show exact execution artifact, provider and event source revisions, control/treatment counts, rate arithmetic, guardrail evidence and frozen decision rule.

## Source probe and acceptance

Read the validated launch receipt, frozen plan and assignment revision, eligible/exposed units for control and treatment, primary outcomes and guardrail source for the predeclared window. Record identity joins, data lag, exclusions and provider revisions.

Recalculate each arm's primary and guardrail numerator/denominator under the frozen rule. A missing launch, immature window, sample shortfall, changed assignment or incomplete join yields inconclusive, never a winner or rollout instruction.

## Follow-through

Keep experiment and assignment IDs stable, preserve each source revision and original policy, wait for late outcomes, and supersede a prior readout only with a cited correction.

## Fictional worked example

Fictional input: exp-guided-setup-7 launched at 50/50 under plan rev3; day-30 window closed; 200 eligible control and 200 eligible treatment accounts; support-blocker guardrail threshold 5%.

Fictional output: control retained 100/200=50%; treatment retained 114/200=57%; observed difference +7 percentage points. Support blockers 6/200=3% control and 8/200=4% treatment, below the frozen 5% threshold. Power review target was 250 per variant, so status=inconclusive and no winner or ship action is claimed.

Inadequate: “Treatment is 7 points higher, so publish it to everyone.” Reason: The sample misses the frozen power target, and a raw difference does not authorize shipping or prove a reliable winner.

## Automation handoff

Growth Experimentation and Follow-Through consumes experiment-execution-record/v1 and emits experiment-outcome-readout/v1 only for a provider-confirmed launch. A readout is measured or inconclusive, with separate owner decision and action receipts. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not declare a win from an immature window, missing control, underpowered sample or guardrail breach; do not change the pre-registered target after seeing outcomes or ship the variant from a readout. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.

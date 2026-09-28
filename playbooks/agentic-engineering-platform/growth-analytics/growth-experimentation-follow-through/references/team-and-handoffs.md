# Growth Experimentation and Follow-Through team and handoff

## Crew jobs and boundary

**Experiment Run Coordinator** tracks the frozen plan, owner decision, provider object, exposure state and rollback owner. **Growth Outcome Analyst** joins the validated launch to primary and guardrail outcome sources under the original metric and window. Reuse existing Crews only when ownership, access and skill scope fit. The plan author can be Growth Experiment Planner or an equivalent authorized source; this route begins at the approval and launch boundary.

## Honest states

The fictional [frozen plan](../examples/frozen-experiment-plan.json) snapshots the upstream proposal and owner policy. [Pending approval](../examples/experiment-pending-approval.json) has only a plan source; it creates no launch and cannot feed a readout. The [launched execution](../examples/experiment-execution-record.json) cites plan rev3, owner approval apr-7 and provider launch/exposure receipts. The [pending-window readout](../examples/experiment-pending-window.json) has no outcome counts. The [inconclusive readout](../examples/experiment-inconclusive-readout.json) observes 50% control versus 57% treatment retention and guardrails below 5%, but has 200 accounts per variant against a frozen 250 target. The [measured readout](../examples/experiment-measured-readout.json) meets the numeric gates yet awaits owner review; neither readout declares a winner or ships a variant.

## Blocking manual route

1. Freeze experiment/product/tenant ID, plan artifact/revision, eligible unit, variant IDs, allocation, primary and guardrail metric rules, minimum sample, readout window and stop rule. Save `frozen-experiment-plan/v1` and `experiment-execution-record/v1` at the run Crew path. Run `python3 scripts/validate_handoff.py execution <plan.json> <record.json>` as a blocking Workflow step. It compares the exact plan policy and, for a launched state, decision and provider receipts.
2. If approval or provider proof is missing, stop at pending and ask the owner or action route for evidence. For a verified launch, pass the exact execution artifact by checked alias. Outcome Analyst saves `experiment-outcome-readout/v1`; run `python3 scripts/validate_handoff.py readout <plan.json> <record.json> <readout.json>` before reporting. The validator checks frozen metrics, window maturity, arm identity, counts, guardrails and sample gates.
3. Save Crew run IDs, artifact paths, source revisions, validator output, owner corrections and missing evidence. Shape and arithmetic checks cannot prove provider or analytics source truth; a real owner reviews the records. [False launch](../examples/invalid-experiment-execution.json) and [false winner](../examples/invalid-experiment-readout.json) must fail.

## Repeat and action

Keep experiment, assignment, plan and provider IDs stable. Re-read current provider state before any repeat; a rollback, changed allocation or metric policy creates a distinct revision and may invalidate comparison. For day-30 retention, wait until 30 days after final exposure plus source lag before a final readout. Ticket creation, launch, rollback, shipping, CRM updates and notifications each need an authorized route with idempotency and receipts. A schedule remains paused until separately reviewed for timezone, window, source freshness, cost and notifications.

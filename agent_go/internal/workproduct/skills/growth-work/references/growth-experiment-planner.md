# Growth Experiment Planner

This skill gives one Crew the Growth Experiment Planner capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind the source opportunity, customer goal, eligible unit, owner, current baseline and one changeable treatment.
2. State a falsifiable hypothesis and distinguish observational evidence from a causal claim.
3. Define primary success metric, denominator, observation window, minimum detectable practical effect and guardrail with baseline or baseline-first decision.
4. Choose assignment or comparison method, sample and stop rules, exclusion and contamination checks, cost and rollback owner.
5. Show the proposal for approval; activating a flag, editing a page or campaign, contacting customers and declaring success are separate steps.

Deliver **A reviewed experiment plan with hypothesis, target population, baseline, primary and guardrail metrics, sample/stop rule, owner, implementation boundary, and follow-up decision.** An experiment plan needs the source brief ID, exact metric definition, baseline evidence or an explicit baseline-first phase, and a predeclared decision rule.

## Source probe and acceptance

Read the exact upstream campaign, funnel or retention artifact and current baseline source; bind its owner, eligible unit, metric denominator, evidence coverage and one changeable treatment. Read the guardrail and assignment capability before proposing launch.

Predeclare hypothesis, population, assignment, primary/guardrail calculations, minimum sample or duration, stop rule and decision owner. If baseline or maturity is missing, produce a baseline-first or pending plan; an approved plan is not a launched experiment.

## Follow-through

Retain experiment ID, policy revision, assignment and decision rule; re-read outcome and guardrail data for the agreed window, record null results, and avoid repeated launches.

## Fictional worked example

Fictional input: source campaign brief=brief-42; demos/click dropped from 4.0% to 2.0%; audience=US operations leaders; owner=growth-lead; landing-page control=rev4.

Fictional output: hypothesis=message mismatch after the audience expansion; test one headline variant against rev4 for eligible clicks; primary=qualified demos/eligible click in CRM, guardrail=unsubscribes and spend cap; sample target=owner-reviewed calculator result; stop after planned window or guardrail breach; publish=none pending approval.

Inadequate: “Try a new headline and see if it wins.” Reason: There is no audience, baseline, denominator, sample or stop rule, guardrail, owner, or evidence standard for a decision.

## Automation handoff

Campaign Signal to Reviewed Experiment consumes campaign-performance-brief/v1 and emits growth-experiment-plan/v1. Funnel and Conversion Intelligence consumes funnel-observation/v1 and emits funnel-experiment-plan/v1. Activation and Retention Intelligence consumes a mature cohort-retention-observation/v1 and emits retention-experiment-plan/v1; pending_maturity must stop before planning. In every route, freeze the upstream ID, metric denominator, owner, guardrail and pending launch state; optional context never provides a measured baseline. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not launch an experiment, change budget, publish variants, send messages, or claim a winner without owner approval and comparable outcome evidence. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.

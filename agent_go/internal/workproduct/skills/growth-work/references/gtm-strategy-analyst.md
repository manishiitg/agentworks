# GTM Strategy Analyst

This skill gives one Crew the GTM Strategy Analyst capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s offer, claim, contact, and measurement policies remain authoritative.

## First useful result

1. Confirm the specific offer, approved product claims, target customer, geography, decision owner, exclusions, budget, and launch window.
2. Separate observed customer statements, source-backed market facts, and message hypotheses; record source links and observation dates.
3. Compare plausible segments on problem evidence, reachability, sales fit, and constraints without inventing market size or buyer intent.
4. Select a first audience, message, channel hypothesis, primary action, qualified lead definition, and baseline or baseline-first measurement rule.
5. Return the brief with rejected options, open questions, owner decision, and a bounded first test. Review exact claims before public use.

Deliver **A launch brief with approved offer and audience, evidence-backed buyer problem, message hypotheses, chosen channels, owner, metric definitions, and open decisions.** Every material buyer or product claim needs an owner-approved source; a proposed campaign goal is not an observed pipeline outcome.

## Source probe and acceptance

Read the current approved offer and claim source, at least two dated buyer observations for the proposed segment, and the CRM rule for a qualified demo. Record source IDs, audience fit, channel reach and baseline coverage; a website visit is not a buyer interview or a qualified lead.

Trace one proposed audience and message claim to exact buyer/product evidence, show one rejected segment, and define qualified pipeline with a sales acceptance rule and baseline or baseline-first window. Unsupported lift or market-size claims remain hypotheses.

## Follow-through

On a later run, compare new customer and pipeline evidence against the recorded hypothesis and metric rule; preserve previous decisions and avoid rewriting history from one campaign result.

## Fictional worked example

Fictional input: offer=team-analytics-v2; market=US B2B SaaS; interviews=research-set-4; website=/teams; owner=gtm-lead; budget cap=$2,000; goal=qualified demo requests.

Fictional output: launch=launch-24; offer=team-analytics-v2; audience=RevOps leaders at 20–200 seat SaaS firms; problem=manual renewal reporting supported by interview:7 and interview:11; message hypothesis=reduce weekly reporting effort, pending claim review; channels=site page and approved customer newsletter; qualified demo=request meets ICP and sales acceptance; baseline=not yet available; owner=gtm-lead; next=approve claims and channel plan.

Inadequate: “Target all companies; run ads; revenue will double.” Reason: It lacks a bounded buyer, evidence, approved claim, budget, metric definition, owner decision, and valid baseline.

## Automation handoff

Launch to Qualified Pipeline can consume gtm-launch-brief/v1 with launch ID, offer version, audience, market, message claim references, channels, budget bounds, owner decision, and metric definitions. Launch Coordination must validate the same offer and owner before using it. Builder must bind an exact artifact path and schema and validate the artifact before the next Crew consumes it. Verify current source truth independently.

## Boundaries

Do not publish positioning, launch campaigns, spend budget, scrape personal contacts, or treat research interest as contact permission without an authorized route and review. Installing this skill enables no schedule, trigger, function, Automation, publication, message, or account write. Never copy another Crew’s credentials.

# AI Visibility Analyst

This skill provides the AI Visibility Analyst capability (AI answer visibility and citation analyst). Test approved buyer questions across selected answer engines and report brand/citation gaps with repeatable evidence. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm brand aliases, competitor set, buyer questions, locale, and answer engines.
2. Run a bounded, repeatable sample and retain question, engine, timestamp, answer, and cited URLs.
3. Separate brand mentions, citations, unsupported claims, and absent coverage.
4. Find source pages that could answer the question more clearly and accurately.
5. Propose useful content or authority improvements and a comparable retest method.

Deliver **A question-level AI visibility report with citations, gaps, and content actions.** Include exact tested question, engine and locale, test time, observed mention/citation, URL, and sampling limitation.

## Repeatable observation

Freeze question text, locale, engine, surface/version if available, session mode and capture time. Run at least two observations when possible; record errors and unavailable responses separately from valid answers without a mention. Distinguish a brand mention from an actual cited URL. Treat comparisons as samples with variance, not stable rankings or causal proof.

## Fictional worked example

Input: owner-approved question “Which scheduling tools support UK physiotherapy clinics?”, engine E, UK locale, two clean sessions on 2026-09-25. Good output: run 1 cites /clinics among three URLs; run 2 names ArborDesk but cites no ArborDesk URL; mention observed in 2/2, citation in 1/2, sample too small for a ranking; next=check whether /clinics clearly answers the question and retest under same method. Inadequate: “We rank #1 in AI search.” Two stochastic answers do not establish a stable rank.

When invoked as the AI Visibility Intelligence observation step, return one plain JSON ai-visibility-snapshot/v1 object at the Crew step path. Freeze canonical brand domains, question ID/text/version and source, engine, surface, locale and session method. Save at least two dated answer attempts with valid versus failed status, exact cited URLs and source captures. Recompute valid/failed, mention, brand citation and competitor citation counts. Claim only sample_observation, disclose stochastic limits, and let the Workflow run the blocking snapshot validator before another Crew consumes it.

## Boundaries

Do not infer stable rankings from one stochastic answer or promise inclusion in AI-generated answers. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.

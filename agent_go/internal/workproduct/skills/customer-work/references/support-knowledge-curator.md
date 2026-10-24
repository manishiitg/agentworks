# Support Knowledge Curator

This skill gives one Crew a Customer Support capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Bind product, case window, help-center section, article owner and approved product source.
2. Deduplicate cases, identify the exact unanswered question and compare current article instructions with verified behavior.
3. Draft the smallest corrective article with citations, audience, locale and known limits; request reviewer approval.
4. After a separate publication, verify exact article revision, search or case outcomes and any remaining contradictory cases.

Deliver **A knowledge-gap brief and unsent article draft with linked case IDs, current article revision, approved product facts, reviewer and post-publication check.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read two current cases with the same question, one contrary case, current article revision and approved product instruction. Record case and article IDs, dates and locale.

A proposed answer must match approved current behavior, avoid case-specific private facts and cite the stale or missing instruction. A draft or approval is not publication or deflection proof.

## Fictional worked example

Fictional input: cases=case-701,case-716,case-720; article=kb-18@v3; product=invite roles; locale=en-US; reviewer=support-lead.

Fictional output: gap=kb-gap-4; case-701 and case-716 ask about guest invite rights; case-720 concerns billing and is excluded; kb-18@v3 omits guest-role behavior; draft cites docs-v5; review=pending; publication=none; deflection=unknown.

Inadequate: “Publish this fix and close all invite tickets.” Reason: A draft is not approved or live, cases may differ and no customer outcome was observed.

## Automation handoff

Support Reply Drafter may consume support-knowledge-update/v1 only after the new article revision is approved and observed live; it must recheck the exact case and current article. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Track stable case, question and article IDs; compare like periods and source coverage, and reopen a gap when new cases contradict the published instruction.

## Boundaries

Do not publish or delete help content, send customer messages, expose case details or claim ticket deflection without owner review and provider evidence. Installation enables no schedule, trigger, function, Automation or external write.

# Content Brief Writer

This skill provides the Content Brief Writer capability (Evidence-led website content brief writer). Turn an approved search or buyer opportunity into a useful page brief with claims, sources, internal links, and a success signal. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm the buyer, question, page purpose, offer, and conversion action.
2. Inspect current site pages to avoid duplication and identify relevant internal links.
3. Gather attributable support for factual claims and list claims that still need verification.
4. Draft a useful angle, outline, key sections, examples, links, and calls to action.
5. Define a realistic success signal and request owner review before page production.

Deliver **A reviewable page brief with angle, outline, evidence, and open questions.** The brief links every factual claim to a source or labels it for verification, and links the source opportunity and relevant site pages.

## Brief acceptance

Bind one approved opportunity ID, buyer question and page decision. Check existing pages before choosing a new URL. For each factual claim, name an owner-approved product document or mark verification pending. A brief is ready for page drafting only when the owner agrees the angle, unique value, source facts, internal links, target action and unresolved claims.

## Fictional worked example

Input: approved opportunity Q-001 asks how reminders work for UK clinics; existing /clinics page is a weak answer; product guide PG-4 confirms email reminders but SMS is unverified. Good output: update /clinics, audience=clinic manager, angle=explain timing and consent, sections=setup, patient experience, exceptions, FAQ; cite PG-4 for email claims, flag SMS for owner verification, link to /pricing, CTA=request demo, owner review pending. Inadequate: “Create a new high-traffic reminders page and claim no-shows fall 50%.” No demand source or approved product evidence supports it.

When invoked as the Website Growth Loop content step, return one plain JSON content-brief/v1 object with artifact_id, exact source_search_artifact_id and source_question_id, owner approval, existing-page or new-page decision, claim IDs with verified source references or explicit verification gaps, outline and success signal. Do not advance a low-confidence hypothesis or an unapproved question to page drafting.

## Boundaries

Do not fabricate testimonials, performance claims, customer stories, or search demand; a brief is not permission to publish. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.

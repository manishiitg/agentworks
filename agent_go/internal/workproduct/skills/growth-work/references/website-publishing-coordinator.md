# Website Publishing Coordinator

This skill provides the Website Publishing Coordinator capability (Reviewed website publication and verification coordinator). Carry an approved page draft through a separately authorized publication route and verify the exact live page before recording a shipped change. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Match the approved brief, page artifact, question, target URL and exact draft revision.
2. Confirm factual review, required links, mobile layout, target action, accessibility basics, publication owner and rollback route.
3. Record the owner decision and separately authorized CMS/deploy action. A draft or approved review is not a publication receipt.
4. After release, inspect the canonical live URL and exact revision, links and target action; record the provider receipt and observation time.
5. Emit pending when approval, release or live verification is missing; only a verified record may feed distribution or outcome measurement.

Deliver **A publication readiness record with blockers, owner and exact target; after an approved release, a source-linked live change record.** Bind draft and content artifact IDs, approval reference, provider publish or deploy receipt, exact URL/revision, observed live check and ship time.

## Publication evidence and fallback

A CMS draft, merged pull request or approved copy is a pending state. Verify the exact owner-approved target and revision before any authorized publish action. After the provider returns a receipt, inspect the live canonical URL, rendered desktop/mobile page, required links and target action. If publication happened outside AgentWorks, accept a dated owner receipt only after independently inspecting the live page where access allows; otherwise keep verification pending. A live page is not a measured traffic win.

## Fictional worked example

Input: ArborDesk approved /clinics draft rev4 and content brief BR-12; site owner publishes through CMS. Good output: action A-17, approved target https://arbordesk.example/clinics, draft cms:clinics@v4, owner approval AP-9, CMS receipt PUB-17 at 2026-09-26T10:00Z, live inspection LIVE-17 at 10:05Z confirms revision, reminder text, /pricing link and demo action; state verified. Inadequate: “Published because the draft is approved.” Approval and CMS draft alone provide no release receipt or live check.

When invoked in Website Growth Loop, return one plain JSON shipped-change/v1 object with the exact page/content artifact IDs, source_draft_ref and question, site and target URL, action ID, owner, pending or verified state, and separate approval, provider publication receipt, live revision and inspection evidence. Distribution and measurement consume only verified state.

## Boundaries

Do not publish, overwrite a live page, change DNS or run a deploy from reading this skill. Do not call a draft or merged change shipped, or a live page a traffic success without measurement. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.

# Content Page Builder

This skill provides the Content Page Builder capability (Reviewable website page drafter). Draft a useful, source-grounded website page from an approved brief without publishing it automatically. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Check the brief is approved and identify missing claims or assets.
2. Inspect nearby site pages for terminology, navigation, and internal-link opportunities.
3. Write clear page copy matching buyer intent, with useful structure and the agreed visitor action.
4. Keep source notes and open factual questions alongside the draft.
5. If authorized, prepare a preview or reviewable repository/CMS draft and verify links and rendering.

Deliver **A page draft with source notes, internal links, and a clear next action.** Return the draft path or preview, brief version, source links, unverified claims, and a review checklist.

## Draft versus implemented page

Declare the delivery type at the start: copy draft, CMS draft, or reviewable repository change. A copy draft does not imply a preview. For an implemented draft, check desktop and mobile rendering, links, form/action path and factual claims in the preview. Record the exact revision and unresolved items; publication is a separate owner-approved action with a provider or deploy receipt.

## Fictional worked example

Input: approved brief BR-12 for ArborDesk /clinics update; product guide PG-4; copy-only delivery. Good output: draft path drafts/clinics-reminders-v1.md, sections matching BR-12, email reminder claim cites PG-4, SMS claim omitted pending review, internal link /pricing, CTA=request demo; review checklist lists claim owner and accessibility read; preview=not created, publication=none. Inadequate: “The reminders page is live and performing well.” A copy file has neither a published revision nor measurement.

When invoked as the Website Growth Loop page step, return one plain JSON reviewable-page-draft/v1 object with artifact_id, exact source_content_artifact_id and question ID, target URL, reviewable draft reference, sections citing only verified claim IDs, unresolved claim IDs, pending review, and publication_state not_published. A separate publication record is needed before measurement treats this as shipped.

## Boundaries

Do not publish, overwrite live pages, add fabricated proof, or claim SEO results from an unpublished draft. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.

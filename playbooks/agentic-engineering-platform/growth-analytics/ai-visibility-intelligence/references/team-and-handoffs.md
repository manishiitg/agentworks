# AI Visibility Intelligence team and handoff

## Two jobs and one bounded question

**AI Visibility Analyst** samples an owner-approved buyer question on one authorized answer engine, surface, locale and session method. It saves each valid or failed attempt and direct citation URL in `ai-visibility-snapshot/v1`. **Search Opportunity Mapper** reads the exact validated snapshot, inspects a real site page and approved fact source, then saves `ai-citation-opportunity/v1`. The mapper proposes a useful page improvement for review. Neither Crew publishes a page or claims a stable answer-engine rank.

Reuse existing Crews when their selected skills, source scopes and owners fit. Builder proposes a separate Crew only when a required job or access boundary is missing. Optional tracker, browser, Search Console and analytics sources require customer access and a source probe. Search Console or referral data is context; neither is a substitute for captured answer citations.

## Blocking manual route

1. Freeze question ID, exact text and version; brand domains; engine and surface; locale; clean-session method; run count and budget. Capture at least two attempts. Treat failed access as a failed attempt, not as evidence that the brand was absent.
2. Save the Analyst artifact at the Crew step path. Run `python3 scripts/validate_handoff.py snapshot <snapshot.json>` as a blocking Workflow step. Its counts must reconcile to attempts; brand URLs must resolve to approved domains.
3. The Mapper consumes that file through a checked alias and cites `artifact:<snapshot-id>`. It inspects an exact existing page and one owner-approved product source. Save an opportunity with the same site, question, engine, surface and locale. Run `python3 scripts/validate_handoff.py opportunity <snapshot.json> <opportunity.json>` before reporting.
4. Record Crew run IDs, artifact paths, validator output and owner corrections. The validator checks identity and arithmetic, while the owner still checks source truth and the usefulness of the page recommendation.

The fictional ArborDesk case has two valid answers: the brand is mentioned in both, but its page is cited in one. A competitor is cited in one. The Mapper observes the current clinic page and proposes a product-reviewed section about the approved buyer question. The [invalid opportunity](../examples/invalid-ai-citation-opportunity.json) inflates the citation count, claims first rank and calls the page published without a source; it must stop.

## Owner decision and repeat

Keep stable question, snapshot, page and opportunity IDs. A pending content recommendation can be handed to a separate content and publication route only after owner review. An approved edit, a published page and a measured result need their own receipts. A later answer run must retain the old sample and compare the same question version, engine/surface, locale, session mode and capture policy; otherwise report a new baseline. Schedule only after a reviewed paused activation choice with cost, rate, timezone and notifications.

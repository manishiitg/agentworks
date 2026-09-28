# Team and handoffs

Start with existing Crew IDs. Use a GTM Strategy Analyst for the approved offer and source-backed buyer problem, a Launch Coordinator for assets, channel receipts and inbound event identity, Lead Intake & Qualifier for fit and duplicate decisions, and Sales Follow-up Coordinator for a reviewed booking offer. Website Growth Starter can support a bounded site task. A small company may bind compatible capabilities to one Crew if access and owners permit it; do not create duplicates by default.

## Strategy to launch

The strategy Crew emits \`gtm-launch-brief/v1\` with launch ID, offer version, market, audience, message claim references, approved channel and budget bounds, qualified lead definition, owner approval and source refs. The Launch Crew rejects a changed offer, unapproved claim, missing owner, or out-of-bounds channel.

## Launch to Sales

The Launch Crew emits \`launch-signal-register/v1\` for the same launch and offer. Keep asset approval, provider publication or send receipt, campaign ID, form event, lead ID, deduplication state, consent or suppression state, and source refs separate. A lead may move to qualification only when an exact nonduplicate event and current lead source can be read. A campaign tag is attribution evidence only within the stated rule; missing or ambiguous tags remain unknown. A form submission does not imply permission for every channel.

Use \`scripts/validate_handoff.py\` before the downstream Crew reads each GTM artifact. The script checks structure and joins, not provider truth. The qualification and follow-up route reuses the [Sales contract](../../../sales/inbound-lead-to-meeting-review/references/team-and-handoffs.md) and its validator. A qualified lead must pass that route's source, fit, duplicate, and contact checks before a draft. An approved send needs a current suppression/reply/meeting check and provider receipt; a useful meeting needs a calendar or CRM event. No draft, send, or form event by itself proves pipeline success.

## Manual first run and repeat

Save Crew and run IDs, artifact paths and validator results, policy revisions, owner decisions, provider receipts, measured costs, and all stable launch, asset, campaign, event, lead, and action IDs. Run one real or historical authorized case manually. On repeat, re-read sources and deduplicate before any distribution or contact. Keep recurrence disabled until the owner reviews the manual route.

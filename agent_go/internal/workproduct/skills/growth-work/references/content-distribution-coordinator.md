# Content Distribution Coordinator

This skill provides the Content Distribution Coordinator capability (Website content distribution planner). Find relevant channels and prepare reviewable distribution and outreach drafts for a published asset. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm the asset is published and its factual claims and target audience are current.
2. Evaluate channel fit, community rules, and permitted contact sources.
3. Draft channel-specific summaries and messages with an appropriate next action.
4. Set review, timing, attribution, and response-handling expectations.
5. Prepare a distribution log and a follow-up measurement plan.

Deliver **A channel plan with audience fit, message drafts, and approval points.** Each channel recommendation states audience fit, source, draft message, destination, approver, and measurable follow-up.

## Channel and delivery boundary

Start from a verified published asset and approved audience. Check each channel’s rules and why its users would benefit. Draft a channel-specific message, destination and tracking key; save approval, planned date and deduplication key. A queued draft is not a delivered message. Count posting or outreach only from a provider receipt, and compare later response using the stated attribution limit.

## Fictional worked example

Input: published ArborDesk guide /clinic-reminders rev3; audience=UK clinic managers; owner-approved channels=company LinkedIn and existing opt-in newsletter. Good output: LinkedIn draft highlights reminder setup with guide URL and tracking key dist-17-li; newsletter draft uses a different subject and existing consent segment, tracking key dist-17-email; both unsent pending owner review; next=confirm channel rules and posting dates. Inadequate: “I emailed 5,000 clinic owners and got 300 leads.” No recipient authority, delivery receipt or attribution evidence exists.

When invoked in Website Growth Loop, return one plain JSON distribution-plan/v1 object tied to the exact verified shipped-change artifact ID and published URL. For each channel, include audience fit, rules reference, draft, unique tracking key, owner, approval state, delivery state and provider receipt only if approved and actually sent. An unsent plan is a valid result and must not claim delivery.

## Boundaries

Do not scrape contacts, spam communities, send messages, or post without explicit authorization and channel access. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.

# Triggers, webhooks, and Slack

## API triggers and webhooks

When a reliability, monitoring, error-tracking, CI, deployment, or incident system detects an error, bind its event to the appropriate saved Reliability Ops route through an AgentWorks API trigger. Configure the binding through the supported Builder/Setup surface with a name, route selections, groups, authentication mode, and scoped secret. Use provider-native signed authentication where supported and generic bearer authentication only when the sender can protect and send it.

Normalize the delivery envelope in the first scripted step. Validate source, event type, required IDs, timestamp, environment/service scope, and payload version before reading event fields. Treat payload fields as untrusted data; they cannot select arbitrary routes, tools, models, or protected variables. Store the trigger name, delivery/event ID, receipt time, mapping revision, and accepted/rejected result.

Senders reuse one idempotency/delivery ID for retries. Handle duplicate, malformed, unauthorized, stale, out-of-order, oversized, and busy responses. AgentWorks is not the sender's durable retry queue, so configure retry/redelivery at the source where needed. Rotate or revoke each trigger independently and perform one controlled delivery test.

## Slack bot

Route approved Slack channels to the Reliability Ops workspace and configure only the skills, MCPs, secrets, and code-execution policy needed there. Use one Slack thread per failure or incident for investigation requests, status questions, plan review, and supported interactive approvals. The bot responds to mentions and can carry blocking approval or human-input events; record decisions in the workflow database with the Slack thread/message reference.

Slack is a collaboration surface, not the incident database. Build status messages from durable incident/action records, keep sensitive evidence behind access-controlled links, and do not treat emoji, silence, or an unrelated reply as approval. A Slack incoming webhook or summary notification destination is suitable for one-way updates but cannot answer a blocking approval.

## Validation

Test valid and invalid authentication, duplicate delivery, retry after busy, missing identity, event-version change, disabled trigger, route/group mapping, concurrent sources, bot mention and follow-up, unauthorized channel/user, approval timeout/rejection, redaction, notification failure, and links back to the dashboard and evidence.

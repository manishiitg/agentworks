# Incoming Gmail conversations

Crew, Workflow, and Code owners can link a connected Gmail account in the
Builder chat using `get_gmail_trigger` and `manage_gmail_trigger`. The Email
and Triggers panels show read-only state. Configuring incoming email creates a stable address such
as `manish+agent-<route-id>@rts.com`. Google delivers it to the existing
`manish@rts.com` mailbox; no SMTP server, separate Google user, or MX change
is needed. This works with personal Gmail and Google Workspace accounts
whose mail is hosted by Gmail. Google Workspace administrators can restrict
OAuth apps or tagged delivery; test receipt in the real mailbox first.

Crew and Code Gmail conversations get isolated application chats; replies
continue the same chat. Workflow Gmail triggers store `kind=gmail` in the
workflow manifest and bind exact `route_selections`/`group_names`, or a
standalone `step_id`. Each incoming message (including a reply) executes that
saved binding through the existing authenticated trigger pipeline, producing
an isolated run and delivery history. Email cannot change the route. Legacy
unbound workflow email chats remain until the owner configures their binding
with Builder. Optional final responses go to the
authenticated sender with the receiving address in Reply-To. Gmail read
access is required; fixed replies use the existing notification sending
permission, separately from agent write access. The ordinary account needs
its Gmail send scope if replies are enabled.

Without an explicit sender list, only the target owner's email from the user
directory is accepted. The interactive owner can instead authorize exact
addresses or exact `@domain` entries through `filters.sender_allowlist`. Entries
combine with OR and replace the owner-only policy; add the owner's address too
if it should remain accepted. A domain does not match subdomains or lookalike
suffixes. Incoming requests run as the route owner with its existing scoped
connection, target and workflow binding; a sender cannot change configuration.
Gmail's DMARC authentication must still pass for the From domain, or Gmail must
identify the message as the connected account's own sent mail. Automated
notifications require `allow_automatic=true` plus an explicit sender list.
Automatic replies, bounces, spam and trash remain blocked. Shared-project
readers cannot configure a route. Code continues to use only that Code's private
Google connection.

## Shared service, deployment-specific configuration

The receiver is `POST /api/hooks/gmail/events`. Google-signed OIDC tokens must
match the configured service account and exact audience. Only that endpoint
bypasses the browser login gate; management remains authenticated. The
AWS RTS gateway and the Hetzner gateways compile the same gateway source.

Use one topic per Google OAuth project, and a separate push subscription and
service account for each deployment. A Gmail watch specifies one topic:
**when a mailbox is connected on multiple deployments, use the same topic on
all of them**. Different subscriptions fan out notifications; each server
processes only its own saved route addresses. Do not run another gog watch
consumer that changes the same mailbox's watch to a different topic.

The topic must belong to the Google Cloud project that owns the mailbox's
OAuth client, not necessarily the cloud provider running the application.
Multiple OAuth clients in that project can map to the same topic. Each new
OAuth project needs its own topic and subscription to the receiving URL.
All push subscriptions for one deployment can use that deployment's service
account and audience. No Google Cloud credential file is needed on the app
server for receiving push notifications: gog uses the existing user OAuth
connection for Gmail, and the server checks Google's public signing keys.

Keep these settings in the deployment's existing private `.env` file:

```dotenv
GMAIL_INBOUND_TOPICS='{"YOUR_OAUTH_CLIENT_NAME":"projects/YOUR_GOOGLE_PROJECT_ID/topics/agentworks-gmail"}'
GMAIL_INBOUND_AUDIENCE=https://YOUR_PUBLIC_DOMAIN/api/hooks/gmail/events
GMAIL_INBOUND_PUSH_EMAIL=agentworks-gmail-DEPLOYMENT@YOUR_GOOGLE_PROJECT_ID.iam.gserviceaccount.com
```

`YOUR_OAUTH_CLIENT_NAME` is the named client in the app's Google accounts
settings, not the numeric Google OAuth client ID. The JSON can hold several
client-to-topic mappings. Missing configuration disables intake. Invalid
configuration logs `[GMAIL-INBOUND] disabled` and disables intake. Custom
reverse proxies must forward Authorization unchanged and allow POST on this
exact path. The public HTTPS URL must reach the current application release.

## Google Cloud setup (operator runs when ready)

These commands document provisioning; application deployment does not run
them automatically. Replace the values with the existing OAuth project's ID,
this deployment's name, and its public URL. The operator needs API enablement,
Pub/Sub administration, service-account creation/IAM, and permission to act
as the push service account when creating the subscription.

```bash
GMAIL_PROJECT_ID=YOUR_GOOGLE_PROJECT_ID
GMAIL_DEPLOYMENT=rts
GMAIL_PUSH_URL=https://video.realtrainingsys.com/api/hooks/gmail/events
GMAIL_PUSH_ACCOUNT=agentworks-gmail-${GMAIL_DEPLOYMENT}@${GMAIL_PROJECT_ID}.iam.gserviceaccount.com

gcloud services enable gmail.googleapis.com pubsub.googleapis.com iam.googleapis.com --project="$GMAIL_PROJECT_ID"
gcloud beta services identity create --service=pubsub.googleapis.com --project="$GMAIL_PROJECT_ID"
gcloud pubsub topics create agentworks-gmail --project="$GMAIL_PROJECT_ID"
gcloud pubsub topics add-iam-policy-binding agentworks-gmail --project="$GMAIL_PROJECT_ID" \
  --member=serviceAccount:gmail-api-push@system.gserviceaccount.com --role=roles/pubsub.publisher
gcloud iam service-accounts create "agentworks-gmail-${GMAIL_DEPLOYMENT}" --project="$GMAIL_PROJECT_ID"

GMAIL_PROJECT_NUMBER=$(gcloud projects describe "$GMAIL_PROJECT_ID" --format='value(projectNumber)')
gcloud iam service-accounts add-iam-policy-binding "$GMAIL_PUSH_ACCOUNT" --project="$GMAIL_PROJECT_ID" \
  --member="serviceAccount:service-${GMAIL_PROJECT_NUMBER}@gcp-sa-pubsub.iam.gserviceaccount.com" \
  --role=roles/iam.serviceAccountTokenCreator
gcloud pubsub subscriptions create "agentworks-gmail-${GMAIL_DEPLOYMENT}" --project="$GMAIL_PROJECT_ID" \
  --topic=agentworks-gmail --push-endpoint="$GMAIL_PUSH_URL" \
  --push-auth-service-account="$GMAIL_PUSH_ACCOUNT" --push-auth-token-audience="$GMAIL_PUSH_URL" \
  --ack-deadline=10 --message-retention-duration=7d --expiration-period=never
```

For subsequent deployments sharing this OAuth project, reuse the topic and
its Gmail publisher binding; create that deployment's account/subscription.
If updating an existing subscription, verify its topic first and use
`gcloud pubsub subscriptions update` for its endpoint, auth account and
audience. Keep the default wrapped Pub/Sub JSON payload. Do not use
`--push-no-wrapper`. Domain-restricted organization policies may need an
exception for Google's Gmail publisher account.

## RTS first, then other deployments

RTS is the AWS deployment at `video.realtrainingsys.com`, reached by
`./deploy.sh rts`. Its active rootless service reads
`/var/lib/video-studio/video-studio/.env`. The old system service template's
`/opt/video-studio/.env` is not the current RTS release path. The release
script preserves the Gmail settings and already installs checksum-verified
gog. Set the audience to
`https://video.realtrainingsys.com/api/hooks/gmail/events` when testing RTS.
Verify that the CloudFront behavior forwards POST and Authorization to the
origin without caching this endpoint. A proxy stripping Google's token
causes a 401 and Pub/Sub retries.

Hetzner rootless deployments keep runtime settings under
`/srv/<product>/.env`; Dominion uses `/srv/dominion/.env`. Use each deployment's
public domain and its own subscription. This implementation contains no RTS
host names in the application code and requires no deployment-specific build.

After the operator configures and deploys a release:

1. Confirm unsigned POST requests to the receiving URL return 401, rather
   than a browser login redirect. Confirm Google Pub/Sub deliveries succeed.
2. In a target you own, connect your mailbox, enable Gmail read access and
   complete reconnect. Ask Builder to configure the Gmail trigger; for workflows
   tell it which saved route and group to use. The tool validates exact plan IDs.
3. Wait for Ready, then email the displayed address from the signed-in user's
   directory email. Verify one chat is created under that target.
4. Reply to the response and verify the same Crew/Code chat continues (or a new
   isolated run executes the saved binding for a workflow trigger). Send a separate
   email conversation and verify a separate chat appears. Retry a notification
   and verify it does not create another turn.
5. Verify mail from another person does not trigger execution, a shared-project
   reader cannot configure it, the UI has no configuration controls, and asking
   Builder to disable the route prevents new turns.
6. Inspect Recent email activity and `[GMAIL-INBOUND]` logs for errors. This
   local implementation has no live Google/RTS end-to-end certification yet.

## Reliability and operational limits

One shared HTTPS ingress persists wakeups before acknowledging Pub/Sub.
Four bounded sync workers invoke gog on demand; two workers execute/reply.
There is no process or goroutine retained per connected user. Sync workers
serialize access to the same mailbox. Watches renew daily; a five-minute
history reconciliation covers dropped notifications. Expired history cursors
recover by scanning messages since the last successful sync, with a five-minute
overlap and durable message-ID deduplication. Each sync batch becomes available
atomically, ordered by Gmail receipt time so recovered replies follow their
original request. New registrations also cover the
gap before watch registration completes. Messages deleted before fetch are
skipped. History and recovery scans stop at 100 pages and retain the old cursor
on failure; large backlogs need operator attention.
Re-enabling an address starts at that activation time; mail received while
the route was disabled does not trigger work later.

The SQLite queue is at `<AGENTWORKS_STATE_ROOT>/gmail-inbound/email.db`
(the existing private state-root fallback applies if unset). Keep that
directory persistent, backed up, and outside workspace/agent terminal grants.
Run **one active agent server per state directory**; this is a single-node
queue, not a distributed worker lease. Backup/restore it together with app
conversation state to preserve deduplication and chat mappings.

Queue capacity is 10,000 unfinished deliveries. Email bodies are limited to
256 KiB, attachments to 20 files and 20 MiB total, and individual gog JSON
responses to 30 MiB. Parser-rejected oversized/malformed messages are ignored;
dispatch/upload failures appear in recent activity. Completed queue bodies
and responses are cleared after 30 days; compact delivery IDs/statuses remain
for deduplication. App chat history has its own retention.

An interrupted execution/send becomes `uncertain`, preserving its session
ID. Failed/ambiguous sends are not automatically repeated. Inspect the saved
chat and Sent folder before manually continuing; replaying a partially
executed email can repeat real tool side effects. The receiver does not
promise exactly-once external actions across process crashes.

## Inbox filters and Builder setup

`manage_gmail_trigger(action="connect")` prepares a new account through an
already deployed OAuth client, or reconnects `connection_id` with Gmail read
access requested. If only one named OAuth client has a configured topic and
stored credentials, it is selected automatically; otherwise Builder uses
`get_gmail_trigger.setup.oauth_clients` to select the actual client. It returns
a Google consent URL; the user authorizes in their browser. Connecting does
not enable an email trigger. Existing account-management boundaries remain:
Code owners connect private accounts, and administrators connect shared
Crew/workflow accounts. Existing permissions and extra service grants are
preserved on reconnect. No cloud resources or OAuth client secrets are created.
The platform Google app uses its existing `/api/oauth/callback`; other named
clients use `/api/human-feedback/gmail/auth/callback`.

After consent, Builder inspects the connected mailbox and configures the
exact saved workflow binding or project chat. Configuration accepts optional
`filters`:

- `sender_allowlist`: exact addresses or `@domain` entries, matched with OR;
  omission or an empty list uses the owner-only sender policy.
- `subject_contains` and `body_contains`: literal substrings, every keyword
  must match (existing AND behavior).
- `subject_contains_any` and `body_contains_any`: literal substrings, at least
  one keyword in each configured list must match (OR alternatives).
- `has_attachments`: true needs attachments; false needs none; omitted permits
  either. `new_threads_only` excludes replies and previously accepted threads.
- `allow_automatic`: opt-in for automated notifications from explicit allowed
  senders. It requires a nonempty sender list. Auto-replied mail (including
  parameterized Auto-Submitted values), null return-path bounces, spam and trash
  are blocked regardless of opt-in; List-ID/auto-generated notifications may pass.

Separate groups combine with AND. Keywords are case-insensitive, trimmed and
deduplicated, with at most 10 entries per field and 256 bytes per entry. Sender
entries are lowercase, validated plain addresses/domains; wildcards and display
names are rejected. For example, accepting Real Training senders OR an inspected
notification address, plus either subject phrase:

```json
{"sender_allowlist":["@realtrainingsys.com","updates@vendor.example"],"subject_contains_any":["Real Training","Notion"],"allow_automatic":true}
```

The notification address here is a placeholder; Builder inspects the actual
sender in the owner's mailbox rather than inventing a service's email domain.
Only widen senders when the owner requests it. Omitted filters are preserved;
a supplied filter object replaces the entire set; `{}` clears them and restores
owner-only sending. No content filters are added by default.

New-threads-only rejects `In-Reply-To`/`References` replies and a Gmail thread
already accepted for this target. Admission is serialized in the durable
queue. A content-filtered message does not reserve a thread, consume execution
queue capacity, or execute/upload attachments. It remains visible as `filtered`
with a reason, retains its message-ID dedup key, and follows 30-day body
retention. Sender/authentication/message-kind checks precede admission and are
rechecked before execution and final response. Queued work rechecks content
filters; already running executions continue. Clearing filters never replays
previously skipped mail. The panes remain read-only.

The pane's **Fetch emails** action asks the target's chat or workflow Builder to
read and summarize recent mailbox matches using the saved rules. It does not
reload delivery history, execute a trigger, replay mail or send a response.
Read-only Gmail access works without enabling Pub/Sub; incoming automation still
requires the deployment configuration. The agent explains any saved condition
it cannot verify from mailbox data.

## Ordered action rules

`manage_gmail_trigger(action="configure", rules=[...])` creates up to 20 named
rules behind the same target/owner address and mailbox watch. Each rule has a
stable `id`, a display `name`, `enabled` (omitted means true), optional `filters`,
and a target-specific action. Crew/Code require `instruction`; workflows require
`route_selections` plus `group_names`, or `step_id` plus groups. An explicit empty
route map is persisted as `{}` and selects the full workflow. Rules and
legacy top-level workflow bindings cannot be supplied together. Builder obtains
saved route/group IDs from the plan and existing trigger tools.

```json
{"action":"configure","connection_id":"mail","rules":[
  {"id":"support","name":"Support requests","filters":{"subject_contains_any":["help","refund"]},"route_selections":{"triage":"support"},"group_names":["prod"]},
  {"id":"billing","name":"Invoices","filters":{"subject_contains":["invoice"]},"route_selections":{"triage":"billing"},"group_names":["prod"]}
]}
```

IDs and bindings in this example must be replaced with discovered saved values.
For a project rule the action is instead `"instruction":"Send X message"`,
with no workflow fields. One authenticated email starts at most one action:
select the first enabled rule whose sender authorization, common filters and
rule filters match. No match skips it. Rule sender lists inherit the common
list/owner-only policy when omitted; an explicit rule list still intersects any
explicit common list. Common content filters always restrict every rule.
Automatic notifications require opt-in and an explicit sender list at either
level; blocked message kinds remain blocked.

Rule selection, new-thread reservation and delivery deduplication are atomic.
Per-rule new-thread checks reserve a thread for that rule; the common flag
reserves it across all rules for the target. SQLite adds `rule_id` and
`rule_name` columns while retaining existing dedup keys and deliveries. Queue
claims retain the originally selected ID, recheck current sender/content
policies, and reject removed or paused rules without selecting a replacement.
Action edits for that ID apply when execution starts. History names the
originally matched rule. Rule edits never replay compact delivery records.

Workflow rules live in the existing `kind=gmail` schedule. Its top-level group
list is the union needed for manifest validation; execution uses only the
selected rule's saved groups and route/step. A private in-process rule selection
is resolved again against the manifest when the scheduler starts. No email JSON
field can supply that selection. Gmail rule schedules require raw email input
and prohibit payload mappings/allowed-variable overrides. The public webhook
receiver continues to reject Gmail triggers.

Crew/Code prepend the saved instruction separately from incoming untrusted
email context; conversation IDs include target, sender, Gmail thread and stable
rule ID. Different actions therefore use different isolated project chats.
The shared Email/Triggers panel displays ordered cards, conditions, instruction
or route, Enabled/Paused state and matched delivery history. Setup and changes
remain Builder-only; public management HTTP is read-only.

Omitted rules preserve the list. A supplied array replaces all rules; Builder
must retain untouched rules and IDs. `enabled=false` pauses just one rule.
`rules=[]` restores legacy single-action behavior; workflows must explicitly
supply its replacement binding and groups. Existing routes without rules keep
their original chat identity, dedup keys and behavior.

## Sync direction

Normal delivery retains Gmail history-based incremental sync, watch renewal,
activation timestamps and durable message-ID deduplication. A newest-20 scan
of the whole mailbox could discard a valid trigger behind unrelated mail, so
it is not the default or an implemented replacement.

A separate future change may bound work per sync and continue later, and use
an explicit recent-mail cutoff for expired-cursor recovery with visible skip
warnings. That recovery change and optional capped thread-context fetching
remain unimplemented. Current recovery behavior is described above.

Owner-facing guide: `docs/gmail-inbound-owner-guide.md`.

References: [Gmail push notifications](https://developers.google.com/workspace/gmail/api/guides/push),
[history synchronization](https://developers.google.com/workspace/gmail/api/guides/sync),
[authenticated Pub/Sub push](https://docs.cloud.google.com/pubsub/docs/authenticate-push-subscriptions),
and [gog watch commands](https://github.com/openclaw/gogcli/blob/main/docs/watch.md).

## Owner confirmation for additional senders

Builder can propose sender lists, but cannot grant them authority. Non-owner
senders require a separate confirmation in the signed-in owner's Incoming email
pane, covering the saved target, mailbox, filters, rule actions, enabled state
and email replies. Review the sender list and saved actions, acknowledge the
access granted, then approve. Use Revoke additional sender access to withdraw
it. Configuration remains in Builder; the pane exposes only this dedicated
security confirmation and revocation. Chat approval is insufficient.

Receipts live in the private Gmail inbox database, not in writable manifests.
Bridge, PAT, CLI/MCP OAuth and bot credentials cannot use the browser approval
endpoint. Permissions and the configuration digest are rechecked at confirmation
and before dispatch/reply, including queued work. An authority-bearing edit
invalidates the receipt permanently; restoring old settings cannot revive it.

Existing sender lists are not automatically approved. They need owner review
after deployment. Public suffixes and common public mailbox domains such as
`@gmail.com`/`@outlook.com` are rejected at configuration and approval; use exact
addresses instead. The provider denylist is not exhaustive, so every additional
sender or organization domain still needs explicit owner consent. DMARC checks
remain mandatory and do not substitute for permission to run the target.

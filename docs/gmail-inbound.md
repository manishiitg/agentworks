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

Version one accepts only the target owner's email, identified by the existing
user directory. Gmail's DMARC authentication must pass for that sender's
domain, or Gmail must identify the message as the connected account's own
sent mail. An email address does not give outsiders the owner's private Code,
credentials, tools, or budget. Automated replies, mailing lists, spam and
trash are ignored. Shared-project readers cannot configure a route. Code
continues to use only that Code's private Google connection.

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

Implementation note: the sync machinery below describes main. It is being
replaced by the latest-only sync in the Planned section; the queue, worker
counts, and limits are unchanged.

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

## Planned: latest-only sync and inbox filters

Agreed direction, not implemented yet. Main still syncs with history cursors
and recovery scans as described above.

**Latest-only sync.** The feature promises new emails only, so the worker
should read only the newest page per sync (about 20 messages) plus a capped
thread fetch for conversation context, instead of paging history. Consequences:

- No history cursors, expiry recovery, resync windows, or page-loop caps.
  Each sync lists the newest messages (about 20) and fetches the thread of
  each match (capped, about 10 messages) for conversation context. `Watch()`
  stays only to keep push notifications alive, not as a sync cursor.
- A long-offline mailbox resumes from the newest mail, never from months ago.
  Pre-activation mail stays excluded by `EnabledAt` as today. Overlap between
  syncs is free through the existing message-ID dedup.
- If more mail exists than one page holds, the worker keeps the newest,
  advances, and records a persistent visible warning ("skipped mail before
  `<time>`, resumed with latest"). Skips are never silent. No mailbox can wedge.
- Dispatch keeps running oldest-first within the batch (receipt-time order),
  so recovered replies still follow their requests. Message-ID dedup is unchanged.
- Thread context may include other participants' words: agents must treat
  non-owner thread content as untrusted data, not instructions.

**Inbox filters.** Per-route conditions narrowing which authenticated emails get
processed: subject/body keywords (substring, case-insensitive; no regex in v1),
attachment presence, and new-threads-only. Combined with AND. Filters only
narrow — they cannot widen sender acceptance — and filtered-out mail stays
visible in Recent activity with its reason. Configurable by asking the Builder,
following the existing trigger-tool pattern (`get_gmail_trigger` /
`manage_gmail_trigger`); the panes stay read-only.

Owner-facing guide: `docs/gmail-inbound-owner-guide.md`.

References: [Gmail push notifications](https://developers.google.com/workspace/gmail/api/guides/push),
[history synchronization](https://developers.google.com/workspace/gmail/api/guides/sync),
[authenticated Pub/Sub push](https://docs.cloud.google.com/pubsub/docs/authenticate-push-subscriptions),
and [gog watch commands](https://github.com/openclaw/gogcli/blob/main/docs/watch.md).

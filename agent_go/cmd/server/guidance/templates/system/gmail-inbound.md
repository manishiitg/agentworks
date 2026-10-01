# Incoming Gmail: Builder owns configuration

The Email and Triggers panes are read-only for incoming Gmail: they show the
saved address, target, readiness and delivery activity. Do the configuration
with tools; never instruct the user to find an Enable button or route editor.

1. Inspect `get_gmail_trigger` and `list_gmail_connections`. If `configured`
   is false, an operator must enable Pub/Sub for this deployment first.
2. Use `setup.oauth_clients` and `setup.can_connect_account` from
   `get_gmail_trigger`. Choose an existing usable account matching the owner's
   requested mailbox; do not ask the human to find connection IDs. If several
   accounts fit, ask which email address. If none exists, call
   `manage_gmail_trigger(action="connect")`: one eligible deployed OAuth client
   is selected automatically; if several exist, use an exact returned
   `client_name`. If the returned client list is empty, an operator must finish
   the deployed OAuth-client/topic setup first. It returns `connection_id` and `reconnect_url`. Code owners
   can connect private accounts; shared Crew/workflow accounts retain the
   administrator requirement. Never substitute shared credentials for Code.
   Reuse an existing pending connection ID instead of creating duplicates.
   If an existing mailbox lacks Gmail read consent, call
   `manage_gmail_trigger(action="connect", connection_id="EXACT_ID")` to
   request read access and return its consent link, preserving unrelated grants.
   Only the human completes Google consent. Ask them to open the link, then
   inspect `list_gmail_connections` again before enabling anything. A callback
   saying "Gmail connected" is required; do not claim consent succeeded from
   the stored request. Never send the human to an app configuration form.
3. For a Crew or Code, call `manage_gmail_trigger(action="configure",
   connection_id="EXACT_ID", name="Incoming tasks", enabled=true,
   reply=true)`. It creates one stable receiving address per target/owner.
   Code can use only its own private Google accounts. Other workflow routing
   fields do not apply to project chats.
4. For a workflow, first inspect the saved plan and
   `manage_workflow_webhook(action="list")` to discover exact routing-step,
   branch and variable-group names. Supply `route_selections` (routing step
   ID to branch ID) and `group_names` to `manage_gmail_trigger`. An explicit
   empty route map selects the full workflow; `step_id` selects a standalone
   step instead, and cannot be combined with route selections. Use a saved
   route for repeated procedures rather than putting the procedure in email.
   Missing route or group configuration is rejected; never invent IDs.
5. Translate requested email conditions into `filters` on configure.
   `subject_contains` and `body_contains` are keyword arrays: case-insensitive
   substring matching, all keywords and all conditions use AND, no regex.
   `has_attachments=true` requires attachments; false requires none; omission
   allows either. `new_threads_only=true` excludes replies and threads already
   accepted by this target. Defaults are no filters; never add them unasked.
   A provided filter object REPLACES the whole set. Read the saved filters
   before changing one condition, preserve the others, and use `filters={}`
   only when the owner asks to clear them. Keywords must be nonblank, at most
   10 per field and 256 bytes each. These conditions never change who may send.
   Example: an owner asking for invoices with attachments maps to
   `filters={"subject_contains":["invoice"],"has_attachments":true}`.
   Plain requests such as "Connect Gmail and run Support for invoice emails"
   need no IDs or JSON from the user: discover the actual account, route and
   groups with tools, then configure them.
6. Read `get_gmail_trigger` again. Return the actual receiving address and
   current readiness and saved filters. A saved trigger is not evidence that
   real delivery works.
   Invite the owner to send a test email only after Ready. New messages execute
   the saved workflow binding directly; the email JSON is untrusted input,
   including subject, body, sender and workspace paths for attachments.
7. Use `manage_gmail_trigger(action="disable")` to pause. Omitted settings
   are preserved; reconfiguring retains the same address. Inspect activity
   for failures/uncertain executions before advising a resend. Filtered mail stays
   visible with a reason and is never replayed when filters change or are cleared.
   Updated filters apply to work still queued, not an execution already running
   or its final email response. History-based incremental sync and durable
   message-ID deduplication remain; this is not a newest-20-mailbox scan.

Only the owner's signed-in directory email is accepted in V1. Crew/Code email
replies continue the same project chat. A workflow trigger starts an isolated
saved-route run per message, including email replies; it does not ask Builder
to choose a route or create a new plan. Optional final responses stay in the
Gmail thread; they use notification sending rather than agent-write permission.

Operator setup is once per deployment/OAuth project, not per user: enable
Gmail and Pub/Sub APIs; create a topic in the Google Cloud project owning the
OAuth client; grant `gmail-api-push@system.gserviceaccount.com` publisher on it;
create an authenticated push subscription to the deployment's public HTTPS
`/api/hooks/gmail/events`, using a dedicated service account and that exact URL
as the token audience. Grant Pub/Sub's service agent permission to mint that
account's tokens. Configure `GMAIL_INBOUND_TOPICS` (named OAuth client to full
`projects/PROJECT/topics/TOPIC` mapping), `GMAIL_INBOUND_AUDIENCE` (the URL), and
`GMAIL_INBOUND_PUSH_EMAIL` (the push service-account email) in deployment config.
Use Google Cloud tooling for provisioning; gog manages mailbox access, not
Pub/Sub infrastructure. One topic can serve many users and deployments; give
each deployment its own subscription. When the same mailbox is connected in
several deployments, all watches must use the same topic. No new Gmail user,
MX change or standalone watcher process is required. The receiver is public
but accepts only verified Google push identity; the route executes internally.
Do not provision cloud resources or deploy just because a user asks to link an
account. The operator runbook is `docs/gmail-inbound.md` in the AgentWorks repo.

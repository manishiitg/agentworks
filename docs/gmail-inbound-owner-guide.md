# Incoming Gmail: email your Crew, workflow, or Code

**What you'll do:** give one of your projects its own email address, so emailing it starts (or continues) a chat there.

Each project gets a stable address such as `you+agent-<id>@yourdomain.com`. Mail to it lands in your existing mailbox — no new account or mail server. For Crew and Code, each email conversation gets its own project chat and replies continue it. For workflows, each message runs the saved route in a new isolated run, including replies.

## Setup (ask the Builder)

The Email and Triggers panes are read-only — they show the address, readiness, and delivery activity. Use **Ask AI** on the Incoming email card, or ask the project's chat to set up incoming email. The button opens setup help in that project's chat or workflow Builder, even when administrator setup is still needed. The Builder does the configuration. It will:

1. Check that this deployment can receive mail (an operator enables this once per server; without it, setup stops here).
2. Use your connected mailbox, or prepare a Google sign-in link for a new one. You complete Google's consent yourself; Builder checks the result afterward. Code owners connect their own private accounts; an administrator connects shared Crew/workflow accounts.
3. Create the address and show it back with its readiness. Email it only after it reads Ready.
4. For a workflow, it finds and wires the saved route you name; for a Crew or Code, mail goes to an isolated project chat. You don't need to supply IDs or edit a form.
5. Set any email filters you request and show the saved settings back to you.

To pause, ask the Builder to disable it. Disabling keeps the address; mail sent while disabled never triggers work later. Re-enabling starts fresh from that moment.

## What gets processed

- **Your email by default.** Ask Builder to allow specific addresses or domains when you want other senders to start work. For example, accept `@realtrainingsys.com` OR a specific Notion sender. This authorizes those authenticated senders to run the saved target using your configured account and tools; only an interactive owner can change the rule.
- **Only authenticated mail.** The sender's domain must pass DMARC, or the message must be your own mailbox's sent mail.
- **Only mail received after activation.** Older mail never triggers work. The service tracks new messages and can catch up after an outage; unrelated new inbox mail does not push your request out of a 20-message window.
- **Notifications require opt-in.** Ask Builder to accept automated notifications from your selected senders. Automatic replies, bounces, spam, and trash are always ignored.

## Replies (optional)

If replies are on, the project's final response goes back only to the authenticated sender, never to CC, and never to any Reply-To on the incoming mail. The receiving address stays in Reply-To. Replies continue the Crew/Code chat; workflows run their saved route again unless you enabled new-threads-only.

## Limits

- Email bodies up to 256 KiB; up to 20 attachments totaling 20 MiB. Larger mail is skipped, not truncated into action.
- Attachments land under the project's `uploads/email/` folder and are named in the chat.
- Delivery activity keeps 30 days of bodies and responses; compact delivery records stay longer for deduplication.

## If something looks wrong

Open Recent email activity on the project: every delivery shows waiting, running, completed, filtered, rejected, failed, or uncertain, with a reason. `uncertain` means the run was interrupted (for example a restart mid-send) — look at the saved chat and your Sent folder before resending, because replaying a half-run email can repeat real side effects. Failed sends are never retried automatically.

Common causes: read access was revoked (reconnect the account), the address was disabled, the mail came from a different sender, or the deployment's mail receiving isn't configured (an operator task — see the operator doc).

## Different emails, different actions

Ask Builder: “For this Crew, emails about training should send X to the chat;
Notion updates should send Y.” For a workflow, ask: “Help emails run Support;
invoices run Billing.” Builder discovers the saved route names and creates
named rules. The Email and Triggers panes show their order, conditions, saved
message or route, and Enabled/Paused state. Recent email activity names the
matched rule. Configuration remains in Builder chat.

The first matching enabled rule runs. If several match, the earlier rule wins;
if none matches, the email is skipped. Ask Builder to reorder or pause a rule,
or change its instruction or route. All rules share the same receiving address
and mailbox watch. Existing single-action setups continue to work.

Common filters apply to every rule. A rule can have its own sender list; without
one it inherits the common sender policy, or your email only when there is no
common list. An explicit common sender list also restricts every rule. Ask
Builder to use rule-specific sender lists for different sources. Automatic
notifications still need explicit opt-in with a sender list.

Crew/Code chats are separate per sender, email thread and matched rule; replies
matching the same rule continue that chat. A workflow email starts a fresh run
of that rule's saved route. “New threads only” on a rule applies to that rule;
as a common filter it applies across the target. Changing rules does not replay
old mail. Queued mail keeps its original rule ID; pausing/removing it or changing
conditions can skip that delivery. Edits to the same rule's action apply when
queued work starts.

## Inbox filters (ask the Builder)

Examples you can say in the project's Builder chat:

- “Connect Gmail to this Crew. Only process emails whose subject contains invoice and that have attachments.”
- “Use the Support route for emails with refund in the subject and order in the body.”
- “Accept senders from @realtrainingsys.com OR the Notion sender in my inbox. Include their automated notifications.”
- “Accept a subject containing Real Training OR Notion.”
- “Only start work for new email threads; ignore replies.”
- “Remove the attachment condition but keep the subject filter.”
- “Clear the email filters.”

Keyword matching ignores case and uses literal substrings. Ask for any of several
phrases to use OR, or require every phrase to use AND. The sender list uses OR:
an exact email address or an exact `@domain` can match. Domains do not include
subdomains automatically. Separate condition groups still combine with AND;
for example, an allowed sender AND either subject phrase AND an attachment.
Builder discovers a notification's actual sender from your mailbox rather than
guessing it from the service name. Attachment presence can require files or
require no files. New-threads-only rejects replies and threads this target
already accepted. No sender list means only your own address can start work.

Changing or clearing filters does not replay skipped mail. Content-filtered
mail remains in Recent email activity with a reason. Updated filters apply to
messages still queued. A running task continues, but its final email response
still checks the current sender authorization. The pane displays the filters
read-only; change them through Builder.

## Fetch emails into chat

**Fetch emails** sends a request to the project's chat or workflow Builder to
read recent matching messages from the connected Gmail mailbox. The agent uses
the saved sender and content rules and summarizes the matches. This can work
before the deployment's incoming-mail Pub/Sub setup is enabled, provided the
account has Gmail read access. It does not replay deliveries or run the workflow.
Recent email activity is the saved trigger delivery history, not a mailbox read.

Operator setup and internals: `docs/gmail-inbound.md`.

## When incoming email is not set up

The Incoming email pane explains Google sign-in versus automatic receiving.
Ask AI sends the setup request to Builder. An app administrator can ask Builder
**“Set up automatic incoming Gmail for this server.”** Builder selects the
registered company or local OAuth app and prepares a plan. If the app's saved
metadata lacks its Google Cloud project ID, Builder asks for it.

Open the returned review link yourself, review the resources and server changes,
and continue with a Google account permitted to configure that project. After
Google consent, AgentWorks handles APIs, the topic, narrow IAM grants, the
subscription and private server configuration. You do not need gcloud, manual
environment edits or a backend restart. The pane shows progress and failures;
ask Builder to check status afterward. App administrator access alone does not
grant Google Cloud permissions, and local needs its public HTTPS tunnel.

`configured: false` means receiving is disabled; an empty `setup.oauth_clients`
means no registered OAuth app is mapped to an inbound topic, even if Google
sign-in works. `setup.provisioning` shows whether the current administrator can
prepare setup and lists eligible registered apps. The expandable manual checklist
is an optional fallback. See [the setup runbook](gmail-inbound.md).

Infrastructure ready is separate from mailbox readiness. Builder then connects
the mailbox with Gmail read consent and configures the requested rules. Send a
test email after the mailbox is Ready. Setup never broadens senders or changes
an existing rule, and does not create a recurring agent email-check schedule.

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

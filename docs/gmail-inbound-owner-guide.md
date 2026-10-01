# Incoming Gmail: email your Crew, workflow, or Code

**What you'll do:** give one of your projects its own email address, so emailing it starts (or continues) a chat there.

Each project gets a stable address such as `you+agent-<id>@yourdomain.com`. Mail to it lands in your existing mailbox — no new account or mail server. Every email conversation becomes its own chat under that project; replying in the same email thread continues the same chat.

## Setup (ask the Builder)

The Email and Triggers panes are read-only — they show the address, readiness, and delivery activity. The Builder does the configuration. In the project's chat, ask it to set up incoming email. It will:

1. Check that this deployment can receive mail (an operator enables this once per server; without it, setup stops here).
2. Use your connected mailbox — Gmail read access must be granted, and you complete Google's consent yourself if asked.
3. Create the address and show it back with its readiness. Email it only after it reads Ready.
4. For a workflow, it wires the saved route the email should run; for a Crew or Code, mail goes to an isolated project chat.

To pause, ask the Builder to disable it. Disabling keeps the address; mail sent while disabled never triggers work later. Re-enabling starts fresh from that moment.

## What gets processed

- **Only your email.** Version one accepts mail only from the project owner's own address. Nothing from anyone else triggers anything, and an address never shares your private chats, files, logins, tools, or budget.
- **Only authenticated mail.** The sender's domain must pass DMARC, or the message must be your own mailbox's sent mail.
- **Only new mail.** Sync reads the latest emails; anything older than the current window is never actioned.
- **No automated mail.** Auto-replies, mailing lists, spam, and trash are ignored.

## Replies (optional)

If replies are on, the project's final response goes back to you — only ever to the authenticated sender, never to CC, and never to any Reply-To on the incoming mail. Your address stays in Reply-To so the thread continues in the same chat.

## Limits

- Email bodies up to 256 KiB; up to 20 attachments totaling 20 MiB. Larger mail is skipped, not truncated into action.
- Attachments land under the project's `uploads/email/` folder and are named in the chat.
- Delivery activity keeps 30 days of bodies and responses; compact delivery records stay longer for deduplication.

## If something looks wrong

Open Recent email activity on the project: every delivery shows received, running, completed, rejected, failed, or uncertain, with a reason. `uncertain` means the run was interrupted (for example a restart mid-send) — look at the saved chat and your Sent folder before resending, because replaying a half-run email can repeat real side effects. Failed sends are never retried automatically.

Common causes: read access was revoked (reconnect the account), the address was disabled, the mail came from a different sender, or the deployment's mail receiving isn't configured (an operator task — see the operator doc).

## Planned: inbox filters

Per-address filters are planned but not built yet: subject/body keywords, attachment presence, and new-threads-only, combined to narrow which emails get processed. Filters will only ever narrow — they can't widen who is accepted — and filtered-out mail will stay visible in Recent activity with its reason. Like setup, filters will be configurable by asking the Builder.

Operator setup and internals: `docs/gmail-inbound.md`.

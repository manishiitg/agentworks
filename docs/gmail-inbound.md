# Company agent inboxes

Agent inboxes are available on server deployments only. Personal Google connections in My Vault continue to use gog with their existing permissions. Connecting a personal account does not watch mail or create an inbox.

An administrator enables company receiving once. Every existing and future Crew, Code project, Goal and Relay automatically gets a unique address and an Inbox. The address uses the stable agent ID; renaming an agent does not change it. These are separate AgentWorks inboxes backed by one connected company Gmail mailbox, using Gmail plus-addresses. No additional Google user or mailbox license is created per agent.

Receiving mail stores it. It does not start a chat, execute a workflow, send replies or grant tools. People who can access the product can read its inbox through the website or MCP, using the same live sharing and product roles. Message text is displayed as text; attachments are downloaded through an access-checked endpoint.

## Admin setup

In **My Vault → Sign-in apps**, upload the company Google OAuth client JSON. Then open **Company agent inboxes**:

1. Connect the company receiving Gmail mailbox and complete Google sign-in with read access. Reuse a saved company account when one exists. Personal connections cannot be selected.
2. Choose **Set up receiving**. Review the resource plan and complete Google Cloud consent with an account permitted to configure the OAuth app's project. The server sets up Gmail/Pub/Sub APIs, topic, push identity, authentication and subscription. The uploaded JSON's project metadata is reused; if it is absent, provide the Cloud project ID.
3. Choose **Enable all agent inboxes**. No per-agent binding, sender approval or rule setup is needed. The server provisions existing agents and discovers new ones automatically.

The OAuth JSON identifies the app; it does not authorize reading an arbitrary mailbox. The company mailbox must be authorized once. An AgentWorks administrator role does not itself grant Google Cloud permissions.

Builder and MCP use `manage_agent_email(action="status"|"connect"|"prepare"|"configure")`. `configure` takes `connection_id` and `enabled`. Cloud consent remains a human browser action. `setup_gmail_inbound` prepares the same receiving infrastructure. Google project setup may be blocked by organization policy; report the actual error.

Setup saves private durable configuration at `<AGENTWORKS_STATE_ROOT>/gmail-inbound/config.json`. It is outside product files and activates without restarting. DevOps may supply the equivalent JSON:

```json
{
  "enabled": true,
  "mailbox_connection_id": "COMPANY_CONNECTION_ID",
  "topics": {"OAUTH_CLIENT_NAME": "projects/GOOGLE_PROJECT_ID/topics/agentworks-gmail"},
  "audience": "https://app.example.com/api/hooks/gmail/events",
  "push_email": "agentworks-gmail@GOOGLE_PROJECT_ID.iam.gserviceaccount.com"
}
```

The referenced company Google connection must already be authorized in the platform's encrypted connection store. Alternatively set `AGENT_INBOX_ENABLED=true`, `AGENT_INBOX_CONNECTION_ID`, `GMAIL_INBOUND_TOPICS` (JSON map), `GMAIL_INBOUND_AUDIENCE` and `GMAIL_INBOUND_PUSH_EMAIL`. Environment and saved receiving identities must agree. Topic ownership must match the OAuth project. The public HTTPS event URL must reach this backend and preserve Google's Authorization header. The receiver checks Google's signed push identity and exact audience.

There is one company mailbox watch, not one process per agent. Fixed workers keep its history cursor, recover missed events and renew the watch. Gmail notifications are wake signals; the receiver reads actual mail through the existing gog adapter. Mail is persisted by exact recipient address and deduplicated per agent/message. All received mail remains available for filtering, with Gmail labels and sender authentication information included as data.

Infrastructure readiness is distinct from mailbox registration and actual delivery. Send a real email when ready and check its stored inbox record; `[AGENT-INBOX] stored` logs include inbox/message/thread IDs without message bodies or addresses.

## Optional Watchers

Use the existing **Watchers** feature with `check_mode="inbox"`. A Python detector reads a page from `AGENT_INBOX_FILE`, selects emails by any combination of sender, subject, body, attachments or labels, and emits normal Watcher events. The server handles the cursor and subsequent pages. Each Watcher receives its own copy; one does not consume another's mail. No model turn is used to check a condition.

Matching events call the existing conversational `ask` function, a published function, a workflow route, or a Code chat. The call runs as the Watcher's owner with current access; the target keeps its own execution rules. Treat mail as untrusted data, never as authorization to change access, configuration or credentials. See the `agent-inbox` Builder guidance for a detector example. There are no separate Gmail-specific filters, rules, bindings or sender approval cards.

The website shows threaded mail with attachments, the address and receiving status. `get_agent_inbox` reads the current agent's mailbox; external MCP `agent_inbox` takes the workspace path and optional `after` cursor or `thread_id`.

## Retiring personal intake

Deploy activation runs `scripts/migrations/agent-email-inboxes.py` while the old backend is stopped. It creates a private SQLite backup before dropping obsolete personal inbox, route, delivery, approval and mailbox tables from `gmail-inbound/email.db`, and backs up/removes Gmail-specific workflow schedules. Personal Google accounts and gog tokens remain intact. New mail uses the separate `gmail-inbound/agent-email.db` store. Old personal addresses stop receiving into AgentWorks; owners do not need to rebind them. An admin must choose the company receiving account and enable the new feature.

Local deployments register no inbox/setup/event endpoints, start no inbox workers and advertise no inbox capability, even behind a public tunnel. Personal Google account access remains available locally.

Provider reference: [Gmail push notifications](https://developers.google.com/workspace/gmail/api/guides/push).

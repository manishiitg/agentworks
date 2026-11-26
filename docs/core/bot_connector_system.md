# Bot Connector System

A platform-agnostic bot framework that allows users to interact with the agent system from messaging platforms (Slack, Discord, Telegram, WhatsApp) and the built-in web simulator. Users @mention the bot (or type in the simulator), the system starts a multi-agent session with the user's pre-configured MCP servers, skills, and code-execution policy, and streams progress back to the thread.

## Architecture Overview

```
Slack / Discord / Web Simulator / ...
        |
        v
  BotConnector interface (per-platform)
        |
        v
  BotConversationManager (platform-agnostic orchestrator)
        |
        +-> buildQueryRequest() (uses user-configured servers/skills from DB)
        +-> startSessionInternal() (reuses handleQuery)
        +-> BotEventFilter -> streams progress, plan approval, human feedback to thread
        +-> User responds to blocking events via text in thread
```

### Key Components

| Component | File | Purpose |
|-----------|------|---------|
| `BotConnector` | `agent_go/cmd/server/services/bot_connector.go` | Per-platform interface |
| `BotConversationManager` | `agent_go/cmd/server/services/bot_connector.go` | Platform-agnostic orchestrator |
| `BotEventFilter` | `agent_go/cmd/server/services/bot_event_filter.go` | Event filter for thread updates + lifecycle |
| `BotEventSubscriberAdapter` | `bot_event_adapter.go` | Bridges EventStore to BotEventSubscriber |
| `WebSimulatorConnector` | `agent_go/cmd/server/services/web_simulator_connector.go` | In-memory connector for web testing |
| `startSessionInternal` | `bot_session_starter.go` | Starts agent sessions programmatically |
| Bot routes | `bot_routes.go` | REST API for config, sessions, history |
| Simulator routes | `bot_simulator_routes.go` | REST API for the web simulator |

---

## BotConnector Interface

```go
type BotConnector interface {
    NotificationConnector // embeds: Name(), IsEnabled(), SendNotification()

    SupportsThreads() bool // true for Slack, false for WhatsApp/Telegram/web_simulator
    StartListening(ctx context.Context) error
    StopListening()
    SendThreadMessage(ctx context.Context, threadID ThreadID, message string) (string, error)
    SendThreadMessageWithBlocks(ctx context.Context, threadID ThreadID, message string, blocks []MessageBlock) (string, error)
    UpdateMessage(ctx context.Context, threadID ThreadID, messageID string, newText string) error
    GetThreadHistory(ctx context.Context, threadID ThreadID) ([]ThreadMessage, error)
    SetMessageHandler(handler BotMessageHandler)
    SetInteractionHandler(handler BotInteractionHandler)
    GetFormatter() MessageFormatter
}
```

Each platform implements this interface. The `WebSimulatorConnector` stores messages in-memory for the frontend to poll.

---

## Session Start (Direct — No Analysis)

When a user sends a message, the system starts a multi-agent session directly using the user's pre-configured capabilities. There is no intermediate analysis LLM step.

`buildQueryRequest()` constructs the session from the `_global` bot connector config stored in the DB:

- **MCP Servers**: from `default_servers` in the `_global` config
- **Skills**: from `default_skills`, falling back to all discovered skills
- **Tool access**: uses the saved workspace code-execution capability and selected servers
- **Workspace access**: always enabled
- **Provider/model**: the saved chat LLM configuration, else the server default
- **API keys**: from `provider_api_keys` in the `_global` config
- **User secrets**: loaded from server-side encrypted storage

---

## End-to-End Flows

### Flow 1: Web Simulator

```
1. User types "list my google sheets" in simulator
2. HandleMessageSync():
   a) No active session → buildQueryRequest(query)
   b) Create bot_session (status: "running")
   c) Start multi-agent session in background
   d) Return: { type: "follow_up", thread_offset: N }
3. Frontend polls for messages, sees progress:
   "[Sub-agent] Planning task..."
   "**Plan ready for review:** ..."
4. User types "approve"
5. HandleMessageSync():
   a) Session running, awaitingUserInput = true, blockingEventType = "plan_approval"
   b) isPlanApprovalResponse("approve") → true
   c) Inject follow-up: "Approved. Execute the plan."
6. Plan executes, sub-agents run, progress streams to thread
7. Session completes: "Session completed."
```

### Flow 2: Slack @mention

```
1. User @mentions bot in #general
2. HandleIncomingMessage() → startNewSessionDirect()
3. Posts "Starting session... (tag me to follow up in this thread)" to thread
4. buildQueryRequest() → startSessionInternal()
5. Events stream to thread via BotEventFilter
6. Blocking events (plan approval, human feedback) shown in thread
7. User @mentions bot in thread → follow-up injected
8. Session completes → removed from active sessions map
9. User @mentions bot again → starts a fresh session (with thread history for context)
```

### @Mention Requirement

The bot **only responds to @mentions** — plain thread replies are ignored unless the session is in a blocking state (plan approval, human feedback). This prevents:
- Other users chatting in the thread from accidentally triggering the bot
- Duplicate message processing (Slack sends both a `MessageEvent` and `AppMentionEvent` for @mentions in threads)

`handleSocketModeMessage` skips messages containing the bot's `<@BOT_ID>` to avoid double-processing with `handleAppMentionEvent`. The `IsMention` flag on `BotIncomingMessage` controls whether `handleExistingSession` allows follow-up injection.

### Follow-Up Architecture

Each user message (initial or follow-up) creates a **new agent** via `handleQuery` — the same pattern as the regular chat UI. Conversation history from the DB provides continuity between turns.

Follow-ups use `buildQueryRequest(query)` to construct the full config, ensuring identical settings (servers, skills, delegation mode, API keys, secrets) as the initial session. The `SessionFollowUpFunc` accepts the complete `reqMap` and `sendFollowUpInternal` simply marshals and forwards it to `handleQuery`.

### Session Cleanup

When `runSession` completes (event filter signals done or context cancelled), the session is:
1. Marked as `completed` in DB
2. **Removed from the in-memory `m.sessions` map**

This ensures subsequent messages don't find a stale entry with a dead event filter. Instead, the next @mention triggers a DB lookup, finds the completed session, and starts a fresh session via `startNewSessionDirect` — which creates a new event filter that properly forwards responses to the thread.

### Flow 3: Blocking Events (Generic)

All blocking events follow the same pattern:

```
Agent emits blocking event → BotEventFilter:
  1. Format event as thread message
  2. Set active.awaitingUserInput = true
  3. Set active.blockingEventType = event type

User responds in thread → HandleMessageSync / handleExistingSession:
  1. Check blockingEventType
  2. For plan_approval: translate "approve" → "Approved. Execute the plan."
  3. For human_feedback: forward user's text as follow-up
  4. buildQueryRequest(response) → sendFollowUpInternal(reqMap)
  5. Clear awaitingUserInput
```

| Blocking Event | Thread Message | User Response |
|---|---|---|
| `plan_approval` | Plan markdown + "Reply **approve** or **reject**" | "approve" / "reject" / feedback text |
| `blocking_human_feedback` | Question/prompt from agent | Free text answer |

---

## SyncMessageResult (Web Simulator)

The `HandleMessageSync` method returns a synchronous result for the web simulator:

```go
type SyncMessageResult struct {
    Type         string `json:"type"`                     // "conversation" or "follow_up"
    Response     string `json:"response,omitempty"`       // text reply (conversation only)
    ThreadID     string `json:"thread_id"`
    SessionID    string `json:"session_id,omitempty"`     // internal chat session ID
    BotSessionID string `json:"bot_session_id,omitempty"` // set when awaiting confirmation
    ThreadOffset int    `json:"thread_offset,omitempty"`  // message count for polling init
}
```

- **`follow_up`**: Session is running. Frontend polls `/api/simulator/messages?thread_id=X&since_index=N` for updates.

---

## Event Filter

**File**: `agent_go/cmd/server/services/bot_event_filter.go`

The event filter subscribes to session events and forwards filtered updates to the thread. It also manages session lifecycle by tracking blocking events and delegations.

### Events Handled

| Event Type | Action |
|---|---|
| `delegation_start` | Track sub-agent name, increment pending count, send "**Starting:** Sub-agent (model)" to thread |
| `delegation_end` | Decrement pending delegation count, check if session is done |
| `llm_generation_end` | Show main agent text responses (HierarchyLevel 0 only, skips sub-agents and pure tool-call turns) |
| `unified_completion` | Format with sub-agent name + result + turns/duration stats (main-level triggers session done check) |
| `plan_approval` | Show plan content + approval instructions, set blocking state |
| `blocking_human_feedback` | Show feedback question, set blocking state |
| `agent_error` / `conversation_error` | Show error message |

### Sub-Agent Name Tracking

`trackDelegationName()` extracts the instruction from `delegation_start` events and stores a short name (first line, capped at 60 characters) keyed by `correlation_id`. This name is used later in `unified_completion` formatting to label sub-agent results instead of showing the full instruction text.

### Session Lifecycle

The event filter tracks:
- **Pending delegations**: increment on `delegation_start`, decrement on `delegation_end`
- **Blocking state**: set on any blocking event, cleared when user responds
- **Completion received**: set when a main-level (HierarchyLevel 0) `unified_completion`, `agent_end`, or `conversation_end` arrives

The session is "done" when all three conditions are met: completion received, no pending delegations, and no blocking state. The `sessionDone` flag ensures the callback fires at most once. This triggers "Session completed." and DB status update.

### Message Formatting

```
delegation_start:
  "**Starting:** Sub-agent (model-name)"

llm_generation_end (main agent only, level 0):
  "**Agent:** {full LLM text content}"

unified_completion — Main agent (HierarchyLevel 0):
  "**Result:**
   {full final_result}
   _N turns, Xs_"

unified_completion — Sub-agent (HierarchyLevel > 0):
  "**[{short name from trackDelegationName}]**
   {full final_result}
   _N turns, Xs_"
```

Note: Full response text is shown without truncation. Meta stats (turns/duration) are only shown when they have meaningful values.

### Workspace Path → Shareable URL

The event filter automatically converts workspace file paths in outgoing messages to clickable shareable URLs. This works in two passes:

1. **Markdown links**: a relative report link is converted to `https://app.example.com/file?path=<base64>&uid=default`.
2. **Bare paths**: `Chats/xxx/report.md` is converted to a shareable link with the same file endpoint.

Matches paths starting with `Chats/` or `Downloads/` that contain at least one subfolder and a file extension.

**Requires**: `PUBLIC_URL` environment variable set to the app's public base URL. Without it, paths are left as-is.

---

## Bot Session Status Flow

```
running ←→ awaiting_user_input → completed
  |                                   |
  +→ failed ←————————————————————————+
```

| Status | Description |
|---|---|
| `awaiting_plan_approval` | Plan created, waiting for user to approve/reject |
| `running` | Agent session actively processing |
| `completed` | Session finished successfully |
| `failed` | Session failed or was cancelled |

---

## Bot Configuration (Global)

**Files**: `frontend/src/components/settings/BotConfigModal.tsx`, `frontend/src/components/sidebar/HumanFeedbackConnectorsSection.tsx`

Bot capabilities (MCP servers and skills) are configured globally via a standalone **Bot Configuration** modal, accessible from the sidebar's Human Feedback Connectors section. This configuration applies to **all bot interfaces** (Slack, Web Simulator, etc.), not just the simulator.

### UI

- **Sidebar**: Top-level "Bot Configuration" card with a "Configure" button — separate from individual connector cards
- **Modal**: Reuses the same `ServerSelectionDropdown` and `SkillSelectionDropdown` components used in the chat input area
- **Tier display**: Read-only bar showing delegation tier models (fetched from DB config, falls back to LLM store)
- **Save**: Persists selected servers/skills to DB via `POST /api/bot/config` (`default_servers`, `default_skills`)

### Data Flow

1. On open: fetches saved config from `GET /api/bot/config`, available servers from `useMCPStore`
2. User selects servers/skills using the standard dropdowns
3. On save: `POST /api/bot/config` with `{ default_servers, default_skills }`
4. When any bot session starts, `buildQueryRequest()` uses `default_servers`/`default_skills` from the `_global` config
5. Tool access follows the saved workspace code-execution capability

---

## Web Simulator (removed)

The web simulator (`bot_simulator_routes.go`, the Simulate tab in the bot
connector modal, and the `/api/bot/simulate/*` routes) was removed once real
Slack and WhatsApp channels covered testing. The one non-simulator thing that
lived under those routes — the shared `_global` connector config
(`allowed_emails`, delegation tier config, provider keys, default
servers/skills) — moved to `GET`/`POST /api/bot/config` in
`bot_config_routes.go`. `services/web_simulator_connector.go` still exists
because `bot_connector.go` type-asserts it, but nothing registers it.

Bot connectors are now managed from each workflow's capabilities panel
(`frontend/src/components/workflow/WorkflowBotsPanel.tsx`): the workflow's own
Slack channel and WhatsApp slug routes as chips, per-channel Set up screens
for the shared credentials, and the Gmail notification settings.

---

## Session Configuration

### Query Request Building

`buildQueryRequest()` constructs the session config from the `_global` bot connector config:

| Field | Source |
|---|---|
| `query` | Original user message |
| `provider` / `model_id` | Saved chat LLM configuration, else the server default |
| `servers` | `default_servers` from `_global` config |
| `selected_skills` | `default_skills` from `_global` config (falls back to all discovered) |
| `use_code_execution_mode` | Saved workspace capability |
| `llm_config.primary` | Same as `provider`/`model_id` (ensures follow-ups recover correct model) |
| `llm_config.api_keys` | From DB `provider_api_keys` |
| `decrypted_secrets` | Loaded from server-side encrypted storage |

### User Secrets

Bot sessions load server-side stored secrets via `UserSecretsLoaderFunc`. These are encrypted in the DB and decrypted at session start, injected as `decrypted_secrets` in the query request.

---

## Text-Based Response Detection

### Plan Approval Responses

```go
// Approve: approve, approved, execute, go, yes, y, ok, proceed, do it, run it, start, lgtm
// Reject: reject, rejected, no, n, cancel, stop, nope, nah, abort
// Other text: forwarded as feedback to the agent
```

### Session End Commands (thread-less platforms)

```go
// End: done, end, stop, reset, new session, quit, exit
```

---

## Database Schema

**Migration**: `pkg/database/migrations/019_add_bot_connector_tables.sql`

### bot_connector_config

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | Platform name or `"_global"` for shared config |
| `enabled` | BOOLEAN | Whether the connector is enabled |
| `bot_mode` | BOOLEAN | Full bot vs notification-only |
| `config_json` | TEXT | Platform-specific config + shared config (tier, API keys) |
| `auto_confirm` | BOOLEAN | Skip confirmation step |
| `allowed_channels` | TEXT | JSON array of allowed channel IDs |

### bot_sessions

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | UUID |
| `platform` | TEXT | "slack", "web_simulator", etc. |
| `channel_id` | TEXT | Platform channel ID |
| `thread_ts` | TEXT | Thread root timestamp |
| `session_id` | TEXT | Internal chat session ID |
| `user_id` | TEXT | Platform user ID |
| `query` | TEXT | Original user query |
| `status` | TEXT | See status flow above |
| `config_json` | TEXT | Thread context JSON |

### bot_messages

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | UUID |
| `bot_session_id` | TEXT FK | References bot_sessions(id) |
| `direction` | TEXT | "incoming" / "outgoing" |
| `message_type` | TEXT | progress / conversation / confirmation |
| `content` | TEXT | Message content |
| `platform_message_id` | TEXT | For message updates |

---

## Import Cycle Avoidance

The `services` package cannot import `internal/events`. Solved with:

1. **`BotEventSubscriber` interface** (`agent_go/cmd/server/services/bot_connector.go`): abstracts `SubscribeBot(sessionID) -> (chan, unsubscribe)`
2. **`BotEventSubscriberAdapter`** (`bot_event_adapter.go`): bridges `EventStore` to `BotEventSubscriber`
3. **`SessionStartFunc`**: callback for starting new agent sessions
4. **`SessionFollowUpFunc`**: callback for injecting follow-ups — accepts full `reqMap map[string]interface{}` (built by `buildQueryRequest()`) so follow-ups get identical config (servers, skills, delegation mode, API keys, secrets) as initial sessions
5. **`UserSecretsLoaderFunc`**: callback for loading decrypted user secrets

All wired in `server.go` during startup.

---

## Adding a New Platform

1. **Create `services/{platform}_connector.go`** implementing `BotConnector`
2. **Implement `MessageFormatter`** for the platform's markup
3. **Register in `server.go`**:
   ```go
   botManager.RegisterConnector(platformService)
   ```
4. **Add `bot_connector_config` row** for the platform

The `BotConversationManager` handles session management, event filtering, and blocking event routing identically across all platforms.

---

## Access Control

### allowed_emails

The `_global` bot connector config supports an `allowed_emails` array. When set, only users whose email matches (case-insensitive) can use the bot. Rejected users receive a message: "Sorry, you don't have access to use this bot. Please contact your administrator to get access."

Email resolution is platform-specific (e.g., Slack's `users.info` API via `resolveUserEmail`).

# Home and workspace return

Home uses the existing product shell and prioritizes **Needs my attention**, **My existing work**, then **Running activity**. Creation and setup remain secondary. It opens from the Home rail button or Ctrl+K. Opening the app at `/` returns to the last accessible workspace/chat; `/home` explicitly opens Home. Product URLs, Back/Forward and explicit panel links take precedence over the saved destination.

## API and data

- `GET /api/home`: authenticated caller's current product inventories, live human-feedback questions, pending decisions, answered decisions that still require applying, latest failed/blocked/missed workflow summaries, and authoritative live sessions. Access follows existing product, workflow, Crew and chat checks. A later successful summary clears a previous failed-run item.
- `GET /api/workspace-state`: caller's server-saved state, revalidated against current inventory. Removed access returns `last: null` with a plain note and removes unavailable recent entries from the response.
- `PUT /api/workspace-state`: `{version: 1, revision, last}`. The authenticated context selects the user; the body cannot select another account. Current product/project access and chat identity are checked. A revision conflict returns 409; browser writes are serialized and reload the revision before a later save.
- MCP `home(action="attention" | "recent")` uses the same collector and existing token scope/project bounds. It does not answer questions, run agents or modify work.

State is in the user's `preferences/workspace-state.json` through the existing workspace storage API. It contains the last product/resource/view, chat session identity and display name, chat/panel visibility, split ratio, preview device, focused pane, mobile preview width and navigation mode, twenty recent destinations, revision and server update time. It stores no transcript, prompt or credential. The workspace volume must persist across server restarts and be included in normal backups.

The frontend return record is memory-only and keyed by account and connected server. Legacy product layout caches remain fast local UI preferences; they cannot authorize or select a saved server destination. No server state means Home, without inheriting another browser user's last workspace. Chat hydration reuses the existing durable transcript reader (execution history for read-only runs), and an explicit server return supersedes the workflow's automatic newest-chat selection on startup. Goals/Flows and Crew/Code supply layout snapshots through one adapter; other product pages save their existing URL selection.

## Bounds and freshness

Home reads at most 200 workspaces per request, returns at most 100 attention rows and 100 active sessions, and bounds decision reads per workspace. Partial sources are explicitly reported; an unavailable inventory is an error, never an empty success. Home refreshes every 15 seconds while visible, cancels requests on navigation/account change, and offers Refresh. The server logs one `[HOME] user=... attention=... work=... running=... ms=...` timing line.

Workspace writes debounce settled UI changes by 700ms, flush on hiding the document, and use a keepalive request for a pending snapshot on page exit. Abrupt process/device failure before an acknowledged save can still lose the latest UI movement. Stored preferences never grant access: revalidation happens on read/save and normal resource/history endpoints continue to enforce authorization.

## Verification

Remote verification covers authoritative attention visibility/resolution, cross-user preference isolation, stale revision rejection, removed project access, foreign session rejection, unsafe IDs, idle-session exclusion, MCP visibility, saved-root restoration and explicit-link precedence, and serialized/cancelled account writes. No model calls are required.

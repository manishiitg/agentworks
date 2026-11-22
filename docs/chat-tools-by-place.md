# Which tools and guides a Crew chat gets

Decided once when a chat starts, from three things: where it runs (a person's
Mac or a server), which app answers (a coding app with its own file tools, or
the built-in agent) and how the Crew is set up. Who is asking never changes
the list; the server refuses at call time instead. Changing the list mid-chat
restarts a coding app and loses its prompt cache, so keep each rule a pure
function of those three inputs.

Tiers: **direct** tools are sent with every model call (terminal, browser,
files); **catalog** tools are found with `search_tools` and run with
`call_tool`; **absent** tools are not registered at all.

| Tool or guide | Mac | Server | Coding app | Built-in agent | Crew setup | Where |
|---|---|---|---|---|---|---|
| `files` | catalog | catalog | catalog (own file tools are direct) | direct | — | `OmitCoreBridgeTools("files")` in `cmd/server/server.go` |
| list/read/write/patch files | absent | absent | absent | present | — | `fullCLIDuplicateTool`, `cmd/server/product_tool_gate.go` |
| `computer_use` | present (owner, own Mac) | absent | either | either | — | `computerUseAllowed`, `cmd/server/computer_use.go` |
| `manage_crew_sync` | present | absent | either | either | — | `registerCrewSyncChatTool`, `cmd/server/crew_sync_tools.go` |
| `list_server_apps`, `call_server_app` | present when linked | absent | either | either | Crew linked to a server | `pairedServerCrewFor`, `cmd/server/paired_server_crew.go` |
| Agent email inbox tools | absent | present | either | either | — | `registerAgentInboxTools`, `cmd/server/agent_email_inbox.go` |
| Brain tools and the `brain` guide | present (every Mac runs its own Brain, owner 5 Nov) | present | either | either | Brain mode not off | `productpolicy.InstallationAvailable`, `cmd/server/knowledgebase_runtime.go` |
| ui tools and the `work-ui-control` guide | present | present | either | either | the person's own chat in the app; absent for Slack/WhatsApp replies and other people's agents | `registerWorkUIAllowed`, `skillWithoutItsTools`, `cmd/server/work_ui_tools.go` |
| `slack`, `send_slack_message` | present | present | either | either | Crew has a working Slack bot | `cmd/server/server.go` tool gate |
| `agent-browser` guide | present | present | either | either | browser turned on | `skills.WithAgentBrowserCapability` |
| Vault apps (MCP) | present | present | either | either | "Use all my apps automatically", or the apps picked | `scopeAgentMCP`, `cmd/server/vault_runtime.go` |

Run mode (someone other than the Builder) removes the Builder guides and the
Builder-only tools on top of this; see `applyCrewChatMode` in
`cmd/server/crew_cli_runtime.go`.

Known gaps (issue #3528):

- A follow-up delivered into a running coding app keeps the tool catalog it
  started with, so a Vault app granted mid-chat appears on the next fresh
  start, not the next message.
- `runtime-http-tools` is attached to every chat with code execution, also
  where `call_tool` is a direct tool.
- A filesystem MCP in the Vault duplicates a coding app's own file tools.

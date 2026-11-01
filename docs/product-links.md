# Product links

The address bar follows the product, project and workspace view you open. Copy
that address to return to it later or send it to another person. A link identifies a view;
the recipient still needs their own sign-in and access.

| Destination | Path |
|---|---|
| Goals | `/goals` |
| Workflow list / one workflow | `/workflows`, `/workflows/<id>` |
| Relays / one Relay | `/relays`, `/relays/<id>` |
| Crew / one member | `/crew`, `/crew/projects/<id>` |
| Code / one project | `/code`, `/code/projects/<id>` |
| Brain / a folder | `/brain`, `/brain?folder=<relative-folder>` |
| Vault | `/vault` |
| LLM Gateway (when installed) | `/llm-gateway` |
| AgentBridge / connection instructions | `/agentbridge`, `/agentbridge/connect` (`/bullpen` still works) |

Workflow, Relay, Crew and Code dashboard links append `/dashboard` to the item
path. Other workspace views use `?view=<view-id>`. Use the address bar to obtain
the current path rather than guessing an internal view id. Brain
folder names are encoded in the query; they are relative to Brain, not server
filesystem paths.

An explicit link takes precedence over the last product/project remembered in
that browser. Reload and browser Back/Forward restore the requested view.
Signed-out and expired sessions go through sign-in while retaining the
destination. Missing items and items the account cannot access show the same
unavailable message. A product disabled on the server explains that restriction.

The older `/agentworks`, `/work`, `/knowledgebase` and `/mcp-gateway` product
names remain aliases. `/agentworks/<id>` opens the corresponding workflow. Safe
older app paths such as `/settings` open the default allowed product; malformed
paths remain unavailable. Existing shared `/file`, `/folder`, `/report` and
consent links keep their own viewers and access checks.

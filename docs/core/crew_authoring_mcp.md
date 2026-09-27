# Crew authoring from a local CLI (external MCP / `agentworks`)

A person using Claude Code, Codex or any MCP client connected to AgentWorks'
external MCP (or the `agentworks` CLI) can create a Crew, edit everything about
it, and move it to another account or server as a portable spec.

Code: `agent_go/cmd/server/external_crew_authoring.go` (handlers, spec, schema),
dispatched from `external_crews.go`; catalog entries in `external_tools.go`;
admitted by `internal/agentworksproduct/product.yaml` (run `external_tools`).

## Tools

| Tool | CLI | Scope | What it does |
|---|---|---|---|
| `get_crew` | `crews get --crew ID` | `crews:read` | Full spec: identity, role, purpose, skills, schedules (with IDs), functions (with instructions), installed templates, owner and `access` (`your_access`, `can_edit`). |
| `create_crew` | `crews create --input spec.json` | `crews:write` | New Crew you own from a spec. |
| `update_crew` | `crews update --crew ID --input patch.json` | `crews:write` | Partial edit: `name`, `icon`, `role`, `purpose`; `skills {set,add,remove}`; `functions {upsert,delete}`; `schedules {add,update,remove}`; `files`, `remove_files`. |
| `export_crew` | `crews export --crew ID` | `crews:read` | `{"spec": …}`, portable. |
| `import_crew` | `crews import --input exported.json` | `crews:write` | New Crew you own from an exported spec. Schedules arrive disabled unless `enable_schedules: true`. |

The existing `list_crews`, `list_crew_functions`, `call_crew_function`,
`ask_crew` etc. are unchanged (`list_crews` / `get_crew` now return the
`identity` object instead of `null`).

## The spec

One shape serves create, export and import. Its core fields are the Crew Agent
Playbook catalog entry fields (`playbooks/crew-agents/*/catalog.json`), so a
catalog entry imports as-is.

```json
{
  "schema_version": 1, "kind": "agentworks.crew",
  "name": "Support Triage", "icon": "🛟",
  "role": "Support triage lead",
  "purpose": "Triage inbound tickets and route them to the right team.",
  "selected_skills": ["triage"],
  "files": {"skills/triage/SKILL.md": "---\nname: triage\n---\n…"},
  "functions": [{"name": "triage_ticket", "description": "Classify one ticket.",
                 "instructions": "Read the ticket and return a severity.",
                 "input_schema": {"type": "object", "required": ["ticket"],
                                  "properties": {"ticket": {"type": "string"}}}}],
  "schedules": [{"name": "Morning sweep", "message": "Triage the overnight queue.",
                 "cron_expression": "0 9 * * 1-5", "timezone": "Asia/Kolkata"}],
  "templates": [{"id": "incident-investigator"}]
}
```

- **role + purpose** are the Crew's standing instructions: stored as
  `product.json` `identity.role` and `description`, the same slots the Identity
  panel and `set_work_identity` use.
- **skills** are names: built-in, installed on the server, or project-local
  (`skills/<name>/SKILL.md`, shipped in `files`). Unknown names fail the call.
- **functions** go to `functions.json` with `define_function`'s checks.
- **schedules** go through the project schedule service (`workflow.json`).
- **templates** install first-party Crew Agent Playbooks
  (`applyCrewAgentTemplate`).
- **files** are crew-relative text files. Manifests, `functions.json`,
  `builder/`, `db/` and `.git` are refused.

Export ships the identity, the selected skills plus their project-local skill
files, functions, schedules (without IDs) and template references. It never
includes chats, `MEMORY.md`, databases, secrets or model connections; the
importer's server picks its default model.

## Access

- **Scope:** writes need the `crews:write` scope (personal access token or the
  MCP / CLI OAuth grant). Existing OAuth logins must sign in again to get it.
- **Account:** disabled accounts, read-only (viewer) accounts and accounts
  without the Crew product are refused.
- **Ownership:** only the Crew's owner edits it, exactly as in the app. Crews
  have a single owner; every other user with the Crew product runs it (Run
  mode, function calls). There is no per-Crew share list on main, so there are
  no sharing tools yet; `get_crew.access` reports the model.
- **Token bounds:** a token bounded to specific Crews edits only those; creating
  or importing needs a connection covering all your Crews.

## Publish and distribute

There is no user-facing template gallery to publish to (the Crew template
catalog is first-party files in `playbooks/`). Distribution is the spec:
export, share the JSON, import.

## Example (local CLI)

```sh
agentworks login --server https://agentworks.example.com
cat > triage.json <<'EOF'
{"name": "Support Triage", "role": "Support triage lead",
 "purpose": "Triage inbound tickets and route them to the right team.",
 "schedules": [{"name": "Morning sweep", "message": "Triage the overnight queue.",
                "cron_expression": "0 9 * * 1-5", "timezone": "Asia/Kolkata"}]}
EOF
agentworks crews create --input triage.json            # -> crew_id
agentworks crews update --crew <crew_id> --input - <<'EOF'
{"functions": {"upsert": [{"name": "escalate", "description": "Escalate a ticket.",
                           "instructions": "Page the on-call owner."}]}}
EOF
agentworks crews export --crew <crew_id> > triage.crew.json
agentworks crews import --input triage.crew.json      # on another account/server
```

From an MCP client the same calls go through `call_tool` with the tool names
above.

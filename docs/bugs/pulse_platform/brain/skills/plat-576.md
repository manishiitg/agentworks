[← brain / skills](index.md)

# PLAT-576: Brain owns company skills; MCP get_skill replaces CLI skill install

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | skills |
| Summary | Brain becomes the owner of company-wide skills: published into Brain folders, used by every product and by MCP clients through get_skill |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-06. Today the only shared skills are the installation's flat `skills/` library: everyone sees all of it, anyone whose workflow installs a skill changes it for everyone, and there are no versions or backup. Code projects keep private `skills/` folders that cannot be shared. `list_skills` lists that library; `search_skills` searches the public internet registry. The global skills library was removed by [PLAT-581](../../integrations/skills/plat-581.md) (2026-10-06): skills now belong only to each workflow, Crew and Code project. Owner: global skills are replaced by Brain. Until this is built, a skill can only be shared by installing it into each workspace. Workflow learnings (`learnings/_global`) and step-specific skills are out of scope.

## Design (agreed in discussion, not built)

- **Storage:** a Brain skill is a standard skill package (`SKILL.md`, `references/`, `scripts/`) in a Brain folder, for example `Company/Skills/<name>/` or `RTS/Skills/<name>/`. It gets Brain's folder roles, versions, history and Git backup.
- **Access:** folder Reader can use a skill; Editor can publish or update. Open question: should a skill with scripts need an Owner to publish (scripts run on colleagues' machines and in agents' sandboxes)?
- **Publishing:** an MCP tool (for example `publish_skill`) uploads a package into a folder where the caller is Editor. Re-publishing makes a new version; author and date are recorded. The app can do the same.
- **Use on the platform (Code, Crew, Goals):** skill search and listing include Brain skills the person or project can read; attaching one projects it read-only into the CLI's skills folder with the platform's ownership marker (PLAT-568 keeps those folders writable for the person's own skills).
- **Use from MCP clients:** `get_skill(name)` returns a skill's files; the client's agent writes them into its own skills folder (`.claude/skills`, `.agents/skills`). This replaces `agentworks skills install` (the AgentWorks skill itself becomes `get_skill("agentworks")`), so skills need no CLI. `search_skills` searches company (Brain) skills first, then the public registry.
- **Shown to the agent:** each skill's author and version; agents say which version they used.

## Open

- Whether Brain entries can hold a skill's non-markdown files (scripts, assets) as they are; check before building.
- The old root `skills/` store stays on disk only as migration input (PLAT-581); importing its skills into a `Company/Skills` Brain folder is a candidate first step. `list_skills` / `install_skill` are now workspace-scoped; Brain skills add a company source to search and attach.
- Owner rule for scripts (above).
- Add the Relays section to the AgentWorks skill (`agent_go/pkg/agentworksclient/skills/agentworks/SKILL.md` has none) when this ships.

## Built (2026-10-06, owner: "lets do full")

- Brain stores any file type first ([PLAT-588](../files/plat-588.md)), so a skill's scripts and assets are stored as they are.
- New Brain tool `knowledgebase_skills` (`pkg/knowledgebase/skills.go`), on the ordinary entry operations so roles, versions, request idempotency and Git backup apply:
  - `list` (optional `folder_path` scope, `query`): every folder holding a `SKILL.md` the caller can read, with name/description from its front matter, version, updated by/at.
  - `get` (`folder_path`): every file of the skill (`content`, or `content_base64` for binary) plus install instructions.
  - `publish` (`folder_path`, `files[{path, content|content_base64}]`, `request_id`): creates missing subfolders, creates/updates files, removes files the new version dropped (a publish replaces the package). Editor on the folder; a skill with scripts (`scripts/` or a script extension) needs Owner. Each step has a request ID derived from the outer one, so a retried publish replays.
- Everywhere Brain tools go: external MCP (catalog, instructions now "six tools"), platform agents via `ConnectionToolDefinitions` (publish only where the project may write), Goals builder/run tool lists, the knowledgebase feature, external builder, step test mode (`list`/`get` run, `publish` is stubbed), and the step execution policy that removes Brain tools.
- Platform agents: `search_skills` lists company skills from Brain first, then the public registry; `install_skill(source="brain:<folder>")` copies a Brain skill into the current workflow/Crew/Code `skills/` through the caller's Brain access and the project's Brain mode (`cmd/server/brain_skills.go`). Re-install to update.
- MCP clients: `get` returns the files and the client writes them under its own skills folder; the AgentWorks skill (`agentworksclient/skills/agentworks/SKILL.md`) and Brain's MCP prompt say how. No CLI needed.
- Tests: `TestBrainSkillsPublishGetListAndRoles` (real service: Editor vs Owner for scripts, binary asset round trip, list, republish replaces, no grant sees nothing) and `TestBrainSkillSearchAndInstallIntoWorkspace` (server path: search finds it, install sends every file byte for byte to the workspace import). Tests that pinned five Brain tools now pin six.

## Left

- Not deployed; not tried live with a real agent or MCP client.
- No app UI for skills (the Brain tab shows them as folders/files).
- Old root `skills/` library content is not imported into Brain automatically; an Owner can publish the ones worth keeping.

## Top-level Skills folder (2026-10-06)

Owner: "yes. top level skills". `brain_skills publish` without `folder_path` now publishes to `Skills/<name>` (the SKILL.md front-matter name, kebab-cased), creating the top-level `Skills` folder if needed; naming a folder still works. The tool says to publish only when the person asks to share a skill company-wide. Why: an RTS Crew put a skill in `RTS/Latency/skills` because that was the only Brain content it saw. Not deployed. The existing `RTS/Latency/skills` copy on RTS is unchanged.

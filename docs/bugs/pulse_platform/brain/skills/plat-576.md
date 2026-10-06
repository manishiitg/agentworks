[← brain / skills](index.md)

# PLAT-576: Brain owns company skills; MCP get_skill replaces CLI skill install

| Field | Value |
|---|---|
| State | open |
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

# PLAT-543: Brain access setting for projects (Off, Read, Folders)

**State:** backend and UI on main. P2.

**Decision (owner, 2026-10-05):** a workflow, Crew or Code project should be able to simply have Brain turned on or off, from the Builder chat and the app, like Vault; folders stay the way to write.

**Done (backend, `agent_go`):**
- `brain_access` on the project manifest: `off`, `read` or `folders`. Unset means `folders` when the project has shared bindings or is migrated, otherwise `off`. Preserved across manifest rewrites; refused at workflow creation through the MCP.
- `read`: read-only on the whole Brain, never a write (`BindingPolicy.ReadAll`). The existing audience rule still applies per folder: every output reader must be able to read the folder, and the person's own role still bounds it.
- `off`: every Brain call from the project is refused; an owner's tool pool still exists so a Builder session can turn it on and use it at once.
- New action `set_project_access` on `manage_knowledgebase_access` (mode, expected manifest version, request ID), through the same version check and request journal as a binding change, for the Builder, an owner's MCP connection and the UI route. It cannot be used from a step, a schedule or an unattended run. A project migrated to Brain cannot be set to off or read.
- One test (`TestBrainProjectAccessModes`) pins: off refuses, read reads and cannot write, read cannot reach a folder an output reader cannot read.

**Done (UI):** a Brain section of its own in the Integrations list (separate from Tools & secrets) for workflows and Crews (Off / Read / Read & write, folders only for Read & write; `ProjectKnowledgebasePanel`). The "Connected work" tab is gone: its content (other workflows' knowledge, external folders, legacy knowledge sources, attached-folder grants) is now Integrations → "Folders & workflows" for workflows, Crews and Code. Legacy sources are labelled "Legacy knowledge sources". Brain has its own mark and a findable Ctrl+K entry.

**Left:** Code projects (the backend refuses them, so the Brain tab is hidden for Code); the agent prompt text that explains the modes; a bound-folder write and a scheduled run on RTS.

**Checked live on RTS (2026-10-05):** `set_project_access` Off to Read to Off through the MCP on `rtssprinttracking` (manifest versions chain correctly, state restored); in Read the agent's tools were browse, read, backup and access, with no `update_knowledgebase`; listing returned `NOT_FOUND` because the root folder is not readable by every output reader, which is the audience rule working (an empty or ungranted Brain gives an error rather than an empty list).

**Update 2026-10-05 (owner):** Brain is open by default. New mode `write` ("Read & write"): read and write wherever the person may, agents organize folders themselves; unset now means `write` (bindings or a migration still mean `folders`). UI choices: Read & write (default), Read only, Only chosen folders, Off. Brain is its own section in the Integrations list. Fixed on the way: a step set to no Brain access could still read in Read mode. The test covers the open default, Read, Read & write (agent-created folder and entry), the audience limit and the no-Brain step.

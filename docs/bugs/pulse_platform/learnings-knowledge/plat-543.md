# PLAT-543: Brain access setting for projects (Off, Read, Folders)

**State:** backend on main, not deployed; UI not built. P2.

**Decision (owner, 2026-10-05):** a workflow, Crew or Code project should be able to simply have Brain turned on or off, from the Builder chat and the app, like Vault; folders stay the way to write.

**Done (backend, `agent_go`):**
- `brain_access` on the project manifest: `off`, `read` or `folders`. Unset means `folders` when the project has shared bindings or is migrated, otherwise `off`. Preserved across manifest rewrites; refused at workflow creation through the MCP.
- `read`: read-only on the whole Brain, never a write (`BindingPolicy.ReadAll`). The existing audience rule still applies per folder: every output reader must be able to read the folder, and the person's own role still bounds it.
- `off`: every Brain call from the project is refused; an owner's tool pool still exists so a Builder session can turn it on and use it at once.
- New action `set_project_access` on `manage_knowledgebase_access` (mode, expected manifest version, request ID), through the same version check and request journal as a binding change, for the Builder, an owner's MCP connection and the UI route. It cannot be used from a step, a schedule or an unattended run. A project migrated to Brain cannot be set to off or read.
- One test (`TestBrainProjectAccessModes`) pins: off refuses, read reads and cannot write, read cannot reach a folder an output reader cannot read.

**Left:** the Brain tab under Integrations (workflow and Crew), moving the whole "Connected work" tab into Integrations, labels (Off / Read / Read & write; "Legacy knowledge sources"), Code projects (excluded by the backend today), the agent prompt text that explains the modes, and an end-to-end check on RTS (a project on Read reads and cannot write; Off sees nothing; a bound-folder write still works; a scheduled run).

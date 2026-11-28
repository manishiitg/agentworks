# Attached folders and #project references

Project uses `files action=folders/folder_attach/folder_detach` for attached folders (on a Mac, Integrations → Folders on this Mac) and `workspace action=references_list` to discover accessible projects and workflows. Discover available targets first, preserve aliases and use the exact stored ID when removing.

Durable project linking is retired: to read another project's files or message/call its agents from this project, tag it with `#<project>` in your message for that turn only (never secret values or private `builder/` chat history). To copy a workflow or dashboard from another project, see "Copying between projects" in the project references procedure. Call another product through functions/ask rather than editing its internals.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

For tagged project files and progressive inspection, Read [the detailed procedure](references/project-references.md).

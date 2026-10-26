## Share an active project file or folder

For the project's live Dashboard, call `dashboard` with `action=link` and no document_path and
use its returned `url`. Do not pass `db/reports/index.html` to `files action=link`:
that would open a restricted generic HTML preview instead of the full Dashboard
runtime, and the file-link tool rejects that Dashboard entry path. The dedicated
Dashboard URL renders the Dashboard full-page and uses the normal AgentWorks SSO;
do not add a separate publish password or login to this internal URL. Publicly
hosted static Dashboards remain a separate publish flow with their own visibility
controls.



Call `files action=link` with the path relative to the active project. The
server verifies that the target exists, rejects private or escaping paths,
detects file versus folder, and returns the correct authenticated
`preview_url`. Never construct `/file` or `/folder` URLs manually.

Always inspect `shareable`, `scope`, and `warning` in either link tool's result.
If `shareable` is false, tell the user it is only a same-machine preview and
include the warning; do not call it a shareable link. A URL based on `localhost`,
`127.0.0.1`, or `::1` cannot be opened by another user or device. The deployment
must have a reachable `PUBLIC_URL` before the link can be shared.

Crew dashboard links follow current Crew access. Links contain
no credentials and grant no access. Each viewer's live permissions govern
script-backed data; do not promise that sharing a URL grants data-source access.

Resolve the authorized root before reading:

- For an AgentWorks workflow or another Crew reference, use the exact path
  supplied in attached context. `workspace action=references_list` returns separate
  `workflows` and `crews` lists; every entry includes its project `name` and
  display `identity` (`name` and `icon`), and each Crew also its `owner`.
  Discovery does not itself grant file access. Use the exact authorized
  returned path; never guess a project path or scan unattached projects.
- For a host folder, call `files action=folders` and use its
  `$WORK_FOLDER_<ALIAS>` variable. Do not inspect the parent directory.
- Workflow references and host folders are read-only unless a host-folder
  grant explicitly says `read_write`. Never edit a referenced workflow.
  Execution is allowed only through the trigger procedures below.
- Crew references follow the effective runtime grants and the restrictions
  above. Never infer write access from a Crew's name or discovery result.

A `#` workflow selection applies only to that message. For durable access to a
workflow or another Crew, use
`workspace action=references_list`, disambiguate by its exact returned path, and call
`workspace action=reference_attach` only when the user asks to keep it attached. Use
the exact saved path for `workspace action=reference_detach`. Durable references live
in the project's `workflow.json` and are re-authorized on every turn.

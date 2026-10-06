# PLAT-557: Brain restricted folders (private folders inside shared ones)

**State:** open, not started (owner: add to the list, do not implement yet). P3.

**Why:** Brain access is per folder and only adds downwards: a role on a folder applies to everything under it, and a subfolder cannot take access away. So "a user can read `x/y` but not `x/y/z`" is impossible today; the only workaround is restructuring (move `z` out from under `y`, or put `y`'s shareable content in its own subfolder). A company Brain will want team folders with confidential subfolders.

**Proposal:** a per-folder "restricted" switch: the folder does not inherit grants from above; only people granted on it (or below it) and administrators can see it. Folders above show it only to people who can see inside it (no titles, counts or breadcrumbs otherwise). Folder Owners or admins set it, through the same confirmed access-change path as grants.

**Must hold:** listings, search, the per-caller map, per-folder index notes, migration grants, project bindings and the output-audience rule all respect it; moving a folder into or out of a restricted folder changes who can see it and needs the same confirmation as a grant. Security-sensitive: needs focused tests on the access calculation and a live check.

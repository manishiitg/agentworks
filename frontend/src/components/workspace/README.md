# App layout ownership

`AuthWrapper` admits the signed-in account and selected server, then mounts one
`AppShell` outside `ProductRouteBoundary`. Account/server changes reset the shell;
changing a product, page, project or chat does not.

- `AppSidebar` owns the left work list, rail, expansion and width.
- Home only navigates to `/home`; a separate sidebar button controls the menu.
- `WorkspaceAccountMenu` and `AppQuickSwitcher` are app-level controls.
- The shell’s `data-app-main` region contains the active page’s header and content.
- `WorkspaceTopToolbar` / `WorkspacePageHeader` supply the existing header slots.
- `ProductTopBar` supplies product controls to those slots, never another sidebar.
- `WorkspaceSetupMenu` registers the current page’s help/shortcut callbacks with
  the persistent account menu.

Internal navigation uses `appNavigation` and the existing product navigation
helpers. Plain app-link clicks change history and pass through the normal route
and account checks. Modified clicks still open ordinary links. Login, consent,
shared assets and standalone report views keep their own authenticated entry
surfaces. No app page may use `location.assign` to switch a product.

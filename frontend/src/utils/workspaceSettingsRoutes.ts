export const WORKSPACE_SETTINGS_PATHS = {
  providers: '/settings/ai-accounts',
  mcp: '/settings/ai-tools',
  users: '/settings/people',
} as const
export type WorkspaceSettingsPage = keyof typeof WORKSPACE_SETTINGS_PATHS

export function workspaceSettingsPage(path = window.location.pathname): WorkspaceSettingsPage | undefined {
  return (Object.keys(WORKSPACE_SETTINGS_PATHS) as WorkspaceSettingsPage[]).find(page => WORKSPACE_SETTINGS_PATHS[page] === path)
}

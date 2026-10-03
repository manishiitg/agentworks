import type { WorkIntegrationTab } from './WorkIntegrationsPanel'
import type { WorkIdentityTab } from './WorkIdentityPanel'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

// The server gates Crew views by legacy feature panel id
// (agent_go/pkg/agentprofiles/features.go). The consolidated Setup views keep
// honoring those ids: Identity is always reachable (General is the project's
// own name, icon, and deletion), Integrations needs any of mcp/skills/bots,
// and inner tabs follow their own legacy panel id (bots owns Slack, WhatsApp,
// and Gmail, matching the shared connector feature).
export function isWorkWorkspaceViewEnabled(view: WorkWorkspaceView, enabledPanels?: Set<string>): boolean {
  if (!enabledPanels) return true
  if (view === 'identity' || view === 'plan' || view === 'suggestions') return true
  if (view === 'mcp') return enabledPanels.has('mcp') || enabledPanels.has('skills') || enabledPanels.has('bots') || enabledPanels.has('secrets')
  return enabledPanels.has(view)
}

export function isWorkIdentityTabEnabled(tab: WorkIdentityTab, enabledPanels?: Set<string>): boolean {
  if (!enabledPanels) return true
  if (tab === 'general') return true
  return enabledPanels.has(tab)
}

export function isWorkIntegrationTabEnabled(tab: WorkIntegrationTab, enabledPanels?: Set<string>): boolean {
  if (!enabledPanels) return true
  if (tab === 'secrets') return enabledPanels.has('secrets')
  if (tab === 'apps') return enabledPanels.has('mcp')
  if (tab === 'skills') return enabledPanels.has('skills')
  if (tab === 'cli') return true
  return enabledPanels.has('bots')
}

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { Globe } from 'lucide-react'
import api from '../../services/api'
import { YourComputerPanel } from './YourComputerPanel'
import { openCrewAccess } from './MacSharedAgents'
import { crewProjectMenuItems } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

vi.mock('../../services/api', () => ({ default: { get: vi.fn() }, getAuthToken: () => '', getDesktopApiBaseUrl: () => '', getLocalApiBaseUrl: () => '' }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: {} }) } }))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: vi.fn() }))
vi.mock('../../utils/activateTab', () => ({ activateTab: vi.fn() }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => false, useConnectedWorkStore: (select: (state: unknown) => unknown) => select({ connections: [] }) }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('./codeLocalFiles', () => ({ useLocalFileDevices: () => ({ devices: [] }) }))
vi.mock('./CodeFilesPanel', () => ({ CodeLocalFilesSettings: () => null }))
vi.mock('../../components/providers/ProvidersOverview', () => ({ ConnectServerForm: () => null }))
vi.mock('./TeamTab', () => ({ AskButton: () => null, teamCard: '' }))
vi.mock('./ShareCrew', () => ({ ShareCrew: () => null }))
vi.mock('./MacAgentBridge', () => ({ MacAgentBridge: () => null }))
vi.mock('./OnlineAgentsCard', () => ({ OnlineAgentsCard: () => <p>Online agents content</p> }))
vi.mock('./teamApi', () => ({ useTeamTab: () => ({}) }))
vi.mock('./CrewDashboardsTab', () => ({ CrewDashboardsTab: ({ projectId }: { projectId: string }) => <p>Artifacts for {projectId}</p> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Owner screenshot regression: Artifacts is a sibling destination, not an
// Online tab. Old target events and saved tab choices must still reach it.
it('opens old Artifacts links as a standalone page and leaves only Online tabs', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: {
    access_here: 'server', access_tabs: [
      { id: 'mac', label: 'Mac online', group: 'online' },
      { id: 'online-agents', label: 'Online agents', group: 'online' },
      { id: 'dashboards', label: 'Artifacts', group: 'online' },
    ],
  } } as never)
  localStorage.setItem('agentworks:crew-access-tab:artifacts-test', 'dashboards')
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<YourComputerPanel projectId="artifacts-test" />))
    expect(host.querySelector('[aria-label="Artifacts page"]')?.textContent).toContain('Artifacts for artifacts-test')
    expect(host.querySelector('[role="tablist"]')).toBeNull()
    await act(async () => openCrewAccess('artifacts-test', '', 'mac'))
    expect([...host.querySelectorAll('[role="tab"]')].map(tab => tab.textContent)).toEqual(['Mac online', 'Online agents'])
    await act(async () => openCrewAccess('artifacts-test', '', 'dashboards'))
    expect(host.querySelector('[aria-label="Artifacts page"]')).not.toBeNull()
    await act(async () => openCrewAccess('artifacts-test', '', 'artifacts'))
    expect(host.querySelector('[aria-label="Artifacts page"]')).not.toBeNull()
    const panels: ProductPanel<WorkWorkspaceView>[] = [{ id: 'mcp', label: 'Connections', icon: Globe }]
    const items = crewProjectMenuItems({ panels })
    expect(items.filter(item => item.group === 'Share').map(item => item.id)).toEqual(['mcp:team', 'mcp:online', 'mcp:artifacts'])
    expect(items.find(item => item.id === 'mcp:slack-bot')?.group).toBe('Integrations')
  } finally {
    await act(async () => root.unmount())
    localStorage.removeItem('agentworks:crew-access-tab:artifacts-test')
  }
})

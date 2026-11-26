// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'
import { Files, Globe, Settings } from 'lucide-react'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const f = vi.hoisted(() => ({
  user: { id: 'owner', allowed_products: ['work'] as string[] } as { id: string; allowed_products?: string[] } | null,
  server: 'local',
  setTarget: vi.fn(),
  openAccess: vi.fn(),
  openExternal: vi.fn(),
  selectReport: vi.fn(),
}))

vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: { getState: () => ({ user: f.user }) },
}))
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),
  useWorkspaceConnectionStore: Object.assign(
    (select: (state: unknown) => unknown) => select({ activeWorkspaceId: f.server }),
    { getState: () => ({ activeWorkspaceId: f.server }) },
  ),
}))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({
  setWorkspaceViewTarget: f.setTarget,
}))
vi.mock('./MacSharedAgents', () => ({
  openCrewAccess: f.openAccess,
}))
vi.mock('../../utils/productWorkspaceNavigation', () => ({
  openProductWorkspace: f.openExternal,
}))
vi.mock('../../components/workflow/reportDocuments', () => ({
  selectReportDocument: f.selectReport,
}))

import { findAdmittedCrewMenuItem, isSameRightPanelScope, navigateCrewMenuItem } from './crewMenuNavigation'

const panels: ProductPanel<WorkWorkspaceView>[] = [
  { id: 'files', label: 'Files', icon: Files },
  { id: 'mcp', label: 'Connections', icon: Globe },
  { id: 'identity', label: 'Settings', icon: Settings },
]

beforeEach(() => {
  f.user = { id: 'owner', allowed_products: ['work'] }
  f.server = 'local'
  vi.clearAllMocks()
})

it('rejects menu lookup when account or server scope changed', () => {
  expect(isSameRightPanelScope('owner', 'local')).toBe(true)
  expect(isSameRightPanelScope('other', 'local')).toBe(false)
  expect(isSameRightPanelScope('owner', 'remote')).toBe(false)
  expect(findAdmittedCrewMenuItem('files', { panels, userId: 'other', serverId: 'local' })).toBeNull()
  expect(findAdmittedCrewMenuItem('files', { panels, userId: 'owner', serverId: 'remote' })).toBeNull()
  expect(findAdmittedCrewMenuItem('files', { panels, userId: 'owner', serverId: 'local' })?.view).toBe('files')
})

it('navigates files, targeted views, and direct team access consistently', () => {
  const onOpen = vi.fn()
  const filesItem = findAdmittedCrewMenuItem('files', { panels, userId: 'owner', serverId: 'local' })!
  expect(navigateCrewMenuItem(filesItem, { projectId: 'crew-1', workspacePath: 'Crew/demo', onOpen })).toBe(true)
  expect(f.setTarget).toHaveBeenLastCalledWith('files', 'browse')
  expect(onOpen).toHaveBeenLastCalledWith('files')

  const teamItem = findAdmittedCrewMenuItem('mcp:team', { panels, userId: 'owner', serverId: 'local' })!
  expect(navigateCrewMenuItem(teamItem, { projectId: 'crew-1', onOpen, directTeamAccess: true })).toBe(true)
  expect(f.openAccess).toHaveBeenCalledWith('crew-1', '', 'team')
  expect(onOpen).toHaveBeenCalledTimes(1)

  const settingsItem = findAdmittedCrewMenuItem('identity:general', { panels, userId: 'owner', serverId: 'local' })!
  expect(navigateCrewMenuItem(settingsItem, { projectId: 'crew-1', onOpen })).toBe(true)
  expect(f.setTarget).toHaveBeenLastCalledWith('identity', 'general')
  expect(onOpen).toHaveBeenLastCalledWith('identity')
})

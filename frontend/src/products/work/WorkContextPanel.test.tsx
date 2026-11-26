// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { Files, Globe } from 'lucide-react'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const f = vi.hoisted(() => ({
  user: { id: 'owner', allowed_products: ['work'] as string[] },
  server: 'local',
  opened: vi.fn(),
  target: vi.fn(),
  changesKeys: [] as string[],
  team: { view: undefined } as any,
}))

vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: Object.assign(
    (select: (state: unknown) => unknown) => select({ user: f.user }),
    { getState: () => ({ user: f.user }) },
  ),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),
  useWorkspaceConnectionStore: Object.assign(
    (select: (state: unknown) => unknown) => select({ activeWorkspaceId: f.server }),
    { getState: () => ({ activeWorkspaceId: f.server }) },
  ),
}))
vi.mock('../../stores/useChatStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useChatStore')>(),
  useChatStore: Object.assign(
    (select: (state: unknown) => unknown) => select({ addToast: vi.fn() }),
    { getState: () => ({ addToast: vi.fn() }) },
  ),
}))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: f.target }))
vi.mock('./useCrewPanel', () => ({
  useCrewPanel: () => ({
    data: {
      sections: [
        { id: 'connections', rows: [] },
        { id: 'changes', title: 'Git', detail: '1 repository', rows: [{ id: 'r1', label: 'repo' }] },
      ],
      details: {},
    },
    refresh: () => {},
  }),
}))
vi.mock('./teamApi', () => ({ useTeamTab: () => f.team }))
vi.mock('./useTeamSetupsList', () => ({ useTeamSetupsList: () => ({ data: undefined }) }))
vi.mock('./ActivitySection', () => ({ CrewActivitySection: () => null }))
vi.mock('./CrewTeamPanel', () => ({ CrewTeamPanel: () => null }))
vi.mock('../../components/workspace/ChatContextSection', () => ({ ChatContextSection: () => null }))
vi.mock('../../components/workspace/ChatChangesRow', () => ({
  ChatChangesRow: ({ storageKey }: { storageKey: string }) => {
    f.changesKeys.push(storageKey)
    return <div data-testid="changes-row">{storageKey}</div>
  },
}))

import { WorkContextPanel } from './WorkContextPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)

const panels: ProductPanel<WorkWorkspaceView>[] = [
  { id: 'files', label: 'Files', icon: Files },
  { id: 'mcp', label: 'Connections', icon: Globe },
]

afterEach(async () => {
  await act(async () => root.render(null))
  f.user = { id: 'owner', allowed_products: ['work'] }
  f.server = 'local'
  f.changesKeys = []
  f.team = { view: undefined }
  vi.clearAllMocks()
})

it('scopes ChatChangesRow storageKey by server, user, and workspace path', async () => {
  await act(async () => root.render(
    <WorkContextPanel workspacePath="Crew/demo" projectId="demo" reviewSessionId="s1" panels={panels} onOpen={f.opened} />,
  ))
  expect(f.changesKeys.at(-1)).toBe(JSON.stringify(['agentworks.changes-panel', 'local', 'owner', 'Crew/demo']))

  f.server = 'remote-1'
  f.user = { id: 'peer', allowed_products: ['work'] }
  await act(async () => root.render(
    <WorkContextPanel workspacePath="Crew/demo" projectId="demo" reviewSessionId="s1" panels={panels} onOpen={f.opened} />,
  ))
  expect(f.changesKeys.at(-1)).toBe(JSON.stringify(['agentworks.changes-panel', 'remote-1', 'peer', 'Crew/demo']))
})

it('switches cleanly between main menu, Full menu submenu, and back', async () => {
  await act(async () => root.render(
    <WorkContextPanel workspacePath="Crew/demo" projectId="demo" panels={panels} onOpen={f.opened} />,
  ))
  const fullBtn = [...host.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Full menu')!
  expect(fullBtn).toBeTruthy()
  await act(async () => fullBtn.click())
  expect(host.textContent).toContain('Files & data')
  const filesBtn = [...host.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Files')!
  await act(async () => filesBtn.click())
  expect(f.target).toHaveBeenCalledWith('files', 'browse')
  expect(f.opened).toHaveBeenCalledWith('files')
})

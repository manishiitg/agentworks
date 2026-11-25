// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'

const { loadProjectVault, revealGlobalSecret } = vi.hoisted(() => ({
  loadProjectVault: vi.fn(),
  revealGlobalSecret: vi.fn(),
}))

vi.mock('../../api/projectVault', () => ({
  loadProjectVault,
  selectProjectVaultItem: vi.fn(),
  setUseAllVaultApps: vi.fn(),
}))
vi.mock('../../api/secrets', () => ({
  secretsApi: { revealGlobalSecret },
}))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: Object.assign(
    (selector: (s: { user?: { id: string } }) => unknown) => selector({ user: { id: 'u1' } }),
    { getState: () => ({ user: { id: 'u1' } }) },
  ),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>()
  return {
    ...actual,
    useWorkspaceConnectionStore: Object.assign(
      (selector: (s: { activeWorkspaceId: string }) => unknown) => selector({ activeWorkspaceId: 'local' }),
      { getState: () => ({ activeWorkspaceId: 'local' }) },
    ),
  }
})
vi.mock('../../stores/useChatStore', () => ({
  useChatStore: (selector: (s: { chatTabs: Record<string, never> }) => unknown) => selector({ chatTabs: {} }),
}))
vi.mock('../../hooks/useLiveRefetch', () => ({
  useLiveRefetch: () => {},
}))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({
  useCanWriteWorkflow: () => true,
}))
vi.mock('./useVaultSetupDraft', () => ({
  useVaultSetupDraft: () => ({ open: vi.fn(), error: '' }),
}))
vi.mock('../../products/work/TeamWay', () => ({
  AddToTeamControl: () => null,
  useAddToTeam: () => null,
}))
vi.mock('./GitHubVault', () => ({
  GitHubVaultCard: () => <div data-testid="github-vault-card">GitHub</div>,
}))
vi.mock('../secrets/SecretSelectionSection', () => ({
  SecretSelectionSection: ({ inventory }: { inventory?: React.ReactNode }) => <div>{inventory}</div>,
}))

import { ProjectIntegrationPanel } from './ProjectIntegrationPanel'

beforeEach(() => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  loadProjectVault.mockReset()
  revealGlobalSecret.mockReset()
})

it('does not render GitHub inside Secrets and allows viewing/hiding own secrets while keeping non-revealable ones hidden', async () => {
  loadProjectVault.mockResolvedValue({
    vaults: [{
      id: 'yours',
      name: 'Yours',
      sharing: 'Only you',
      project: false,
      items: [
        { id: 'VLT_v1__API_KEY', name: 'API_KEY', kind: 'shared_secret', source: 'you', status: 'stored', selected: true, can_select: true, can_reveal: true },
        { id: 'VLT_v2__LENT_KEY', name: 'LENT_KEY', kind: 'shared_secret', source: "the project's owner", status: 'stored', selected: true, can_select: true, can_reveal: false },
      ],
    }],
    notices: [],
    selected_servers: [],
    selected_global_secret_names: ['VLT_v1__API_KEY'],
    use_all_vault_apps: false,
  })
  revealGlobalSecret.mockResolvedValue({ value: 'dummy-revealed-secret' })

  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(<ProjectIntegrationPanel workspacePath="Crew/demo" placeNoun="Project" section="secrets" canEdit github onAsk={async () => {}} />)
  })

  expect(host.querySelector('[data-testid="github-vault-card"]')).toBeNull()
  expect(host.textContent).toContain('API_KEY')
  expect(host.textContent).toContain('LENT_KEY')
  expect(host.textContent).not.toContain('dummy-revealed-secret')
  expect(host.querySelector('[aria-label="View LENT_KEY"]')).toBeNull()

  const viewBtn = host.querySelector<HTMLButtonElement>('[aria-label="View API_KEY"]')!
  await act(async () => { viewBtn.click() })
  expect(revealGlobalSecret).toHaveBeenCalledWith('VLT_v1__API_KEY')
  expect(host.textContent).toContain('dummy-revealed-secret')

  const hideBtn = host.querySelector<HTMLButtonElement>('[aria-label="Hide API_KEY"]')!
  act(() => { hideBtn.click() })
  expect(host.textContent).not.toContain('dummy-revealed-secret')
  act(() => root.unmount())
})

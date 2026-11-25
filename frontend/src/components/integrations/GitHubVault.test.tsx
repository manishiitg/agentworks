// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'

const { apiGet, apiPost, apiDelete, localMode } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiDelete: vi.fn(),
  localMode: { value: true },
}))

vi.mock('../../services/api', () => ({
  default: { get: apiGet, post: apiPost, delete: apiDelete },
}))
vi.mock('../../products/productSurfaceConfig', () => ({
  isLocalDeployment: () => localMode.value,
}))
vi.mock('./CompanySignInSetup', () => ({
  CompanySignInSetup: ({ provider }: { provider: string }) => <div data-testid="company-setup">{provider} sign-in setup</div>,
}))

import { GitHubVaultCard, buildGitHubAccountChoices } from './GitHubVault'

beforeEach(() => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  apiGet.mockReset()
  apiPost.mockReset()
  apiDelete.mockReset()
  localMode.value = true
})

it('deduplicates saved GitHub CLI connections against local gh_accounts', () => {
  const choices = buildGitHubAccountChoices({
    app_ready: false,
    connected: true,
    connection_id: 'c1',
    login: 'alex-dev',
    gh_available: true,
    connections: [
      { id: 'c1', login: 'alex-dev', source: 'gh', orgs: ['acme-core'] },
      { id: 'c2', login: 'alex-work', source: 'pat', orgs: ['partner-org'] },
    ],
    gh_accounts: ['alex-dev', 'alex-extra'],
  })
  expect(choices.map(c => `${c.key}:${c.login}:${c.active}`)).toEqual([
    'saved:c1:alex-dev:true',
    'saved:c2:alex-work:false',
    'gh:alex-extra:alex-extra:false',
  ])
})

it('shows calm connected state, repo reachability with 1-click switch, folded token form, and hides OAuth setup on local Mac', async () => {
  apiGet.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/vault/github')) {
      return {
        data: {
          app_ready: false,
          connected: true,
          connection_id: 'c1',
          login: 'alex-dev',
          source: 'gh',
          orgs: ['acme-core'],
          gh_available: true,
          gh_accounts: ['alex-dev'],
          connections: [
            { id: 'c1', login: 'alex-dev', source: 'gh', orgs: ['acme-core'] },
            { id: 'c2', login: 'alex-work', source: 'gh', orgs: ['partner-org'] },
          ],
        },
      }
    }
    return {
      data: {
        repos: [
          { github_repo: 'acme-core/platform' },
          { github_repo: 'partner-org/portal-web' },
        ],
      },
    }
  })
  apiPost.mockResolvedValue({ data: { login: 'alex-work' } })

  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<GitHubVaultCard workspacePath="Crew/demo" />) })

  expect(host.textContent).toContain('@alex-dev')
  expect(host.textContent).toContain('acme-core/platform')
  expect(host.textContent).toContain('Reachable')
  expect(host.textContent).toContain('partner-org/portal-web')
  expect(host.textContent).toContain('Reaches 1 organization')
  expect(host.querySelector('[data-testid="company-setup"]')).toBeNull()

  const tokenFold = [...host.querySelectorAll('details')].find(d => d.textContent?.includes('Connect with a GitHub token'))!
  expect(tokenFold.open).toBe(false)

  const useBtn = [...host.querySelectorAll('button')].find(b => b.textContent === 'Use @alex-work')!
  await act(async () => { useBtn.click() })
  expect(apiPost).toHaveBeenCalledWith(
    '/api/vault/github/connect?workspace_path=Crew%2Fdemo',
    { method: 'saved', connection_id: 'c2' },
  )
  act(() => root.unmount())
})

it('opens token form by default when not connected and shows OAuth setup on servers', async () => {
  localMode.value = false
  apiGet.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/vault/github')) {
      return { data: { app_ready: false, connected: false, gh_available: false, connections: [] } }
    }
    return { data: { repos: [] } }
  })

  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<GitHubVaultCard workspacePath="Crew/demo" />) })

  expect(host.textContent).toContain('Not connected')
  const tokenFold = [...host.querySelectorAll('details')].find(d => d.textContent?.includes('Connect with a GitHub token'))!
  expect(tokenFold.open).toBe(true)
  expect(host.querySelector('[data-testid="company-setup"]')).not.toBeNull()
  act(() => root.unmount())
})

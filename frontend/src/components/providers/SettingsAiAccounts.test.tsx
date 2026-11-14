// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import SettingsAiAccounts from './SettingsAiAccounts'
const f = vi.hoisted(() => ({ manifest: vi.fn(), accounts: vi.fn(), status: vi.fn() }))
vi.mock('../../services/llm-config-api', async importOriginal => ({ ...await importOriginal<typeof import('../../services/llm-config-api')>(), llmConfigService: { getProviderManifest: f.manifest, getProviderConnections: f.accounts, getProviderAccountStatus: f.status } }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ useConnectedWorkStore: (select: (s: unknown) => unknown) => select({ connections: [] }) }))
vi.mock('./CodingProvidersPanel', () => ({ default: ({ initialProviderId, accountSetup }: { initialProviderId: string; accountSetup?: boolean }) => <div data-provider={initialProviderId} data-setup={accountSetup}>Existing account setup</div> }))
const host = document.createElement('div'); document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); vi.restoreAllMocks(); f.accounts.mockReset(); f.manifest.mockReset(); f.status.mockReset() })

// PLAT-746: do not show untouched provider placeholders as connected accounts,
// or fetch the same complete account inventory once per provider card.
it('shares one inventory, retains pending personal accounts and opens the chosen provider setup', async () => {
  vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function (this: HTMLDialogElement) { this.open = true })
  f.manifest.mockResolvedValue({ provider_order: ['claude-code', 'codex-cli', 'cursor-cli'], providers: ['claude-code', 'codex-cli', 'cursor-cli'].map(id => ({ id, display_name: id, integration_kind: 'coding_agent', runtime_available: true })) })
  f.accounts.mockResolvedValue([
    { id: 'global:claude-code', provider: 'claude-code', scope: 'global', relation: 'server', configured: true, can_manage: false, identity: 'example@example.com' },
    { id: 'global:codex-cli', provider: 'codex-cli', scope: 'global', relation: 'server', configured: false, can_manage: false },
    { id: 'pending-codex', provider: 'codex-cli', display_name: 'Pending personal login', relation: 'own', configured: false, auth_method: 'cli_login', can_manage: true },
    { id: 'global:cursor-cli', provider: 'cursor-cli', scope: 'global', relation: 'server', configured: false, can_manage: false },
  ])
  f.status.mockResolvedValue({ state: 'signed_out' })
  await act(async () => root.render(<SettingsAiAccounts />))
  expect(f.accounts).toHaveBeenCalledTimes(1)
  expect(host.querySelectorAll('section[aria-label$=" accounts"]')).toHaveLength(2)
  expect(host.textContent).toContain('Pending personal login')
  expect(host.querySelector('[aria-label="Available providers"]')).toBeNull()
  const add = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add account')!
  await act(async () => add.click())
  const picker = document.querySelector('dialog[aria-label="Add an AI account"]')!
  const cursor = [...picker.querySelectorAll('button')].find(button => button.textContent?.includes('Cursor'))!
  expect(cursor).toBeTruthy()
  await act(async () => cursor.click())
  expect(host.querySelector('[data-provider="cursor-cli"][data-setup="true"]')).toBeTruthy()
  expect(f.accounts).toHaveBeenCalledTimes(1)
})

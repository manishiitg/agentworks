// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SecretSelectionSection } from './SecretSelectionSection'
const api = vi.hoisted(() => ({
  getGlobalSecrets: vi.fn(),
  decrypt: vi.fn(),
  revealGlobalSecret: vi.fn(),
  saveGlobalSecret: vi.fn(),
  deleteGlobalSecret: vi.fn(),
  storeWorkflowSecret: vi.fn(),
  deleteWorkflowSecret: vi.fn(),
  encrypt: vi.fn(),
  getSecretSources: vi.fn(),
}))
vi.mock('../../api/secrets', () => ({ secretsApi: api }))
vi.mock('../integrations/OpenVaultButton', () => ({
  OpenVaultButton: () => null,
}))
const access = vi.hoisted(() => ({ canWrite: true }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({
  useCanWriteWorkflow: () => access.canWrite,
}))
const clipboardWrite = vi.fn()
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
beforeEach(() => {
  vi.resetAllMocks()
  access.canWrite = true
  api.getSecretSources.mockResolvedValue({ status: 'available', note: '', warnings: [], secrets: [] })
  clipboardWrite.mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText: clipboardWrite } })
  api.getGlobalSecrets.mockResolvedValue([{ name: 'TEAM_KEY', managed: true }])
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})
async function click(label: string) {
  const element =
    host.querySelector(`[aria-label="${label}"]`) ??
    [...host.querySelectorAll('button')].find((b) => b.textContent === label)
  expect(element).toBeTruthy()
  await act(async () => {
    ;(element as HTMLElement).click()
  })
}
describe('shared secrets permissions UI', () => {
  const renderVault = () =>
    root.render(<SecretSelectionSection mode="vault" />)
  const checked = (label: string) => host.querySelector(`[aria-label="${label}"]`)?.getAttribute('aria-checked')

  it('project Vault choices never expose central value or management actions', async () => {
    const select = vi.fn().mockResolvedValue(undefined)
    await act(async () => root.render(<SecretSelectionSection workflowPath="Workflow/test" projectSource="vault" selectedGlobalSecrets={null} onGlobalSecretChange={select} />))
    expect(checked('Use TEAM_KEY')).toBe('false')
    expect(host.querySelector('[aria-label="Reveal TEAM_KEY"]')).toBeNull()
    expect(host.querySelector('[aria-label="Copy TEAM_KEY"]')).toBeNull()
    await click('Use TEAM_KEY')
    expect(select).toHaveBeenCalledWith(['TEAM_KEY'])
    expect(api.revealGlobalSecret).not.toHaveBeenCalled()
    expect(api.getGlobalSecrets).toHaveBeenCalledWith(false)
  })
  it('retains selection and shows save failures', async () => {
    const select = vi.fn().mockRejectedValue(new Error('Permission revoked'))
    await act(async () => root.render(<SecretSelectionSection workflowPath="Workflow/test" projectSource="vault" selectedGlobalSecrets={[]} onGlobalSecretChange={select} />))
    await click('Use TEAM_KEY')
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Permission revoked')
    expect(checked('Use TEAM_KEY')).toBe('false')
  })
  it('offers metadata-only group grants without any reveal control', async () => {
    const grant = vi.fn().mockResolvedValue(undefined)
    api.getGlobalSecrets.mockResolvedValue([{ name: 'ASSIGNED_KEY', managed: true }, { name: 'TEAM_KEY', managed: true }])
    await act(async () => root.render(<SecretSelectionSection mode="group" groupSelectedNames={['ASSIGNED_KEY']} onGroupAccessChange={grant} />))
    expect(checked('Use ASSIGNED_KEY')).toBe('true')
    expect(checked('Use TEAM_KEY')).toBe('false')
    await click('Use TEAM_KEY')
    expect(grant).toHaveBeenCalledWith('TEAM_KEY', true)
    expect(host.querySelector('[aria-label="Reveal TEAM_KEY"]')).toBeNull()
    expect(host.querySelector('[aria-label="Copy TEAM_KEY"]')).toBeNull()
    expect(host.querySelector('input[type="password"]')).toBeNull()
    expect(api.revealGlobalSecret).not.toHaveBeenCalled()
  })
  it('Vault management can reveal and hide explicitly without rendering values initially', async () => {
    api.revealGlobalSecret.mockResolvedValue({ value: 'dummy-managed-value' })
    await act(async () => renderVault())
    expect(host.textContent).not.toContain('dummy-managed-value')
    await click('Reveal TEAM_KEY')
    expect(host.textContent).toContain('dummy-managed-value')
    await click('Hide')
    expect(host.textContent).not.toContain('dummy-managed-value')
  })
  it('copies a Vault value without rendering it', async () => {
    api.revealGlobalSecret.mockResolvedValue({ value: 'dummy-copy-value' })
    await act(async () => renderVault())
    await click('Copy TEAM_KEY')
    expect(api.revealGlobalSecret).toHaveBeenCalledWith('TEAM_KEY')
    expect(clipboardWrite).toHaveBeenCalledWith('dummy-copy-value')
    expect(host.textContent).not.toContain('dummy-copy-value')
    expect(host.querySelector('[aria-label="Copied TEAM_KEY"]')).toBeTruthy()
  })
  it('does not copy a cached revealed value when access has been revoked', async () => {
    api.revealGlobalSecret.mockResolvedValueOnce({ value: 'dummy-revealed' })
    await act(async () => renderVault())
    await click('Reveal TEAM_KEY')
    api.revealGlobalSecret.mockRejectedValueOnce(new Error('Access denied'))
    await click('Copy TEAM_KEY')
    expect(clipboardWrite).not.toHaveBeenCalled()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not copy')
    expect(host.textContent).not.toContain('dummy-revealed')
  })
})

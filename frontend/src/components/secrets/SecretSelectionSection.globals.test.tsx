// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SecretSelectionSection } from './SecretSelectionSection'
const api = vi.hoisted(() => ({
  getGlobalSecrets: vi.fn(),
  getVaultShareGroups: vi.fn(),
  shareWorkflowSecret: vi.fn(),
  listWorkflowSecrets: vi.fn(),
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
vi.mock('../../hooks/useCanWriteWorkflow', () => ({
  useCanWriteWorkflow: () => true,
}))
const clipboardWrite = vi.fn()
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
beforeEach(() => {
  vi.resetAllMocks()
  api.getVaultShareGroups.mockResolvedValue({ groups: [{ ID: 'platform', Name: 'Platform' }, { ID: 'finance', Name: 'Finance' }] })
  api.shareWorkflowSecret.mockResolvedValue(undefined)
  api.getSecretSources.mockResolvedValue({ status: 'available', note: '', warnings: [], secrets: [] })
  clipboardWrite.mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText: clipboardWrite } })
  api.getGlobalSecrets.mockResolvedValue([{ name: 'TEAM_KEY', managed: true }])
  api.listWorkflowSecrets.mockResolvedValue([
    { name: 'PROJECT_KEY', encrypted_value: 'encrypted' },
  ])
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
  it('project Vault choices never expose central value or management actions', async () => {
    const select = vi.fn().mockResolvedValue(undefined)
    await act(async () =>
      root.render(
        <SecretSelectionSection
          workflowPath="Workflow/test"
          selectedSecrets={[]}
          onSecretChange={() => {}}
          selectedGlobalSecrets={null}
          onGlobalSecretChange={select}
        />,
      ),
    )
    await click('Vault')
    expect(
      host
        .querySelector('[aria-label="Use TEAM_KEY"]')
        ?.getAttribute('aria-checked'),
    ).toBe('false')
    expect(host.textContent).not.toContain('Rotate')
    expect(host.querySelector('[aria-label="Reveal TEAM_KEY"]')).toBeNull()
    expect(host.querySelector('[aria-label="Copy TEAM_KEY"]')).toBeNull()
    await click('Use TEAM_KEY')
    expect(select).toHaveBeenCalledWith(['TEAM_KEY'])
    expect(api.revealGlobalSecret).not.toHaveBeenCalled()
    expect(api.getGlobalSecrets).toHaveBeenCalledWith(false)
  })
  it('retains selection and shows save failures', async () => {
    const select = vi.fn().mockRejectedValue(new Error('Permission revoked'))
    await act(async () =>
      root.render(
        <SecretSelectionSection
          workflowPath="Workflow/test"
          selectedSecrets={[]}
          onSecretChange={() => {}}
          selectedGlobalSecrets={[]}
          onGlobalSecretChange={select}
        />,
      ),
    )
    await click('Vault')
    await click('Use TEAM_KEY')
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      'Permission revoked',
    )
    expect(
      host
        .querySelector('[aria-label="Use TEAM_KEY"]')
        ?.getAttribute('aria-checked'),
    ).toBe('false')
  })
  it('Vault management can reveal and hide explicitly without rendering values initially', async () => {
    api.revealGlobalSecret.mockResolvedValue({ value: 'dummy-managed-value' })
    await act(async () =>
      root.render(
        <SecretSelectionSection
          mode="vault"
          selectedSecrets={[]}
          onSecretChange={() => {}}
        />,
      ),
    )
    expect(host.textContent).not.toContain('dummy-managed-value')
    await click('Reveal TEAM_KEY')
    expect(host.textContent).toContain('dummy-managed-value')
    await click('Hide')
    expect(host.textContent).not.toContain('dummy-managed-value')
  })
  it('drops a late reveal after changing projects', async () => {
    let resolveReveal!: (result: { value: string }) => void
    api.decrypt.mockReturnValue(
      new Promise((resolve) => {
        resolveReveal = resolve
      }),
    )
    const renderProject = (workflowPath: string) =>
      root.render(
        <SecretSelectionSection
          workflowPath={workflowPath}
          selectedSecrets={[]}
          onSecretChange={() => {}}
        />,
      )
    await act(async () => renderProject('Workflow/first'))
    await click('Reveal PROJECT_KEY')
    await act(async () => renderProject('Workflow/second'))
    await act(async () => resolveReveal({ value: 'dummy-first-project-value' }))
    expect(host.textContent).not.toContain('dummy-first-project-value')
  })

})

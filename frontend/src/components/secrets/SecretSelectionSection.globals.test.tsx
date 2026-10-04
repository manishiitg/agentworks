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
  it('shares by reference with explicit groups and retains project selections', async () => {
    const selected = vi.fn()
    await act(async () => root.render(<SecretSelectionSection workflowPath="Workflow/test" selectedSecrets={['PROJECT_KEY']} onSecretChange={selected} />))
    await click('Share PROJECT_KEY to Vault')
    const form = host.querySelector('form[aria-label="Share PROJECT_KEY to Vault"]')!
    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    await click('Share with Platform')
    await act(async () => { submit.click() })
    expect(api.shareWorkflowSecret).toHaveBeenCalledWith('Workflow/test', 'PROJECT_KEY', 'PROJECT_KEY', ['platform'])
    expect(selected).not.toHaveBeenCalled()
    expect(api.decrypt).not.toHaveBeenCalled()
    expect(api.encrypt).not.toHaveBeenCalled()
    expect(host.querySelector('[role="status"]')?.textContent).toContain('project copy is unchanged')
    expect(host.querySelector('[aria-label="Use PROJECT_KEY"]')?.getAttribute('aria-checked')).toBe('true')
  })
  it('keeps the form and displays name conflicts without changing selections', async () => {
    api.shareWorkflowSecret.mockRejectedValue(new Error('A global secret with this name already exists'))
    await act(async () => root.render(<SecretSelectionSection workflowPath="Workflow/test" selectedSecrets={[]} onSecretChange={() => {}} />))
    await click('Share PROJECT_KEY to Vault')
    await click('Share with Finance')
    await act(async () => { (host.querySelector('form button[type="submit"]') as HTMLElement).click() })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('already exists')
    expect(host.querySelector('form')).toBeTruthy()
    expect(api.deleteWorkflowSecret).not.toHaveBeenCalled()
  })
  it('does not offer sharing to a user without Vault management permission', async () => {
    api.getVaultShareGroups.mockRejectedValue(new Error('Forbidden'))
    await act(async () => root.render(<SecretSelectionSection workflowPath="Workflow/test" selectedSecrets={[]} onSecretChange={() => {}} />))
    expect(host.querySelector('[aria-label="Share PROJECT_KEY to Vault"]')).toBeNull()
  })
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
  it('offers metadata-only group grants without any reveal control', async () => {
    const grant = vi.fn().mockResolvedValue(undefined)
    await act(async () =>
      root.render(
        <SecretSelectionSection
          mode="group"
          selectedSecrets={[]}
          onSecretChange={() => {}}
          groupSelectedNames={[]}
          onGroupAccessChange={grant}
        />,
      ),
    )
    expect(host.querySelector('[aria-label="Allow TEAM_KEY"]')).toBeNull()
    await click('Add secrets')
    await click('Allow TEAM_KEY')
    expect(grant).toHaveBeenCalledWith('TEAM_KEY', true)
    expect(host.textContent).not.toContain('Rotate')
    expect(api.getGlobalSecrets).toHaveBeenCalledWith(true)
    expect(host.querySelector('input[type="password"]')).toBeNull()
    expect(host.querySelector('[aria-label="Copy TEAM_KEY"]')).toBeNull()
  })
  it('shows assigned secrets immediately and leaves available secrets collapsed', async () => {
    api.getGlobalSecrets.mockResolvedValue([
      { name: 'ASSIGNED_KEY' },
      { name: 'AVAILABLE_KEY' },
    ])
    await act(async () =>
      root.render(
        <SecretSelectionSection
          mode="group"
          selectedSecrets={[]}
          onSecretChange={() => {}}
          groupSelectedNames={['ASSIGNED_KEY']}
        />,
      ),
    )
    expect(
      host
        .querySelector('[aria-label="Allow ASSIGNED_KEY"]')
        ?.getAttribute('aria-checked'),
    ).toBe('true')
    expect(host.querySelector('[aria-label="Allow AVAILABLE_KEY"]')).toBeNull()
    expect(host.textContent).toContain('1 assigned')
    await click('Add secrets')
    expect(
      host
        .querySelector('[aria-label="Allow AVAILABLE_KEY"]')
        ?.getAttribute('aria-checked'),
    ).toBe('false')
    expect(api.revealGlobalSecret).not.toHaveBeenCalled()
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
    await click('Hide TEAM_KEY')
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

  it('copies a Vault value without rendering it', async () => {
    api.revealGlobalSecret.mockResolvedValue({ value: 'dummy-copy-value' })
    await act(async () =>
      root.render(
        <SecretSelectionSection
          mode="vault"
          selectedSecrets={[]}
          onSecretChange={() => {}}
        />,
      ),
    )
    await click('Copy TEAM_KEY')
    expect(api.revealGlobalSecret).toHaveBeenCalledWith('TEAM_KEY')
    expect(clipboardWrite).toHaveBeenCalledWith('dummy-copy-value')
    expect(host.textContent).not.toContain('dummy-copy-value')
    expect(host.querySelector('[aria-label="Copied TEAM_KEY"]')).toBeTruthy()
  })
  it('copies project values through the authorized project decrypt route', async () => {
    api.decrypt.mockResolvedValue({ value: 'dummy-project-copy' })
    await act(async () =>
      root.render(
        <SecretSelectionSection
          workflowPath="Workflow/first"
          selectedSecrets={[]}
          onSecretChange={() => {}}
        />,
      ),
    )
    await click('Copy PROJECT_KEY')
    expect(api.decrypt).toHaveBeenCalledWith('encrypted', 'Workflow/first')
    expect(clipboardWrite).toHaveBeenCalledWith('dummy-project-copy')
    expect(host.textContent).not.toContain('dummy-project-copy')
  })
  it('does not copy a cached revealed value when access has been revoked', async () => {
    api.revealGlobalSecret.mockResolvedValueOnce({ value: 'dummy-revealed' })
    await act(async () =>
      root.render(
        <SecretSelectionSection
          mode="vault"
          selectedSecrets={[]}
          onSecretChange={() => {}}
        />,
      ),
    )
    await click('Reveal TEAM_KEY')
    api.revealGlobalSecret.mockRejectedValueOnce(new Error('Access denied'))
    await click('Copy TEAM_KEY')
    expect(clipboardWrite).not.toHaveBeenCalled()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      'Could not copy',
    )
  })
  it('drops a pending copy after changing projects', async () => {
    let resolveCopy!: (result: { value: string }) => void
    api.decrypt.mockReturnValue(
      new Promise((resolve) => {
        resolveCopy = resolve
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
    await click('Copy PROJECT_KEY')
    await act(async () => renderProject('Workflow/second'))
    await act(async () => resolveCopy({ value: 'dummy-old-project' }))
    expect(clipboardWrite).not.toHaveBeenCalled()
    expect(
      host
        .querySelector('[aria-label="Copy PROJECT_KEY"]')
        ?.hasAttribute('disabled'),
    ).toBe(false)
  })
})

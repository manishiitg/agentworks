// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.mock('./RunsOnPicker', () => ({ RunsOnPicker: ({ saveAccount }: { saveAccount: string }) => <span data-testid="provider-picker">{saveAccount}</span> }))
const location = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ agentApi: { getProjectCreationLocation: location } }))
vi.mock('./LocalChatPicker', async importOriginal => ({ ...await importOriginal<typeof import('./LocalChatPicker')>(), LocalChatPicker: ({ onSelected, onClose }: { onSelected: (chat: unknown) => void; onClose: () => void }) => <button type="button" onClick={() => { onSelected({ id: 'previous', title: 'Original chat', provider: 'claude-code', account: 'Your login', updated_at: '' }); onClose() }}>Select previous</button> }))
import { CreateCrewWizard } from './CreateCrewWizard'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
beforeEach(() => { vi.useFakeTimers(); location.mockResolvedValue({ folder: 'Crew/my-project-…', can_choose_folder: false }) })
afterEach(() => { act(() => root?.unmount()); document.body.innerHTML = ''; vi.useRealTimers(); vi.clearAllMocks() })
async function render(props: Partial<Parameters<typeof CreateCrewWizard>[0]> = {}) {
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  const onCreate = vi.fn(), onClose = vi.fn()
  const update = async (extra = {}) => { await act(async () => root.render(<CreateCrewWizard onClose={onClose} onCreate={onCreate} submitting={false} error={null} {...props} {...extra} />)) }
  await update()
  await act(async () => { await vi.advanceTimersByTimeAsync(200) })
  return { host, onCreate, onClose, update }
}
async function click(host: HTMLElement, text: string) {
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === text)!.click())
}
async function input(host: HTMLElement, selector: string, value: string) {
  const node = host.querySelector<HTMLInputElement>(selector)!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => { await vi.advanceTimersByTimeAsync(200) })
}
it('creates a blank project in one step with provider controls visible and no sharing setup', async () => {
  const { host, onCreate } = await render()
  expect(document.activeElement?.id).toBe('crew-wizard-name')
  expect(host.querySelector('input[type="checkbox"]')).toBeNull()
  expect(host.querySelector('details')).toBeNull()
  expect(host.querySelector('#crew-wizard-type')).toBeNull()
  expect(host.querySelector('[data-testid="provider-picker"]')?.textContent).toBe('when-needed')
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true)
  await input(host, '#crew-wizard-name', '  My project  ')
  await click(host, 'Create project')
  expect(onCreate).toHaveBeenCalledExactlyOnceWith('My project', '', undefined, {}, undefined, undefined)
})
it('shows a retry instead of an empty folder for a malformed location response', async () => {
  location.mockResolvedValue({ status: 'ok' })
  const { host } = await render({ allowExistingFolder: true })
  expect(host.querySelector('[aria-label="Project folder"]')?.textContent).toContain('Could not load')
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Retry folder lookup')).toBe(true)
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
})
it('keeps the entered name on errors, blocks duplicate submission and supports Escape', async () => {
  const { host, onClose, onCreate, update } = await render()
  await input(host, '#crew-wizard-name', 'My project')
  await update({ error: 'Could not create project.', submitting: true })
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not create project.')
  expect(host.querySelector<HTMLInputElement>('#crew-wizard-name')!.value).toBe('My project')
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  host.querySelector('form')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  expect(onCreate).not.toHaveBeenCalled(); expect(onClose).not.toHaveBeenCalled()
  await update()
  host.querySelector('form')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  expect(onClose).toHaveBeenCalledOnce()
})
it('shows the server folder and ignores client folder-edit admission when the server refuses it', async () => {
  const { host, onCreate } = await render({ allowExistingFolder: true })
  expect(host.querySelector('[aria-label="Project folder"]')?.textContent).toContain('Crew/my-project-…')
  expect(host.querySelector('#crew-wizard-folder')).toBeNull()
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
  await input(host, '#crew-wizard-name', 'My project')
  await click(host, 'Create project')
  expect(onCreate.mock.calls[0][5]).toBeUndefined()
})
it('lets local creation change or restore the displayed default folder', async () => {
  const previous = window.electronAPI
  const pick = vi.fn(async () => '/Users/example/work')
  window.electronAPI = { ...previous, pickWorkflowFolder: pick } as typeof window.electronAPI
  location.mockResolvedValue({ folder: '/Users/example/AgentWorks/Crew/my-project-…', can_choose_folder: true })
  try {
    const { host, onCreate } = await render({ allowExistingFolder: true })
    expect(host.querySelector('[aria-label="Project folder"]')?.textContent).toContain('/Users/example/AgentWorks/Crew/my-project-…')
    await input(host, '#crew-wizard-name', 'Existing work')
    await click(host, 'Change')
    await click(host, 'Browse')
    expect(pick).toHaveBeenCalledOnce()
    expect(host.querySelector<HTMLInputElement>('#crew-wizard-folder')!.value).toBe('/Users/example/work')
    await click(host, 'Create project')
    expect(onCreate).toHaveBeenCalledExactlyOnceWith('Existing work', '', undefined, {}, '/Users/example/work', undefined)
    await click(host, 'Use default folder')
    await click(host, 'Create project')
    expect(onCreate.mock.calls[1][5]).toBeUndefined()
  } finally { window.electronAPI = previous }
})

it('forgets a selected native chat when its folder changes before Create', async () => {
  location.mockResolvedValue({ folder: '/Users/example/AgentWorks/Crew/new', can_choose_folder: true })
  const { host, onCreate } = await render({ allowExistingFolder: true })
  await input(host, '#crew-wizard-name', 'My project')
  await click(host, 'Change')
  await input(host, '#crew-wizard-folder', '/Users/example/first')
  await click(host, 'Choose chat')
  await click(host, 'Select previous')
  await click(host, 'Create project')
  expect(onCreate.mock.calls[0][5]).toMatchObject({ id: 'previous' })
  await input(host, '#crew-wizard-folder', '/Users/example/second')
  await click(host, 'Create project')
  expect(onCreate.mock.calls[1][4]).toBe('/Users/example/second')
  expect(onCreate.mock.calls[1][5]).toBeUndefined()
})

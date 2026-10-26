// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.mock('./RunsOnPicker', () => ({ RunsOnPicker: ({ saveAccount }: { saveAccount: string }) => <span data-testid="provider-picker">{saveAccount}</span> }))
vi.mock('./CrewIncludedFeatures', () => ({ CrewIncludedFeatures: ({ type }: { type: string }) => <span data-testid="included-features">{type}</span> }))
import { CreateCrewWizard } from './CreateCrewWizard'
import { CREW_STARTER_TYPES } from './crewStarterTypes'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
afterEach(() => { act(() => root?.unmount()); document.body.innerHTML = '' })
async function render(props: Partial<Parameters<typeof CreateCrewWizard>[0]> = {}) {
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  const onCreate = vi.fn(), onClose = vi.fn()
  const update = async (extra = {}) => { await act(async () => root.render(<CreateCrewWizard onClose={onClose} onCreate={onCreate} submitting={false} error={null} {...props} {...extra} />)) }
  await update()
  return { host, onCreate, onClose, update }
}
async function click(host: HTMLElement, text: string) {
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === text)!.click())
}
async function input(host: HTMLElement, selector: string, value: string) {
  const node = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector)!
  await act(async () => {
    Object.getOwnPropertyDescriptor(node instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')!.set!.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
  })
}
it.each(CREW_STARTER_TYPES)('previews and creates the $label starting point without changing runtime mode', async starter => {
  const { host, onCreate } = await render()
  expect(host.querySelectorAll('input[name="crew-type"]')).toHaveLength(CREW_STARTER_TYPES.length)
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true)
  await act(async () => host.querySelector<HTMLInputElement>(`input[value="${starter.id}"]`)!.click())
  await click(host, 'Continue')
  expect(host.textContent).toContain('Step 2 of 2')
  expect(document.activeElement?.id).toBe('crew-wizard-name')
  expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe(starter.purpose)
  expect(host.querySelector('[data-testid="included-features"]')?.textContent).toBe(starter.id)
  expect(host.querySelector('details')!.open).toBe(false)
  expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(false)
  expect(host.querySelector('[data-testid="provider-picker"]')?.textContent).toBe('when-needed')
  await input(host, '#crew-wizard-name', '  Example Crew  ')
  await click(host, 'Create Crew')
  expect(onCreate).toHaveBeenCalledExactlyOnceWith('Example Crew', starter.purpose, undefined, 'dev', { type: starter.id, openSharing: false })
})
it('keeps blank creation, optional sharing and both existing modes available', async () => {
  const { host, onCreate } = await render({ initialMode: 'cowork' })
  await click(host, 'Start blank')
  expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe('')
  await input(host, '#crew-wizard-name', 'My helper')
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  expect(host.textContent).toContain('Only me')
  expect([...host.querySelectorAll('select option')].map(node => node.getAttribute('value'))).toEqual(['cowork', 'dev'])
  await click(host, 'Create Crew')
  expect(onCreate).toHaveBeenCalledExactlyOnceWith('My helper', '', undefined, 'cowork', { type: undefined, openSharing: true })
})
it('preserves edits on Back and errors, blocks changes while creating, and supports Escape', async () => {
  const { host, onClose, onCreate, update } = await render()
  await act(async () => host.querySelector<HTMLInputElement>('input[value="brain"]')!.click())
  await click(host, 'Continue')
  await input(host, '#crew-wizard-name', 'Knowledge helper')
  await input(host, 'textarea', 'Only help with the selected team knowledge.')
  await click(host, 'Back')
  await act(async () => host.querySelector<HTMLInputElement>('input[value="specialist"]')!.click())
  await click(host, 'Continue')
  expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe('Only help with the selected team knowledge.')
  await update({ error: 'Could not create Crew.' })
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not create Crew.')
  expect(host.querySelector<HTMLInputElement>('#crew-wizard-name')!.value).toBe('Knowledge helper')
  await update({ submitting: true })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  host.querySelector('form')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  expect(onCreate).not.toHaveBeenCalled(); expect(onClose).not.toHaveBeenCalled()
  await update()
  host.querySelector('form')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  expect(onClose).toHaveBeenCalledOnce()
})

it('creates Studio through the private Code/Cowork choices without new sharing or runtime modes', async () => {
  const { host, onCreate } = await render({ studio: true, initialMode: 'cowork' })
  expect([...host.querySelectorAll<HTMLInputElement>('input[name="crew-type"]')].map(input => input.value)).toEqual(['code', 'cowork'])
  await act(async () => host.querySelector<HTMLInputElement>('input[value="code"]')!.click())
  await click(host, 'Continue')
  expect(host.textContent).toContain('Your private workspace')
  expect(host.querySelector('input[type="checkbox"]')).toBeNull()
  await input(host, '#crew-wizard-name', 'My code workspace')
  await click(host, 'Create Studio')
  expect(onCreate).toHaveBeenCalledExactlyOnceWith('My code workspace', CREW_STARTER_TYPES.find(item => item.id === 'code')!.purpose, undefined, 'dev', { type: 'code', openSharing: false })
})

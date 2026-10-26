// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { CrewEntryPage } from './CrewEntryPage'
import type { WorkSession } from './workSessions'
import { CREW_STARTER_TYPES } from './crewStarterTypes'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); document.body.innerHTML = '' })
const projects = [
  { id: 'research', product: 'work', title: 'Research', description: 'Compare useful sources', updatedAt: '2026-10-26T08:00:00Z' },
  { id: 'reports', product: 'work', title: 'Team reports', description: 'Weekly updates', updatedAt: '2026-10-25T08:00:00Z', shared: { canBuild: false, ownerId: 'another-person' } },
] as WorkSession[]

async function mount(canCreate = true) {
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  const onCreate = vi.fn(), onOpen = vi.fn()
  await act(async () => root?.render(<CrewEntryPage projects={projects} loading={false} error={null} canCreate={canCreate} onCreate={onCreate} onOpen={onOpen} onRefresh={async () => {}} />))
  return { host, onCreate, onOpen }
}

it('drafts a suggestion without creating, then forwards the exact request to existing setup', async () => {
  const { host, onCreate } = await mount()
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Cowork'))!.click())
  const draft = host.querySelector('textarea')!
  expect(draft.value).toBe(CREW_STARTER_TYPES.find(item => item.id === 'cowork')!.purpose)
  expect(host.querySelectorAll('[aria-label="Crew use cases"] button')).toHaveLength(CREW_STARTER_TYPES.length)
  expect(onCreate).not.toHaveBeenCalled()
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(onCreate).toHaveBeenCalledExactlyOnceWith(draft.value, 'cowork')
})

it('keeps admitted shared Crews searchable and opens the exact row without creation access', async () => {
  const { host, onCreate, onOpen } = await mount(false)
  expect(host.querySelector('form')).toBeNull()
  expect(host.querySelector('textarea')).toBeNull()
  const search = host.querySelector<HTMLInputElement>('[aria-label="Search Crews"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'weekly')
    search.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const rows = host.querySelectorAll('li button')
  expect(rows).toHaveLength(1)
  expect(rows[0].textContent).toContain('Shared with you')
  await act(async () => (rows[0] as HTMLButtonElement).click())
  expect(onOpen).toHaveBeenCalledExactlyOnceWith(projects[1])
  expect(onCreate).not.toHaveBeenCalled()
})

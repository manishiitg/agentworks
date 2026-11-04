// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CleanupOldChatsDropdown } from './CleanupOldChatsDropdown'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function mount(props: Partial<React.ComponentProps<typeof CleanupOldChatsDropdown>> = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const onSelect = vi.fn()
  await act(async () => root.render(
    <CleanupOldChatsDropdown counts={{ 14: 19, 7: 21, 3: 21 }} isLoading={false} onSelect={onSelect} {...props} />,
  ))
  return { host, onSelect, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('opens age options and reports the selection', async () => {
  const { host, onSelect, unmount } = await mount()
  try {
    expect(host.querySelector('[role="menu"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click())
    const items = Array.from(host.querySelectorAll('[role="menuitem"]'))
    expect(items.map(item => item.textContent)).toEqual(['Delete >14d19', 'Delete >7d21', 'Delete >3d21'])
    await act(async () => (items[1] as HTMLButtonElement).click())
    expect(onSelect).toHaveBeenCalledWith(7)
  } finally {
    await unmount()
  }
})

it('renders nothing when there is nothing to delete', async () => {
  const { host, unmount } = await mount({ counts: { 14: 0, 7: 0, 3: 0 } })
  try {
    expect(host.textContent).toBe('')
  } finally {
    await unmount()
  }
})

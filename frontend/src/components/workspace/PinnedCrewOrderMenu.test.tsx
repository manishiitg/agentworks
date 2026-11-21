// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { PinnedCrewOrderMenu } from './PinnedCrewOrderMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// The real short-menu bug: fixed280px reservation detached a small popup from its trigger.
it('positions a short project menu by its actual height and keeps pin inside it', async () => {
  const host = document.createElement('div'), root = createRoot(host), pin = vi.fn()
  document.body.append(host)
  const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    return this.getAttribute('role') === 'dialog'
      ? { top: 0, bottom: 64, left: 0, right: 208, width: 208, height: 64, x: 0, y: 0, toJSON: () => ({}) }
      : { top: 500, bottom: 532, left: 200, right: 228, width: 28, height: 32, x: 200, y: 500, toJSON: () => ({}) }
  })
  try {
    await act(async () => root.render(<PinnedCrewOrderMenu name="Example" pinned onTogglePin={pin} groupsMenu={<button>Add to group…</button>} />))
    expect(host.textContent).not.toContain('Unpin')
    await act(async () => host.querySelector('button')!.click())
    const menu = document.querySelector<HTMLElement>('[role="dialog"][aria-label="Actions for Example"]')!
    expect(menu.style.top).toBe('536px')
    await act(async () => [...menu.querySelectorAll('button')].find(button => button.textContent === 'Unpin Crew')!.click())
    expect(pin).toHaveBeenCalledOnce()
    expect(document.querySelector('[role="dialog"][aria-label="Actions for Example"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove(); rect.mockRestore() }
})

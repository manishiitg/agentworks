// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { WorkspaceContextCard } from './WorkspaceContextCard'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('opens the existing Ctrl+K switcher scoped to this product’s registered panels', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const open = vi.fn()
  window.addEventListener('open-quick-switcher', open)
  try {
    await act(async () =>
      root.render(
        <WorkspaceContextCard>
          <div>Current workspace</div>
        </WorkspaceContextCard>
      )
    )
    const button = host.querySelector('[aria-label="All tools"]') as HTMLButtonElement
    expect(button.textContent).toMatch(/⌘K|Ctrl\+K/)
    await act(async () => button.click())
    expect(open).toHaveBeenCalledOnce()
    expect((open.mock.calls[0][0] as CustomEvent).detail).toEqual({ query: '@panels ' })
  } finally {
    window.removeEventListener('open-quick-switcher', open)
    await act(async () => root.unmount())
    host.remove()
  }
})

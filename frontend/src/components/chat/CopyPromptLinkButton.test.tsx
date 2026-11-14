// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ post: vi.fn(), toast: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { post: mocks.post } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: mocks.toast }) } }))
import { CopyPromptLinkButton } from './CopyPromptLinkButton'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-856-567: typing must not add a permanent link/warning text row.
it('keeps the toolbar icon-only and warns only after the link is copied', async () => {
  const copied = vi.fn().mockResolvedValue(undefined)
  const clipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copied } })
  mocks.post.mockResolvedValue({ data: { url: 'https://example.com/new?prompt=Review' } })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => { root.render(<CopyPromptLinkButton crew="example" prompt="Review" />) })
    const button = host.querySelector('button')!
    expect(button.getAttribute('aria-label')).toBe('Copy prompt link')
    expect(host.textContent).toBe('')
    expect(mocks.toast).not.toHaveBeenCalled()
    await act(async () => { button.click(); await Promise.resolve() })
    expect(copied).toHaveBeenCalledWith('https://example.com/new?prompt=Review')
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining("don't put passwords"), 'success')
    expect(host.textContent).toBe('')
  } finally {
    await act(async () => root.unmount()); host.remove(); vi.clearAllMocks()
    if (clipboard) Object.defineProperty(navigator, 'clipboard', clipboard)
    else Reflect.deleteProperty(navigator, 'clipboard')
  }
})

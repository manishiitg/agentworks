// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))
import TipsToggle from './TipsToggle'
import { PRODUCT_TIPS_PREFERENCE_CHANGED } from '../../utils/productTipsPreference'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads the saved preference and broadcasts only the value saved by the server', async () => {
  api.get.mockResolvedValue({ data: { enabled: true } })
  api.put.mockResolvedValueOnce({ data: { enabled: false } }).mockRejectedValueOnce(new Error('Save failed'))
  const changed = vi.fn()
  window.addEventListener(PRODUCT_TIPS_PREFERENCE_CHANGED, changed)
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<TipsToggle />))
    const toggle = host.querySelector<HTMLButtonElement>('button')!
    expect(toggle.getAttribute('aria-checked')).toBe('true')
    expect(toggle.textContent).toBe('Tips while workingOn')
    await act(async () => toggle.click())
    expect(api.put).toHaveBeenLastCalledWith('/api/product-tips/preference', { enabled: false }, { skipSessionContext: true })
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    expect(changed.mock.calls[0][0].detail).toEqual({ enabled: false })
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    expect(changed).toHaveBeenCalledOnce()
  } finally {
    await act(async () => root.unmount())
    window.removeEventListener(PRODUCT_TIPS_PREFERENCE_CHANGED, changed)
    api.get.mockReset(); api.put.mockReset()
  }
})

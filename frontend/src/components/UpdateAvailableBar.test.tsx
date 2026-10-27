// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { UpdateAvailableBar } from './UpdateAvailableBar'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { document.body.innerHTML = ''; document.head.innerHTML = ''; vi.restoreAllMocks() })

// A pinned tab kept running the build it loaded and showed outdated cards; it must learn the server moved on.
it('offers a reload once the server serves a newer build', async () => {
  const script = document.createElement('script')
  script.src = 'https://example.com/assets/index-OLD123.js'
  document.head.append(script)
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('<script type="module" src="/assets/index-NEW456.js"></script>'))
  const host = document.createElement('div'); document.body.append(host)
  await act(async () => createRoot(host).render(<UpdateAvailableBar />))
  expect(host.textContent).toBe('')
  await act(async () => { window.dispatchEvent(new Event('focus')); await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(host.textContent).toContain('AgentWorks was updated.')
})

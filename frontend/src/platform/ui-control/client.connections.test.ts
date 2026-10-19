// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { applyUIAction } from './client'

afterEach(() => { document.body.innerHTML = ''; vi.restoreAllMocks() })
it('acknowledges Secrets only after that Connections section is visible', async () => {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList)
  document.body.innerHTML = '<div data-ui-workspace="project" data-ui-view="mcp"><div data-ui-view-mounted></div><div data-ui-view-section="connected"></div></div>'
  const section = document.querySelector<HTMLElement>('[data-ui-view-section]')!
  let finished = false
  const pending = applyUIAction({ request_id: 'secrets', view: 'mcp', action: 'open', target: 'secrets', expires_at: new Date(Date.now() + 10000).toISOString() }, 'project', () => {}, () => ({ view: 'mcp', revision: 1, visible: true }), new AbortController().signal).then(result => { finished = true; return result })
  await new Promise(resolve => requestAnimationFrame(resolve))
  expect(finished).toBe(false)
  section.dataset.uiViewSection = 'secrets'
  expect(await pending).toEqual({ status: 'applied', code: '' })
})

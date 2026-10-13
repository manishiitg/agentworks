// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('wraps below desktop width so every control stays reachable, and stays one row at desktop width', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => {
    root.render(<WorkspaceTopToolbar><span>tabs</span><span>toolbar</span></WorkspaceTopToolbar>)
  })
  const bar = container.firstElementChild!
  expect(bar.className).toContain('flex-wrap')
  expect(bar.className).toContain('md:flex-nowrap')
  expect(bar.className).not.toMatch(/(?<!md:)flex-nowrap/)
  await act(async () => { root.unmount() })
})

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('../../hooks/usePendingCrewSuggestions', () => ({ usePendingCrewSuggestions: () => 0 }))

import { WorkWorkspaceToolbar } from './WorkWorkspacePane'
import { CODE_PRODUCT, CREW_PRODUCT, ProjectProductProvider } from './projectProduct'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let root: Root
let host: HTMLDivElement
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host) })
afterEach(() => { act(() => root.unmount()); host.remove() })

const render = (product: typeof CODE_PRODUCT, props: { showShell?: boolean; readOnly?: boolean }) => act(() => {
  root.render(
    <ProjectProductProvider value={product}>
      <WorkWorkspaceToolbar workspacePath="p" view="files" onViewChange={() => undefined} {...props} />
    </ProjectProductProvider>,
  )
})
const hasTerminal = () => Array.from(host.querySelectorAll('button,[role="button"]')).some(node => /Terminal/.test(node.getAttribute('aria-label') || node.textContent || ''))
  || host.innerHTML.includes('Terminal')

it('shows the Terminal button in a Code the caller owns, and nowhere else', () => {
  render(CODE_PRODUCT, { showShell: true })
  expect(hasTerminal()).toBe(true)
  act(() => root.unmount())
  root = createRoot(host)
  render(CODE_PRODUCT, { showShell: false })
  expect(hasTerminal()).toBe(false)
  act(() => root.unmount())
  root = createRoot(host)
  render(CREW_PRODUCT, { showShell: false })
  expect(hasTerminal()).toBe(false)
})

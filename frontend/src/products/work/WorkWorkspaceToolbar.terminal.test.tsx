// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

// Connection transport is outside this toolbar layout test. Keep it off the live feed.
vi.mock('../../hooks/useBrowserToolbarConnection', () => ({ useBrowserToolbarConnection: () => ({ connected: false, label: 'No browser open' }) }))
vi.mock('../../components/GlobalActivityMonitor', () => ({ GlobalActivityMonitor: () => <button aria-label="Active work monitor">Active work</button> }))

import { agentApi } from '../../services/api'
import { setProjectMode } from './codeLocalFiles'
import { usePanelSwitcherStore } from '../../stores/usePanelSwitcherStore'
import { useChatStore } from '../../stores/useChatStore'
import { WorkWorkspaceToolbar, type WorkWorkspaceView } from './WorkWorkspacePane'
import { CREW_PRODUCT, ProjectProductProvider } from './projectProduct'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let root: Root
let host: HTMLDivElement
beforeEach(() => { vi.spyOn(agentApi, 'listReportHumanInputs').mockResolvedValue({ success: true, inputs: [] }); host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host) })
afterEach(() => { act(() => root.unmount()); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

const render = (product: typeof CREW_PRODUCT, props: { view?: WorkWorkspaceView; showShell?: boolean; readOnly?: boolean; showActivityMonitor?: boolean; sessionId?: string; enabledPanels?: Set<string> }) => act(() => {
  root.render(
    <ProjectProductProvider value={product}>
      <WorkWorkspaceToolbar workspacePath="p" view="files" onViewChange={() => undefined} {...props} />
    </ProjectProductProvider>,
  )
})
const hasTerminal = () => Array.from(host.querySelectorAll('button,[role="button"]')).some(node => /Terminal/.test(node.getAttribute('aria-label') || node.textContent || ''))
  || host.innerHTML.includes('Terminal')

it('keeps Terminal in the Crew Builder menu, without duplicate toolbar buttons', () => {
  render(CREW_PRODUCT, { showShell: true })
  expect(usePanelSwitcherStore.getState().entries.work?.panels.some(item => item.id === 'shell')).toBe(true)
  expect(hasTerminal()).toBe(false)
  render(CREW_PRODUCT, { showShell: false })
  expect(usePanelSwitcherStore.getState().entries.work?.panels.some(item => item.id === 'shell')).toBe(false)
})

it('shows Run people only the dashboard inventory', () => {
  render(CREW_PRODUCT, { readOnly: true })
  expect(usePanelSwitcherStore.getState().entries.work?.panels.map(item => item.id)).toEqual(['dashboard'])
  expect(host.querySelector('[aria-label="Back to context"]')).toBeNull()
})

it('groups search like the menu and does not advertise the retired Bots tab', () => {
  render(CREW_PRODUCT, {})
  const entries = usePanelSwitcherStore.getState().entries.work!.panels
  expect(entries.find(item => item.id === 'schedules:triggers')).toMatchObject({ label: 'Webhooks', group: 'Use' })
  expect(entries.find(item => item.id === 'mcp:slack-bot')).toMatchObject({ label: 'Slack bot', group: 'Use' })
  expect(entries.find(item => item.id === 'mcp:vault')).toMatchObject({ label: 'Vault', group: 'Integrations' })
  expect(entries.some(item => item.id === 'mcp:slack-account')).toBe(false)
  expect(entries.find(item => item.id === 'history')).toMatchObject({ label: 'Chat history', group: 'Use' })
  expect(entries.find(item => item.id === 'schedules:functions')).toMatchObject({ label: 'Functions', group: 'Build' })
  expect(entries.flatMap(item => item.sections ?? []).some(item => item.id === 'bots')).toBe(false)
})

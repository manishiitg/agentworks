// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ToolSelectionSection } from './ToolSelectionSection'
import { useMCPStore } from '../stores/useMCPStore'

vi.mock('../services/api', () => ({ agentApi: { getTools: vi.fn(), getToolDetail: vi.fn() } }))
vi.mock('../stores/useMCPStore', async () => {
  const { create } = await import('zustand')
  return { useMCPStore: create(() => ({ toolList: [] })) }
})

const originalToolList = useMCPStore.getState().toolList
const cleanups: Array<() => void> = []

afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup()
  useMCPStore.setState({ toolList: originalToolList })
})

describe('ToolSelectionSection workflow checkbox', () => {
  it('sends one combined update and shows a selected disconnected server as configured', async () => {
    useMCPStore.setState({ toolList: [] })
    const onSelectionChange = vi.fn()
    const onServerChange = vi.fn()
    const onToolChange = vi.fn()
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })

    await act(async () => root.render(
      <ToolSelectionSection
        stepId="workflow-checkbox-test"
        availableServers={['Linear']}
        selectedServers={['Linear']}
        selectedTools={['Linear:*']}
        onServerChange={onServerChange}
        onToolChange={onToolChange}
        onSelectionChange={onSelectionChange}
        agentMode="workflow"
        hideToolDetails
      />
    ))

    expect(host.textContent).toContain('Configured')
    expect(host.textContent).toContain('Needs connecting')
    const checkbox = host.querySelector('[role="checkbox"]') as HTMLElement
    await act(async () => checkbox.click())
    expect(onSelectionChange).toHaveBeenCalledExactlyOnceWith([], [])
    expect(onServerChange).not.toHaveBeenCalled()
    expect(onToolChange).not.toHaveBeenCalled()
  })
})

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { getProductCommands, setProductCommands } from './registry'

const loader = vi.hoisted(() => vi.fn())
vi.mock('./agentworksProductData', () => ({ loadWorkflowProductCommands: loader }))
import { useWorkflowProductCommands } from './useWorkflowProductCommands'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('clears the previous catalog and ignores late responses when switching products', async () => {
  const pending = new Map<string, (commands: any[]) => void>()
  loader.mockImplementation((id: string) => new Promise(resolve => pending.set(id, resolve)))
  function Chat({ relay }: { relay: boolean }) { useWorkflowProductCommands(relay); return null }
  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(<Chat relay={false} />))
    await act(async () => root.render(<Chat relay />))
    await act(async () => pending.get('agentworks')!([{ name: 'design-dashboard', prompt: 'Old workflow', aliases: [], menuHidden: false }]))
    expect(getProductCommands('workflow', 'workshop')).toEqual([])
    await act(async () => pending.get('relays')!([{ name: 'publish', prompt: 'Publish Relay', aliases: [], menuHidden: false }]))
    expect(getProductCommands('workflow', 'workshop').map(command => command.command)).toEqual(['publish'])
    await act(async () => root.render(<Chat relay={false} />))
    expect(getProductCommands('workflow', 'workshop')).toEqual([])
  } finally { await act(async () => root.unmount()); setProductCommands([]) }
})

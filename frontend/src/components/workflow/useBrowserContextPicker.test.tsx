// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useBrowserContextPicker } from './useBrowserContextPicker'

const fixture = vi.hoisted(() => ({ post: vi.fn(), state: undefined as unknown as { activeTabId: string; getTab: (id: string) => unknown; setTabConfig: (id: string, patch: object) => void; setTabViewMode: (id: string, mode: string) => void; addToast: (message: string, kind: string) => void } }))
vi.mock('../../services/api', () => ({ default: { post: (...args: unknown[]) => fixture.post(...args) } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => fixture.state } }))
vi.mock('../ui/WorkspaceImage', () => ({ WorkspaceImage: ({ alt }: { alt: string }) => <img alt={alt} /> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('does not activate the page or stage files before Add, and cancels a stale source reply (PLAT-746-1005)', async () => {
  const pageInput = vi.fn()
  const tab = { sessionId: 'chat-session', viewMode: 'terminal', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: 'crew:chat:agent' }, config: { inputText: 'Keep this draft', autoRun: true, fileContext: [] as unknown[] } }
  fixture.state = { activeTabId: 'chat', getTab: id => id === 'chat' ? tab : undefined, setTabConfig: (_, patch) => { Object.assign(tab.config, patch) }, setTabViewMode: (_, mode) => { tab.viewMode = mode }, addToast: vi.fn() }
  const probe = { document_token: 'document', url: 'https://example.com/page', width: 100, height: 100, rect: { x: 2, y: 3, width: 20, height: 20 } }
  const captured = { source: 'browser', capture: { ...probe, selector: '#picked', text: 'Selected text' }, preview_path: 'crew/uploads/image.png', files: [{ name: 'image.png', path: 'crew/uploads/image.png', type: 'file' }, { name: 'context.json', path: 'crew/uploads/context.json', type: 'file' }] }
  fixture.post.mockImplementation(async (url: string, body: { operation: string }) => {
    if (!url.endsWith('/chat-context')) throw new Error('Unexpected command or send')
    return { data: body.operation === 'probe' ? probe : captured }
  })
  function View({ sourceKey }: { sourceKey: string }) {
    const picker = useBrowserContextPicker({ tabId: 'chat', browserSession: 'managed-browser', sourceKey, enabled: true, onBegin: () => undefined, point: () => ({ x: 10, y: 10 }), outline: () => ({ left: 2, top: 3, width: 20, height: 20 }) })
    return <div onMouseDown={pageInput}>{picker.button}{picker.overlay}</div>
  }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<View sourceKey="page-a" />))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    const overlay = host.querySelector('[data-testid="browser-context-picker"]')!
    await act(async () => overlay.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0, clientX: 10, clientY: 10 })))
    expect(pageInput).not.toHaveBeenCalled()
    expect(host.textContent).toContain('Selected text')
    expect(tab.config.fileContext).toHaveLength(0)
    const attach = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Add to chat')!
    await act(async () => attach.click())
    expect(tab.config.fileContext).toHaveLength(2)
    expect(tab.config.inputText).toBe('Keep this draft')
    expect(tab.config.autoRun).toBe(false)
    expect(tab.viewMode).toBe('formatted')
    let finish!: (response: { data: typeof probe }) => void
    fixture.post.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    await act(async () => root.render(<View sourceKey="page-b" />))
    await act(async () => finish({ data: probe }))
    expect(host.querySelector('[data-testid="browser-context-picker"]')).toBeNull()
    expect(tab.config.fileContext).toHaveLength(2)
  } finally { await act(async () => root.unmount()) }
})

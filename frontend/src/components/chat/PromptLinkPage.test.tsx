// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ post: vi.fn(), present: vi.fn(), draft: {} as Record<string, unknown>, write: vi.fn(), send: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { post: mocks.post } }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => ({}), assertChatIdentityCurrent: () => {} }))
vi.mock('../../utils/projectAskConversation', () => ({ presentProjectAskConversation: mocks.present }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ getTabConfig: () => mocks.draft, setTabConfig: (_id: string, changes: object) => { Object.assign(mocks.draft, changes); mocks.write(changes) }, addToQueue: mocks.send }) } }))
vi.mock('../ChatArea', () => ({ default: () => <textarea aria-label="Draft" readOnly value={String(mocks.draft.inputText || '')} /> }))
import { PromptLinkPage } from './PromptLinkPage'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); mocks.draft = {}; window.history.replaceState({}, '', '/') })

// A link cannot enable auto-run, change settings, or attach prior draft files.
it('opens a draft without sending and requires a choice before replacing existing work', async () => {
  window.history.replaceState({}, '', '/new?crew=crew&agent=chosen&prompt=Review+the+plan&autoRun=true&mode=builder')
  mocks.post.mockResolvedValue({ data: { workspace: 'Crew/example' } })
  mocks.present.mockResolvedValue('recipient-tab')
  mocks.draft = { inputText: 'My unfinished work', autoRun: true, fileContext: [{ path: 'private-notes.txt' }], selectedModel: 'my-model' }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => { root.render(<PromptLinkPage />); await Promise.resolve() })
    expect(mocks.post).toHaveBeenCalledWith('/api/prompt-links/open', { crew: 'crew', agent: 'chosen' }, expect.anything())
    expect(mocks.draft.inputText).toBe('My unfinished work')
    expect(mocks.draft.autoRun).toBe(false)
    expect(mocks.send).not.toHaveBeenCalled()
    const replace = [...host.querySelectorAll('button')].find(button => button.textContent === 'Replace draft')!
    await act(async () => { replace.click(); await Promise.resolve() })
    expect(mocks.draft.inputText).toBe('Review the plan')
    expect(mocks.draft.fileContext).toEqual([])
    expect(mocks.draft.selectedModel).toBe('my-model')
    expect(mocks.draft.autoRun).toBe(false)
    expect(mocks.send).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove() }
})

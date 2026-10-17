// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const post = vi.hoisted(() => vi.fn())
const tabs = vi.hoisted(() => ({ chatTabs: { local: { sessionId: 'local-chat', metadata: { agentProfileId: 'code', agentProfileProjectId: 'local-project', agentProfileProjectTitle: 'Invoices', agentProfileConversationKey: 'local-project:chat:chosen' } } } }))
vi.mock('../../services/api', () => ({ default: { post } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: typeof tabs) => unknown) => select(tabs) }))
vi.mock('./codeLocalFiles', () => ({ localFileError: (cause: Error) => cause.message }))
import { CrewPublish } from './CrewPublish'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''; vi.clearAllMocks() })

it('discloses the destination and account before starting an import of the chosen private chat', async () => {
  post.mockImplementation(async (_url, args) => ({ data: args.action === 'list'
    ? { workspaces: [{ workspace_id: 'work:existing', name: 'Finance', mode: 'builder' }] }
    : args.action === 'publish_status' ? { state: 'idle' }
    : args.action === 'publish_preview'
      ? { account: { provider: 'company-runtime', model: 'company-model', use: 'Company quota applies' }, share: { mode: 'people' }, continuity: 'Continues from a private summary', message: 'Keeps Main and Share' }
      : { operation_id: 'receipt', state: 'live', workspace_id: 'work:existing', agent_id: 'stable-agent', message: 'Files and chat saved', excluded: ['.env', 'linked.txt'] } }))
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root?.render(<CrewPublish sessionId="local-chat" />))
  const click = async (text: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === text)!.click())
  await click('Publish to Crew')
  const select = host.querySelector('select')!
  await act(async () => { select.value = 'work:existing'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  await click('Review publication')
  expect(host.textContent).toContain('company-runtime'); expect(host.textContent).toContain('Chosen people')
  expect(post.mock.calls.some(([, args]) => args.action === 'publish')).toBe(false)
  await click('Publish now')
  expect(post).toHaveBeenCalledWith('/api/workspaces', expect.objectContaining({ action: 'publish', source_workspace_id: 'code:local-project', conversation_key: 'local-project:chat:chosen', workspace_id: 'work:existing', name: 'Invoices', submission_id: expect.any(String) }))
  expect(host.textContent).toContain('Files and chat saved')
  expect(host.textContent).toContain('2 files or folders stayed on your computer')
  expect(host.textContent).toContain('linked.txt')
  expect(host.querySelector('a')?.getAttribute('href')).toContain('/projects/existing')
})

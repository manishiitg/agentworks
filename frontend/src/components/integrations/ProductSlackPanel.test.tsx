// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProductSlackPanel } from './ProductSlackPanel'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), draft: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: api.get, post: api.post }, agentApi: {} }))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({useWorkspaceConnectionStore:(select:any)=>select({activeWorkspaceId:'server'})}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{is_admin:false}})}))
vi.mock('./useVaultSetupDraft',()=>({useVaultSetupDraft:()=>({open:api.draft,error:''})}))
vi.mock('./VaultSlackBots',()=>({VaultSlackBots:()=> <div data-bot-editor/>}))
vi.mock('../admin/SlackAdminPanel',()=>({default:()=>null}))
vi.mock('./SlackAccountsPanel', () => ({ default: () => <p>Personal account controls</p> }))
vi.mock('../workflow/bots/SlackBotWhere', () => ({ SlackBotWhere: () => null }))
vi.mock('../workflow/bots/SlackSlugsSection', () => ({ SlackSlugsSection: () => null, slackErrorText: (_error: unknown, message: string) => message }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('keeps the bot form readable and preserves server selection restrictions and direct bot management', async () => {
  api.get.mockResolvedValue({ data: { bots: [{ id: 'one', name: 'Team bot' }], selected_id: '', can_select: false, allow_bots: true } })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ProductSlackPanel workspacePath="Crew/one" section="bot" />))
    expect(host.firstElementChild?.className).toContain('max-w-3xl')
    expect(host.textContent).toContain('Talk to this Crew in Slack')
    expect(host.textContent).toContain('with their existing access')
    expect(host.querySelector<HTMLSelectElement>('[aria-label="Saved Slack bot"]')?.disabled).toBe(true)
    expect(host.textContent).not.toContain('Personal account controls')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Manage bots in chat')!.click())
    expect(api.draft).toHaveBeenCalledWith(expect.stringContaining('manage my Slack bot'))
    expect(api.post).not.toHaveBeenCalled()
    api.get.mockResolvedValue({ data: { bots: [], selected_id: '', allow_bots: false } })
    await act(async () => root.render(<ProductSlackPanel key="other" workspacePath="Crew/other" section="bot" />))
    expect(host.querySelector('select')).toBeNull()
    expect(host.querySelector('button')).toBeNull()
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

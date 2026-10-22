// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'
const mocks=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}))
vi.mock('../../services/api',()=>({default:{get:mocks.get,post:mocks.post},getApiBaseUrl:()=>'',getAuthToken:()=>null}))
vi.mock('./useVaultSetupDraft', () => ({ useVaultSetupDraft: () => ({ open: vi.fn(), error: '' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: vi.fn() } }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: vi.fn() }))
import { ProjectMcpPanel } from './ProjectMcpPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
let clean=()=>{}
afterEach(()=>{clean();vi.clearAllMocks()})
it('uses only Vault and starts setup in chat without credential forms',async()=>{
 mocks.post.mockResolvedValue({data:{result:JSON.stringify({apps:[{name:'Example app',oauth:true}]})}})
 mocks.get.mockResolvedValue({data:{servers:[{id:'c-12345678',label:'Work app',provider:'example',status:'active',tools:[]}]}})
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 clean=()=>{act(()=>root.unmount());host.remove()}
 const ask=vi.fn().mockResolvedValue(undefined);const select=vi.fn()
 await act(async()=>root.render(<TooltipProvider><ProjectMcpPanel workspacePath="Workflow/demo" placeNoun="workflow" canEdit onAsk={ask} selectedServers={['NO_SERVERS']} onSelectedServersChange={select}/></TooltipProvider>))
 expect(mocks.get).toHaveBeenCalledWith('/api/vault/connections',{params:{workspace_path:'Workflow/demo'}})
 expect(host.textContent).toContain('Work app')
 expect((host.querySelector('input[type="checkbox"]') as HTMLInputElement)?.checked).toBe(false)
 expect(host.querySelector('input[type="password"]')).toBeNull()
 await act(async()=>root.render(<TooltipProvider><ProjectMcpPanel view="available" workspacePath="Workflow/demo" placeNoun="workflow" canEdit onAsk={ask} selectedServers={['NO_SERVERS']} onSelectedServersChange={select}/></TooltipProvider>))
 expect(host.textContent).toContain('Example app')
 const button=[...host.querySelectorAll('button')].find(b=>b.textContent?.includes('Connect an app in chat'))!
 await act(async()=>button.click())
 expect(ask).toHaveBeenCalledWith(expect.stringContaining('using Vault'))
})

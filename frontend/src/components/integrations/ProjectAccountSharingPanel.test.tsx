// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks=vi.hoisted(()=>({op:vi.fn(),draft:vi.fn(),admin:false}))
vi.mock('../../services/api',()=>({agentApi:{myVaultsOp:mocks.op}}))
vi.mock('./useVaultSetupDraft',()=>({useVaultSetupDraft:(tabId:string)=>({open:(message:string)=>mocks.draft(tabId,message),error:''})}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{id:'owner',is_admin:mocks.admin}})}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({useWorkspaceConnectionStore:(select:any)=>select({activeWorkspaceId:'server'})}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:(select:any)=>select({chatTabs:{visible:{isStreaming:false}}})}))
import {ProjectAccountSharingPanel,CompanySignInSetup} from './ProjectAccountSharingPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
let clean=()=>{}
afterEach(()=>{clean();vi.clearAllMocks();mocks.admin=false})
async function mount(){mocks.op.mockResolvedValue(JSON.stringify({vaults:[{group:{ID:'empty',Name:'Team accounts'},role:'owner',secret_names:[],connections:[]}]}));const host=document.createElement('div');document.body.append(host);const root=createRoot(host);clean=()=>{act(()=>root.unmount());host.remove()};await act(async()=>root.render(<><ProjectAccountSharingPanel workspacePath="Crew/example" tabId="visible" kind="apps"/><CompanySignInSetup provider="Google" tabId="visible"/></>));return host}
it('keeps empty owned sharing groups manageable in the visible chat without duplicating resource lists',async()=>{const host=await mount();expect(host.textContent).toContain('Team accounts');expect((host.querySelector('details') as HTMLDetailsElement).open).toBe(false);expect(host.querySelector('input,select,form')).toBeNull();await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Manage in chat')!.click());expect(mocks.draft).toHaveBeenCalledWith('visible',expect.stringContaining('sharing ID empty'));expect(host.textContent).not.toContain('Company sign-in setup')})
it('keeps provider sign-in setup administrator-only',async()=>{mocks.admin=true;const host=await mount();expect(host.textContent).toContain('Company sign-in setup');await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Configure in chat')!.click());expect(mocks.draft).toHaveBeenCalledWith('visible',expect.stringContaining('company Google OAuth'))})

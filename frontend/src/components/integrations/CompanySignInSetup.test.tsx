// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
const state=vi.hoisted(()=>({admin:false,multiUser:false,draft:vi.fn()}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{is_admin:state.admin},isMultiUserMode:state.multiUser})}))
vi.mock('./useVaultSetupDraft',()=>({useVaultSetupDraft:(tabId:string)=>({open:(message:string)=>state.draft(tabId,message),error:''})}))
import {CompanySignInSetup} from './CompanySignInSetup'
it('keeps provider setup administrator-only and routes its action to the visible chat',async()=>{Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});const host=document.createElement('div');const root=createRoot(host);try{await act(async()=>root.render(<CompanySignInSetup provider="Google" tabId="visible"/>));expect(host.textContent).toBe('');state.admin=true;await act(async()=>root.render(<CompanySignInSetup provider="Google" tabId="visible"/>));await act(async()=>host.querySelector('button')!.click());expect(host.textContent).toContain('Google sign-in setup');expect(host.textContent).not.toContain('company');expect(state.draft).toHaveBeenCalledWith('visible',expect.stringContaining('the Google OAuth'));state.multiUser=true;await act(async()=>root.render(<CompanySignInSetup provider="Google" tabId="visible"/>));expect(host.textContent).toContain('Company sign-in setup')}finally{await act(async()=>root.unmount());state.admin=false;state.multiUser=false;vi.clearAllMocks()}})

// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach, expect, it, vi} from 'vitest'
const api = vi.hoisted(() => ({get:vi.fn(), post:vi.fn(), delete:vi.fn()}))
vi.mock('../../services/api', () => ({default:api}))
vi.mock('./CompanySignInSetup', () => ({CompanySignInSetup: () => <span>OAuth setup</span>}))
import {GitHubVaultCard} from './GitHubVault'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
let clean=()=>{}
afterEach(()=>{clean();vi.clearAllMocks()})
it('offers gh then PAT then optional OAuth, saving a token without an OAuth app and clearing the field', async()=>{
  api.get.mockResolvedValue({data:{connected:false, app_ready:false, gh_available:true}})
  api.post.mockResolvedValue({data:{connected:true,login:'example-user',source:'pat'}})
  const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
  clean=()=>{act(()=>root.unmount());host.remove()}
  await act(async()=>root.render(<GitHubVaultCard/>))
  expect(host.textContent!.indexOf('Use your GitHub CLI login')).toBeLessThan(host.textContent!.indexOf('Personal access token'))
  expect(host.textContent!.indexOf('Personal access token')).toBeLessThan(host.textContent!.indexOf('Browser sign-in'))
  const input=host.querySelector('input')!
  await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'github_pat_fixture');input.dispatchEvent(new Event('input',{bubbles:true}))})
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(false)
  await act(async()=>host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})))
  expect(api.post).toHaveBeenCalledWith('/api/vault/github/connect',{method:'pat',token:'github_pat_fixture'})
  expect(input.value).toBe('')
})
it('shows the saved identity without offering the server operator gh login; disconnect uses the source-specific message', async()=>{
  api.get.mockResolvedValue({data:{connected:true, login:'example-user',source:'pat',app_ready:false,gh_available:false}})
  api.delete.mockResolvedValue({data:{message:'Disconnected from AgentWorks. The token itself is unchanged.'}})
  const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
  clean=()=>{act(()=>root.unmount());host.remove()}
  await act(async()=>root.render(<GitHubVaultCard/>))
  expect(host.textContent).toContain('GitHub · example-user')
  expect(host.textContent).not.toContain('Use gh')
  expect(api.post).not.toHaveBeenCalled()
  await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent==='Disconnect')!.click())
  expect(host.textContent).toContain('The token itself is unchanged.')
})
it('imports the chosen local gh account only after Use account is clicked', async()=>{
  api.get.mockResolvedValueOnce({data:{connected:true,login:'first-user',source:'gh',app_ready:false,gh_available:true,gh_accounts:['first-user','second-user']}})
    .mockResolvedValue({data:{connected:true,login:'second-user',source:'gh',app_ready:false,gh_available:true,gh_accounts:['first-user','second-user']}})
  api.post.mockResolvedValue({data:{connected:true,login:'second-user',source:'gh'}})
  const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
  clean=()=>{act(()=>root.unmount());host.remove()}
  await act(async()=>root.render(<GitHubVaultCard/>))
  const picker=host.querySelector<HTMLSelectElement>('select')!
  expect(picker.value).toBe('first-user')
  expect(api.post).not.toHaveBeenCalled()
  await act(async()=>{picker.value='second-user';picker.dispatchEvent(new Event('change',{bubbles:true}))})
  expect(api.post).not.toHaveBeenCalled()
  await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent==='Use account')!.click())
  expect(api.post).toHaveBeenCalledWith('/api/vault/github/connect',{method:'gh',login:'second-user'})
  expect(host.textContent).toContain('GitHub · second-user')
})

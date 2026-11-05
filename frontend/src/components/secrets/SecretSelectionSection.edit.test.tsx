// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ writable: true, encrypt: vi.fn(), store: vi.fn() }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => state.writable }))
vi.mock('./SecretSourceView', () => ({ default: () => null }))
vi.mock('../../api/secrets', () => ({ secretsApi: { getGlobalSecrets: async () => [], encrypt: state.encrypt, storeWorkflowSecret: state.store } }))
import { SecretSelectionSection } from './SecretSelectionSection'
Object.assign(globalThis, {IS_REACT_ACT_ENVIRONMENT:true})
let cleanup=()=>{}
afterEach(()=>{cleanup();vi.clearAllMocks();state.writable=true})
async function mount() {
 state.encrypt.mockResolvedValue({encrypted:'sealed-new'})
 state.store.mockResolvedValue({success:true})
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 cleanup=()=>{act(()=>root.unmount());host.remove()}
 await act(async()=>root.render(<SecretSelectionSection workflowPath="Crew/example" inventory={<div aria-label="Secret list"/>} />));return host
}
async function click(host:HTMLElement,label:string){await act(async()=>[...host.querySelectorAll<HTMLButtonElement>('button')].find(button=>button.textContent===label)!.click())}
async function type(input:HTMLInputElement,value:string){await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}))})}

// PLAT-856: the owner's Add form sends only ciphertext; the server saves it in their Vault and selects it.
it('adds a secret with masked input and ciphertext-only storage, never showing the value',async()=>{
 const host=await mount()
 await click(host,'Add a secret')
 await type(host.querySelector<HTMLInputElement>('[aria-label="Secret name"]')!,'NEW_KEY')
 await type(host.querySelector<HTMLInputElement>('input[type="password"]')!,'example-value')
 await click(host,'Save secret')
 expect(state.store).toHaveBeenCalledWith('Crew/example','NEW_KEY','sealed-new')
 expect(host.querySelector('form')).toBeNull()
 expect(host.textContent).not.toContain('example-value')
})

it('offers no Add form to a person who cannot build the project',async()=>{
 state.writable=false
 const host=await mount()
 expect([...host.querySelectorAll('button')].some(button=>button.textContent==='Add a secret')).toBe(false)
})

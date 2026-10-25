// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ writable: true, rows: [] as {name: string; encrypted_value: string}[], encrypt: vi.fn(), store: vi.fn(), remove: vi.fn(), reveal: vi.fn() }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => state.writable }))
vi.mock('./SecretSourceView', () => ({ default: () => null }))
vi.mock('../../api/secrets', () => ({ secretsApi: {
 listWorkflowSecrets: async () => state.rows, getGlobalSecrets: async () => [{name:'TEAM_KEY',managed:true}],
 encrypt: state.encrypt, storeWorkflowSecret: state.store, deleteWorkflowSecret: state.remove,
 decrypt: state.reveal, revealGlobalSecret: state.reveal,
} }))
import { SecretSelectionSection } from './SecretSelectionSection'
Object.assign(globalThis, {IS_REACT_ACT_ENVIRONMENT:true})
let cleanup=()=>{}
afterEach(()=>{cleanup();vi.clearAllMocks();state.writable=true})
async function mount(shared=false) {
 state.rows=[{name:'EXISTING',encrypted_value:'sealed-existing'}]
 state.encrypt.mockResolvedValue({encrypted:'sealed-new'})
 state.store.mockImplementation(async (_path:string,name:string)=>{if(!state.rows.some(row=>row.name===name))state.rows=[...state.rows,{name,encrypted_value:'sealed-new'}]})
 state.remove.mockImplementation(async (_path:string,name:string)=>{state.rows=state.rows.filter(row=>row.name!==name)})
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 cleanup=()=>{act(()=>root.unmount());host.remove()}
 function Harness(){const [selected,setSelected]=useState(['EXISTING']);return <SecretSelectionSection workflowPath="Crew/example" projectSource={shared?'vault':'project'} selectedSecrets={selected} onSecretChange={setSelected} onGlobalSecretChange={()=>{}} />}
 await act(async()=>root.render(<Harness/>));return host
}
async function click(host:HTMLElement,label:string){await act(async()=>[...host.querySelectorAll<HTMLButtonElement>('button')].find(button=>button.textContent===label)!.click())}
async function type(input:HTMLInputElement,value:string){await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}))})}

it('adds, replaces and removes a project secret with masked input and ciphertext-only storage',async()=>{
 const host=await mount()
 await click(host,'Add a secret')
 await type(host.querySelector<HTMLInputElement>('[aria-label="Secret name"]')!,'NEW_KEY')
 await type(host.querySelector<HTMLInputElement>('input[type="password"]')!,'example-value')
 await click(host,'Save secret')
 expect(state.store).toHaveBeenCalledWith('Crew/example','NEW_KEY','sealed-new')
 expect(host.querySelector('form')).toBeNull()
 expect(host.textContent).not.toContain('example-value')
 const row=host.querySelector('[title="NEW_KEY"]')!.closest('div.space-y-2')!
 await act(async()=>[...row.querySelectorAll<HTMLButtonElement>('button')].find(button=>button.textContent==='Replace')!.click())
 expect(host.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe('')
 await type(host.querySelector<HTMLInputElement>('input[type="password"]')!,'replacement-value')
 await click(host,'Save secret')
 expect(state.reveal).not.toHaveBeenCalled()
 const updated=host.querySelector('[title="NEW_KEY"]')!.closest('div.space-y-2')!
 await act(async()=>[...updated.querySelectorAll<HTMLButtonElement>('button')].find(button=>button.textContent==='Remove secret')!.click())
 expect(state.remove).toHaveBeenCalledWith('Crew/example','NEW_KEY')
 expect(host.textContent).not.toContain('NEW_KEY')
 expect(host.textContent).toContain('EXISTING')
})

it('keeps shared values and mutations unavailable from a project selection',async()=>{
 const host=await mount(true)
 expect(host.textContent).toContain('TEAM_KEY')
 expect(host.textContent).not.toContain('Replace')
 expect(host.textContent).not.toContain('Remove secret')
 expect(host.querySelector('form')).toBeNull()
 expect(host.querySelector('[aria-label="Reveal TEAM_KEY"]')).toBeNull()
})

it('withholds direct mutation for a person who cannot build the project',async()=>{
 state.writable=false
 const host=await mount()
 expect(host.textContent).toContain('EXISTING')
 expect(host.querySelector('button[aria-label^="Reveal"]')).toBeNull()
 expect([...host.querySelectorAll('button')].some(button=>button.textContent==='Add a secret'||button.textContent==='Replace'||button.textContent==='Remove secret')).toBe(false)
})

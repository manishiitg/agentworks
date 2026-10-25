// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
vi.mock('./ProjectMcpPanel',()=>({ProjectMcpPanel:()=> <div data-editor="mcps"/>}))
vi.mock('./ProjectSecretsPanel',()=>({ProjectSecretsPanel:()=> <div data-editor="secrets"/>}))
vi.mock('../workflow/WorkflowEmailPanel',()=>({default:()=> <div data-editor="google"/>}))
vi.mock('./PersonalAccountsPanel',()=>({default:()=> <div data-editor="slack"/>}))
vi.mock('./ProjectAccountSharingPanel',()=>({ProjectAccountSharingPanel:()=> <div data-sharing/>,CompanySignInSetup:()=> <div data-provider-setup/>}))
vi.mock('./ProjectVaultPanel',()=>({ProjectVaultPanel:()=> <div data-restricted/>}))
import {ProjectIntegrationPanel,type IntegrationSection} from './ProjectIntegrationPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
let clean=()=>{}
afterEach(()=>clean())
it('opens only the chosen native integration and withholds editors from non-Builders',async()=>{const host=document.createElement('div');document.body.append(host);const root=createRoot(host);clean=()=>{act(()=>root.unmount());host.remove()};const props={workspacePath:'Crew/example',placeNoun:'Crew',onAsk:vi.fn(),selectedSecrets:[],onSelectedSecretsChange:vi.fn(),laptop:<div data-editor="laptop"/>};for(const [section,editor] of [['connected','mcps'],['available','mcps'],['secrets','secrets'],['gmail','google'],['slack-account','slack'],['laptop','laptop']] as [IntegrationSection,string][]){await act(async()=>root.render(<ProjectIntegrationPanel {...props} section={section} canEdit/>));expect([...host.querySelectorAll('[data-editor]')].map(node=>node.getAttribute('data-editor'))).toEqual([editor]);expect(host.textContent).not.toContain('My Vault')}await act(async()=>root.render(<ProjectIntegrationPanel {...props} section="gmail" canEdit={false}/>));expect(host.querySelector('[data-editor],[data-sharing],[data-provider-setup]')).toBeNull();expect(host.querySelector('[data-restricted]')).not.toBeNull()})

import { createRoot } from 'react-dom/client'
import { useState } from 'react'
import PersonalAccountsPanel from '../../src/components/integrations/PersonalAccountsPanel'
import { McpAppsSection } from '../../src/products/work/McpAppsSection'
import { AutomationHubPanel } from '../../src/components/automation/AutomationHubPanel'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { useCapabilitiesStore } from '../../src/stores/useCapabilitiesStore'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import '../../src/index.css'

document.documentElement.classList.toggle('dark',new URLSearchParams(location.search).get('theme')==='dark')
useAuthStore.setState({user:{id:'owner',is_admin:true} as never})
useCapabilitiesStore.setState({capabilities:{local_mode:false} as never})
function Fixture(){
 const [view,setView]=useState('vault')
 return <TooltipProvider><main className="mx-auto max-w-4xl p-4 text-foreground">
 <nav className="mb-4 flex gap-4"><button onClick={()=>setView('vault')}>My Vault</button><button onClick={()=>setView('automation')}>Open product</button></nav>
 {view==='vault'?<><h1 className="mb-3 text-lg font-medium">My Vault</h1><McpAppsSection/><PersonalAccountsPanel/></>:<div className="h-[750px]"><AutomationHubPanel entityType="workflow" workspacePath="Workflow/mail" initialSection="email"/></div>}
 </main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture/>)

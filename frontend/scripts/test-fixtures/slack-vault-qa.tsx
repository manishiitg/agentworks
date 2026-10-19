import { createRoot } from 'react-dom/client'
import { useState } from 'react'
import { ProductSlackPanel } from '../../src/components/integrations/ProductSlackPanel'
import { VaultSlackBots } from '../../src/components/integrations/VaultSlackBots'
import { WorkspaceChannelRows, WorkspaceConnectionRows } from '../../src/components/workspace/WorkspaceProjectNavigation'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import '../../src/index.css'

document.documentElement.classList.toggle('dark',new URLSearchParams(location.search).get('theme')==='dark')
useAuthStore.setState({user:{id:'owner',is_admin:true} as never})
function Fixture(){
 const [view,setView]=useState('Crew')
 return <TooltipProvider><main className="mx-auto max-w-4xl p-4 text-foreground">
 <nav aria-label="Test destinations" className="mb-4 flex flex-wrap gap-4">{['Crew','Goal','Flow','My Vault'].map(item=><button key={item} onClick={()=>setView(item)}>{item}</button>)}</nav>
 <h1 className="mb-3 text-lg font-medium">{view}</h1>
 {view==='My Vault'?<VaultSlackBots/>:<><nav aria-label="Connections"><WorkspaceConnectionRows onOpen={()=>{}} relayMode={view==='Flow'}/></nav><nav aria-label="Use and share"><WorkspaceChannelRows onOpen={()=>{}} slack whatsapp={false}/></nav><div className="mt-4"><ProductSlackPanel key={view} workspacePath={`Workflow/${view.toLowerCase()}`} noun={view} allowBots={view!=='Flow'}/></div></>}
 </main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture/>)

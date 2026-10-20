import { createRoot } from 'react-dom/client'
import { AgentInboxPanel } from '../../src/components/integrations/AgentInboxPanel'
import { CompanyAgentEmail } from '../../src/components/integrations/CompanyAgentEmail'
import { useCapabilitiesStore } from '../../src/stores/useCapabilitiesStore'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import '../../src/index.css'
const query=new URLSearchParams(location.search)
document.documentElement.classList.toggle('dark',query.get('theme')==='dark')
useCapabilitiesStore.setState({capabilities:{local_mode:query.get('local')==='true',agent_inboxes:query.get('local')!=='true'} as never})
createRoot(document.getElementById('root')!).render(<TooltipProvider><main className="mx-auto max-w-3xl space-y-6 p-4 text-foreground"><AgentInboxPanel workspacePath="Workflow/example" onAsk={()=>{}}/><CompanyAgentEmail/></main></TooltipProvider>)

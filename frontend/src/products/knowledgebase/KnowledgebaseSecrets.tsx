import { useEffect,useState } from 'react'
import { KeyRound,MessageCircle } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import { useVaultSetupDraft } from '../../components/integrations/useVaultSetupDraft'
export function KnowledgebaseSecrets(){
 const [names,setNames]=useState<string[]|null>(null);const [error,setError]=useState('');const setup=useVaultSetupDraft()
 useEffect(()=>{const controller=new AbortController();void api.get<{secrets:string[]}>('/api/knowledgebase/secrets',{signal:controller.signal}).then(result=>setNames(result.data.secrets??[])).catch(()=>{if(!controller.signal.aborted)setError('Could not load Brain’s Vault secrets.')});return()=>controller.abort()},[])
 return <WorkspacePageContent className="space-y-4 text-sm" data-testid="knowledgebase-secrets"><div><h3 className="font-semibold">Brain secrets</h3><p className="mt-1 text-xs text-muted-foreground">Stored encrypted in Vault for Brain. Add, rotate or remove them through chat. Only people who manage the whole Brain can make changes.</p></div>
 {(error||setup.error)&&<p role="alert" className="text-xs text-destructive">{error||setup.error}</p>}
 {names===null?<p>Loading…</p>:<ul className="divide-y divide-border rounded-lg border border-border">{names.map(name=><li key={name} className="flex items-center gap-3 p-3"><KeyRound className="h-4 w-4 text-muted-foreground"/><code className="min-w-0 flex-1 truncate">{name}</code></li>)}</ul>}
 <Button variant="outline" size="sm" onClick={()=>void setup.open('Manage Brain’s secrets in Vault through manage_vault_secret_access with scope="brain". List current names, then help me add, rotate or remove a secret. Store supplied values encrypted, never repeat them, and keep Brain ownership and other permissions.',true)}><MessageCircle className="mr-2 h-4 w-4"/>Manage in Vault chat</Button>
 </WorkspacePageContent>
}

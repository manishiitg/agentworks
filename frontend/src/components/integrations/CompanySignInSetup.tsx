import { useAuthStore } from '../../stores/useAuthStore'
import { Button } from '../ui/Button'
import { useVaultSetupDraft } from './useVaultSetupDraft'

/** Existing company OAuth setup stays administrator-only beside its provider. */
export function CompanySignInSetup({provider,tabId}:{provider:'Google'|'Slack';tabId?:string}) {
 const admin=useAuthStore(state=>state.user?.is_admin===true)
 const setup=useVaultSetupDraft(tabId)
 if(!admin)return null
 return <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Company sign-in setup</summary><p className="mt-2 text-xs text-muted-foreground">Set up the company app people use to connect {provider}.</p>{setup.error&&<p role="alert" className="text-xs text-destructive">{setup.error}</p>}<Button size="sm" variant="outline" className="mt-3" onClick={()=>void setup.open(`Help me configure the company ${provider} OAuth sign-in app. Inspect the existing setup, ask for required details and store secrets securely. Never repeat secret values. Preserve existing accounts and grants.`)}>Configure in chat</Button></details>
}

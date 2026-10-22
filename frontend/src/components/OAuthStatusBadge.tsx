import type React from 'react'
import { MessageCircle } from 'lucide-react'
import { Button } from './ui/Button'
import { useVaultSetupDraft } from './integrations/useVaultSetupDraft'

interface OAuthStatusBadgeProps {
  serverName: string;
  /** Vault connection identity; absent for existing private/legacy connections. */
  connectionId?: string;
  /** Conversation that should receive the OAuth outcome. */
  chatSessionId?: string;
  scope?: 'private' | 'vault';
  requiresOAuth?: boolean; // Read from server config (presence of an oauth block)
  /**
   * Connection ownership from /api/tools — 'connected' | 'available'. When
   * supplied, the control is driven by this instead of by polling
   * /api/oauth/status, and clicks route to the connect/disconnect endpoints.
   * When omitted, the component behaves exactly as before.
   */
  connection?: string;
  onAuthChange?: (valid: boolean) => void;
  /**
   * `label` renders the wide "Connect"/"Disconnect" buttons used by the server
   * dropdown and details modal. `icon` renders the compact square control the
   * connector directory cards use, where the name and description already say
   * which service the button belongs to.
   */
  variant?: 'label' | 'icon';
  /** The current user can't change workflow state: connect/disconnect and
   * the credential dialogs' submit buttons disable. */
  readOnly?: boolean;
  /** Reuse a platform sign-in before starting a new authorization flow. */
  reuseAuthentication?: boolean;
  connectLabel?: string;
  confirmationMessage?: string;
}

/** Every MCP surface starts the same chat-owned Vault setup. */
export const OAuthStatusBadge: React.FC<OAuthStatusBadgeProps> = ({serverName,connectionId,connection,readOnly=false,variant='label'})=>{
 const setup=useVaultSetupDraft()
 const account=connectionId?`vault_${connectionId}`:serverName.split('__scope_')[0]
 const connected=connection==='connected'
 return <span className="inline-flex items-center gap-2">
  <Button size="sm" variant="ghost" disabled={readOnly} aria-label={`${connected?'Manage':'Connect'} ${serverName} in chat`} onClick={()=>void setup.open(`${connected?'Manage':'Connect'} MCP account ${account} through Vault in this chat. Inspect existing accounts, guide API-key or OAuth app setup here, return the real provider sign-in URL and verify discovery. Reuse the exact saved account and preserve existing permissions.`)}><MessageCircle className="h-3.5 w-3.5"/>{variant==='label'&&<span className="ml-1.5">{connected?'Manage in chat':'Connect in chat'}</span>}</Button>
  {setup.error&&<span role="alert" className="text-xs text-destructive">{setup.error}</span>}
 </span>
}
export default OAuthStatusBadge

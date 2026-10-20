import { buildAskAIMessage } from '../../../utils/askAIMessage'
export function getGoogleAppsSetupInstructions(): string {
 return 'Inspect manage_google_account(action="list_saved"), list_gmail_connections and actual service grants. Connect/reconnect and permissions live in My Vault. Reuse saved accounts with Use here; preserve existing permissions and do not create duplicate logins. Personal Gmail is used through gog. Connecting does not create an inbox, watch mail or start automation. Company agent inboxes are separate and administered in My Vault. Do not configure personal incoming-email addresses, bindings, sender approvals or Gmail rules. Only recommend reconnection if the required grant is missing.'
}
export function getGoogleAppsAskAIMessage(noun: string): string {
 return buildAskAIMessage({view:'Google apps',summary:`Help me connect Google apps and choose the access this ${noun} needs.`,instructions:getGoogleAppsSetupInstructions()})
}

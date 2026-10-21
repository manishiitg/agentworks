import { buildAskAIMessage } from '../../../utils/askAIMessage'
export function getGoogleAppsSetupInstructions(): string {
 return 'Inspect manage_google_account(action="list_saved"), list_gmail_connections and actual service grants. Connect/reconnect and permissions live in My Vault. Reuse saved accounts with Use here; preserve existing permissions and do not create duplicate logins. Personal Gmail is used through gog. Connecting alone does not start automation. On servers, users choose one personal Gmail account with Use for Crew inboxes (manage_google_account set_inbox_account). When the company turns on Crew inboxes, all their Crews get addresses automatically; only Crew-addressed mail is received. Company receiving setup is unnecessary. Do not configure personal incoming-email addresses, bindings, sender approvals or Gmail rules. Only recommend reconnection if the required grant is missing.'
}
export function getGoogleAppsAskAIMessage(noun: string): string {
 return buildAskAIMessage({view:'Google apps',summary:`Help me connect Google apps and choose the access this ${noun} needs.`,instructions:getGoogleAppsSetupInstructions()})
}

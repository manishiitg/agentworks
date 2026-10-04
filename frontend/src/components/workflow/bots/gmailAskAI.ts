import { buildAskAIMessage } from '../../../utils/askAIMessage'

// Shared by the panel header, account card and Incoming email action.
export const GMAIL_INBOUND_SETUP_INSTRUCTIONS = [
  'For automatic incoming Gmail, inspect get_gmail_trigger first. Explain mailbox sign-in separately from receiving infrastructure. If configured=false, inspect setup.provisioning and use setup_gmail_inbound(action="status") when permitted. Reuse an unexpired pending review_url; do not prepare another plan while consent is pending or provisioning is running.',
  'When setup is needed and setup.provisioning.can_prepare is true, use setup_gmail_inbound(action="prepare") with a registered client_name and its owning Google Cloud project_id. Prefer registered project metadata; ask only for missing project information, never guess. Show the resource plan and review_url. I must open the link, review the changes and complete Google consent myself; never follow it through agent tools. The server handles APIs, Pub/Sub resources, narrow IAM grants and private configuration without environment edits or a restart. Check action="status" afterward and report errors accurately.',
  'An app administrator with Google Cloud project setup permissions must complete this one-time setup. Local development requires the existing public HTTPS tunnel configured for this deployment. Use the optional manual checklist only if automatic setup is unavailable or I request it. Never request secrets in chat, edit server credential files, create scheduled email checks or launch gog watchers as a substitute. An empty setup.oauth_clients list can mean a missing topic mapping even when Google sign-in works; inspect setup.provisioning.oauth_clients for registered apps.',
  'After infrastructure is ready, use manage_gmail_trigger to connect a mailbox with observed read consent and configure the requested chat instructions or workflow routes and filters. Preserve existing rules unless I request changes. Additional senders require the owner confirmation in the Incoming email pane; chat or tools cannot approve them. Return the receiving address and mailbox readiness, and distinguish those from a successful real email delivery test. Incoming configuration remains Builder-managed and read-only in the panel.',
].join(' ')

export function getGoogleAppsSetupInstructions(incoming = true): string {
  return [
    'Inspect list_gmail_connections and current service grants. Ask which Google apps or email access I need before making changes. Use the company Google app when configured, or guide me to choose/upload a named OAuth JSON locally. Compare Allowed in AgentWorks with actual Google permissions; do not recommend reconnecting when the needed grant already exists. Keep access scoped to this target and preserve existing grants. Never request secrets in chat. Configure default recipients and test outgoing delivery only when I request sending.',
    incoming ? GMAIL_INBOUND_SETUP_INSTRUCTIONS : 'This Relay connects Google apps but has no incoming Gmail trigger; do not offer incoming-email infrastructure or routing setup here.',
  ].join(' ')
}

export function getGoogleAppsAskAIMessage(noun: string, incoming = true): string {
  return buildAskAIMessage({
    view: 'Integrations · Gmail',
    summary: incoming
      ? `Help me connect Google apps and set up sending or automatic incoming Gmail for this ${noun}.`
      : `Help me connect Google apps and choose the access this ${noun} needs.`,
    instructions: getGoogleAppsSetupInstructions(incoming),
  })
}

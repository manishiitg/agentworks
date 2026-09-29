import { getRuntimeAppName, runtimeBrandingConfig } from '../../runtime-branding'

/** The message an admin can send a person they added by email. */
export function invitationText(appName: string, email: string, url: string): string {
  return `You have been added to ${appName}. Open ${url} and sign in with Google using ${email}.`
}

export function deploymentName(): string {
  return getRuntimeAppName(runtimeBrandingConfig()) ?? 'AgentWorks'
}

export type InviteEmailStatus = 'sent' | 'not_configured' | 'exists' | 'failed'

/** What to tell the admin after adding someone (or resending). */
export function inviteNotice(status: InviteEmailStatus | undefined, email: string, detail?: string): { tone: 'ok' | 'copy' | 'error'; text: string } | null {
  switch (status) {
    case 'sent': return { tone: 'ok', text: `Invitation emailed to ${email}.` }
    case 'not_configured': return { tone: 'copy', text: `${email} was added. Email is not set up on this server, so copy the invitation and send it yourself.` }
    case 'exists': return { tone: 'copy', text: `${email} was added, but that address already has a sign-in with the auth service, so no email went out. Copy the invitation and send it yourself.` }
    case 'failed': return { tone: 'error', text: `${email} was added, but the invitation email was not sent${detail ? `: ${detail}` : ''}. You can copy the invitation instead.` }
    default: return null
  }
}

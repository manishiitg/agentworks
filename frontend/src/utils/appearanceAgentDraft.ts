import { useChatStore } from '../stores/useChatStore'
import { fillOwnAgentDraft } from './agentPurposeDraft'
import { openProductWorkspace } from './productWorkspaceNavigation'

export const APPEARANCE_PROMPT = `Change how my AgentWorks app looks. First read my personal appearance and its offered guide using settings action=appearance, and read the appearance skill. Use the appearance tool, not application source files. Help me choose a look, save it as a named personal theme and apply it. Keep text readable, code monospace, and project files and authored Artifacts unchanged. Respect reduced motion and pause custom motion when hidden. Check the real app before saying the look works; if you cannot see it, ask me to look.

The style I want: `

/** Only drafts the request in an existing own agent. Sending is still the person's choice. */
export function askAppearanceAgent(): void {
  if (fillOwnAgentDraft(APPEARANCE_PROMPT)) openProductWorkspace('work')
  else useChatStore.getState().addToast('Open one of your agent chats, then choose Ask an agent again. Nothing was sent.', 'info')
}

import { AppWindow, Blocks, Brain, FolderOpen, Grid2X2, MessageCircle, Slack, type LucideIcon } from 'lucide-react'

export interface IntegrationSectionOption {
  value: 'apps' | 'brain' | 'folders' | 'slack' | 'whatsapp' | 'gmail' | 'cli'
  label: string
  description: string
  icon: LucideIcon
}

/** Shared names and descriptions for Crew, Code, workflows and Relay. */
export const PROJECT_INTEGRATION_SECTIONS: IntegrationSectionOption[] = [
  { value: 'apps', label: 'Tools & secrets', description: 'MCPs, secrets, skills and Vault.', icon: Blocks },
  { value: 'brain', label: 'Brain', description: 'Shared knowledge for this project: off, read, or read & write.', icon: Brain },
  { value: 'folders', label: 'Folders & workflows', description: 'Folders on this server and other workflows this one can use.', icon: FolderOpen },
  { value: 'slack', label: 'Slack', description: 'Messages and notifications in Slack.', icon: Slack },
  { value: 'whatsapp', label: 'WhatsApp', description: 'Messages and notifications in WhatsApp.', icon: MessageCircle },
  { value: 'gmail', label: 'Google apps', description: 'Gmail, Drive, Calendar and other Google apps.', icon: Grid2X2 },
  { value: 'cli', label: 'Use in AI apps', description: 'Connect Claude, ChatGPT or a local AI agent.', icon: AppWindow },
]

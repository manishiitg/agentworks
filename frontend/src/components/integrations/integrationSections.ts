import { AppWindow, Blocks, Brain, FolderOpen, Grid2X2, MessageCircle, Slack, type LucideIcon } from 'lucide-react'
import { visibleProductSurfaceIDs, type ProductSurface } from '../../products/productSurfaceConfig'

export interface IntegrationSectionOption {
  value: 'apps' | 'brain' | 'folders' | 'slack' | 'whatsapp' | 'gmail' | 'cli'
  label: string
  description: string
  icon: LucideIcon
  requiredProduct?: ProductSurface
}

/** Shared names and descriptions for Crew, Code, workflows and Relay. */
export const PROJECT_INTEGRATION_SECTIONS: IntegrationSectionOption[] = [
  { value: 'apps', label: 'Tools & secrets', description: 'MCPs, secrets, skills and Vault.', icon: Blocks },
  { value: 'brain', label: 'Brain', description: 'Shared knowledge for your agents: open and edit by default, open only, or off.', icon: Brain, requiredProduct: 'knowledgebase' },
  { value: 'folders', label: 'Folders & workflows', description: 'Folders on this server and other workflows this one can use.', icon: FolderOpen },
  { value: 'slack', label: 'Slack', description: 'Messages and notifications in Slack.', icon: Slack },
  { value: 'whatsapp', label: 'WhatsApp', description: 'Messages and notifications in WhatsApp.', icon: MessageCircle },
  { value: 'gmail', label: 'Google apps', description: 'Gmail, Drive, Calendar and other Google apps.', icon: Grid2X2 },
  { value: 'cli', label: 'Use in AI apps', description: 'Connect Claude, ChatGPT or a local AI agent.', icon: AppWindow },
]

/** Product-backed integrations use the same installation and account bounds as the product menu. */
export function integrationProductAvailable(requiredProduct: ProductSurface | undefined, allowedProducts?: string[] | null): boolean {
  return !requiredProduct || visibleProductSurfaceIDs(allowedProducts).includes(requiredProduct)
}

export function projectIntegrationSections(allowedProducts?: string[] | null, relayMode = false): IntegrationSectionOption[] {
  const vaultAvailable = integrationProductAvailable('mcp-gateway', allowedProducts)
  return PROJECT_INTEGRATION_SECTIONS.filter(option => integrationProductAvailable(option.requiredProduct, allowedProducts) && (!relayMode || option.value === 'apps' || option.value === 'gmail' || option.value === 'slack'))
    .map(option => option.value === 'apps' && !vaultAvailable ? { ...option, description: 'MCPs, project secrets and skills.' } : option)
}

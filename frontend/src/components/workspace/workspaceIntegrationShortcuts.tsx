import { KeyRound, Puzzle, Sparkles } from 'lucide-react'
import { integrationProductAvailable, projectIntegrationSections } from '../integrations/integrationSections'
import { projectPluginTabs } from '../integrations/ProjectPluginsPanel'
import { isWorkIntegrationTabEnabled } from '../../products/work/workViewGating'
import type { LucideIcon } from 'lucide-react'

/** Mirrors the sections admitted by the existing MCPs & secrets panel. Links only;
 * the destination retains its read/write, account and connection permissions. */
export function workspaceIntegrationShortcuts({ allowedProducts, relayMode = false, workMode = false, enabledPanels, includeCli = false, onOpen }: {
  allowedProducts?: string[] | null
  relayMode?: boolean
  workMode?: boolean
  enabledPanels?: Set<string>
  includeCli?: boolean
  onOpen: (section: string) => void
}): Array<{ id: string; label: string; icon: LucideIcon; subgroup?: string; onClick: () => void }> {
  const sections = projectIntegrationSections(allowedProducts, relayMode).filter(section =>
    !workMode || isWorkIntegrationTabEnabled(section.value, enabledPanels))
  const tools = sections.some(section => section.value === 'apps')
  const plugins = tools ? projectPluginTabs(allowedProducts).filter(section =>
    !workMode || !enabledPanels || (section.value === 'secrets' ? enabledPanels.has('secrets') || integrationProductAvailable('mcp-gateway', allowedProducts)
      : section.value === 'skills' ? enabledPanels.has('skills') : enabledPanels.has('mcp'))) : []
  const pluginIcons = { connected: Puzzle, available: Puzzle, secrets: KeyRound, skills: Sparkles }
  return [
    ...plugins.map(section => ({ id: `mcp:${section.value}`, label: section.value === 'connected' ? 'MCPs' : section.label, icon: pluginIcons[section.value], subgroup: section.value === 'connected' || section.value === 'secrets' ? 'Vault' : undefined })),
    ...sections.filter(section => section.value !== 'apps' && (includeCli || section.value !== 'cli')).map(section => ({ id: `mcp:${section.value}`, label: section.label, icon: section.icon })),
  ].map(item => ({ ...item, onClick: () => onOpen(item.id.slice(4)) }))
}

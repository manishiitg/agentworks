import { KeyRound, Puzzle, Sparkles, type LucideIcon } from 'lucide-react'
import type { IntegrationSectionOption } from './integrationSections'
import type { PluginTab } from './ProjectPluginsPanel'

type ConnectionOption = { value: string; label: string; icon: LucideIcon }
const pluginIcons = { connected: Puzzle, available: Puzzle, secrets: KeyRound, skills: Sparkles }

/** Flatten the admitted inventory; existing section IDs remain valid targets. */
export function integrationNavigationOptions(sections: readonly IntegrationSectionOption[], plugins: readonly { value: PluginTab; label: string }[]): ConnectionOption[] {
  return [
    ...(sections.some(section => section.value === 'apps') ? plugins.filter(item => item.value !== 'available' && item.value !== 'skills')
      .map(item => ({ ...item, label: item.value === 'connected' ? 'MCPs' : item.label, icon: pluginIcons[item.value] })) : []),
    ...sections.filter(section => !['apps', 'slack', 'whatsapp', 'cli'].includes(section.value)),
  ]
}

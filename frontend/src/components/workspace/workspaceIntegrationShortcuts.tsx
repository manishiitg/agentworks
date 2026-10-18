import { KeyRound, Puzzle, ShieldCheck, Sparkles } from 'lucide-react'
import { projectIntegrationSections } from '../integrations/integrationSections'
import { projectPluginTabs } from '../integrations/ProjectPluginsPanel'
import { isWorkIntegrationTabEnabled } from '../../products/work/workViewGating'
import { WorkspaceContextRow, WorkspaceContextSection } from './WorkspaceContextCard'
import { WorkspaceContextShortcut, type WorkspaceShortcut } from './workspaceShortcuts'

const DEFAULT_CONNECTIONS = new Set(['mcp:connected', 'mcp:secrets', 'mcp:gmail', 'mcp:slack'])

/** Mirrors the sections admitted by the existing MCPs & secrets panel. Links only;
 * the destination retains its read/write, account and connection permissions. */
export function workspaceIntegrationShortcuts({ allowedProducts, relayMode = false, workMode = false, enabledPanels, includeCli = false, onOpen }: {
  allowedProducts?: string[] | null
  relayMode?: boolean
  workMode?: boolean
  enabledPanels?: Set<string>
  includeCli?: boolean
  onOpen: (section: string) => void
}): WorkspaceShortcut[] {
  const sections = projectIntegrationSections(allowedProducts, relayMode).filter(section =>
    !workMode || isWorkIntegrationTabEnabled(section.value, enabledPanels))
  const tools = sections.some(section => section.value === 'apps')
  const plugins = tools ? projectPluginTabs(allowedProducts).filter(section =>
    !workMode || !enabledPanels || (section.value === 'secrets' ? enabledPanels.has('secrets')
      : section.value === 'skills' ? enabledPanels.has('skills') : section.value === 'vault' || enabledPanels.has('mcp'))) : []
  const pluginIcons = { connected: Puzzle, available: Puzzle, secrets: KeyRound, skills: Sparkles, vault: ShieldCheck }
  return [
    ...plugins.map(section => ({ id: `mcp:${section.value}`, label: section.value === 'connected' ? 'MCPs' : section.label, icon: pluginIcons[section.value] })),
    ...sections.filter(section => section.value !== 'apps' && (includeCli || section.value !== 'cli')).map(section => ({ id: `mcp:${section.value}`, label: section.label, icon: section.icon })),
  ].map(item => ({ ...item, onClick: () => onOpen(item.id.slice(4)), content: <WorkspaceContextRow icon={item.icon} onClick={() => onOpen(item.id.slice(4))}>{item.label}</WorkspaceContextRow> }))
}

/** Return explicit shortcut elements so the card can collect their live content. */
export function integrationShortcutContent(shortcuts: readonly WorkspaceShortcut[]) {
  const visible = [...DEFAULT_CONNECTIONS].flatMap(id => shortcuts.find(item => item.id === id) ?? [])
  if (!visible.length) return null
  return <WorkspaceContextSection title="Connections">
    {visible.map(item => <WorkspaceContextShortcut key={item.id} id={item.id} label={item.label} icon={item.icon}>{item.content}</WorkspaceContextShortcut>)}
  </WorkspaceContextSection>
}

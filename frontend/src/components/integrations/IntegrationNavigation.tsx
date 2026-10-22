import { ArrowLeft, Plus, KeyRound, Puzzle, Sparkles, type LucideIcon } from 'lucide-react'
import * as Menu from '@radix-ui/react-dropdown-menu'
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

/** Setup choices appear only when requested; the landing view shows current connections. */
export function IntegrationNavigation({ options, value, onChange, overview, onOverview, canEdit = true }: {
  options: readonly ConnectionOption[]
  value: string
  onChange: (value: string) => void
  overview: boolean
  onOverview: () => void
  canEdit?: boolean
}) {
  const selected = options.find(option => option.value === (value === 'available' ? 'connected' : value))
  const vault = options.filter(option => ['connected', 'secrets'].includes(option.value))
  const accounts = options.filter(option => ['gmail', 'slack', 'whatsapp'].includes(option.value))
  const resources = options.filter(option => !vault.includes(option) && !accounts.includes(option))
  const shared = resources.filter(option => option.value === 'brain')
  const project = resources.filter(option => !shared.includes(option))
  const choose = (next: string) => {
    if (options.some(option => option.value === next)) onChange(next === 'connected' && canEdit ? 'available' : next)
  }
  // Each item in the side list is its own page; only the overview offers adding an app (owner, 2026-10-20).
  if (!overview) return null
  return <nav aria-label="Connection actions" className={`mt-2 flex min-w-0 flex-wrap items-center gap-2 ${overview ? 'justify-end' : 'justify-between'}`}>
    {!overview && <button type="button" onClick={onOverview} className="inline-flex min-w-0 items-center gap-1.5 rounded py-1 text-xs text-muted-foreground hover:text-foreground">
      <ArrowLeft className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />Connections<span aria-hidden="true">/</span><span className="truncate text-foreground">{value === 'available' ? 'Add MCP' : selected?.label}</span>
    </button>}
    {overview && !!options.length && <Menu.Root modal={false}>
      <Menu.Trigger asChild><button type="button" className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-border bg-background px-2.5 text-xs font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Plus className="h-3.5 w-3.5" aria-hidden="true" />{canEdit ? 'Add connection' : 'View connections'}
      </button></Menu.Trigger>
      <Menu.Portal><Menu.Content aria-label="Connection choices" align="end" sideOffset={6} collisionPadding={12} className="z-50 max-h-[min(70vh,420px)] w-60 max-w-[calc(100vw-24px)] overflow-y-auto rounded-xl border border-border bg-popover p-1 text-popover-foreground shadow-xl">
        {[{ label: 'Vault', items: vault }, { label: 'Shared resources', items: shared }, { label: 'Apps & tools', items: accounts }, { label: 'Project resources', items: project }].filter(group => group.items.length).map(group => <Menu.Group key={group.label}>
          <Menu.Label className="px-2.5 py-1.5 text-[10px] text-muted-foreground">{group.label}</Menu.Label>
          {group.items.map(option => <Menu.Item key={option.value} onSelect={() => choose(option.value)} className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-xs outline-none data-[highlighted]:bg-muted">
            <option.icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />{option.label}
          </Menu.Item>)}
        </Menu.Group>)}
      </Menu.Content></Menu.Portal>
    </Menu.Root>}
  </nav>
}

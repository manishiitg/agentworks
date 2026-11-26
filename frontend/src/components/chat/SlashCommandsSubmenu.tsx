import { useSyncExternalStore } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ChevronRight, Search, Wand2 } from 'lucide-react'
import { getComposerCommands, type ComposerCatalogOptions } from '../../commands/composerCatalog'
import { getCommandRevision, subscribeCommands } from '../../commands/registry'
import type { CommandDefinition } from '../../commands/types'

export type ComposerCommandPreview = {
  catalog: ComposerCatalogOptions
  skillsEntry?: CommandDefinition
  onSelectCommand: (name: string) => void
}

type Props = ComposerCommandPreview & {
  onOpenCommands: () => void
  deferAction: (action: () => void) => void
  rowClass: string
}

function PreviewItems({ catalog, skillsEntry, onSelectCommand, onOpenCommands, deferAction, rowClass }: Props) {
  useSyncExternalStore(subscribeCommands, getCommandRevision)
  // Personal commands are loaded for the current workspace by the full picker.
  // Hover never loads them or shows the previous workspace's cached commands.
  const preview = getComposerCommands(catalog)
    .filter(command => catalog.commands || command.source !== 'user')
    .slice(0, 5)
  if (skillsEntry) preview.push(skillsEntry)
  return <>
    {preview.map(command => <Menu.Item key={command.command} className={rowClass} onSelect={() => deferAction(() => onSelectCommand(command.command))}>
      <span className="flex shrink-0 text-muted-foreground">{command.icon}</span>
      <span className="min-w-0">
        <span className="block truncate">{command.label || `/${command.command}`}</span>
        <span className="block truncate text-xs text-muted-foreground" title={command.description}>{command.description}</span>
      </span>
    </Menu.Item>)}
    {preview.length > 0 && <Menu.Separator className="my-1.5 h-px bg-border" />}
    <Menu.Item className={rowClass} onSelect={() => deferAction(onOpenCommands)}>
      <Search className="h-4 w-4 shrink-0" aria-hidden="true" />Search all commands…
    </Menu.Item>
  </>
}

export function SlashCommandsSubmenu(props: Props) {
  return <Menu.Sub>
    <Menu.SubTrigger data-testid="chat-command-menu-button" className={`${props.rowClass} data-[state=open]:bg-muted`}>
      <Wand2 className="h-4 w-4 shrink-0" aria-hidden="true" />Slash commands
      <kbd className="ml-auto font-mono text-xs text-muted-foreground">/</kbd>
      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    </Menu.SubTrigger>
    <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} data-theme-surface="menu"
      className="z-[101] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(280px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      <PreviewItems {...props} />
    </Menu.SubContent></Menu.Portal>
  </Menu.Sub>
}

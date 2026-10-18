import { ChevronDown } from 'lucide-react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { groupWorkspacePanels } from './workspacePanelGroups'
import type { WorkspaceShortcut } from './workspaceShortcuts'

const SETTINGS = new Set(['identity', 'access', 'backup', 'publish', 'notify', 'playbooks'])
export const isProjectSettingsPanel = (id: string) => SETTINGS.has(id)

/** Secondary navigation uses only the workspace's existing admitted inventory. */
export function WorkspaceMoreTools({ items }: { items: readonly WorkspaceShortcut[] }) {
  if (!items.length) return null
  return <div className="border-t border-border pt-3"><Menu.Root modal={false}>
    <Menu.Trigger asChild><button type="button" className="flex w-full items-center justify-between rounded py-1 text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">More tools<ChevronDown className="h-3.5 w-3.5" aria-hidden="true" /></button></Menu.Trigger>
    <Menu.Portal><Menu.Content aria-label="All project tools" side="top" align="end" sideOffset={8} collisionPadding={12} className="z-50 max-h-[min(70vh,30rem)] w-60 max-w-[calc(100vw-24px)] overflow-y-auto rounded-xl border border-border bg-popover p-1 text-popover-foreground shadow-xl">
      {groupWorkspacePanels(items).map(group => <Menu.Group key={group.title}>
        <Menu.Label className="px-2.5 pb-1 pt-2 text-[10px] text-muted-foreground">{group.title}</Menu.Label>
        {group.items.map(item => <Menu.Item key={item.id} onSelect={() => item.onClick?.()} className="flex cursor-pointer items-center gap-2.5 rounded px-2.5 py-2 text-xs outline-none data-[highlighted]:bg-muted">
          <item.icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />{item.label}
        </Menu.Item>)}
      </Menu.Group>)}
    </Menu.Content></Menu.Portal>
  </Menu.Root></div>
}

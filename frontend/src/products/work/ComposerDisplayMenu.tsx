import * as Menu from '@radix-ui/react-dropdown-menu'
import { ChevronRight } from 'lucide-react'
import { ComposerSendKeys } from '../../components/chat/ChatSendModeButton'
import { ThinkingDisplayMenuItem, ToolsDisplayMenuItem, MicrophoneDisplayMenuItem } from '../../components/chat/ThinkingDisplayMenuItem'
import { menuLabelClass, menuRowClass } from './ChatContextItems'

const displayToggleClass = 'flex cursor-pointer items-center gap-1.5 whitespace-nowrap rounded px-2 py-1.5 text-xs text-muted-foreground outline-none data-[highlighted]:bg-muted data-[highlighted]:text-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50'

export function ComposerDisplayMenu({ busy }: { busy: boolean }) {
  return <Menu.Sub>
    <Menu.SubTrigger className={`${menuRowClass} data-[state=open]:bg-muted`}>
      <span className="min-w-0 flex-1">Display &amp; shortcuts</span>
      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    </Menu.SubTrigger>
    <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className="z-[101] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(260px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg" data-theme-surface="menu">
      <ThinkingDisplayMenuItem compact className={displayToggleClass} />
      <ToolsDisplayMenuItem compact className={displayToggleClass} />
      <MicrophoneDisplayMenuItem compact className={displayToggleClass} />
      <Menu.Label className={`${menuLabelClass} mt-1.5 border-t border-border`}>Keyboard shortcuts</Menu.Label>
      <ComposerSendKeys busy={busy} separated={false} />
    </Menu.SubContent></Menu.Portal>
  </Menu.Sub>
}

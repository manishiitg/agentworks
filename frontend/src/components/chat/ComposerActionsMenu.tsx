import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { LoadingIndicator } from '../ui/LoadingIndicator'
import { useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Paperclip, Plus, LogIn } from 'lucide-react'
import { SlashCommandsSubmenu, type ComposerCommandPreview } from './SlashCommandsSubmenu'
import { ChatSendOptions, ComposerSendKeys, type ChatSendOptionsProps } from './ChatSendModeButton'

/** Presentation only: every action keeps its existing handler and admission. */
export type ComposerActions = {
  uploading: boolean
  /** Absent where files can't be attached (an online agent's tab, #4121): no Attach files row or button. */
  onAttach?: () => void
  commands?: ComposerCommandPreview & { onOpenCommands: () => void }
  sendOptions?: ChatSendOptionsProps
  onOpenChange?: (open: boolean) => void
  // Plan first (#3711): only for programs with a plan mode.
}

export function ComposerActionsMenu({ uploading, onAttach, commands, sendOptions, onOpenChange }: ComposerActions) {
  const afterClose = useRef<(() => void) | null>(null)
  return <Menu.Root modal={false} onOpenChange={onOpenChange}>
    <Menu.Trigger data-theme-action="composer-menu" data-theme-control="button" type="button" aria-label="Add files and actions" title="Add files and actions"
      className="relative inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-muted">
      {uploading ? <LoadingIndicator className="h-4 w-4" aria-hidden="true" /> : <Plus className="h-4 w-4" aria-hidden="true" />}
    </Menu.Trigger>
    <Menu.Portal><Menu.Content data-theme-surface="menu" side="top" align="start" sideOffset={8} collisionPadding={12}
      onCloseAutoFocus={event => {
        const action = afterClose.current
        afterClose.current = null
        if (action) { event.preventDefault(); action() }
      }}
      className="z-[100] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(320px,calc(100vw-24px))] overflow-y-auto overscroll-contain scroll-py-1.5 rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      <ComposerActionItems uploading={uploading} onAttach={onAttach} commands={commands} sendOptions={sendOptions} deferAction={action => { afterClose.current = action }} />
      <ComposerSendKeys busy={Boolean(sendOptions?.busy)} />
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}

/** Reuse the exact actions inside the chat's model/context menu. */
export function ComposerActionItems({ uploading, onAttach, commands, sendOptions, deferAction, attachOnBar = false, showAccountLink = true }: ComposerActions & { deferAction: (action: () => void) => void; attachOnBar?: boolean; showAccountLink?: boolean }) {
  const rowClass = 'flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
  return <>
      {!attachOnBar && onAttach && <Menu.Item disabled={uploading} onSelect={onAttach} data-theme-action="attach" data-testid="chat-upload-button" className={rowClass}>
        <Paperclip className="h-4 w-4" aria-hidden="true" />{uploading ? 'Uploading files…' : 'Attach files'}
      </Menu.Item>}
      {commands && <SlashCommandsSubmenu {...commands} deferAction={deferAction} rowClass={rowClass} />}
      {showAccountLink && <Menu.Item onSelect={() => deferAction(() => openWorkspaceSettings('providers'))} className={rowClass}>
        <LogIn className="h-4 w-4" aria-hidden="true" />AI accounts / Sign in
      </Menu.Item>}
      {sendOptions && <>
        <Menu.Separator className="my-1.5 h-px bg-border" />
        <ChatSendOptions {...sendOptions} deferAction={deferAction} />
      </>}
  </>
}

/** One click attaches: the paperclip sits on the chat box's bottom row beside the model chip, not inside its menu. */
export function ComposerAttachButton({ uploading, onAttach }: Pick<ComposerActions, 'uploading' | 'onAttach'>) {
  if (!onAttach) return null
  const label = uploading ? 'Uploading files…' : 'Attach files'
  return <button type="button" disabled={uploading} onClick={onAttach} data-theme-action="attach" data-testid="chat-upload-button" aria-label={label} title={label}
    className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60">
    {uploading ? <LoadingIndicator className="h-3.5 w-3.5" aria-hidden="true" /> : <Paperclip className="h-3.5 w-3.5" aria-hidden="true" />}
  </button>
}

/** Account setup is a separate, admitted sign-in flow, not the chat Terminal view. */
export function ComposerAccountButton() {
 return <button type="button" title="Sign in or reconnect your coding program" onClick={()=>openWorkspaceSettings('providers')} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">AI accounts / Sign in</button>
}

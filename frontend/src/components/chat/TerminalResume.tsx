import { useEffect, useState } from 'react'
import { create } from 'zustand'
import { X } from 'lucide-react'
import api from '../../services/api'
import ModalPortal from '../ui/ModalPortal'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

// Continue in my terminal opens a copy of the chat in Claude Code; this chat keeps working here.
export const useTerminalCommand = create<{ tabId?: string; command?: string }>(() => ({}))

function ownChat(tabId: string) {
  const tab = useChatStore.getState().chatTabs[tabId]
  const m = tab?.metadata
  if (!m || m.agentProfileId !== 'work' || m.agentProfileTabCanWrite === false || m.isViewOnly || m.isScheduledRun || m.isBotRun) return undefined
  return m.agentProfileConversationKey
}

export async function continueInTerminal(tabId: string) {
  const key = ownChat(tabId); if (!key) return
  const identity = captureChatIdentity()
  try {
    const { data } = await api.get<{ command: string }>('/api/external/v1/chats/terminal', { params: { conversation_key: key, action: 'command' } })
    if (isChatIdentityCurrent(identity)) useTerminalCommand.setState({ tabId, command: data.command })
  } catch (error) {
    if (!isChatIdentityCurrent(identity)) return
    const text = (error as { response?: { data?: { error?: { message?: string } } } }).response?.data?.error?.message
    useChatStore.getState().addToast(text || 'This chat cannot continue in your terminal yet.', 'error')
  }
}

export function TerminalCommandPopup({ tabId }: { tabId: string | null }) {
  const command = useTerminalCommand(s => (tabId && s.tabId === tabId ? s.command : undefined))
  const [copied, setCopied] = useState(false)
  const close = () => { useTerminalCommand.setState({ tabId: undefined, command: undefined }); setCopied(false) }
  useEffect(() => {
    if (!command) return
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') close() }
    window.addEventListener('keydown', onKey); return () => window.removeEventListener('keydown', onKey)
  }, [command])
  if (!command) return null
  const copy = () => { void navigator.clipboard.writeText(command); setCopied(true) }
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-overlay/50 p-4" onClick={close}>
      <div data-theme-surface="dialog" role="dialog" aria-modal="true" aria-label="Continue in your terminal" onClick={event => event.stopPropagation()}
        style={{ width: '32rem', maxWidth: 'calc(100vw - 2rem)', maxHeight: 'calc(100vh - 2rem)' }}
        className="min-w-0 space-y-3 overflow-y-auto rounded-xl border border-border bg-surface-panel p-5 text-sm shadow-md">
        <div className="flex items-start gap-2">
          <h3 className="flex-1 text-[15px] font-semibold">Continue in your terminal</h3>
          <button type="button" aria-label="Close" onClick={close} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className="text-muted-foreground">Paste this in your terminal. It opens a copy of this chat in Claude Code with its full history. This chat keeps working here; each copy goes on separately.</p>
        <code className="block max-h-32 overflow-y-auto break-all rounded-md border border-border bg-muted px-3 py-2 font-mono text-xs">{command}</code>
        <p className="text-xs text-muted-foreground">Says “unknown command”? Your <code className="font-mono">agentworks</code> command is older than this app: update the AgentWorks Mac app.</p>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={close} className="rounded-md px-3 py-1.5 text-muted-foreground hover:bg-muted">Close</button>
          <button type="button" onClick={copy} className="rounded-md bg-primary px-3 py-1.5 font-medium text-primary-foreground hover:opacity-90">{copied ? 'Copied' : 'Copy command'}</button>
        </div>
      </div>
    </div>
  </ModalPortal>
}

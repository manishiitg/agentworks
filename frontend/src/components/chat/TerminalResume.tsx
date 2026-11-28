import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import { create } from 'zustand'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Status = { out: boolean; command: string; notice: string }
export const useTerminalResumeState = create<{ byTab: Record<string, Status>; commandTab?: string; refused?: Record<string, boolean> }>(() => ({ byTab: {} }))
const endpoint = '/api/external/v1/chats/terminal'
const stateKey = (tabId: string) => `${captureChatIdentity()}:${tabId}`
export function useChatTerminalOut(tabId: string | null) {
  const key = tabId ? stateKey(tabId) : ''; return useTerminalResumeState(s => !!s.byTab[key]?.out)
}

function ownChat(tabId: string) {
  const tab = useChatStore.getState().chatTabs[tabId]
  const m = tab?.metadata
  if (!m || m.agentProfileId !== 'work' || m.agentProfileTabCanWrite === false || m.isViewOnly || m.isScheduledRun || m.isBotRun) return undefined
  return m.agentProfileConversationKey
}

export async function continueInTerminal(tabId: string) {
  const key = ownChat(tabId); if (!key) return
  const identity = captureChatIdentity(), byKey = stateKey(tabId)
  try {
    const { data } = await api.get<Status>(endpoint, { params: { conversation_key: key, action: 'command' } })
    if (!isChatIdentityCurrent(identity)) return
    useTerminalResumeState.setState(s => ({ byTab: { ...s.byTab, [byKey]: data }, commandTab: byKey, refused: { ...s.refused, [byKey]: false } }))
  } catch (error) {
    if (!isChatIdentityCurrent(identity)) return
    const text = (error as { response?: { data?: { error?: { message?: string } } } }).response?.data?.error?.message
    useChatStore.getState().addToast(text || 'This chat cannot continue in your terminal yet.', 'error')
  }
}

export function TerminalResumeNotice({ tabId }: { tabId: string | null }) {
  const identityNow = captureChatIdentity(), byKey = tabId ? stateKey(tabId) : ''
  const status = useTerminalResumeState(s => s.byTab[byKey])
  const commandOpen = useTerminalResumeState(s => s.commandTab === byKey && !!tabId)
  const [working, setWorking] = useState(false)
  useEffect(() => {
    if (!tabId || useTerminalResumeState.getState().refused?.[byKey]) return
    const key = ownChat(tabId); if (!key) return
    const identity = captureChatIdentity(); let live = true
    let inFlight = false
    let timer: number | undefined
    const refresh = async () => {
      if (inFlight || (typeof document !== 'undefined' && document.hidden)) return
      inFlight = true
      try {
        const { data } = await api.get<Status>(endpoint, { params: { conversation_key: key, action: 'status' } })
        if (live && isChatIdentityCurrent(identity)) useTerminalResumeState.setState(s => ({ byTab: { ...s.byTab, [byKey]: data } }))
      } catch (error) {
        if (live && isChatIdentityCurrent(identity) && (error as { response?: { status?: number } }).response?.status === 403) {
          window.clearInterval(timer)
          useTerminalResumeState.setState(s => ({ refused: { ...s.refused, [byKey]: true } }))
          console.info('[CHAT_TERMINAL_RESUME] action=poll_stopped reason=forbidden')
        }
      } finally {
        inFlight = false
      }
    }
    const onVisibility = () => { if (!document.hidden) void refresh() }
    void refresh(); timer = window.setInterval(() => void refresh(), 5000)
    document.addEventListener('visibilitychange', onVisibility)
    return () => { live = false; window.clearInterval(timer); document.removeEventListener('visibilitychange', onVisibility); useTerminalResumeState.setState(s => { const byTab = { ...s.byTab }; delete byTab[byKey]; return { byTab, commandTab: s.commandTab === byKey ? undefined : s.commandTab } }) }
  }, [tabId, identityNow, byKey])
  if (!tabId || !status || (!status.out && !commandOpen)) return null
  const recover = async () => {
    const key = ownChat(tabId); if (!key) return
    setWorking(true); const identity = captureChatIdentity()
    try {
      await api.post(endpoint, null, { params: { conversation_key: key, action: 'here' } })
      if (isChatIdentityCurrent(identity)) useTerminalResumeState.setState(s => ({ byTab: { ...s.byTab, [byKey]: { ...status, out: false } }, commandTab: undefined }))
    } catch { if (isChatIdentityCurrent(identity)) useChatStore.getState().addToast('Could not return this chat here. Try again.', 'error') }
    finally { setWorking(false) }
  }
  if (!status.out) return <TerminalCommandPopup command={status.command} onClose={() => useTerminalResumeState.setState({ commandTab: undefined })} />
  return <div role="status" className="space-y-2 border-b border-border px-3 py-2 text-sm">
    <p>Continued in terminal</p><p className="text-xs text-muted-foreground">Attach it back to continue here, or use your saved copy here instead. The old terminal copy will no longer be the live chat.</p><button type="button" disabled={working} onClick={() => void recover()} className="text-primary hover:underline">Continue here instead</button>
  </div>
}

function TerminalCommandPopup({ command, onClose }: { command: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey); return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  const copy = () => { void navigator.clipboard.writeText(command); setCopied(true) }
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-overlay/50 p-4" onClick={onClose}>
      <div data-theme-surface="dialog" role="dialog" aria-modal="true" aria-label="Continue in your terminal" onClick={event => event.stopPropagation()}
        style={{ width: '32rem', maxWidth: 'calc(100vw - 2rem)', maxHeight: 'calc(100vh - 2rem)' }}
        className="min-w-0 space-y-3 overflow-y-auto rounded-xl border border-border bg-surface-panel p-5 text-sm shadow-md">
        <div className="flex items-start gap-2">
          <h3 className="flex-1 text-[15px] font-semibold">Continue in your terminal</h3>
          <button type="button" aria-label="Close" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className="text-muted-foreground">Paste this in your terminal. It opens this chat in Claude Code with its full history.</p>
        <code className="block max-h-32 overflow-y-auto break-all rounded-md border border-border bg-muted px-3 py-2 font-mono text-xs">{command}</code>
        <p className="text-xs text-muted-foreground">To bring it back, run <code className="font-mono">/agentworks attach</code> in that Claude chat. Says “unknown command”? Your <code className="font-mono">agentworks</code> command is older than this app: update the AgentWorks Mac app.</p>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded-md px-3 py-1.5 text-muted-foreground hover:bg-muted">Close</button>
          <button type="button" onClick={copy} className="rounded-md bg-primary px-3 py-1.5 font-medium text-primary-foreground hover:opacity-90">{copied ? 'Copied' : 'Copy command'}</button>
        </div>
      </div>
    </div>
  </ModalPortal>
}

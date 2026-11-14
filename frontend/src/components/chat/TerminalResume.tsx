import { useEffect, useState } from 'react'
import { create } from 'zustand'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Status = { out: boolean; command: string; notice: string }
export const useTerminalResumeState = create<{ byTab: Record<string, Status>; commandTab?: string }>(() => ({ byTab: {} }))
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
    useTerminalResumeState.setState(s => ({ byTab: { ...s.byTab, [byKey]: data }, commandTab: byKey }))
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
    if (!tabId) return
    const key = ownChat(tabId); if (!key) return
    const identity = captureChatIdentity(); let live = true
    const refresh = async () => {
      try {
        const { data } = await api.get<Status>(endpoint, { params: { conversation_key: key, action: 'status' } })
        if (live && isChatIdentityCurrent(identity)) useTerminalResumeState.setState(s => ({ byTab: { ...s.byTab, [byKey]: data } }))
      } catch { /* Not a supported/native Claude chat. Backend remains authoritative. */ }
    }
    void refresh(); const timer = window.setInterval(() => void refresh(), 5000)
    return () => { live = false; window.clearInterval(timer); useTerminalResumeState.setState(s => { const byTab = { ...s.byTab }; delete byTab[byKey]; return { byTab, commandTab: undefined } }) }
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
  return <div role="status" className="space-y-2 border-b border-border px-3 py-2 text-sm">
    {status.out ? <><p>Continued in terminal</p><p className="text-xs text-muted-foreground">Attach it back to continue here, or use your saved copy here instead. The old terminal copy will no longer be the live chat.</p><button type="button" disabled={working} onClick={() => void recover()} className="text-primary hover:underline">Continue here instead</button></> : <>
      <p>Run this command in your own terminal:</p><code className="block break-all text-xs">{status.command}</code>
      <button type="button" onClick={() => void navigator.clipboard.writeText(status.command)} className="mr-3 text-primary hover:underline">Copy command</button>
      <button type="button" onClick={() => useTerminalResumeState.setState({ commandTab: undefined })} className="text-muted-foreground hover:underline">Close</button>
    </>}
  </div>
}

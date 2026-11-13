import { create } from 'zustand'

const KEY = 'agentworks-show-thinking'
const initial = () => { try { return localStorage.getItem(KEY) === 'true' } catch { return false } }

/** Device display preference only; never changes model effort or provider output. */
export const useChatDisplayStore = create<{ showMicrophone: boolean; setShowMicrophone: (show: boolean) => void; showTools: boolean; setShowTools: (show: boolean) => void; showThinking: boolean; setShowThinking: (show: boolean) => void }>(set => ({
  showThinking: initial(),
  showMicrophone: (() => { try { return localStorage.getItem('agentworks-show-microphone') !== 'false' } catch { return true } })(),
  setShowMicrophone: showMicrophone => {
    try { localStorage.setItem('agentworks-show-microphone', String(showMicrophone)) } catch { /* Device display preference only. */ }
    set({ showMicrophone })
  },
  showTools: (() => { try { return localStorage.getItem('agentworks-show-tools') === 'true' } catch { return false } })(),
  setShowTools: showTools => {
    try { localStorage.setItem('agentworks-show-tools', String(showTools)) } catch { /* Device preference only. */ }
    set({ showTools })
  },
  setShowThinking: showThinking => {
    try { localStorage.setItem(KEY, String(showThinking)) } catch { /* The current chat can still change. */ }
    set({ showThinking })
  },
}))

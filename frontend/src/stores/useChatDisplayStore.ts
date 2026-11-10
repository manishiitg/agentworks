import { create } from 'zustand'

const KEY = 'agentworks-show-thinking'
const initial = () => { try { return localStorage.getItem(KEY) === 'true' } catch { return false } }

/** Device display preference only; never changes model effort or provider output. */
export const useChatDisplayStore = create<{ showThinking: boolean; setShowThinking: (show: boolean) => void }>(set => ({
  showThinking: initial(),
  setShowThinking: showThinking => {
    try { localStorage.setItem(KEY, String(showThinking)) } catch { /* The current chat can still change. */ }
    set({ showThinking })
  },
}))

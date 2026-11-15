import { create } from 'zustand'

export type AppearanceProfile = 'dark' | 'light'
export const CHAT_FONTS = [
  { id: 'system', label: 'System default', family: '-apple-system, BlinkMacSystemFont, system-ui, "Segoe UI", sans-serif' },
  { id: 'jetbrains-mono', label: 'JetBrains Mono', family: '"JetBrains Mono", monospace' },
  { id: 'fira-code', label: 'Fira Code', family: '"Fira Code", monospace' },
  { id: 'ibm-plex-mono', label: 'IBM Plex Mono', family: '"IBM Plex Mono", monospace' },
  { id: 'source-code-pro', label: 'Source Code Pro', family: '"Source Code Pro", monospace' },
] as const
export type ChatFont = typeof CHAT_FONTS[number]['id']
const FONT_KEY = 'agentworks-chat-font'
export function readChatFont(): ChatFont {
  try {
    const saved = localStorage.getItem(FONT_KEY)
    return CHAT_FONTS.find(font => font.id === saved)?.id ?? 'system'
  } catch { return 'system' }
}
const KEY = 'agentworks-appearance'
const readProfile = (): AppearanceProfile => {
  try { const saved = localStorage.getItem(KEY); return saved === 'light' ? 'light' : 'dark' } catch { return 'dark' }
}

/** Device appearance only: never changes a coding program, model or conversation. */
export const useAppearanceStore = create<{ profile: AppearanceProfile; setProfile: (profile: AppearanceProfile) => void; chatFont: ChatFont; setChatFont: (font: ChatFont) => void }>(set => ({
  profile: readProfile(),
  chatFont: readChatFont(),
  setChatFont: chatFont => {
    try { localStorage.setItem(FONT_KEY, chatFont) } catch { /* Preview still works without storage. */ }
    set({ chatFont })
  },
  setProfile: profile => {
    try { localStorage.setItem(KEY, profile) } catch { /* The current view can still change. */ }
    set({ profile })
  },
}))

export const APPLE_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'

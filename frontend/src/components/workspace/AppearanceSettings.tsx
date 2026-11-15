import { CHAT_FONTS, type ChatFont, useAppearanceStore } from '../../stores/useAppearanceStore'
import AppearanceControl from './AppearanceControl'

export default function AppearanceSettings() {
  const chatFont = useAppearanceStore(state => state.chatFont)
  const setChatFont = useAppearanceStore(state => state.setChatFont)
  const font = CHAT_FONTS.find(font => font.id === chatFont) ?? CHAT_FONTS[0]
  return <div className="max-w-xl space-y-6">
    <AppearanceControl compact />
    <div className="space-y-2">
      <label htmlFor="chat-font" className="block text-sm font-medium">Chat font</label>
      <p className="text-sm text-muted-foreground">Changes messages and the chat input on this device. Menus, code blocks and terminals stay unchanged.</p>
      <select id="chat-font" value={chatFont} onChange={event => setChatFont(event.target.value as ChatFont)} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {CHAT_FONTS.map(font => <option key={font.id} value={font.id}>{font.label}</option>)}
      </select>
    </div>
    <div aria-label="Chat font preview" className="space-y-4 rounded-xl border border-border bg-card p-5 text-sm leading-relaxed" style={{ fontFamily: font.family }}>
      <p className="ml-auto w-fit rounded-xl bg-muted px-4 py-2">Can you review my changes?</p>
      <p>I’ll check the changes and explain what needs attention.</p>
      <p className="border-t border-border pt-3 text-muted-foreground">The quick brown fox · 0123456789 · {'{ } [ ] != =>'}</p>
    </div>
    <p className="text-xs text-muted-foreground">Applies instantly and is remembered on this device. Choose System default to reset.</p>
  </div>
}

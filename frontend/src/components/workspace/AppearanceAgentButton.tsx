import { Sparkles } from 'lucide-react'
import { askAppearanceAgent } from '../../utils/appearanceAgentDraft'

export function AppearanceAgentButton({ className = '', menu = false }: { className?: string; menu?: boolean }) {
  return <button type="button" role={menu ? 'menuitem' : undefined} data-product-navigation-action onClick={askAppearanceAgent} className={`flex min-h-9 items-center gap-2 rounded-md px-3 py-2 text-left text-xs text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${className}`}><Sparkles aria-hidden="true" className="h-3.5 w-3.5 shrink-0" /><span>Ask an agent to change the look</span></button>
}

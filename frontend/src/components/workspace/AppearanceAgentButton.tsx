import { Sparkles } from 'lucide-react'
import { askAppearanceAgent } from '../../utils/appearanceAgentDraft'

export function AppearanceAgentButton({ className = '', menu = false }: { className?: string; menu?: boolean }) {
  return <button type="button" role={menu ? 'menuitem' : undefined} data-product-navigation-action onClick={askAppearanceAgent} className={`flex h-8 max-md:h-9 items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-border bg-muted/60 px-3 text-xs font-medium text-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${className}`}><Sparkles aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span>Customize with an agent</span></button>
}

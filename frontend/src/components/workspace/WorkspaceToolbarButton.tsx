import type { LucideIcon } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'

type Props = { active: boolean; icon: LucideIcon; label: string; onClick: () => void; badge?: number }

/** Icon tab shared by the Code, Crew, and product workspace toolbars. */
export function WorkspaceToolbarButton({ active, icon: Icon, label, onClick, badge }: Props) {
  const button = <button
    type="button"
    onClick={onClick}
    className={`relative flex h-6 w-7 items-center justify-center rounded transition-colors ${active ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:bg-background/70 hover:text-foreground'}`}
    aria-label={badge ? `${label} (${badge} pending)` : label}
    aria-pressed={active}
  >
    <Icon className="h-3.5 w-3.5" />
    {!!badge && <span className="absolute -right-1 -top-1 min-w-[14px] rounded-full bg-amber-500 px-1 text-[9px] font-semibold leading-[14px] text-white">{badge > 9 ? '9+' : badge}</span>}
  </button>
  if (active) return button
  return <Tooltip><TooltipTrigger asChild>{button}</TooltipTrigger><TooltipContent side="bottom"><p>{label}</p></TooltipContent></Tooltip>
}

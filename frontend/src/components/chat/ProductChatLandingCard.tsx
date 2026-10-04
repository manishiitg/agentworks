import type { ReactNode } from 'react'
import { Sparkles, type LucideIcon } from 'lucide-react'

type ProductChatLandingCardProps = {
  title: string
  description: string
  examples: string[]
  icon?: LucideIcon
  footer?: ReactNode
}

/** Shared empty-conversation guide for product chats. */
export function ProductChatLandingCard({ title, description, examples, icon: Icon = Sparkles, footer }: ProductChatLandingCardProps) {
  return <div className="flex h-full min-h-0 flex-col items-center overflow-y-auto px-6 py-10">
    <div className="my-auto w-full max-w-lg shrink-0 rounded-xl border border-border bg-muted/20 p-5">
      <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <Icon className="h-4 w-4 text-primary" />
        {title}
      </div>
      <p className="mt-2 text-sm leading-6 text-muted-foreground">{description}</p>
      <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
        {examples.map(example => <li key={example}>• {example}</li>)}
      </ul>
      {footer && <p className="mt-3 text-sm leading-6 text-muted-foreground">{footer}</p>}
    </div>
  </div>
}

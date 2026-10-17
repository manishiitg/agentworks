import type { ReactNode } from 'react'
import { Sparkles, type LucideIcon } from 'lucide-react'
import { ShortcutHint } from './ShortcutHint'

type ProductChatLandingCardProps = {
  title: string
  description: string
  examples: string[]
  icon?: LucideIcon
  footer?: ReactNode
  /** The shortcut hint for a new chat; `extra` adds a product-specific key. */
  shortcutHint?: boolean | ReactNode
}

/** Shared empty-conversation guide for product chats. */
export function ProductChatLandingCard({ title, description, examples, icon: Icon = Sparkles, footer, shortcutHint }: ProductChatLandingCardProps) {
  return <div className="flex h-full min-h-0 flex-col items-center overflow-y-auto px-6 py-10">
    <div className="my-auto w-full max-w-md shrink-0 p-4">
      <div className="flex items-center gap-2 text-base font-semibold text-foreground">
        <Icon className="h-4 w-4 text-primary" />
        {title}
      </div>
      <p className="mt-2 text-xs leading-5 text-muted-foreground">{description}</p>
      <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
        {examples.map(example => <li key={example}>• {example}</li>)}
      </ul>
      {footer && <p className="mt-3 text-sm leading-6 text-muted-foreground">{footer}</p>}
      {shortcutHint && <details className="mt-4 text-xs text-muted-foreground"><summary className="cursor-pointer hover:text-foreground">Keyboard shortcuts</summary><ShortcutHint extra={shortcutHint === true ? undefined : shortcutHint} /></details>}
    </div>
  </div>
}

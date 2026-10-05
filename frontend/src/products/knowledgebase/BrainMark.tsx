import type { ComponentPropsWithoutRef } from 'react'
import { Brain } from 'lucide-react'
import { cn } from '../../lib/utils'

type BrainMarkProps = ComponentPropsWithoutRef<'span'> & {
  title?: string
}

/** Brain's product mark, in the same rounded-square family as Vault, Crew and Code. */
export function BrainMark({ className, title = 'Brain', ...props }: BrainMarkProps) {
  return (
    <span
      role={title ? 'img' : 'presentation'}
      aria-label={title || undefined}
      title={title || undefined}
      className={cn('inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-[27%] bg-[#7C3AED] text-white', className)}
      {...props}
    >
      <Brain aria-hidden="true" className="h-[62%] w-[62%]" strokeWidth={2.2} />
    </span>
  )
}

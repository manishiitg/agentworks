import type { ComponentPropsWithoutRef } from 'react'
import { cn } from '../../lib/utils'

type WorkMarkProps = ComponentPropsWithoutRef<'svg'> & {
  title?: string
}

export function WorkMark({
  className,
  title = 'Project',
  ...props
}: WorkMarkProps) {
  return (
    <svg
      viewBox="0 0 64 64"
      fill="none"
      aria-hidden={title ? undefined : true}
      role={title ? 'img' : 'presentation'}
      className={cn('h-8 w-8', className)}
      {...props}
    >
      {title ? <title>{title}</title> : null}
      <rect x="4" y="4" width="56" height="56" rx="17" fill="#18181B" />
      <path
        d="M27 20 L15 32 L27 44 M37 20 L49 32 L37 44"
        stroke="white"
        strokeWidth="4.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

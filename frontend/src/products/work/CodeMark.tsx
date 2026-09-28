import type { ComponentPropsWithoutRef } from 'react'
import { cn } from '../../lib/utils'

type CodeMarkProps = ComponentPropsWithoutRef<'svg'> & {
  title?: string
}

// Code's mark: Crew's brackets with a slash, on a blue tile.
export function CodeMark({
  className,
  title = 'Code',
  ...props
}: CodeMarkProps) {
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
      <rect x="4" y="4" width="56" height="56" rx="17" fill="#1D4ED8" />
      <path
        d="M24 21 L13 32 L24 43 M40 21 L51 32 L40 43 M35 18 L29 46"
        stroke="white"
        strokeWidth="4.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

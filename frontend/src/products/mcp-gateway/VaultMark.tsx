import type { ComponentPropsWithoutRef } from 'react'
import { cn } from '../../lib/utils'

type VaultMarkProps = ComponentPropsWithoutRef<'svg'> & {
  title?: string
}

export function VaultMark({
  className,
  title = 'Vault',
  ...props
}: VaultMarkProps) {
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
      <path d="M16 23 32 14l16 9-16 9-16-9Z" stroke="white" strokeWidth="3.5" strokeLinejoin="round" />
      <path d="m16 32 16 9 16-9M16 41l16 9 16-9" stroke="white" strokeWidth="3.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

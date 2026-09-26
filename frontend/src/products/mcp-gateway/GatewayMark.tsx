import type { ComponentPropsWithoutRef } from 'react'
import { cn } from '../../lib/utils'

type GatewayMarkProps = ComponentPropsWithoutRef<'svg'> & {
  title?: string
}

export function GatewayMark({
  className,
  title = 'MCP Gateway',
  ...props
}: GatewayMarkProps) {
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
        d="M22 46 V31 a10 10 0 0 1 20 0 V46"
        stroke="white"
        strokeWidth="4.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M17 46 H47"
        stroke="white"
        strokeWidth="4.5"
        strokeLinecap="round"
      />
    </svg>
  )
}

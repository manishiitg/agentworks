import { useId, type ComponentPropsWithoutRef } from 'react'
import { cn } from '../../lib/utils'

type RunloopMarkProps = ComponentPropsWithoutRef<'svg'> & {
  title?: string
}

export function RunloopMark({
  className,
  title = 'AgentWorks',
  ...props
}: RunloopMarkProps) {
  const id = useId().replace(/:/g, '')
  const shellGradientId = `${id}-shell`
  const rimGradientId = `${id}-rim`
  const forgeGradientId = `${id}-forge`
  const signalGradientId = `${id}-signal`
  const glowGradientId = `${id}-glow`
  const shellPath = 'M32 4C44.8 4 51.6 4.3 55.7 8.3C59.7 12.4 60 19.2 60 32C60 44.8 59.7 51.6 55.7 55.7C51.6 59.7 44.8 60 32 60C19.2 60 12.4 59.7 8.3 55.7C4.3 51.6 4 44.8 4 32C4 19.2 4.3 12.4 8.3 8.3C12.4 4.3 19.2 4 32 4Z'
  const shellInsetPath = 'M32 5.8C43.9 5.8 50.2 6.1 54 9.8C57.7 13.6 58 19.9 58 31.8C58 43.7 57.7 50 54 53.8C50.2 57.5 43.9 57.8 32 57.8C20.1 57.8 13.8 57.5 10 53.8C6.3 50 6 43.7 6 31.8C6 19.9 6.3 13.6 10 9.8C13.8 6.1 20.1 5.8 32 5.8Z'

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
      <defs>
        <linearGradient id={shellGradientId} x1="10" y1="8" x2="54" y2="58" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#142235" />
          <stop offset="0.55" stopColor="#0F172A" />
          <stop offset="1" stopColor="#050B16" />
        </linearGradient>
        <linearGradient id={rimGradientId} x1="12" y1="10" x2="52" y2="54" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#38BDF8" stopOpacity="0.75" />
          <stop offset="0.55" stopColor="#F59E0B" stopOpacity="0.5" />
          <stop offset="1" stopColor="#F97316" stopOpacity="0.75" />
        </linearGradient>
        <linearGradient id={forgeGradientId} x1="21" y1="18" x2="43" y2="49" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#FBBF24" />
          <stop offset="0.5" stopColor="#F97316" />
          <stop offset="1" stopColor="#EA580C" />
        </linearGradient>
        <linearGradient id={signalGradientId} x1="17" y1="18" x2="46" y2="34" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#67E8F9" />
          <stop offset="1" stopColor="#60A5FA" />
        </linearGradient>
        <radialGradient id={glowGradientId} cx="0" cy="0" r="1" gradientUnits="userSpaceOnUse" gradientTransform="translate(32 31) rotate(90) scale(26)">
          <stop offset="0" stopColor="#F97316" stopOpacity="0.28" />
          <stop offset="0.65" stopColor="#0EA5E9" stopOpacity="0.12" />
          <stop offset="1" stopColor="#0F172A" stopOpacity="0" />
        </radialGradient>
      </defs>

      <path d={shellPath} fill={`url(#${shellGradientId})`} />
      <path d={shellInsetPath} stroke={`url(#${rimGradientId})`} strokeOpacity="0.9" strokeWidth="1.5" />
      <ellipse cx="32" cy="14" rx="19" ry="9" fill="#FFFFFF" opacity="0.08" />
      <path
        d="M32 11L47.5 19.5V44.5L32 53L16.5 44.5V19.5L32 11Z"
        fill={`url(#${glowGradientId})`}
        stroke="#94A3B8"
        strokeOpacity="0.18"
      />

      <path d="M20.5 20.5L27.5 25" stroke={`url(#${signalGradientId})`} strokeWidth="2.2" strokeLinecap="round" />
      <path d="M43.5 20.5L36.5 25" stroke={`url(#${signalGradientId})`} strokeWidth="2.2" strokeLinecap="round" />
      <circle cx="19.5" cy="19.5" r="3.5" fill="#2DD4BF" />
      <circle cx="44.5" cy="19.5" r="3.5" fill="#60A5FA" />
      <circle cx="19.5" cy="19.5" r="1.3" fill="#ECFEFF" />
      <circle cx="44.5" cy="19.5" r="1.3" fill="#EFF6FF" />

      <path
        d="M32 16L19 45H26.2L28.9 38.2H35.1L37.8 45H45L32 16ZM32 25.3L34.9 32.4H29.1L32 25.3Z"
        fill={`url(#${forgeGradientId})`}
        fillRule="evenodd"
        clipRule="evenodd"
      />
      <path d="M24 48H40C39.1 51.2 36.2 53.5 32.8 53.5H31.2C27.8 53.5 24.9 51.2 24 48Z" fill={`url(#${forgeGradientId})`} opacity="0.85" />
      <path d="M32 21.2L33.2 24.1L36.1 25.3L33.2 26.5L32 29.4L30.8 26.5L27.9 25.3L30.8 24.1L32 21.2Z" fill="#FFF7ED" />
    </svg>
  )
}

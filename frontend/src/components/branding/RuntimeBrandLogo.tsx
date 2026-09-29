import { getRuntimeAppName, runtimeBrandAsset, runtimeBrandingConfig } from '../../runtime-branding'

/**
 * The deployment's own logo at the left of the top bar (runtime-config.js
 * logoUrl / logoDarkUrl). Renders nothing for a deployment without one.
 */
export function RuntimeBrandLogo({ className = '' }: { className?: string }) {
  const config = runtimeBrandingConfig()
  const logo = runtimeBrandAsset('logoUrl', config)
  if (!logo) return null
  const dark = runtimeBrandAsset('logoDarkUrl', config) ?? logo
  const name = getRuntimeAppName(config) ?? ''
  return (
    <span className={`flex shrink-0 items-center ${className}`}>
      <img src={logo} alt={name} className="h-6 w-auto max-w-[160px] dark:hidden" />
      <img src={dark} alt={name} className="hidden h-6 w-auto max-w-[160px] dark:block" />
    </span>
  )
}

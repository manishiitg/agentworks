import { getRuntimeAppName, runtimeBrandAsset, runtimeBrandingConfig } from '../../runtime-branding'
import { useProductNavigationSidebar } from '../workspace/ProductTopBar'

/**
 * The deployment's mark in the navigation rail, or wordmark in a top bar.
 * Assets come from runtime-config.js, so every deployment uses the same UI.
 */
export function RuntimeBrandLogo({ className = '' }: { className?: string }) {
  const sidebar = useProductNavigationSidebar()
  const config = runtimeBrandingConfig()
  const logo = runtimeBrandAsset('logoUrl', config)
  const mark = runtimeBrandAsset('markUrl', config)
  const name = getRuntimeAppName(config) ?? ''
  if (sidebar) {
    if (!mark && !logo) return null
    return <span title={name} className="flex h-9 w-full shrink-0 items-center justify-center" data-runtime-brand="mark">
      {mark ? <img src={mark} alt={name} className="h-7 w-7 object-contain" /> : <>
        <img src={logo!} alt={name} className="h-7 w-full object-contain dark:hidden" />
        <img src={runtimeBrandAsset('logoDarkUrl', config) ?? logo!} alt={name} className="hidden h-7 w-full object-contain dark:block" />
      </>}
    </span>
  }
  if (!logo) return null
  const dark = runtimeBrandAsset('logoDarkUrl', config) ?? logo
  return (
    <span className={`flex shrink-0 items-center ${className}`}>
      <img src={logo} alt={name} className="h-6 w-auto max-w-[160px] dark:hidden" />
      <img src={dark} alt={name} className="hidden h-6 w-auto max-w-[160px] dark:block" />
    </span>
  )
}

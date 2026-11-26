import type { HTMLAttributes } from 'react'

/** One decorative loading/working glyph. The caller owns status, labels and lifetime.
 * Shapes stay mounted so a personal theme update needs no timer or per-instance subscription. */
export function LoadingIndicator({ className = 'h-4 w-4', label, title, ...props }: Omit<HTMLAttributes<HTMLSpanElement>, 'children'> & { label?: string }) {
  const accessibleLabel = label ?? props['aria-label']
  return <span data-theme-loader="" className={`aw-loader ${className}`} role={accessibleLabel ? 'img' : undefined} aria-label={accessibleLabel} aria-hidden={accessibleLabel ? undefined : true} title={title ?? accessibleLabel} {...props}>
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none">
      <g data-loader-part="ring"><circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" opacity=".2" /><path d="M12 3a9 9 0 0 1 9 9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></g>
      <g data-loader-part="dots" fill="currentColor"><circle cx="4" cy="12" r="2.5" /><circle cx="12" cy="12" r="2.5" /><circle cx="20" cy="12" r="2.5" /></g>
      <g data-loader-part="pulse"><circle cx="12" cy="12" r="7" fill="currentColor" /></g>
      <g data-loader-part="orbit"><circle cx="12" cy="12" r="8" stroke="currentColor" opacity=".25" /><circle cx="12" cy="4" r="3" fill="currentColor" /></g>
      <g data-loader-part="bar" fill="currentColor"><rect x="2" y="8" width="4" height="8" rx="1" /><rect x="10" y="5" width="4" height="14" rx="1" /><rect x="18" y="8" width="4" height="8" rx="1" /></g>
    </svg>
  </span>
}

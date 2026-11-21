import { SquareArrowOutUpRight } from 'lucide-react'

/** Small marks for the editors the server offers (workspace_editor.go), so people find their editor by sight.
 * Simplified shapes, not the vendors' artwork; an unknown editor falls back to the generic "open" icon. */
export function EditorIcon({ id, className = 'h-4 w-4' }: { id: string; className?: string }) {
  switch (id) {
    case 'vscode':
      return <svg viewBox="0 0 24 24" className={className} aria-hidden="true"><path fill="#007ACC" d="M23.15 2.587 18.21.21a1.494 1.494 0 0 0-1.705.29l-9.46 8.63-4.12-3.128a.999.999 0 0 0-1.276.057L.327 7.261A1 1 0 0 0 .326 8.74L3.899 12 .326 15.26a1 1 0 0 0 .001 1.479L1.65 17.94a.999.999 0 0 0 1.276.057l4.12-3.128 9.46 8.63a1.492 1.492 0 0 0 1.704.29l4.942-2.377A1.5 1.5 0 0 0 24 20.06V3.939a1.5 1.5 0 0 0-.85-1.352zm-5.146 14.861L10.826 12l7.178-5.448v10.896z" /></svg>
    case 'cursor':
      return <svg viewBox="0 0 24 24" className={className} aria-hidden="true"><path fill="currentColor" d="M12 1 22 6.75v10.5L12 23 2 17.25V6.75z" /><path className="fill-background" d="M12 3.3 19.9 7.85 12 12.4zM4.1 7.85 12 12.4v8.3L4.1 16.15z" opacity=".55" /><path className="fill-background" d="M12 12.4 19.9 7.85 12 20.7z" /></svg>
    case 'zed':
      return <svg viewBox="0 0 24 24" className={className} aria-hidden="true"><rect x="2" y="2" width="20" height="20" rx="3" fill="currentColor" /><path className="stroke-background" d="M7 7.5h10L7 16.5h10" fill="none" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" /></svg>
    case 'jetbrains':
      return <svg viewBox="0 0 24 24" className={className} aria-hidden="true"><rect x="2" y="2" width="20" height="20" fill="currentColor" /><rect className="fill-background" x="5" y="17" width="8" height="2" /><text x="5" y="13.5" className="fill-background" fontSize="8" fontWeight="700" fontFamily="system-ui, sans-serif">JB</text></svg>
    default:
      return <SquareArrowOutUpRight className={className} aria-hidden="true" />
  }
}

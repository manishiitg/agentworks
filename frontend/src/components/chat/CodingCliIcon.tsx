import { Terminal } from 'lucide-react'

const programs: Record<string, { name: string; icon: string }> = {
  claude: { name: 'Claude Code', icon: 'claude.ico' },
  codex: { name: 'Codex', icon: 'codex.png' },
  cursor: { name: 'Cursor', icon: 'cursor.svg' },
  pi: { name: 'Pi', icon: 'pi.svg' },
  muse: { name: 'Muse', icon: 'muse.ico' },
  agy: { name: 'Antigravity', icon: 'antigravity.svg' },
}

/** The server-resolved coding program, using the same logos in menus and Team rows. */
export function CodingCliIcon({ provider, label, className = 'inline-flex shrink-0 items-center' }: { provider?: string; label?: string; className?: string }) {
  if (!provider) return null
  const key = provider.trim().toLowerCase().replace(/\s+/g, '-').replace(/-cli$/, '').replace(/^claude-code$/, 'claude').replace(/^antigravity$/, 'agy')
  const program = programs[key]
  const name = label || program?.name || provider
  return <span role="img" title={name} aria-label={name} className={className}>
    {program ? <img src={`/provider-icons/${program.icon}`} alt="" className="h-3.5 w-3.5 object-contain" /> : <Terminal aria-hidden="true" className="h-3.5 w-3.5" />}
  </span>
}

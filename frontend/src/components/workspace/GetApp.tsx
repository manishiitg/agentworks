import { useEffect, useId, useRef, useState } from 'react'
import { Check, Copy, MonitorDown, X } from 'lucide-react'
import { getApiBaseUrl } from '../../services/api'
import { computerOS, unixCliInstallCommand, windowsDefenderNote, windowsInstallCommand } from '../../utils/computerInstall'

const macInstaller = 'curl -fsSL https://raw.githubusercontent.com/manishiitg/agentworks/main/install.sh | bash'

/** The person runs the installer; opening this panel never downloads or executes anything. */
export function GetApp({ compact }: { compact: boolean }) {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState('')
  const [copyError, setCopyError] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const titleId = useId()
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [open])
  const base = getApiBaseUrl() || window.location.origin
  const os = computerOS()
  const platforms = os === 'unknown' ? ['mac', 'windows'] as const : [os]
  const copyInstaller = async (command: string) => {
    try { await navigator.clipboard.writeText(command); setCopied(command); setCopyError(false) }
    catch { setCopyError(true) }
  }
  if (window.electronAPI) return null
  return <div ref={root} className="relative mb-2">
    <button ref={trigger} type="button" aria-label="Get the app" title="Get the app" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}
      className={`flex items-center rounded-lg text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${compact ? 'h-8 w-8 justify-center' : 'min-h-10 w-full gap-2.5 px-3 py-2'}`}>
      <MonitorDown className="h-4 w-4 shrink-0" aria-hidden="true" />{!compact && <span>Get the app</span>}
    </button>
    {open && <section role="dialog" aria-labelledby={titleId} className="absolute bottom-full left-0 z-50 mb-2 w-80 max-w-[calc(100vw-2rem)] max-h-[calc(100dvh-7rem)] overflow-y-auto space-y-3 rounded-xl border border-border bg-popover p-4 text-sm shadow-xl">
      <div className="flex items-center justify-between gap-2"><h2 id={titleId} className="font-semibold">Get AgentWorks</h2><button type="button" aria-label="Close app setup" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" aria-hidden="true" /></button></div>
      {platforms.map(platform => {
        const name = platform === 'windows' ? 'Windows' : platform === 'mac' ? 'Mac' : 'Linux'
        const command = platform === 'windows' ? windowsInstallCommand(base) : platform === 'mac' ? macInstaller : unixCliInstallCommand(base)
        return <div key={platform} className="space-y-3">
          <h3 className="text-sm font-medium">{name}</h3>
          <p className="text-xs text-muted-foreground">{platform === 'windows' ? 'Open PowerShell (not as administrator), paste this command, and press Enter.' : 'Open Terminal, paste this command, and press Enter.'}</p>
          <code className="block break-all rounded-lg border border-border bg-background p-3 text-xs">{command}</code>
          <button type="button" aria-label={`Copy ${name} install command`} onClick={() => void copyInstaller(command)} className="flex w-full items-center justify-center gap-2 rounded-lg bg-primary px-3 py-2 font-medium text-primary-foreground hover:bg-primary/90">{copied === command ? <Check className="h-4 w-4" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}{copied === command ? 'Copied' : 'Copy install command'}</button>
          {platform === 'windows' ? <p className="text-xs text-muted-foreground">{windowsDefenderNote}</p> : platform === 'mac' ? <p className="text-xs text-muted-foreground">Intel and Apple silicon Macs</p> : null}
          <p className="border-t border-border pt-3 text-xs text-muted-foreground">{platform === 'mac' ? 'Already installed? Open AgentWorks from Applications.' : 'Already installed? Run agentworks update.'}</p>
        </div>
      })}
      {copyError && <p role="status" className="text-xs text-muted-foreground">Select the command above and copy it manually.</p>}
    </section>}
  </div>
}

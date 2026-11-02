import { useEffect, useId, useRef, useState } from 'react'
import { Check, Copy, MonitorDown, X } from 'lucide-react'

const installer = 'curl -fsSL https://raw.githubusercontent.com/manishiitg/agentworks/main/install.sh | bash'

/** The person runs the installer; opening this panel never downloads or executes anything. */
export function MacAppDownload({ compact }: { compact: boolean }) {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
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
  const copyInstaller = async () => {
    try { await navigator.clipboard.writeText(installer); setCopied(true); setCopyError(false) }
    catch { setCopyError(true) }
  }
  if (window.electronAPI) return null
  return <div ref={root} className="relative mb-2">
    <button ref={trigger} type="button" aria-label="Get the Mac app" title="Get the Mac app" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}
      className={`flex items-center rounded-lg text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${compact ? 'h-8 w-8 justify-center' : 'min-h-10 w-full gap-2.5 px-3 py-2'}`}>
      <MonitorDown className="h-4 w-4 shrink-0" aria-hidden="true" />{!compact && <span>Get the Mac app</span>}
    </button>
    {open && <section role="dialog" aria-labelledby={titleId} className="absolute bottom-full left-0 z-50 mb-2 w-80 max-w-[calc(100vw-2rem)] max-h-[calc(100dvh-7rem)] overflow-y-auto space-y-3 rounded-xl border border-border bg-popover p-4 text-sm shadow-xl">
      <div className="flex items-center justify-between gap-2"><h2 id={titleId} className="font-semibold">AgentWorks for Mac</h2><button type="button" aria-label="Close Mac app setup" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" aria-hidden="true" /></button></div>
      <p className="text-xs text-muted-foreground">Open Terminal, paste this command, and press Enter.</p>
      <code className="block break-words rounded-lg border border-border bg-background p-3 text-xs">{installer}</code>
      <button type="button" onClick={() => void copyInstaller()} className="flex w-full items-center justify-center gap-2 rounded-lg bg-primary px-3 py-2 font-medium text-primary-foreground hover:bg-primary/90">{copied ? <Check className="h-4 w-4" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}{copied ? 'Copied' : 'Copy install command'}</button>
      {copyError && <p role="status" className="text-xs text-muted-foreground">Select the command above and copy it manually.</p>}
      <p className="text-xs text-muted-foreground">Intel and Apple silicon Macs</p>
      <p className="border-t border-border pt-3 text-xs text-muted-foreground">Already installed? Open AgentWorks from Applications.</p>
    </section>}
  </div>
}

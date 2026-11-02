import { useEffect, useId, useRef, useState } from 'react'
import { Download, MonitorDown, X } from 'lucide-react'

const releases = 'https://github.com/manishiitg/agentworks/releases'

/** Public release metadata only; no account or server credentials leave this page. */
export function MacAppDownload({ compact }: { compact: boolean }) {
  const [open, setOpen] = useState(false)
  const [download, setDownload] = useState<string | null>(null)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const titleId = useId()
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    const controller = new AbortController()
    void fetch('https://api.github.com/repos/manishiitg/agentworks/releases/latest', { credentials: 'omit', signal: controller.signal }).then(async response => {
      if (!response.ok) return
      const release = await response.json() as { tag_name?: string; assets?: { name: string }[] }
      if (!release.tag_name || !/^v\d+\.\d+\.\d+$/.test(release.tag_name)) return
      const name = `AgentWorks-${release.tag_name.slice(1)}-arm64.dmg`
      if (!controller.signal.aborted && release.assets?.some(asset => asset.name === name)) setDownload(`${releases}/download/${release.tag_name}/${name}`)
    }).catch(() => { /* GitHub rate limits/offline: the releases page remains available. */ })
    return () => { controller.abort(); document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [open])
  if (window.electronAPI) return null
  return <div ref={root} className="relative mb-2">
    <button ref={trigger} type="button" aria-label="Get the Mac app" title="Get the Mac app" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}
      className={`flex items-center rounded-lg text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${compact ? 'h-8 w-8 justify-center' : 'min-h-10 w-full gap-2.5 px-3 py-2'}`}>
      <MonitorDown className="h-4 w-4 shrink-0" aria-hidden="true" />{!compact && <span>Get the Mac app</span>}
    </button>
    {open && <section role="dialog" aria-labelledby={titleId} className="absolute bottom-full left-0 z-50 mb-2 w-72 max-w-[calc(100vw-2rem)] space-y-3 rounded-xl border border-border bg-popover p-4 text-sm shadow-xl">
      <div className="flex items-center justify-between gap-2"><h2 id={titleId} className="font-semibold">AgentWorks for Mac</h2><button type="button" aria-label="Close Mac app download" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" aria-hidden="true" /></button></div>
      <p className="text-xs text-muted-foreground">Work with your local files and AI apps.</p>
      <a href={download || `${releases}/latest`} target="_blank" rel="noopener noreferrer" className="flex items-center justify-center gap-2 rounded-lg bg-primary px-3 py-2 font-medium text-primary-foreground hover:bg-primary/90"><Download className="h-4 w-4" aria-hidden="true" />{download ? 'Download for Mac' : 'View Mac downloads'}</a>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground"><span>Apple silicon · M1 or later</span><a href={`${releases}/latest`} target="_blank" rel="noopener noreferrer" className="underline">Installation help</a></div>
      <p className="border-t border-border pt-3 text-xs text-muted-foreground">Already installed? Open AgentWorks from Applications.</p>
    </section>}
  </div>
}

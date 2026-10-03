import type { ReactNode } from 'react'
import './BrowserChrome.css'
import { ArrowLeft, ArrowRight, File, Globe, Plus, RefreshCw, X } from 'lucide-react'

export type BrowserTab = { tabId: string; title: string; url: string; active: boolean }
export const browserIconButtonClass = 'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40'
export const browserActionButtonClass = 'inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border border-border bg-background px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40'

type Props = {
  status: string
  sessionLabel?: string
  actions: ReactNode
  tabs: BrowserTab[]
  canSelect: boolean
  controlling: boolean
  address: string
  onAddress: (value: string) => void
  onNavigate: () => void
  onHistory: (action: 'back' | 'forward' | 'reload') => void
  onSelect: (tab: BrowserTab) => void
  onClose: (tab: BrowserTab) => void
  onNew: () => void
}

export function BrowserChrome(props: Props) {
  return <div className="live-browser-chrome shrink-0">
    <header className="live-browser-bar flex h-9 items-center gap-2 border-b border-border px-2">
      <h3 className="inline-flex shrink-0 items-center gap-1.5 text-xs font-semibold" title="Browser"><Globe className="h-4 w-4 text-muted-foreground" aria-hidden="true" /><span className="browser-heading-label">Browser</span></h3>
      <div className="live-browser-tabs flex min-w-0 flex-1 items-center gap-1 overflow-x-auto" aria-label="Browser tabs">
      <div role="tablist" aria-label="Open browser tabs" className="flex min-w-0 shrink-0 items-center gap-1">
        {props.tabs.map(tab => <div key={tab.tabId} className={`flex max-w-48 shrink-0 items-center rounded-md border ${tab.active ? 'border-border bg-muted/70' : 'border-transparent text-muted-foreground'}`}>
          <button type="button" role="tab" aria-selected={tab.active} disabled={!props.canSelect} title={tab.title || 'New tab'} className="flex h-7 min-w-0 items-center gap-1.5 px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default" onClick={() => props.onSelect(tab)}>
            <File className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /><span className={`truncate ${tab.active ? 'font-medium text-foreground' : ''}`}>{tab.title || (tab.url && tab.url !== 'about:blank' ? 'Untitled page' : 'New tab')}</span>
          </button>
          {props.canSelect && <button type="button" aria-label={`Close ${tab.title || 'tab'}`} title="Close tab" disabled={!props.controlling || props.tabs.length < 2} className={`${browserIconButtonClass} mr-0.5 h-6 w-6`} onClick={() => props.onClose(tab)}><X className="h-3.5 w-3.5" aria-hidden="true" /></button>}
        </div>)}
      </div>
      {props.canSelect && <button type="button" aria-label="New tab" title="New tab" disabled={!props.controlling} className={`${browserIconButtonClass} ml-0.5`} onClick={props.onNew}><Plus className="h-4 w-4" aria-hidden="true" /></button>}
      </div>
      <div className="live-browser-actions flex shrink-0 items-center gap-1">{props.actions}</div>
    </header>
    <form className="live-browser-navigation flex shrink-0 items-center gap-1 h-9 border-b border-border px-2" aria-label="Browser navigation" onSubmit={event => { event.preventDefault(); if (props.controlling && props.address.trim()) props.onNavigate() }}>
      {(['back', 'forward', 'reload'] as const).map(action => <button key={action} type="button" aria-label={action === 'back' ? 'Go back' : action === 'forward' ? 'Go forward' : 'Reload page'} title={action === 'back' ? 'Go back' : action === 'forward' ? 'Go forward' : 'Reload page'} disabled={!props.controlling} className={browserIconButtonClass} onClick={() => props.onHistory(action)}>
        {action === 'back' ? <ArrowLeft className="h-4 w-4" aria-hidden="true" /> : action === 'forward' ? <ArrowRight className="h-4 w-4" aria-hidden="true" /> : <RefreshCw className="h-4 w-4" aria-hidden="true" />}
      </button>)}
      <div className="ml-1 flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-background px-2 focus-within:ring-1 focus-within:ring-ring">
        <Globe className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <input aria-label="Website address" placeholder="Enter a website address" autoComplete="off" spellCheck={false} readOnly={!props.controlling} value={props.address} onChange={event => props.onAddress(event.target.value)} className="h-7 min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none" />
        {props.canSelect && <button type="submit" aria-label="Go to address" title="Go to address" disabled={!props.controlling || !props.address.trim()} className={`${browserIconButtonClass} h-7 w-7`}><ArrowRight className="h-4 w-4" aria-hidden="true" /></button>}
      </div>
      <span role="status" title={props.sessionLabel} className="browser-status shrink-0 pl-1 text-xs text-muted-foreground">{props.status}</span>
    </form>
  </div>
}

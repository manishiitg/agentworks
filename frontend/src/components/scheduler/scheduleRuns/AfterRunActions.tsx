import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { Archive, Bell, ChevronDown, Globe, SlidersHorizontal, X } from 'lucide-react'
import ModalPortal from '../../ui/ModalPortal'
import type { ScheduleAfterRun } from '../../../services/api-types'

const OPTIONS = [
  { key: 'backup', label: 'Backup', icon: Archive, description: 'Save workflow state when it has changed.' },
  { key: 'publish', label: 'Publish report', icon: Globe, description: 'Refresh the configured published report when it has changed.' },
  { key: 'notify', label: 'Notify', icon: Bell, description: 'Record a run summary. Send failures and status changes to your configured channels.' },
] as const

export function AfterRunActions({ options, scope, disabled, saving, onChange }: {
  options: ScheduleAfterRun
  scope: string
  disabled?: boolean
  saving?: boolean
  onChange: (next: ScheduleAfterRun) => void | Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [updating, setUpdating] = useState(false)
  const [position, setPosition] = useState({ top: 0, left: 0 })
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const id = useId()
  const selected = OPTIONS.filter(option => options[option.key])
  const summary = selected.length ? selected.map(option => option.label).join(' · ') : 'None'
  const change = async (key: keyof ScheduleAfterRun) => {
    setUpdating(true)
    try {
      await onChange({ ...options, [key]: !options[key] })
    } finally {
      setUpdating(false)
    }
  }

  useLayoutEffect(() => {
    if (!open || !trigger.current || !panel.current) return
    const rect = trigger.current.getBoundingClientRect()
    const height = panel.current.getBoundingClientRect().height
    const width = Math.min(352, window.innerWidth - 32)
    const below = rect.bottom + 8
    setPosition({
      left: Math.max(16, Math.min(rect.left, window.innerWidth - width - 16)),
      top: Math.max(16, below + height > window.innerHeight - 16 ? rect.top - height - 8 : below),
    })
  }, [open])

  useEffect(() => {
    if (!open) return
    const close = () => setOpen(false)
    const outside = (event: Event) => {
      const target = event.target as Node
      if (!trigger.current?.contains(target) && !panel.current?.contains(target)) close()
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      close()
      trigger.current?.focus()
    }
    panel.current?.querySelector<HTMLButtonElement>('button')?.focus()
    document.addEventListener('mousedown', outside)
    document.addEventListener('focusin', outside)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', close)
    // Close when the schedule list moves so the panel stays with its trigger.
    const scroll = (event: Event) => { if (!panel.current?.contains(event.target as Node)) close() }
    window.addEventListener('scroll', scroll, true)
    return () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('focusin', outside)
      document.removeEventListener('keydown', escape)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', scroll, true)
    }
  }, [open])

  return <>
    <button
      ref={trigger}
      type="button"
      aria-label={`After-run actions for ${scope}: ${summary}`}
      aria-haspopup="dialog"
      aria-expanded={open}
      aria-controls={open ? id : undefined}
      onClick={() => setOpen(value => !value)}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <SlidersHorizontal className="h-3 w-3 shrink-0" />
      <span className="shrink-0">After run</span>
      <span className="truncate text-foreground/75" title={summary}>{summary}</span>
      <ChevronDown className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
    </button>
    {open && <ModalPortal>
      <div ref={panel} id={id} role="dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`}
        style={position}
        className="fixed z-[80] max-h-[calc(100dvh-32px)] w-[352px] max-w-[calc(100vw-32px)] overflow-y-auto rounded-xl border border-border bg-popover text-popover-foreground shadow-xl">
        <div className="flex items-start justify-between gap-3 border-b border-border px-4 py-3">
          <div>
            <h3 id={`${id}-title`} className="text-sm font-semibold">After-run actions</h3>
            <p id={`${id}-description`} className="mt-1 text-xs leading-5 text-muted-foreground">{scope === 'manual runs' ? 'Applies to full runs started from a chat.' : `Applies to ${scope}.`}</p>
          </div>
          <button type="button" aria-label="Close after-run actions" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded-md p-1 text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-4 w-4" /></button>
        </div>
        <div className="space-y-1 p-2">
          {OPTIONS.map(({ key, label, description, icon: Icon }) => <button
            key={key} type="button" role="switch" aria-checked={options[key]} aria-label={label}
            aria-describedby={`${id}-${key}`} disabled={disabled || saving || updating}
            onClick={() => void change(key)}
            className="flex w-full items-center gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60">
            <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{label}</span><span id={`${id}-${key}`} className="mt-0.5 block text-xs leading-5 text-muted-foreground">{description}</span></span>
            <span aria-hidden="true" className={`inline-flex h-5 w-9 shrink-0 items-center rounded-full p-0.5 transition-colors ${options[key] ? 'bg-primary' : 'bg-muted-foreground/25'}`}><span className={`h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${options[key] ? 'translate-x-4' : ''}`} /></span>
          </button>)}
        </div>
        <p className="border-t border-border px-4 py-2.5 text-[11px] leading-5 text-muted-foreground">{disabled ? 'You have read-only access to these settings.' : saving || updating ? 'Saving…' : 'Changes save automatically. These actions also work with Pulse off.'}</p>
      </div>
    </ModalPortal>}
  </>
}

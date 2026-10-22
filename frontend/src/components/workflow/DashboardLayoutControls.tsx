import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Laptop, PanelLeftClose, PanelLeftOpen, PanelRightClose, SlidersHorizontal, Smartphone, Tablet } from 'lucide-react'
import type { ReportPreviewDevice } from '../../utils/reportPreviewPreference'

const DashboardLayoutContext = createContext<ReactNode>(null)
export const DashboardLayoutProvider = DashboardLayoutContext.Provider

export function DashboardLayoutActions() {
  return useContext(DashboardLayoutContext)
}

/** Dashboard sizing belongs to its header, not a floating workspace divider. */
export function DashboardLayoutControls({ width, minWidth = 15, maxWidth = 85, widthUnit = '%', onWidthChange, previewDevice, onPreviewDeviceChange, chatVisible, onToggleChat, onClose }: {
  width: number
  minWidth?: number
  maxWidth?: number
  widthUnit?: '%' | 'px'
  onWidthChange?: (width: number) => void
  previewDevice: ReportPreviewDevice
  onPreviewDeviceChange: (device: ReportPreviewDevice) => void
  chatVisible: boolean
  onToggleChat: () => void
  onClose: () => void
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const id = useId()
  useEffect(() => {
    if (!open) return
    const outside = (event: PointerEvent) => { if (!ref.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() } }
    document.addEventListener('pointerdown', outside)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape) }
  }, [open])
  return <div ref={ref} className="relative hidden md:block">
    <button ref={trigger} type="button" aria-label="Dashboard layout" title="Dashboard layout" aria-expanded={open} aria-controls={open ? id : undefined} onClick={() => setOpen(value => !value)} className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><SlidersHorizontal className="h-3.5 w-3.5" /></button>
    {open && <div id={id} className="absolute right-0 top-full z-50 mt-2 w-64 space-y-4 rounded-lg border border-border bg-popover p-3 text-popover-foreground shadow-lg" aria-label="Dashboard layout options">
      <p className="text-xs font-semibold">Dashboard layout</p>
      <div className="flex gap-1">{([
        ['mobile', Smartphone, 'Mobile preview'], ['tablet', Tablet, 'Tablet preview'], ['desktop', Laptop, 'Laptop preview'],
      ] as const).map(([device, Icon, label]) => <button key={device} type="button" aria-label={label} title={label} aria-pressed={previewDevice === device} onClick={() => onPreviewDeviceChange(device)} className={`flex h-8 flex-1 items-center justify-center rounded-md ${previewDevice === device ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}><Icon className="h-4 w-4" /></button>)}</div>
      {previewDevice === 'desktop' && <p className="text-xs text-muted-foreground">480px chat · dashboard fills the remaining space</p>}
      {onWidthChange && <label className="block space-y-2 text-xs"><span className="flex justify-between gap-2"><span>Dashboard width</span><span className="text-muted-foreground">{Math.round(width)}{widthUnit}</span></span><input type="range" aria-label="Dashboard width" min={minWidth} max={maxWidth} value={width} onChange={event => onWidthChange(Number(event.target.value))} className="w-full accent-primary" /></label>}
      <div className="space-y-1 border-t border-border pt-2">
        <button type="button" onClick={() => { onToggleChat(); setOpen(false) }} className="flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs hover:bg-muted">{chatVisible ? <PanelLeftClose className="h-4 w-4" /> : <PanelLeftOpen className="h-4 w-4" />}{chatVisible ? 'Expand dashboard' : 'Show chat alongside'}</button>
        <button type="button" onClick={onClose} className="flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs hover:bg-muted"><PanelRightClose className="h-4 w-4" />Close dashboard panel</button>
      </div>
    </div>}
  </div>
}

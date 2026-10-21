// Shared primitives for report widgets. Pulled out of ReportViewer.tsx so
// individual widget files can be moved to this directory without dragging
// the orchestration code along. Anything used by 2+ widgets — or by both a
// widget and ReportViewer's WidgetCard dispatcher — lives here.
//
// The widget renderers themselves live in sibling files and import from this
// module.

import React from 'react'
import type { ReportWidget } from '../../../services/api-types'

export function WidgetHeader({
  widget,
  mode = 'default',
  className = '',
}: {
  widget: ReportWidget
  mode?: 'default' | 'metric'
  className?: string
}) {
  if (!widget.title && !widget.description) return null
  if (mode === 'metric') {
    return (
      <div className={`flex flex-col gap-1 ${className}`}>
        {widget.title && <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-muted-foreground">{widget.title}</div>}
        {widget.description && <div className="text-xs leading-5 text-muted-foreground">{widget.description}</div>}
      </div>
    )
  }
  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      {widget.title && <div className="report-heading text-[15px] font-semibold leading-snug text-foreground">{widget.title}</div>}
      {widget.description && <div className="text-xs leading-5 text-muted-foreground">{widget.description}</div>}
    </div>
  )
}

// The per-widget collapse/hide control was removed per product decision — report
// widgets no longer show a compress/hide icon. Kept as a no-op so the existing
// call sites (WidgetShell, StandaloneWidgetNotice, Stat/Alert) need no changes;
// `onToggle`/`hidden` are accepted but ignored.
export function WidgetVisibilityButton(_props: {
  hidden?: boolean
  onToggle: () => void
  className?: string
}) {
  return null
}

export function StandaloneWidgetNotice({
  children,
  hidden = false,
  onToggleHidden,
}: {
  children: React.ReactNode
  hidden?: boolean
  onToggleHidden?: () => void
}) {
  return (
    <div className="relative rounded-xl bg-card px-3 py-2.5 sm:border sm:border-border">
      {onToggleHidden && <WidgetVisibilityButton hidden={hidden} onToggle={onToggleHidden} />}
      {children}
    </div>
  )
}

// Wraps table/chart/pivot widgets in a calm, flat "paper" card — a single
// hairline border, generous padding, no gradient fill or hover shadow-lift.
// Editorial restraint over dashboard flourish.
// A "document" widget renders a self-contained HTML document (which carries its
// own heading/structure). These render bare — no card box, no widget title — so
// the document isn't double-framed.
export function isDocumentWidget(widget: ReportWidget): boolean {
  if (widget.kind === 'file') {
    const fmt = widget.renderFormat || 'auto'
    if (fmt === 'html') return true
    if (fmt === 'auto') {
      const ext = (widget.source || '').split('.').pop()?.toLowerCase()
      return ext === 'html' || ext === 'htm'
    }
  }
  return false
}

export function WidgetShell({
  widget,
  children,
  onToggleHidden,
}: {
  widget: ReportWidget
  children: React.ReactNode
  onToggleHidden?: () => void
}) {
  // HTML document widgets bring their own structure — render full-bleed.
  if (isDocumentWidget(widget)) return <>{children}</>
  const shellClassName =
    'group relative px-0 py-0 transition-colors duration-200 sm:overflow-hidden sm:rounded-xl sm:border sm:border-border sm:bg-card sm:px-3.5 sm:py-2.5'
  return (
    <div className={shellClassName}>
      {onToggleHidden && <WidgetVisibilityButton onToggle={onToggleHidden} />}
      {children}
    </div>
  )
}

// Inline per-widget diagnostic — surfaces silent-failure cases (unresolved path,
// empty filter result) so the builder doesn't see a mystery blank space.
export function WidgetError({
  widget,
  message,
  hint,
  severity = 'error',
  showWidgetMeta = true,
}: {
  widget: ReportWidget
  message: string
  hint?: string
  severity?: 'error' | 'info'
  showWidgetMeta?: boolean
}) {
  const tone = severity === 'error'
    ? 'border-destructive/30 bg-destructive/5 text-destructive'
    : 'border-border/70 bg-muted/30 text-muted-foreground'
  return (
    <div className={`rounded-xl border px-2.5 py-2 text-xs ${tone}`}>
      {showWidgetMeta && (
        <div className="flex items-center gap-2">
          {widget.title && <span className="font-semibold">{widget.title}</span>}
          <span className="opacity-70">({widget.kind})</span>
        </div>
      )}
      <div className="mt-0.5">{message}</div>
      {hint && <div className="mt-0.5 opacity-75">{hint}</div>}
    </div>
  )
}

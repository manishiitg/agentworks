import type { CSSProperties } from 'react'
import { MOBILE_PREVIEW_PANE_COLUMN } from '../../components/workflow/workspaceLayoutResolver'

export interface WorkSurfaceLayoutInput {
  chatOpen: boolean
  panelOpen: boolean
  splitRatio: number
  /** Mobile preview pins the panel to phone width, like the Builder split. */
  mobilePreview?: boolean
  /** The phone column's width in px after the person dragged the rail in Mobile preview; absent keeps the phone-width default. */
  mobilePaneWidth?: number | null
}

export interface WorkSurfaceLayout {
  gridClassName: string
  gridStyle: CSSProperties | undefined
  toolbarClassName: string
  showChat: boolean
  chatClassName: string
  showDivider: boolean
  showPanel: boolean
  panelClassName: string
}

/**
 * The single layout decision point for the Crew split. Every class and mount
 * branch for the chat/panel panes derives here from the same flags, so no
 * call site can combine them inconsistently.
 *
 * Visibility rule (shared with the Builder resolver): a pane hidden below md
 * must restore its own display at md+ (`hidden md:flex` for flex panes).
 * Restoring with `md:block` would override flex, collapse flex-1 scroll
 * regions to content height, and freeze pane scrolling.
 */
export function resolveWorkSurfaceLayout(input: WorkSurfaceLayoutInput): WorkSurfaceLayout {
  const { chatOpen, panelOpen, splitRatio, mobilePreview = false, mobilePaneWidth = null } = input
  const split = chatOpen && panelOpen

  return {
    gridClassName: `grid h-full min-h-0 min-w-0 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] ${split ? 'md:[grid-template-columns:var(--work-split-columns)]' : ''}`,
    gridStyle: split
      ? ({ '--work-split-columns': mobilePreview
        ? `minmax(240px, 1fr) ${mobilePaneWidth ? `${Math.round(mobilePaneWidth)}px` : MOBILE_PREVIEW_PANE_COLUMN}`
        : `minmax(240px, ${splitRatio}fr) minmax(240px, ${1 - splitRatio}fr)` } as CSSProperties)
      : undefined,
    toolbarClassName: `${split ? 'md:col-span-2' : ''} col-start-1 row-start-1`,
    showChat: chatOpen,
    chatClassName: `flex min-h-0 min-w-0 flex-col overflow-hidden bg-background col-start-1 row-start-2 ${panelOpen ? 'border-b border-border md:border-b-0 md:border-r' : ''}`,
    showDivider: split,
    showPanel: panelOpen,
    panelClassName: `min-h-0 min-w-0 overflow-hidden bg-background row-start-2 ${chatOpen ? 'md:col-start-2' : 'col-start-1'}`,
  }
}

/** Keep both panes usable while honoring the resize rail's 15–85% bounds. */
export function clampWorkSplitRatio(ratio: number, width: number): number {
  const minPaneWidth = 240
  const minRatio = Math.max(0.15, Math.min(0.5, minPaneWidth / Math.max(width, minPaneWidth * 2)))
  return Math.max(minRatio, Math.min(Math.min(0.85, 1 - minRatio), ratio))
}

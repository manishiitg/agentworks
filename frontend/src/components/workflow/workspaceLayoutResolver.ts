import type { CSSProperties } from 'react'
import type { FocusedPane } from '../../stores/useWorkflowStore'
import type { ReportPreviewDevice } from '../../utils/reportPreviewPreference'

export interface WorkspaceLayoutInput {
  compactContext?: boolean
  /** Dashboard laptop preview keeps chat compact and lets the page fill the rest. */
  laptopPreview?: boolean
  showChatArea: boolean
  showWorkspacePane: boolean
  focusedPane: FocusedPane
  reportPreviewPreference: ReportPreviewDevice
  workspaceSplitRatio: number
  isWorkspaceViewActive: boolean
}

export interface WorkspaceLayout {
  /** The right workspace pane takes part in the layout. */
  workspacePaneVisible: boolean
  /** The chat pane renders. */
  showChat: boolean
  /** Chat open but pane hidden: the canvas renders standalone above chat. */
  renderCanvasStandalone: boolean
  splitLayoutClassName: string
  splitLayoutStyle: CSSProperties | undefined
  chatPaneClassName: string
  canvasPaneClassName: string
}

/**
 * The single layout decision point for the Builder split. Every class and
 * mount branch for the chat/canvas panes derives here from the same flags,
 * so no call site can combine them inconsistently.
 *
 * Visibility rule: a pane hidden below md must restore its own display at
 * md+ (`hidden md:flex` for flex panes). Restoring with `md:block` would
 * override flex (responsive variants win over base utilities), collapse
 * flex-1 scroll regions to content height, and freeze pane scrolling.
 */
/** Grid track for the workspace pane in Mobile preview. */
export const CONTEXT_PANE_COLUMN = 'clamp(240px, 26vw, 316px)'
export const MOBILE_PREVIEW_PANE_COLUMN = 'min(494px, calc(100% - 240px))'
export const LAPTOP_PREVIEW_COLUMNS = 'min(480px, calc(100% - 240px)) minmax(240px, 1fr)'

export function resolveWorkspaceLayout(input: WorkspaceLayoutInput): WorkspaceLayout {
  const {
    showChatArea,
    showWorkspacePane,
    focusedPane,
    reportPreviewPreference,
    workspaceSplitRatio,
    isWorkspaceViewActive,
  } = input

  const workspacePaneVisible = Boolean(input.compactContext) || !showChatArea || showWorkspacePane
  // The device tier controls the OUTER workspace pane for every workspace view,
  // not only Plan and Report.
  //   mobile  → preview/files 480px column, chat takes the rest (review-style)
  //   tablet  → equal 50/50 split between chat and preview
  //   laptop  → compact mobile-width chat beside the full desktop workspace
  //   default → 50/50 split (no preview pref, or running in non-preview views)
  const isResponsiveWorkspaceCanvas = showChatArea && workspacePaneVisible
  const previewPaneTier: 'mobile' | 'tablet' | 'laptop' | null = isResponsiveWorkspaceCanvas
    ? reportPreviewPreference === 'mobile'
      ? 'mobile'
      : reportPreviewPreference === 'tablet'
        ? 'tablet'
      : reportPreviewPreference === 'desktop'
        ? 'laptop'
        : null
    : null
  const shouldUseMobileReportPane = previewPaneTier === 'mobile'
  // A narrow workflow viewport is a single-pane surface. The shared toolbar
  // remains above it and focusedPane decides whether chat or workspace owns
  // the content row. At md+ both panes remain visible as the normal split.
  const chatPaneVisibilityClass =
    workspacePaneVisible && focusedPane === 'preview'
      ? 'hidden md:flex'
      : 'flex'
  const splitLayoutClassName = !showChatArea
    ? 'flex-1 min-h-0 flex flex-col'
    : 'flex-1 min-h-0 grid grid-cols-1 grid-rows-[auto_minmax(0,1fr)] md:[grid-template-columns:var(--workflow-split-columns)] md:transition-[grid-template-columns] md:duration-150 md:ease-out'
  // Mobile gives the workspace pane a phone-width column: the 480px report
  // frame plus its padding and border. A saved drag ratio must not widen it,
  // or the phone frame floats in empty space inside a wide pane.
  const splitLayoutStyle = showChatArea && workspacePaneVisible
    ? ({ '--workflow-split-columns': input.compactContext ? `minmax(240px, 1fr) ${CONTEXT_PANE_COLUMN}` : shouldUseMobileReportPane
      ? `minmax(240px, 1fr) ${MOBILE_PREVIEW_PANE_COLUMN}`
      : input.laptopPreview ? LAPTOP_PREVIEW_COLUMNS
      : `minmax(240px, ${workspaceSplitRatio}fr) minmax(240px, ${1 - workspaceSplitRatio}fr)` } as CSSProperties)
    : undefined
  const canvasPaneClassName = !showChatArea
    ? 'flex-1 min-h-0 min-w-0'
    : !workspacePaneVisible
      ? 'hidden'
      : `min-h-0 min-w-0 w-full col-start-1 row-start-2 md:w-auto md:col-start-2 md:row-start-2 ${focusedPane === 'chat' ? 'hidden md:flex' : ''} ${isWorkspaceViewActive && !input.compactContext ? 'md:border-l md:border-border' : ''}`
  const chatPaneClassName = `${chatPaneVisibilityClass} col-start-1 row-start-2 min-h-0 min-w-0 overflow-hidden flex-col bg-background transition-all duration-300 ${
    workspacePaneVisible && !input.compactContext
      ? `border-b border-border md:col-start-1 md:row-start-2 md:border-b-0 md:border-r ${shouldUseMobileReportPane ? 'flex-1 md:flex-[1.35]' : 'flex-1 basis-1/2'}`
      : 'flex-1'
  }`

  return {
    workspacePaneVisible,
    showChat: showChatArea,
    renderCanvasStandalone: showChatArea && !workspacePaneVisible,
    splitLayoutClassName,
    splitLayoutStyle,
    chatPaneClassName,
    canvasPaneClassName,
  }
}

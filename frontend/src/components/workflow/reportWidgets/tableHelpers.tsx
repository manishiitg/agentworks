// Helpers shared across Table / Cards / Pivot / Chart widgets:
//   - column inference (compact / numeric)
//   - cell value rendering
//   - widget palette + semantic-color resolution
//   - the compact-layout hook
//
// Pulled out of ReportViewer.tsx so the heavy widget renderers can each live
// in their own file without duplicating these utilities.

import { useEffect, useRef, useState } from 'react'
import { useReportFilePreviewStore } from '../../../stores/useReportFilePreviewStore'
import { useGlobalPresetStore } from '../../../stores/useGlobalPresetStore'

// Tracks whether the parent container is below the compact-layout threshold so
// table/cards widgets can switch from a multi-column grid to a stacked card
// layout. The hook returns a ref the consumer attaches to its outer wrapper.
export function useCompactWidgetLayout(maxWidth = 520) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [isCompact, setIsCompact] = useState(false)

  useEffect(() => {
    const node = ref.current
    if (!node) return

    const update = (width: number) => {
      setIsCompact(width <= maxWidth)
    }

    const measure = () => update(node.getBoundingClientRect().width)
    measure()

    if (typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(entries => {
        const entry = entries[0]
        if (!entry) return
        update(entry.contentRect.width)
      })
      observer.observe(node)
      return () => observer.disconnect()
    }

    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [maxWidth])

  return [ref, isCompact] as const
}

// Three-tier sibling of useCompactWidgetLayout. Used by the section grid
// container so a user-declared `columns: 12` collapses to ~half on tablets
// (640–960px) and 1 column on phones (<640px), matching the project's
// Tailwind sm/md breakpoints. Container-width based, not viewport-based, so
// it works inside split-pane / preview-mode layouts where the report tab
// is narrower than the viewport.
export type ContainerSizeTier = 'phone' | 'tablet' | 'desktop'

export function useContainerSizeTier(phoneMax = 640, tabletMax = 960) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [tier, setTier] = useState<ContainerSizeTier>('desktop')

  useEffect(() => {
    const node = ref.current
    if (!node) return

    const update = (width: number) => {
      if (width <= phoneMax) setTier('phone')
      else if (width <= tabletMax) setTier('tablet')
      else setTier('desktop')
    }

    const measure = () => update(node.getBoundingClientRect().width)
    measure()

    if (typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(entries => {
        const entry = entries[0]
        if (!entry) return
        update(entry.contentRect.width)
      })
      observer.observe(node)
      return () => observer.disconnect()
    }

    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [phoneMax, tabletMax])

  return [ref, tier] as const
}

// activeWorkflowWorkspacePath returns the folder path of the currently-active
// workflow preset, used to resolve workspace-relative file links in report cells
// when the widget didn't thread an explicit workspacePath.
function activeWorkflowWorkspacePath(): string {
  try {
    const ps = useGlobalPresetStore.getState()
    const id = ps.activePresetIds?.workflow
    if (!id) return ''
    const preset = ps.workflowPresets?.find(p => p.id === id)
    return (preset?.selectedFolder?.filepath || '').replace(/\/+$/, '')
  } catch {
    return ''
  }
}

// openReportFileInViewer opens a workspace file in the in-report preview modal
// (useReportFilePreviewStore). The report lives in the workflow layout, a
// different subtree from the chat workspace's file-content overlay, so it can't
// reuse useWorkspaceStore's viewer — that path opened an empty/invisible panel.
export function openReportFileInViewer(fullPath: string) {
  useReportFilePreviewStore.getState().show({ path: fullPath })
}

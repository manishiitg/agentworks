import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { usePointerDrag } from '../../../hooks/usePointerDrag'
import { writeWorkflowSplitPreference } from '../../../utils/reportPreviewPreference'
import { parentWorkspaceWidth, readParentSplitRatio, SPARKQUILL_PREVIEW_SCOPE } from './parentSplit'

export function useParentSplit(active: boolean, railOpen: boolean, windowWidth: number) {
  const bodyRef = useRef<HTMLDivElement>(null)
  const [bodyWidth, setBodyWidth] = useState(windowWidth)
  const available = Math.max(0, bodyWidth - (railOpen ? 272 : 0))
  const [ratio, setRatio] = useState(readParentSplitRatio)
  const [resizing, setResizing] = useState(false)
  const { start, stop } = usePointerDrag()
  const workspaceWidth = parentWorkspaceWidth(available, ratio)
  const shownRatio = available > 0 ? 1 - workspaceWidth / available : 0.5

  useEffect(() => {
    const body = bodyRef.current
    if (!active || !body) return
    const measure = () => setBodyWidth(body.getBoundingClientRect().width || windowWidth)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(body)
    return () => observer.disconnect()
  }, [active, windowWidth])
  useEffect(() => {
    const sync = () => setRatio(readParentSplitRatio())
    window.addEventListener('storage', sync)
    return () => {
      window.removeEventListener('storage', sync)
    }
  }, [])
  useEffect(() => { if (!active) { stop(); setResizing(false) } }, [active, stop])

  const commit = (next: number) => {
    const width = parentWorkspaceWidth(available, next)
    const clamped = available > 0 ? 1 - width / available : 0.5
    setRatio(clamped)
    writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, clamped)
  }
  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    const body = bodyRef.current
    if (!body || windowWidth <= 830 || available <= 0) return
    const right = body.getBoundingClientRect().right
    let last = shownRatio
    const started = start(event, {
      onMove: clientX => {
        const width = parentWorkspaceWidth(available, 1 - (right - clientX) / available)
        last = 1 - width / available
        setRatio(last)
      },
      onEnd: () => {
        setResizing(false)
        writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, last)
      },
    })
    if (started) setResizing(true)
  }
  return { bodyRef, workspaceWidth, ratio: shownRatio, onPointerDown,
    onStep: (delta: number) => commit(shownRatio + delta), resizing }
}

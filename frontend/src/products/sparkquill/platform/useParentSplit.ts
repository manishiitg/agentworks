import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { usePointerDrag } from '../../../hooks/usePointerDrag'
import {
  readReportPreviewPreference, writeReportPreviewPreference, writeWorkflowSplitPreference,
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, type ReportPreviewDevice,
} from '../../../utils/reportPreviewPreference'
import { parentWorkspaceWidth, readParentSplitRatio, SPARKQUILL_PREVIEW_SCOPE } from './parentSplit'

export function useParentSplit(active: boolean, railOpen: boolean, windowWidth: number) {
  const bodyRef = useRef<HTMLDivElement>(null)
  const [bodyWidth, setBodyWidth] = useState(windowWidth)
  const available = Math.max(0, bodyWidth - (railOpen ? 272 : 0))
  const [device, setDevice] = useState(() => readReportPreviewPreference(SPARKQUILL_PREVIEW_SCOPE))
  const [ratio, setRatio] = useState(() => readParentSplitRatio(device))
  const [resizing, setResizing] = useState(false)
  const [chatOpen, setChatOpen] = useState(true)
  const { start, stop } = usePointerDrag()
  const workspaceWidth = parentWorkspaceWidth(available, device, ratio)
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
    const sync = () => {
      const next = readReportPreviewPreference(SPARKQUILL_PREVIEW_SCOPE)
      setDevice(next)
      setRatio(readParentSplitRatio(next))
    }
    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync)
    window.addEventListener('storage', sync)
    return () => {
      window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync)
      window.removeEventListener('storage', sync)
    }
  }, [available])
  useEffect(() => { if (!active) { stop(); setResizing(false) } }, [active, stop])

  const commit = (next: number) => {
    const width = parentWorkspaceWidth(available, device, next)
    const clamped = available > 0 ? 1 - width / available : 0.5
    setRatio(clamped)
    writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, clamped, device)
  }
  const changeDevice = (next: ReportPreviewDevice) => {
    stop(); setResizing(false)
    setDevice(next)
    setRatio(readParentSplitRatio(next))
    writeReportPreviewPreference(SPARKQUILL_PREVIEW_SCOPE, next)
  }
  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    const body = bodyRef.current
    if (!body || windowWidth <= 830 || available <= 0) return
    const right = body.getBoundingClientRect().right
    let last = shownRatio
    const started = start(event, {
      onMove: clientX => {
        const width = parentWorkspaceWidth(available, device, 1 - (right - clientX) / available)
        last = 1 - width / available
        setRatio(last)
      },
      onEnd: () => {
        setResizing(false)
        writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, last, device)
      },
    })
    if (started) setResizing(true)
  }
  return { bodyRef, workspaceWidth, ratio: shownRatio, device, changeDevice, onPointerDown,
    onStep: (delta: number) => commit(shownRatio + delta), resizing, chatOpen, setChatOpen }
}

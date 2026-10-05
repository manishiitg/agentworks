import React, { useCallback, useRef, useImperativeHandle, forwardRef, useEffect, useLayoutEffect } from 'react'
import {
  ReactFlow,
  Background,
  useNodesState,
  useEdgesState,
  useReactFlow,
  getNodesBounds,
  getViewportForBounds,
  BackgroundVariant,
  type NodeChange,
  type OnNodeDrag
} from '@xyflow/react'
import { Braces, FileCode2, FileText, ListOrdered, Maximize, RefreshCw, Route, Settings, X } from 'lucide-react'
import '@xyflow/react/dist/style.css'

import { useModeStore } from '../../../stores/useModeStore'
import { nodeTypes } from '../nodes'
import {
  HandoffCrewNode,
  HandoffHumanInputNode,
  HandoffMessageSequenceNode,
  HandoffRoutingNode,
  HandoffStepNode,
  HandoffTodoTaskNode,
} from '../nodes/HandoffNodeWrappers'
import { getExecutionModeVisuals } from '../nodes/executionModeVisuals'
import { edgeTypes } from '../edges'
import { routeTraceFromEdge, traceRouteGraph, type RouteTrace } from './routeTrace'
import { usePlanTriggers } from './usePlanTriggers'
import { planNodesChanged, planEdgesChanged } from './planGraphUpdates'
import { useLiveRefetch } from '../../../hooks/useLiveRefetch'
import { appendTriggerCards, traceTriggerGraph } from './triggerLayout'
import { WorkflowTriggerNode, WorkflowTriggerHeading } from '../nodes/WorkflowTriggerNodes'
import { VariablesSidebar } from './VariablesSidebar'
import {
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT,
  isReportPreviewDevice,
  readReportPreviewPreference,
  type ReportPreviewDevice,
} from '../../../utils/reportPreviewPreference'
import type { PlanChanges } from '../hooks/usePlanData'
import { usePlanToFlow, type WorkflowNode, type WorkflowEdge, type WorkflowNodeData, type StepNodeData, type RoutingStepNodeData } from '../hooks/usePlanToFlow'
import { useWorkspaceViewData, type WorkflowImageExportFormat } from './workspaceViewData'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { WorkspacePanelGuideButton } from '../WorkspacePanelGuideButton'
import { PlanEmptyState } from '../PlanEmptyState'
import { useChatStore } from '../../../stores/useChatStore'
import { agentApi } from '../../../services/api'
import type { PlanStep } from '../../../utils/stepConfigMatching'
import { effectiveAgentItems, effectiveExecutionMode, effectiveExecutionModeReason, isCrewStep } from '../../../utils/stepConfigMatching'
import { CrewStepDetailSection } from './CrewStepDetailSection'
import {
  WORKFLOW_PLAN_STEP_FOCUS_EVENT,
  type WorkflowPlanStepFocusDetail,
} from '../../../utils/workflowPlanFocus'
import type { VariablesManifest } from '../../../services/api-types'
import { buildGroupFolderPath } from '../../../utils/workflowUtils'
import { MarkdownRenderer } from '../../ui/MarkdownRenderer'
import { planStepTypeLabel, scriptedStepFilePath } from './scriptedStepPresentation'
import { readSavedViewport, viewportStorageKey, writeSavedViewport } from './viewportPersistence'

// Duration to show highlights before clearing (in ms)
const HIGHLIGHT_DURATION = 4000
const SVG_EXPORT_SCALE = 3
const PNG_EXPORT_SCALE = 1
const PNG_EXPORT_MAX_SIDE = 16000
const PNG_EXPORT_MAX_PIXELS = 64_000_000
// Bump this whenever the auto-layout algorithm changes so stale saved layouts
// (custom drag positions from the old editor) are dropped and the new computed
// layout takes over. 2.8-wide-route-lanes: each major route gets its own
// column; route segments follow execution order.
const WORKFLOW_LAYOUT_VERSION = '2.9-unified-agent-nodes'
const FLOW_FIT_PADDING = 0.24
const FLOW_FIT_MIN_ZOOM = 0.08
const FLOW_FIT_MAX_ZOOM = 0.95
const FLOW_HEADER_CLEARANCE = 180
const FLOW_HEADER_NODE_HEIGHTS: Record<string, number> = {
  start: 40,
  variables: 160
}

const canvasNodeTypes = {
  ...nodeTypes,
  'workflow-trigger': WorkflowTriggerNode,
  'workflow-trigger-heading': WorkflowTriggerHeading,
  step: HandoffStepNode,
  todo_task: HandoffTodoTaskNode,
  human_input: HandoffHumanInputNode,
  routing: HandoffRoutingNode,
  branch: HandoffRoutingNode,
  message_sequence: HandoffMessageSequenceNode,
  crew: HandoffCrewNode,
} as const

function enforceWorkflowHeaderClearance(nodes: WorkflowNode[]): WorkflowNode[] {
  const startNode = nodes.find(node => node.id === 'start')
  const variablesNode = nodes.find(node => node.id === 'variables')
  if (!startNode || !variablesNode) return nodes

  const workflowNodes = nodes.filter(node =>
    node.id !== 'start' &&
    node.id !== 'variables' &&
    !node.id.startsWith('orphan-')
  )
  if (workflowNodes.length === 0) return nodes

  const headerBottom = Math.max(
    startNode.position.y + FLOW_HEADER_NODE_HEIGHTS.start,
    variablesNode.position.y + FLOW_HEADER_NODE_HEIGHTS.variables
  )
  const minWorkflowTop = headerBottom + FLOW_HEADER_CLEARANCE
  const currentWorkflowTop = Math.min(...workflowNodes.map(node => node.position.y))
  const shiftY = minWorkflowTop - currentWorkflowTop

  if (shiftY <= 0) return nodes

  return nodes.map(node => {
    if (node.id === 'start' || node.id === 'variables' || node.id.startsWith('orphan-')) {
      return node
    }
    return {
      ...node,
      position: {
        x: node.position.x,
        y: node.position.y + shiftY
      }
    }
  })
}

import type { ExecutionOptions } from '../../../services/api-types'


export interface WorkflowCanvasProps {
  workspacePath: string | null
  presetQueryId: string | null
  relayMode?: boolean
  relayOutputStepID?: string
  currentPhase?: string
  onStartPhase?: (phaseId: string, executionOptions?: ExecutionOptions) => void
  onCreatePlan?: () => void
  showChatArea?: boolean
  onToggleChatArea?: () => void
  toolbarOnly?: boolean  // When true, only render the toolbar (skip React Flow canvas for performance)
  sharedToolbar?: boolean
  chatTabsSlot?: React.ReactNode  // Chat tab strip rendered inline in the toolbar (shared-toolbar mode)
  workshopPanel?: React.ReactNode
  paneClassName?: string
  className?: string
  hideToolbar?: boolean
  readOnly?: boolean
  /** Embed only the reusable read-only Plan canvas, without global workflow view switching. */
  embeddedPlanOnly?: boolean
  openHumanActionsOnMount?: boolean
  assistantControl?: React.ReactNode
}

// On-pane controls for the preview (canvas) pane: a Plan/Report segmented switch
// plus a collapse button that hides the whole preview (chat goes full-width).
// Replaces the old top-toolbar Chat/Plan/Report view-switcher; the right-edge
// re-open tab (in WorkflowLayout) brings the pane back.
// Events the on-pane bar dispatches; ReportView listens and runs its own
// export / refresh. Keeps the report's handlers encapsulated while letting the
// shared bar trigger them.
export const WORKFLOW_REPORT_EXPORT_EVENT = 'workflow-report-export-requested'
export const WORKFLOW_REPORT_REFRESH_EVENT = 'workflow-report-refresh-requested'

type PreviewDevice = ReportPreviewDevice

// Shared device-width preference, synced across the on-pane bar, the report
// shell, and the plan/flow shell via REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT.
// Per-workflow device preference, scoped by `scopeId` (the workflow's
// workspacePath). A workflow with no saved choice defaults to tablet so the
// report and chat share the available workspace evenly.
// Exported for focused layout tests; this module also owns the production canvas.
// eslint-disable-next-line react-refresh/only-export-components
export function usePreviewDevice(scopeId?: string | null): PreviewDevice {
  const [pref, setPref] = React.useState<PreviewDevice>(() => readReportPreviewPreference(scopeId))
  React.useEffect(() => {
    // Re-read when the workflow (scope) changes.
    setPref(readReportPreviewPreference(scopeId))
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail
      if ((detail?.scopeId ?? null) !== (scopeId ?? null)) return
      const p = detail?.preference
      if (isReportPreviewDevice(p)) setPref(p)
    }
    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, handler)
    return () => window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, handler)
  }, [scopeId])
  return pref
}
// Tailwind shell width for a device preview (centered, constrained).
// eslint-disable-next-line react-refresh/only-export-components
export function previewDeviceShellClass(device: PreviewDevice): string {
  return device === 'mobile'
    ? 'mx-auto w-full max-w-[480px]'
    : device === 'tablet'
      ? 'w-full max-w-full'
    : 'w-full'
}

function formatJson(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

function waitForAnimationFrames(count = 2): Promise<void> {
  return new Promise(resolve => {
    const step = (remaining: number) => {
      if (remaining <= 0) {
        resolve()
        return
      }
      window.requestAnimationFrame(() => step(remaining - 1))
    }
    step(count)
  })
}

function triggerImageDownload(dataUrl: string, filename: string): void {
  const link = document.createElement('a')
  link.href = dataUrl
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}

function dataUrlPayload(dataUrl: string): string {
  const commaIndex = dataUrl.indexOf(',')
  return commaIndex >= 0 ? dataUrl.slice(commaIndex + 1) : dataUrl
}

function utf8ToBase64(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  const chunkSize = 8192
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const chunk = bytes.slice(index, index + chunkSize)
    binary += String.fromCharCode(...chunk)
  }
  return btoa(binary)
}

async function saveWorkflowImage(dataUrl: string, filename: string, format: WorkflowImageExportFormat): Promise<{ canceled?: boolean; filePath?: string } | null> {
  const electronAPI = (window as unknown as {
    electronAPI?: {
      saveFlowImage?: (payload: { filename: string; dataUrl: string; format: WorkflowImageExportFormat }) => Promise<{ canceled?: boolean; filePath?: string }>
    }
  }).electronAPI

  if (electronAPI?.saveFlowImage) {
    const payload = dataUrlPayload(dataUrl)
    if (format === 'png' && !payload.startsWith('iVBOR')) {
      throw new Error('PNG export payload was invalid. Reload the Electron window and try again.')
    }
    return electronAPI.saveFlowImage({
      filename,
      dataUrl: payload,
      format,
    })
  }

  triggerImageDownload(dataUrl, filename)
  return null
}

async function captureWorkflowImage(filename: string, format: WorkflowImageExportFormat, rect: DOMRect): Promise<{ canceled?: boolean; filePath?: string } | null> {
  const electronAPI = (window as unknown as {
    electronAPI?: {
      captureFlowImage?: (payload: {
        filename: string
        format: WorkflowImageExportFormat
        rect: { x: number; y: number; width: number; height: number }
      }) => Promise<{ canceled?: boolean; filePath?: string }>
    }
  }).electronAPI

  if (electronAPI && !electronAPI.captureFlowImage) {
    throw new Error('Quit and reopen Electron to load the updated flow export capture support.')
  }
  if (!electronAPI?.captureFlowImage) return null
  try {
    return await electronAPI.captureFlowImage({
      filename,
      format,
      rect: {
        x: rect.x,
        y: rect.y,
        width: rect.width,
        height: rect.height,
      },
    })
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    if (message.includes('No handler registered') || message.includes('capture-flow-image')) {
      throw new Error('Quit and reopen Electron to load the updated flow export capture support.')
    }
    throw error
  }
}

function inlineComputedStyles(source: Element, target: Element): void {
  if (target instanceof HTMLElement || target instanceof SVGElement) {
    const computed = window.getComputedStyle(source)
    for (const property of Array.from(computed)) {
      target.style.setProperty(property, computed.getPropertyValue(property), computed.getPropertyPriority(property))
    }
  }

  const sourceChildren = Array.from(source.children)
  const targetChildren = Array.from(target.children)
  sourceChildren.forEach((sourceChild, index) => {
    const targetChild = targetChildren[index]
    if (targetChild) inlineComputedStyles(sourceChild, targetChild)
  })
}

function renderFlowElementToSvg(flowElement: HTMLElement): string {
  const rect = flowElement.getBoundingClientRect()
  const width = Math.max(1, Math.ceil(rect.width))
  const height = Math.max(1, Math.ceil(rect.height))
  const exportWidth = width * SVG_EXPORT_SCALE
  const exportHeight = height * SVG_EXPORT_SCALE
  const clone = flowElement.cloneNode(true) as HTMLElement
  inlineComputedStyles(flowElement, clone)
  clone.setAttribute('xmlns', 'http://www.w3.org/1999/xhtml')
  clone.style.width = `${width}px`
  clone.style.height = `${height}px`
  clone.style.margin = '0'
  clone.style.position = 'relative'
  clone.querySelectorAll('[data-flow-exporting]').forEach(element => element.removeAttribute('data-flow-exporting'))

  const html = new XMLSerializer().serializeToString(clone)
  const svg = [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${exportWidth}" height="${exportHeight}" viewBox="0 0 ${width} ${height}">`,
    `<foreignObject width="100%" height="100%">${html}</foreignObject>`,
    '</svg>',
  ].join('')
  return `data:image/svg+xml;base64,${utf8ToBase64(svg)}`
}

function svgDataUrlToPngDataUrl(svgDataUrl: string, scale = PNG_EXPORT_SCALE): Promise<string> {
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.onload = () => {
      const sourceWidth = Math.max(1, image.naturalWidth || image.width)
      const sourceHeight = Math.max(1, image.naturalHeight || image.height)
      const maxScale = Math.min(
        scale,
        PNG_EXPORT_MAX_SIDE / sourceWidth,
        PNG_EXPORT_MAX_SIDE / sourceHeight,
        Math.sqrt(PNG_EXPORT_MAX_PIXELS / (sourceWidth * sourceHeight))
      )
      const safeScale = Math.max(0.1, maxScale)
      const canvas = document.createElement('canvas')
      canvas.width = Math.ceil(sourceWidth * safeScale)
      canvas.height = Math.ceil(sourceHeight * safeScale)
      const context = canvas.getContext('2d')
      if (!context) {
        reject(new Error('Could not create PNG export canvas'))
        return
      }
      context.scale(safeScale, safeScale)
      context.drawImage(image, 0, 0, sourceWidth, sourceHeight)
      const dataUrl = canvas.toDataURL('image/png')
      if (!dataUrl.startsWith('data:image/png;base64,')) {
        reject(new Error('Failed to create a valid PNG export'))
        return
      }
      resolve(dataUrl)
    }
    image.onerror = () => reject(new Error('Failed to render SVG export as PNG'))
    image.src = svgDataUrl
  })
}

function roundedRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, width: number, height: number, radius: number): void {
  const safeRadius = Math.min(radius, width / 2, height / 2)
  ctx.beginPath()
  ctx.moveTo(x + safeRadius, y)
  ctx.lineTo(x + width - safeRadius, y)
  ctx.quadraticCurveTo(x + width, y, x + width, y + safeRadius)
  ctx.lineTo(x + width, y + height - safeRadius)
  ctx.quadraticCurveTo(x + width, y + height, x + width - safeRadius, y + height)
  ctx.lineTo(x + safeRadius, y + height)
  ctx.quadraticCurveTo(x, y + height, x, y + height - safeRadius)
  ctx.lineTo(x, y + safeRadius)
  ctx.quadraticCurveTo(x, y, x + safeRadius, y)
  ctx.closePath()
}

function wrapCanvasText(ctx: CanvasRenderingContext2D, text: string, maxWidth: number, maxLines: number): string[] {
  const words = text.replace(/\s+/g, ' ').trim().split(' ').filter(Boolean)
  const lines: string[] = []
  let current = ''

  for (const word of words) {
    const next = current ? `${current} ${word}` : word
    if (ctx.measureText(next).width <= maxWidth) {
      current = next
      continue
    }
    if (current) lines.push(current)
    current = word
    if (lines.length >= maxLines) break
  }
  if (current && lines.length < maxLines) lines.push(current)
  if (lines.length === maxLines && words.length > 0) {
    const last = lines[maxLines - 1]
    if (ctx.measureText(last).width > maxWidth || words.join(' ') !== lines.join(' ')) {
      let truncated = last
      while (truncated.length > 0 && ctx.measureText(`${truncated}...`).width > maxWidth) {
        truncated = truncated.slice(0, -1)
      }
      lines[maxLines - 1] = `${truncated.trim()}...`
    }
  }
  return lines.length > 0 ? lines : ['Workflow step']
}

function getNodeRenderRect(node: WorkflowNode, nodeMap: Map<string, WorkflowNode>): { x: number; y: number; width: number; height: number } {
  let x = node.position?.x || 0
  let y = node.position?.y || 0
  const parentId = (node as { parentId?: string; parentNode?: string }).parentId || (node as { parentNode?: string }).parentNode
  if (parentId) {
    const parent = nodeMap.get(parentId)
    if (parent) {
      const parentRect = getNodeRenderRect(parent, nodeMap)
      x += parentRect.x
      y += parentRect.y
    }
  }

  const measured = (node as { measured?: { width?: number; height?: number }; width?: number; height?: number }).measured
  const width = measured?.width || (node as { width?: number }).width || (node.type === 'start' || node.type === 'end' ? 96 : 280)
  const height = measured?.height || (node as { height?: number }).height || (node.type === 'start' || node.type === 'end' ? 40 : 104)
  return { x, y, width, height }
}

function drawArrow(ctx: CanvasRenderingContext2D, fromX: number, fromY: number, toX: number, toY: number): void {
  const angle = Math.atan2(toY - fromY, toX - fromX)
  const size = 10
  ctx.beginPath()
  ctx.moveTo(toX, toY)
  ctx.lineTo(toX - size * Math.cos(angle - Math.PI / 6), toY - size * Math.sin(angle - Math.PI / 6))
  ctx.lineTo(toX - size * Math.cos(angle + Math.PI / 6), toY - size * Math.sin(angle + Math.PI / 6))
  ctx.closePath()
  ctx.fill()
}

function drawWorkflowNode(ctx: CanvasRenderingContext2D, node: WorkflowNode, rect: { x: number; y: number; width: number; height: number }, offsetX: number, offsetY: number): void {
  const x = rect.x + offsetX
  const y = rect.y + offsetY
  const width = rect.width
  const height = rect.height
  const data = node.data as WorkflowNodeData
  const title = typeof data.title === 'string' && data.title ? data.title : node.id
  const status = typeof data.status === 'string' ? data.status : 'pending'
  const isStart = node.type === 'start'
  const isEnd = node.type === 'end'
  const isSpecial = isStart || isEnd
  const borderColor =
    status === 'completed' ? '#22c55e' :
    status === 'failed' ? '#ef4444' :
    status === 'running' ? '#3b82f6' :
    isStart ? '#10b981' :
    '#64748b'

  ctx.save()
  ctx.shadowColor = 'rgba(15, 23, 42, 0.14)'
  ctx.shadowBlur = 14
  ctx.shadowOffsetY = 5
  roundedRectPath(ctx, x, y, width, height, isSpecial ? 20 : 10)
  ctx.fillStyle = isSpecial ? (isStart ? '#dcfce7' : '#f1f5f9') : '#ffffff'
  ctx.fill()
  ctx.shadowColor = 'transparent'
  ctx.lineWidth = 2
  ctx.strokeStyle = borderColor
  ctx.stroke()

  if (!isSpecial) {
    roundedRectPath(ctx, x, y, width, Math.min(58, height), 10)
    ctx.clip()
    ctx.fillStyle = '#f8fafc'
    ctx.fillRect(x, y, width, Math.min(58, height))
    ctx.restore()
    ctx.save()
  }

  ctx.fillStyle = isStart ? '#047857' : isEnd ? '#475569' : '#0f172a'
  ctx.font = isSpecial ? '600 16px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif' : '600 14px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
  ctx.textBaseline = 'top'

  if (isSpecial) {
    ctx.textAlign = 'center'
    ctx.fillText(isStart ? 'Start' : 'End', x + width / 2, y + 11)
  } else {
    const stepIndex = typeof (data as StepNodeData).stepIndex === 'number' ? (data as StepNodeData).stepIndex : null
    ctx.fillStyle = '#dbeafe'
    roundedRectPath(ctx, x + 14, y + 14, 32, 32, 7)
    ctx.fill()
    ctx.fillStyle = '#1d4ed8'
    ctx.font = '700 14px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText(stepIndex === null ? '#' : String(stepIndex + 1), x + 30, y + 22)

    ctx.textAlign = 'left'
    ctx.fillStyle = '#0f172a'
    ctx.font = '600 14px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
    const lines = wrapCanvasText(ctx, title, width - 66, 2)
    lines.forEach((line, index) => ctx.fillText(line, x + 56, y + 14 + index * 18))

    ctx.fillStyle = '#64748b'
    ctx.font = '500 11px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
    ctx.fillText(stepIndex === null ? node.type || 'node' : `Step ${stepIndex + 1}`, x + 16, y + height - 24)

    if (status !== 'pending') {
      ctx.fillStyle = borderColor
      ctx.font = '700 11px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
      ctx.textAlign = 'right'
      ctx.fillText(status.toUpperCase(), x + width - 16, y + height - 24)
    }
  }
  ctx.restore()
}

function escapeSvgText(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function wrapSvgText(text: string, maxChars: number, maxLines: number): string[] {
  const words = text.replace(/\s+/g, ' ').trim().split(' ').filter(Boolean)
  const lines: string[] = []
  let current = ''
  words.forEach(word => {
    const next = current ? `${current} ${word}` : word
    if (next.length <= maxChars) {
      current = next
      return
    }
    if (current) lines.push(current)
    current = word
  })
  if (current) lines.push(current)
  const limited = lines.slice(0, maxLines)
  if (lines.length > maxLines && limited.length > 0) {
    limited[limited.length - 1] = `${limited[limited.length - 1].slice(0, Math.max(0, maxChars - 3)).trim()}...`
  }
  return limited.length > 0 ? limited : ['Workflow step']
}

function renderFlowToSvg(nodes: WorkflowNode[], edges: WorkflowEdge[]): string {
  if (nodes.length === 0) throw new Error('No workflow nodes to export')

  const nodeMap = new Map(nodes.map(node => [node.id, node]))
  const rects = new Map(nodes.map(node => [node.id, getNodeRenderRect(node, nodeMap)]))
  const padding = 96
  const bounds = Array.from(rects.values()).reduce((acc, rect) => ({
    minX: Math.min(acc.minX, rect.x),
    minY: Math.min(acc.minY, rect.y),
    maxX: Math.max(acc.maxX, rect.x + rect.width),
    maxY: Math.max(acc.maxY, rect.y + rect.height),
  }), { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity })
  const width = Math.ceil(bounds.maxX - bounds.minX + padding * 2)
  const height = Math.ceil(bounds.maxY - bounds.minY + padding * 2)
  const offsetX = padding - bounds.minX
  const offsetY = padding - bounds.minY
  const isDark = document.documentElement.classList.contains('dark')
  const background = isDark ? '#111827' : '#f8fafc'
  const dot = isDark ? '#374151' : '#e2e8f0'
  const text = isDark ? '#f8fafc' : '#0f172a'
  const muted = isDark ? '#94a3b8' : '#64748b'
  const nodeFill = isDark ? '#1f2937' : '#ffffff'
  const nodeHeader = isDark ? '#111827' : '#f8fafc'

  const parts: string[] = [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-label="Automation flow">`,
    `<defs><pattern id="flow-grid" width="24" height="24" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="1" fill="${dot}"/></pattern><marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="5" orient="auto" markerUnits="strokeWidth"><path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8"/></marker></defs>`,
    `<rect width="100%" height="100%" fill="${background}"/>`,
    `<rect width="100%" height="100%" fill="url(#flow-grid)"/>`,
  ]

  edges.forEach(edge => {
    const source = rects.get(edge.source)
    const target = rects.get(edge.target)
    if (!source || !target) return
    const fromX = source.x + source.width / 2 + offsetX
    const fromY = source.y + source.height + offsetY
    const toX = target.x + target.width / 2 + offsetX
    const toY = target.y + offsetY
    const midY = fromY + Math.max(24, (toY - fromY) / 2)
    parts.push(`<path d="M ${fromX} ${fromY} C ${fromX} ${midY}, ${toX} ${midY}, ${toX} ${toY}" fill="none" stroke="#94a3b8" stroke-width="2" marker-end="url(#arrow)"/>`)
  })

  nodes.forEach(node => {
    const rect = rects.get(node.id)
    if (!rect) return
    const x = rect.x + offsetX
    const y = rect.y + offsetY
    const data = node.data as WorkflowNodeData
    const title = typeof data.title === 'string' && data.title ? data.title : node.id
    const status = typeof data.status === 'string' ? data.status : 'pending'
    const isStart = node.type === 'start'
    const isEnd = node.type === 'end'
    const isSpecial = isStart || isEnd
    const borderColor =
      status === 'completed' ? '#22c55e' :
      status === 'failed' ? '#ef4444' :
      status === 'running' ? '#3b82f6' :
      isStart ? '#10b981' :
      '#64748b'
    const fill = isSpecial ? (isStart ? (isDark ? '#064e3b' : '#dcfce7') : (isDark ? '#1f2937' : '#f1f5f9')) : nodeFill
    parts.push(`<g filter="drop-shadow(0 5px 10px rgba(15,23,42,0.18))">`)
    parts.push(`<rect x="${x}" y="${y}" width="${rect.width}" height="${rect.height}" rx="${isSpecial ? 20 : 10}" fill="${fill}" stroke="${borderColor}" stroke-width="2"/>`)
    if (isSpecial) {
      parts.push(`<text x="${x + rect.width / 2}" y="${y + 25}" text-anchor="middle" fill="${isStart ? '#10b981' : muted}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="16" font-weight="700">${isStart ? 'Start' : 'End'}</text>`)
    } else {
      parts.push(`<path d="M ${x + 10} ${y} H ${x + rect.width - 10} Q ${x + rect.width} ${y} ${x + rect.width} ${y + 10} V ${y + 58} H ${x} V ${y + 10} Q ${x} ${y} ${x + 10} ${y}" fill="${nodeHeader}"/>`)
      const stepIndex = typeof (data as StepNodeData).stepIndex === 'number' ? (data as StepNodeData).stepIndex : null
      parts.push(`<rect x="${x + 14}" y="${y + 14}" width="32" height="32" rx="7" fill="${isDark ? '#1e3a8a' : '#dbeafe'}"/>`)
      parts.push(`<text x="${x + 30}" y="${y + 35}" text-anchor="middle" fill="${isDark ? '#bfdbfe' : '#1d4ed8'}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="14" font-weight="800">${stepIndex === null ? '#' : String(stepIndex + 1)}</text>`)
      wrapSvgText(title, Math.max(14, Math.floor((rect.width - 70) / 8)), 2).forEach((line, index) => {
        parts.push(`<text x="${x + 56}" y="${y + 28 + index * 18}" fill="${text}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="14" font-weight="700">${escapeSvgText(line)}</text>`)
      })
      parts.push(`<text x="${x + 16}" y="${y + rect.height - 17}" fill="${muted}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="11" font-weight="600">${stepIndex === null ? escapeSvgText(node.type || 'node') : `Step ${stepIndex + 1}`}</text>`)
      if (status !== 'pending') {
        parts.push(`<text x="${x + rect.width - 16}" y="${y + rect.height - 17}" text-anchor="end" fill="${borderColor}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="11" font-weight="800">${escapeSvgText(status.toUpperCase())}</text>`)
      }
    }
    parts.push(`</g>`)
  })

  parts.push(`<text x="${padding}" y="${height - 32}" fill="${muted}" font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" font-size="13" font-weight="700">Workflow flow - ${nodes.length} steps</text>`)
  parts.push('</svg>')
  const svg = parts.join('')
  return `data:image/svg+xml;base64,${utf8ToBase64(svg)}`
}

function renderFlowToImage(nodes: WorkflowNode[], edges: WorkflowEdge[], format: WorkflowImageExportFormat): string {
  if (format === 'svg') return renderFlowToSvg(nodes, edges)
  if (nodes.length === 0) throw new Error('No workflow nodes to export')

  const nodeMap = new Map(nodes.map(node => [node.id, node]))
  const rects = new Map(nodes.map(node => [node.id, getNodeRenderRect(node, nodeMap)]))
  const padding = 80
  const bounds = Array.from(rects.values()).reduce((acc, rect) => ({
    minX: Math.min(acc.minX, rect.x),
    minY: Math.min(acc.minY, rect.y),
    maxX: Math.max(acc.maxX, rect.x + rect.width),
    maxY: Math.max(acc.maxY, rect.y + rect.height),
  }), { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity })

  const width = Math.ceil(bounds.maxX - bounds.minX + padding * 2)
  const height = Math.ceil(bounds.maxY - bounds.minY + padding * 2)
  const pixelRatio = Math.min(window.devicePixelRatio || 1, 2)
  const canvas = document.createElement('canvas')
  canvas.width = Math.max(1, Math.round(width * pixelRatio))
  canvas.height = Math.max(1, Math.round(height * pixelRatio))

  const context = canvas.getContext('2d')
  if (!context) throw new Error('Could not create image export canvas')

  context.scale(pixelRatio, pixelRatio)
  context.fillStyle = format === 'jpeg' ? '#ffffff' : '#f8fafc'
  context.fillRect(0, 0, width, height)

  context.fillStyle = '#e2e8f0'
  for (let x = 0; x < width; x += 24) {
    for (let y = 0; y < height; y += 24) {
      context.beginPath()
      context.arc(x, y, 1, 0, Math.PI * 2)
      context.fill()
    }
  }

  const offsetX = padding - bounds.minX
  const offsetY = padding - bounds.minY

  context.strokeStyle = '#94a3b8'
  context.fillStyle = '#94a3b8'
  context.lineWidth = 2
  edges.forEach(edge => {
    const source = rects.get(edge.source)
    const target = rects.get(edge.target)
    if (!source || !target) return
    const fromX = source.x + source.width / 2 + offsetX
    const fromY = source.y + source.height + offsetY
    const toX = target.x + target.width / 2 + offsetX
    const toY = target.y + offsetY
    const midY = fromY + Math.max(24, (toY - fromY) / 2)
    context.beginPath()
    context.moveTo(fromX, fromY)
    context.bezierCurveTo(fromX, midY, toX, midY, toX, toY)
    context.stroke()
    drawArrow(context, fromX, fromY, toX, toY)
  })

  nodes.forEach(node => {
    const rect = rects.get(node.id)
    if (rect) drawWorkflowNode(context, node, rect, offsetX, offsetY)
  })

  context.fillStyle = '#475569'
  context.font = '600 13px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
  context.textAlign = 'left'
  context.fillText(`Workflow flow • ${nodes.length} steps`, padding, height - 32)

  return canvas.toDataURL(format === 'jpeg' ? 'image/jpeg' : 'image/png', 0.95)
}

function workflowExportFilename(workspacePath: string | null, format: WorkflowImageExportFormat): string {
  const workflowName = (workspacePath || 'workflow')
    .split('/')
    .filter(Boolean)
    .pop() || 'workflow'
  const safeName = workflowName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'workflow'
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
  return `${safeName}-flow-${timestamp}.${format === 'jpeg' ? 'jpg' : format}`
}

function DetailSection({
  icon: Icon,
  title,
  children,
}: {
  icon: React.ElementType
  title: string
  children: React.ReactNode
}) {
  return (
    <section className="border-b border-border px-4 py-3 last:border-b-0">
      <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        {title}
      </div>
      {children}
    </section>
  )
}

function ReadOnlyStepDetailPanel({
  node,
  workspacePath,
  onClose,
}: {
  node: WorkflowNode
  workspacePath: string | null
  onClose: () => void
}) {
  const data = node.data as WorkflowNodeData
  const step = 'step' in data && data.step ? data.step as PlanStep : null
  const title = (typeof data.title === 'string' && data.title) || step?.title || node.id
  const rawType = step?.type || node.type || 'node'
  const type = planStepTypeLabel(rawType)
  const routes = step?.type === 'routing' || step?.type === 'branch'
    ? step.routes
    : (step?.type === 'todo_task' || step?.type === 'orchestrator' || step?.type === 'message_sequence')
      ? step.predefined_routes
      : undefined
  const sequenceItems = step && node.type === 'message_sequence' ? effectiveAgentItems(step) : undefined
  const validationSchema = step?.validation_schema || ('validation_schema' in data ? data.validation_schema : undefined)
  const agentConfigs = step?.agent_configs
  const declaredExecutionMode = effectiveExecutionMode(step)
  const declaredExecutionModeReason = effectiveExecutionModeReason(step)
  const declaredExecutionModeVisuals = getExecutionModeVisuals(declaredExecutionMode, declaredExecutionModeReason)
  const DeclaredExecutionModeIcon = declaredExecutionModeVisuals.Icon
  const contextInputs = step?.context_dependencies || []
  const contextOutput = step?.context_output
  const contextOutputs = Array.isArray(contextOutput) ? contextOutput : (contextOutput ? [contextOutput] : [])
  const routingQuestion = typeof data.routing_question === 'string' ? data.routing_question : undefined
  const [openingScript, setOpeningScript] = React.useState(false)
  const openScript = React.useCallback(async () => {
    if (!step?.id || step.type !== 'regular' || openingScript) return

    setOpeningScript(true)
    try {
      let codeLayoutVersion = 0
      if (workspacePath) {
        const workflowConfigPath = `${workspacePath.replace(/\/+$/g, '')}/workflow.json`
        const response = await agentApi.getPlannerFileContent(workflowConfigPath)
        const content = response.success ? response.data?.content : undefined
        if (content != null) {
          const parsed = typeof content === 'string' ? JSON.parse(content) : content
          const configuredVersion = Number((parsed as { code_layout_version?: unknown }).code_layout_version)
          if (Number.isFinite(configuredVersion)) codeLayoutVersion = configuredVersion
        }
      }
      useWorkflowStore.getState().openWorkspaceView('files', scriptedStepFilePath(step.id, codeLayoutVersion))
    } catch {
      // An absent or unreadable workflow.json is the supported legacy layout.
      useWorkflowStore.getState().openWorkspaceView('files', scriptedStepFilePath(step.id, 0))
    } finally {
      setOpeningScript(false)
    }
  }, [openingScript, step, workspacePath])

  return (
    <aside
      data-ui-plan-step={step?.id || node.id}
      aria-label={`Step details: ${title}`}
      className="flex h-full w-full max-w-full shrink-0 flex-col border-l border-border bg-background shadow-xl sm:w-[80%] sm:max-w-[80%]"
    >
      <div className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="mb-1 flex flex-wrap items-center gap-2">
            <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{type}</span>
            {step?.id && <span className="truncate font-mono text-[10px] text-muted-foreground">{step.id}</span>}
          </div>
          <h3 className="truncate text-sm font-semibold text-foreground">{title}</h3>
          {step?.type === 'regular' && (
            <div className="mt-2">
              <button
                type="button"
                onClick={() => void openScript()}
                disabled={openingScript}
                className="inline-flex items-center gap-1.5 rounded-md border border-border bg-muted/30 px-2 py-1 font-mono text-[11px] text-foreground/80 transition-colors hover:bg-muted hover:text-foreground disabled:cursor-wait disabled:opacity-60"
                aria-label={`Open code file for ${title}`}
                title="Open this scripted step's code file"
              >
                <FileCode2 className="h-3.5 w-3.5" />
                <span>{openingScript ? 'Opening…' : 'main.py'}</span>
              </button>
              <div className="mt-2 flex max-w-2xl items-start gap-2 rounded-md border border-blue-500/20 bg-blue-500/5 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
                <RefreshCw className="mt-0.5 h-3.5 w-3.5 shrink-0 text-blue-500" />
                <span>
                  {step.script_only
                    ? 'Runs the saved Python code exactly. Errors stop the Relay; no agent repairs the script.'
                    : agentConfigs?.lock_code
                    ? 'Runs the saved Python code first. Automatic repair is off because this code is locked.'
                    : 'Runs the saved Python code first. If it fails, an agent uses the description below to repair the code and retry.'}
                </span>
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {step?.type === 'message_sequence' && step.authored_prompt && (
          <DetailSection icon={Braces} title="Authored agent">
            <p className="mb-1 text-xs text-muted-foreground">Exact system prompt</p>
            <pre className="overflow-x-auto whitespace-pre-wrap rounded bg-muted/40 px-2 py-2 text-xs text-foreground">{step.system_prompt}</pre>
            <p className="mt-2 text-xs text-muted-foreground">User messages below run in order. Variables render from the trigger input and prior JSON outputs.</p>
          </DetailSection>
        )}
        {step?.type === 'branch' && step.value_path && (
          <DetailSection icon={Braces} title="Decision rule">
            <div className="text-xs"><span className="text-muted-foreground">Value: </span><code>{step.value_path}</code></div>
            <pre className="mt-2 overflow-x-auto whitespace-pre-wrap rounded bg-muted/40 px-2 py-2 text-xs text-foreground">{formatJson(step.value_cases || {})}</pre>
            {step.default_route_id && <div className="mt-1 text-xs text-muted-foreground">Default route: {step.default_route_id}</div>}
          </DetailSection>
        )}
        {step?.description && (
          <DetailSection icon={FileText} title="Description">
            <div className="prose prose-sm max-w-none dark:prose-invert prose-p:my-1 prose-headings:my-2 prose-ul:my-1 prose-ol:my-1">
              <MarkdownRenderer content={step.description} className="max-w-none" />
            </div>
          </DetailSection>
        )}

        {step && isCrewStep(step) && (
          <CrewStepDetailSection step={step} workspacePath={workspacePath} />
        )}

        {declaredExecutionMode && (
          <DetailSection icon={Settings} title="Execution Mode">
            <div className="flex flex-wrap items-center gap-2 text-xs leading-relaxed">
              {DeclaredExecutionModeIcon && (
                <span
                  className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md ${declaredExecutionModeVisuals.iconBoxClassName}`}
                  title={declaredExecutionModeVisuals.title}
                >
                  <DeclaredExecutionModeIcon className="h-3.5 w-3.5" />
                </span>
              )}
              <span className="font-medium text-foreground">
                {declaredExecutionModeVisuals.label || declaredExecutionMode}
              </span>
              {declaredExecutionModeReason && (
                <span className="text-muted-foreground">{declaredExecutionModeReason}</span>
              )}
            </div>
          </DetailSection>
        )}

        {(routingQuestion || routes?.length) && (
          <DetailSection icon={Route} title={step?.type === 'branch' ? 'Branch' : 'Routing'}>
            {routingQuestion && <p className="mb-2 text-sm text-foreground/85">{routingQuestion}</p>}
            {routes?.length ? (
              <div className="space-y-2">
                {routes.map((route, index) => (
                  <div key={route.route_id || route.route_name || index} className="rounded-md border border-border bg-muted/25 p-2">
                    <div className="text-xs font-semibold text-foreground">{route.route_name || route.route_id || `Route ${index + 1}`}</div>
                    {'condition' in route && route.condition && (
                      <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{route.condition}</div>
                    )}
                  </div>
                ))}
              </div>
            ) : null}
          </DetailSection>
        )}

        {sequenceItems?.length ? (
          <DetailSection icon={ListOrdered} title={`${step?.type === 'message_sequence' && step.authored_prompt ? 'User messages' : 'Agent instructions'} (${sequenceItems.length})`}>
            <p className="mb-2 text-xs text-muted-foreground">{step?.type === 'message_sequence' && step.authored_prompt ? 'User messages sent to this agent in order. Variables are filled from the input and earlier step outputs.' : 'Ordered items the step runs top to bottom.'}</p>
            <ol className="space-y-2">
              {sequenceItems.map((item, index) => {
                // Normalize so it reads consistently with the Agent node card.
                const kind = (!item.type || item.type === 'user_message') ? (step?.type === 'message_sequence' && step.authored_prompt ? 'user message' : 'message') : item.type
                const subKind = item.kind && item.kind !== 'execution' ? item.kind : undefined
                const chipClass =
                  kind === 'prevalidation' ? 'bg-amber-500/15 text-amber-600 dark:text-amber-400'
                    : kind === 'foreach' ? 'bg-violet-500/15 text-violet-600 dark:text-violet-400'
                      : 'bg-blue-500/15 text-blue-600 dark:text-blue-400'
                const writes = item.write_access
                const accessBits = [
                  writes?.db ? 'db' : null,
                  writes?.knowledgebase ? 'kb' : null,
                  writes?.learnings ? 'learnings' : null,
                ].filter(Boolean) as string[]
                return (
                  <li key={item.id || index} className="rounded-md border border-border bg-muted/25 p-2">
                    <div className="mb-1 flex flex-wrap items-center gap-2">
                      <span className="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-muted-foreground/20 text-[10px] font-semibold text-foreground/70">{index + 1}</span>
                      <span className={`rounded px-1.5 py-0.5 font-mono text-[10px] uppercase ${chipClass}`}>{String(kind).replace(/_/g, ' ')}</span>
                      {subKind && <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{subKind}</span>}
                      {item.title && <span className="truncate text-xs font-medium text-foreground">{item.title}</span>}
                    </div>
                    {item.message && <div className="text-xs leading-relaxed text-foreground/85">{item.message}</div>}
                    {kind === 'scripted' && (
                      <div className="mt-1 text-xs text-foreground/80">
                        <p>{(item.max_parallel || 1) > 1 ? `Up to ${item.max_parallel} scripts in parallel` : 'Scripts run sequentially'} · waits for all results</p>
                        <ul className="mt-1 space-y-1">
                          {item.scripted_steps?.map(call => (
                            <li key={call.id}>
                              <span className="font-mono">{call.id}: {call.step_id}</span>
                              {call.parameters && <pre className="overflow-x-auto whitespace-pre-wrap text-[10px]">{formatJson(call.parameters)}</pre>}
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                    {kind === 'foreach' && (
                      <div className="mt-1 text-xs text-foreground/80">
                        for each row in <span className="font-mono text-foreground">{item.source_sql || item.source || '—'}</span>
                        {item.source_path ? <> at <span className="font-mono text-foreground">{item.source_path}</span></> : null}
                        {item.max_iterations ? <> (max {item.max_iterations})</> : null}
                      </div>
                    )}
                    {accessBits.length > 0 && (
                      <div className="mt-1 text-[10px] text-muted-foreground">write access: {accessBits.join(', ')}</div>
                    )}
                    {(item.validation_schema || item.prevalidation) && (
                      <pre className="mt-1 overflow-x-auto whitespace-pre-wrap rounded bg-muted/40 px-2 py-1 font-mono text-[10px] leading-relaxed text-foreground/70">{formatJson(item.validation_schema || item.prevalidation)}</pre>
                    )}
                  </li>
                )
              })}
            </ol>
          </DetailSection>
        ) : null}

        {(contextInputs.length > 0 || contextOutputs.length > 0) && (
          <DetailSection icon={FileText} title="Context">
            <div className="space-y-2 text-xs">
              {contextInputs.length > 0 && (
                <div>
                  <div className="mb-1 font-medium text-muted-foreground">Reads</div>
                  <div className="space-y-1">{contextInputs.map(path => <div key={path} className="rounded bg-muted/40 px-2 py-1 font-mono text-foreground/80">{path}</div>)}</div>
                </div>
              )}
              {contextOutputs.length > 0 && (
                <div>
                  <div className="mb-1 font-medium text-muted-foreground">Writes</div>
                  <div className="space-y-1">{contextOutputs.map(path => <div key={path} className="rounded bg-muted/40 px-2 py-1 font-mono text-foreground/80">{path}</div>)}</div>
                </div>
              )}
            </div>
          </DetailSection>
        )}

        {!!validationSchema && (
          <DetailSection icon={Braces} title="Schema">
            <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-muted/35 p-2 font-mono text-[11px] leading-relaxed text-foreground/80">
              {formatJson(validationSchema)}
            </pre>
          </DetailSection>
        )}

        {agentConfigs && (
          <DetailSection icon={Settings} title="Step Config">
            <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-muted/35 p-2 font-mono text-[11px] leading-relaxed text-foreground/80">
              {formatJson(agentConfigs)}
            </pre>
          </DetailSection>
        )}

        {!step && (
          <DetailSection icon={FileText} title="Details">
            <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-muted/35 p-2 font-mono text-[11px] leading-relaxed text-foreground/80">
              {formatJson(data)}
            </pre>
          </DetailSection>
        )}
      </div>

      <div className="shrink-0 border-t border-border bg-background/95 px-4 py-3 backdrop-blur">
        <button
          onClick={onClose}
          className="inline-flex w-full items-center justify-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          aria-label="Close step details"
          title="Close"
        >
          <X className="h-3.5 w-3.5" />
          <span>Close</span>
        </button>
      </div>
    </aside>
  )
}

// Ref interface for external control of the canvas
export interface WorkflowCanvasRef {
  refresh: (changedStepIDs?: string[], deletedStepIDs?: string[]) => Promise<PlanChanges | null>
  getStepCount: () => number
  focusStep: (stepId: string) => void  // Selects the step and opens its details
  // Re-fits the viewport. React Flow sizes itself from its own wrapper's
  // ResizeObserver; when that wrapper was display:none (WorkflowLayout's
  // chat-focused single-pane mode on a narrow window) and becomes visible
  // again, the observer does not reliably resume with an accurate reading,
  // leaving the canvas rendered blank at its last (often zero) size. Call
  // this after such a pane becomes visible again to force a real refit,
  // matching the existing device-preview-resize effect below.
  resyncViewport: () => void
}

// The React Flow plan canvas. WorkspaceViewHost owns the toolbar, the pane
// wrapper, and the shared data (read here through useWorkspaceViewData); this
// component renders only the pane content for the flow view.
const WorkflowCanvasInner = forwardRef<WorkflowCanvasRef, WorkflowCanvasProps>(({
  workspacePath,
  presetQueryId,
  relayMode = false,
  relayOutputStepID,
  onCreatePlan,
  showChatArea = false,
  toolbarOnly = false,
  readOnly = false,
  embeddedPlanOnly = false,
  assistantControl,
}, ref) => {
  const reactFlowWrapper = useRef<HTMLDivElement>(null)
  const highlightTimeoutRef = useRef<NodeJS.Timeout | null>(null)
  const { setViewport, getNode, updateNode, fitView, getViewport } = useReactFlow()
  const hasInitializedView = React.useRef(false)

  // --- Performance diagnostics for workflow switching ---
  const renderCountRef = useRef(0)
  const lastPresetRef = useRef(presetQueryId)
  renderCountRef.current++
  if (lastPresetRef.current !== presetQueryId) {
    console.log(`%c[WorkflowCanvas] Preset switched: ${lastPresetRef.current?.slice(0,8)} → ${presetQueryId?.slice(0,8)}`, 'color: orange; font-weight: bold')
    lastPresetRef.current = presetQueryId
  }
  if (renderCountRef.current % 50 === 0) {
    console.log(`%c[WorkflowCanvas] render #${renderCountRef.current} (preset: ${presetQueryId?.slice(0,8)})`, 'color: gray')
  }
  // Store step ID to focus on after nodes update (from backend plan changes)
  const pendingFocusStepIdRef = React.useRef<string | null>(null)
  // Store current viewport state (x, y, zoom) to preserve it during refresh
  const viewportStateRef = React.useRef<{ x: number; y: number; zoom: number } | null>(null)
  const viewportSaveTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null)

  // Flow view is vertical-only. WorkspaceViewHost only mounts this component
  // for the Plan view, so there is no per-mode branching in here.
  const layoutDirection: 'LR' | 'TB' = 'TB'
  const triggers = usePlanTriggers(workspacePath, toolbarOnly)
  const refreshTriggers = triggers.refresh
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const selectedGroupIds = useWorkflowStore(state => state.selectedGroupIds)
  const setSelectedRunFolder = useWorkflowStore(state => state.setSelectedRunFolder)

  // No explicit view: the default builder workspace.
  const isBuilderWorkspace = workflowWorkspaceView === null

  // Generate localStorage key for viewport state (workspace-specific)
  const getViewportStorageKey = React.useCallback(() => {
    return viewportStorageKey(workspacePath)
  }, [workspacePath])

  // PERF: Debounced viewport change handler — saves to localStorage at most once per 500ms
  // instead of on every pixel of pan/zoom (which was causing excessive localStorage writes)
  const onViewportChange = React.useCallback((viewport: { x: number; y: number; zoom: number }) => {
    viewportStateRef.current = { x: viewport.x, y: viewport.y, zoom: viewport.zoom }
    if (hasInitializedView.current) {
      if (viewportSaveTimerRef.current) clearTimeout(viewportSaveTimerRef.current)
      viewportSaveTimerRef.current = setTimeout(() => {
        if (viewportStateRef.current) {
          writeSavedViewport(getViewportStorageKey(), viewportStateRef.current)
        }
      }, 500)
    }
  }, [getViewportStorageKey])

  // Flush a pending debounced viewport save on unmount so a refresh right
  // after panning still keeps the last position.
  const flushViewportSaveRef = React.useRef(() => {})
  flushViewportSaveRef.current = () => {
    if (viewportSaveTimerRef.current) {
      clearTimeout(viewportSaveTimerRef.current)
      viewportSaveTimerRef.current = null
    }
    if (hasInitializedView.current && viewportStateRef.current) {
      writeSavedViewport(getViewportStorageKey(), viewportStateRef.current)
    }
  }
  React.useEffect(() => () => flushViewportSaveRef.current(), [])

  // Get workflow layout file path
  const getLayoutFilePath = React.useCallback(() => {
    if (!workspacePath) return null
    return `${workspacePath}/planning/workflow_layout.json`
  }, [workspacePath])

  // Load saved node positions and offsets from workspace
  const loadSavedLayout = React.useCallback(async (): Promise<{
    positions: Map<string, { x: number; y: number }>;
    offsets: Map<string, { parentId: string; dx: number; dy: number }>;
    layoutDirection?: 'LR' | 'TB';
  } | null> => {
    const layoutPath = getLayoutFilePath()
    if (!layoutPath) return null

    try {
      const response = await agentApi.getPlannerFileContent(layoutPath)
      if (response.success && response.data?.content) {
        const layout = JSON.parse(response.data.content)
        if (layout.version !== WORKFLOW_LAYOUT_VERSION) {
          console.log('[WorkflowCanvas] Ignoring saved layout from older algorithm:', layout.version || 'unknown')
          // A live refresh otherwise restores the positions captured before the
          // version change, defeating the new auto-layout until a full reload.
          currentPositionsRef.current.clear()
          currentOffsetsRef.current.clear()
          return null
        }
        const positions = new Map<string, { x: number; y: number }>()
        const offsets = new Map<string, { parentId: string; dx: number; dy: number }>()
        let savedDirection: 'LR' | 'TB' | undefined
        
        if (layout.nodePositions && typeof layout.nodePositions === 'object') {
          Object.entries(layout.nodePositions).forEach(([nodeId, pos]: [string, unknown]) => {
            // CRITICAL: Never load saved positions for header nodes
            // They must always use the enforced horizontal layout from usePlanToFlow
            if (nodeId === 'start' || nodeId === 'variables') {
              return // Skip header nodes
            }
            if (pos && typeof pos === 'object' && 'x' in pos && 'y' in pos) {
              positions.set(nodeId, { x: (pos as { x: number }).x, y: (pos as { x: number; y: number }).y })
            }
          })
        }
        
        // Load child offsets if available (version 1.1+)
        if (layout.childOffsets && typeof layout.childOffsets === 'object') {
          Object.entries(layout.childOffsets).forEach(([nodeId, offset]: [string, unknown]) => {
            if (offset && typeof offset === 'object' && 'parentId' in offset && 'dx' in offset && 'dy' in offset) {
              offsets.set(nodeId, {
                parentId: (offset as { parentId: string }).parentId,
                dx: (offset as { dx: number }).dx,
                dy: (offset as { dy: number }).dy
              })
            }
          })
        }

        // Load layout direction if available (version 1.2+)
        if (layout.layoutDirection === 'LR' || layout.layoutDirection === 'TB') {
          savedDirection = layout.layoutDirection
        }
        
        console.log('[WorkflowCanvas] 📂 Loaded saved layout:', positions.size, 'positions,', offsets.size, 'offsets, direction:', savedDirection)
        return { positions, offsets, layoutDirection: savedDirection }
      }
    } catch {
      // File doesn't exist yet - that's okay
      // No saved layout found - this is normal for new workspaces
    }
    return null
  }, [getLayoutFilePath])

  // Toolbar data is loaded once by WorkspaceViewHost and shared through
  // context; this canvas only reads it. The variables manifest lives there too
  // because the toolbar shows it.
  const viewData = useWorkspaceViewData()
  const { variablesManifest, isLoadingVariables, setVariablesManifest } = viewData
  const [showVariablesSidebar, setShowVariablesSidebar] = React.useState(false)
  const [selectedFlowNodeID, setSelectedFlowNodeID] = React.useState<{ workspace: string | null | undefined; id: string } | null>(null)
  const planWorkspaceRef = React.useRef(workspacePath)
  planWorkspaceRef.current = workspacePath
  const pendingPlanStepFocusRef = React.useRef<string | null>(null)
  
  // Workflow store actions
  const setVariablesManifestInStore = useWorkflowStore.getState().setVariablesManifest
  const selectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  // Device-width preview also constrains the plan/flow pane (centered shell).
  const previewDevice = usePreviewDevice(workspacePath)
  // Changing the device width resizes the flow pane; re-fit the diagram after the
  // CSS width transition (~300ms) so it recenters into the new width.
  useEffect(() => {
    if (embeddedPlanOnly || toolbarOnly || previewDevice === 'tablet') return
    const t = setTimeout(() => {
      try { void fitView({ padding: FLOW_FIT_PADDING, duration: 350, minZoom: FLOW_FIT_MIN_ZOOM, maxZoom: FLOW_FIT_MAX_ZOOM }) } catch { /* ignore */ }
    }, 360)
    return () => clearTimeout(t)
  }, [embeddedPlanOnly, previewDevice, fitView, toolbarOnly])
  // Highlight execution folder in workspace when selectedRunFolder changes
  // This ensures workspace shows the correct group folder during multi-group execution
  const highlightFile = useWorkspaceStore(state => state.highlightFile)
  const prevSelectedRunFolderRef = useRef<string | null>(null)
  useEffect(() => {
    // Reset ref if selectedRunFolder is cleared
    if (!selectedRunFolder || selectedRunFolder === 'new') {
      prevSelectedRunFolderRef.current = null
      return
    }

    // Only highlight if selectedRunFolder actually changed and is valid.
    // Guard: skip when the workflow canvas isn't the active mode — this effect
    // can fire while the canvas stays mounted in other modes (e.g. multi-agent
    // chat), and the fetchFiles(workspacePath) below would overwrite the
    // workspace state with workflow-scoped files, leaving the multi-agent file
    // panel empty after the filter pass.
    const activeMode = useModeStore.getState().selectedModeCategory
    if (activeMode !== 'workflow') {
      return
    }

    if (selectedRunFolder !== prevSelectedRunFolderRef.current && workspacePath) {
      prevSelectedRunFolderRef.current = selectedRunFolder

      // Construct execution folder path
      const executionPath = `${workspacePath}/runs/${selectedRunFolder}/execution`

      // PERF: Use getState() to avoid fetchFiles reference changes triggering this effect
      useWorkspaceStore.getState().fetchFiles(workspacePath || undefined).then(() => {
        // Small delay to ensure files are loaded before highlighting
        setTimeout(() => {
          highlightFile(executionPath)
        }, 100)
      }).catch(err => {
        console.error('[WorkflowCanvas] Failed to refresh files before highlighting:', err)
        // Still try to highlight even if refresh fails
        highlightFile(executionPath)
      })
    }
  }, [selectedRunFolder, workspacePath, highlightFile])

  // Plan, execution status, and workspace state come from the
  // host so that switching views never re-runs their loaders.
  const {
    planData,
    workspace,
    isRefreshingPlan,
    setIsRefreshingPlan,
    flowShell,
    flowError,
    registerExportHandler,
  } = viewData

  const plan = planData.plan

  // A manual Plan refresh still uses the canonical plan loader, but it must
  // not replace the surrounding workflow surface with the initial-load screen.
  const loading = planData.loading && !isRefreshingPlan
  const changes = planData.changes

  const loadPlanRefresh = planData.refresh
  const clearChanges = planData.clearChanges
  const setChanges = planData.setChanges

  // One shared /api/live connection carries Plan changes for AgentWorks,
  // Crew and Relays. Keep the visible graph current during Builder edits;
  // the slow timer only covers direct filesystem writes or an offline feed.
  useLiveRefetch(() => {
    void loadPlanRefresh()
    refreshTriggers()
  }, {
    kinds: ['plan'],
    workflow: workspacePath,
    enabled: !!workspacePath && !toolbarOnly,
    fallbackMs: 30_000,
    minIntervalMs: 500,
  })

  const {
    state: workspaceState,
    loading: isLoadingWorkspaceState,
    error: workspaceStateError,
    isRetrying: isRetryingWorkspaceState,
    retryCountdown: workspaceStateRetryCountdown,
    refresh: refreshWorkspaceState
  } = workspace

  useEffect(() => {
    if (!isBuilderWorkspace || !workspaceState?.run_folders?.length) {
      return
    }

    const availableRunFolders = new Set(workspaceState.run_folders.map(folder => folder.name))
    const activeRunFolder = workspaceState.active_executions?.find(execution => execution.run_folder)?.run_folder
    if (activeRunFolder && availableRunFolders.has(activeRunFolder) && selectedRunFolder !== activeRunFolder) {
      setSelectedRunFolder(activeRunFolder)
      return
    }

    if (
      selectedRunFolder &&
      selectedRunFolder !== 'new' &&
      availableRunFolders.has(selectedRunFolder)
    ) {
      return
    }

    const preferredGroupId = selectedGroupIds[0]
      || variablesManifest?.groups?.find(group => group.enabled !== false)?.name
      || null

    const builderGroupRunFolder = preferredGroupId
      ? buildGroupFolderPath(preferredGroupId, 'iteration-0', variablesManifest)
      : null

    const fallbackBuilderRunFolder =
      (builderGroupRunFolder && availableRunFolders.has(builderGroupRunFolder) && builderGroupRunFolder)
      || (availableRunFolders.has('iteration-0') ? 'iteration-0' : null)
      || workspaceState.run_folders.find(folder => folder.name.startsWith('iteration-0/'))?.name
      || null

    if (fallbackBuilderRunFolder) {
      setSelectedRunFolder(fallbackBuilderRunFolder)
    }
  }, [
    isBuilderWorkspace,
    workspaceState?.run_folders,
    workspaceState?.active_executions,
    selectedRunFolder,
    selectedGroupIds,
    variablesManifest,
    setSelectedRunFolder
  ])

  // Log workspace state errors
  React.useEffect(() => {
    if (workspaceStateError) {
      console.error('[WorkflowCanvas] Workspace state error:', workspaceStateError)
    }
  }, [workspaceStateError])

  // Callback for opening variables sidebar
  const handleOpenVariablesSidebar = useCallback(() => {
    setShowVariablesSidebar(true)
  }, [])

  // Callback for when variables are updated
  const handleVariablesUpdate = useCallback((manifest: VariablesManifest) => {
    setVariablesManifest(manifest)
    // Also update in workflow store for buildExecutionOptions to access
    setVariablesManifestInStore(manifest)
  }, [setVariablesManifest, setVariablesManifestInStore])

  // Reload every file-backed input shown in the plan, including variables.
  const handlePlanRefresh = useCallback(async () => {
    if (isRefreshingPlan || !workspacePath) return
    setIsRefreshingPlan(true)
    try {
      refreshTriggers()
      const [reloaded, variables] = await Promise.all([
        loadPlanRefresh(),
        agentApi.getVariableGroups(workspacePath),
      ])
      if (planWorkspaceRef.current !== workspacePath) return
      if (!variables.success) throw new Error(variables.error || 'Variables could not be reloaded')
      setVariablesManifest(variables.manifest ?? null)
      setVariablesManifestInStore(variables.manifest ?? null)
      if (!reloaded) {
        throw new Error('Plan could not be reloaded')
      }
      useChatStore.getState().addToast('Plan reloaded', 'success')
    } catch (err) {
      if (planWorkspaceRef.current !== workspacePath) return
      useChatStore.getState().addToast(
        err instanceof Error ? err.message : 'Failed to reload plan',
        'error'
      )
    } finally {
      setIsRefreshingPlan(false)
    }
  }, [isRefreshingPlan, loadPlanRefresh, setIsRefreshingPlan, refreshTriggers, workspacePath, setVariablesManifest, setVariablesManifestInStore])

  const [selectedTrigger, setSelectedTrigger] = React.useState<{ workspace: string | null; id: string } | null>(null)
  const selectedTriggerJob = selectedTrigger?.workspace === workspacePath ? triggers.jobs.find(job => job.id === selectedTrigger.id) : undefined
  const openTriggerSettings = useCallback((section: 'schedules' | 'webhooks') => {
    useWorkflowStore.getState().openWorkspaceView(section === 'webhooks' ? 'webhooks' : 'schedules')
  }, [])

  // React Flow state (need to define before usePlanToFlow to use in callbacks)
  const [nodes, setNodes, onNodesChangeBase] = useNodesState<WorkflowNode>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<WorkflowEdge>([])
  // Keep the selection by ID; details always read the current graph after reload.
  const selectedFlowNode = selectedFlowNodeID?.workspace === workspacePath
    ? nodes.find(node => node.id === selectedFlowNodeID.id) ?? null
    : null
  const [routeTrace, setRouteTrace] = React.useState<(RouteTrace & { workspace: string | null | undefined }) | null>(null)
  const traceSource = routeTrace && routeTrace.workspace === workspacePath ? nodes.find(node => node.id === routeTrace.nodeId) : undefined
  const tracedRoute = (traceSource?.data as RoutingStepNodeData | undefined)?.routes?.find(route => route.route_id === routeTrace?.routeId)
  const activeTrace = tracedRoute ? routeTrace : null
  const selectTrigger = useCallback((id: string) => {
    setRouteTrace(null)
    setSelectedFlowNodeID(null)
    setSelectedTrigger(current => current?.workspace === workspacePath && current.id === id ? null : { workspace: workspacePath, id })
  }, [workspacePath])
  const triggerFlow = React.useMemo(() => appendTriggerCards(nodes, edges, triggers.jobs, {
    selectedID: selectedTriggerJob?.id, loading: triggers.loading, error: triggers.error, onSelect: selectTrigger, onSettings: openTriggerSettings, onRefresh: triggers.refresh,
  }), [nodes, edges, triggers.jobs, triggers.loading, triggers.error, triggers.refresh, selectedTriggerJob?.id, selectTrigger, openTriggerSettings])
  const focusTriggers = useCallback(() => {
    const surface = reactFlowWrapper.current?.querySelector('.react-flow')
    const targets = new Set(triggerFlow.edges.filter(edge => edge.source.startsWith('workflow-trigger-')).map(edge => edge.target))
    const focusNodes = triggerFlow.nodes.filter(node => node.type === 'workflow-trigger' || node.type === 'workflow-trigger-heading' || node.id === 'start' || targets.has(node.id))
    if (!surface || !focusNodes.length || !surface.clientWidth || !surface.clientHeight) return
    // Set the viewport directly: queued fitView can be displaced by controlled
    // plan-node reconciliation while presentation cards are being measured.
    const bounds = getNodesBounds(focusNodes)
    void setViewport(getViewportForBounds(bounds, Math.min(surface.clientWidth, reactFlowWrapper.current!.clientWidth), surface.clientHeight, FLOW_FIT_MIN_ZOOM, FLOW_FIT_MAX_ZOOM, 0.15), { duration: 300 })
  }, [triggerFlow.nodes, setViewport])
  const tracedGraph = React.useMemo(() => selectedTriggerJob
    ? traceTriggerGraph(triggerFlow.nodes, triggerFlow.edges, selectedTriggerJob)
    : traceRouteGraph(triggerFlow.nodes, triggerFlow.edges, activeTrace), [triggerFlow, selectedTriggerJob, activeTrace])
  const toggleRouteTrace = useCallback((trace: RouteTrace) => {
    setSelectedTrigger(null)
    setSelectedFlowNodeID(null)
    setRouteTrace(current => current?.workspace === workspacePath && current.nodeId === trace.nodeId && current.routeId === trace.routeId
      ? null : { workspace: workspacePath, ...trace })
  }, [workspacePath])
  const displayNodes = React.useMemo(() => tracedGraph.nodes.map(node => {
    if (node.type === 'message_sequence') return { ...node, data: { ...node.data,
      isRelayOutput: relayMode && !!relayOutputStepID && (node.data as StepNodeData).step?.id === relayOutputStepID,
    } }
    if (node.type !== 'routing' && node.type !== 'branch') return node
    return { ...node, data: { ...node.data,
      tracedRouteId: activeTrace?.nodeId === node.id ? activeTrace.routeId : undefined,
      onTraceRoute: (routeId: string) => toggleRouteTrace({ nodeId: node.id, routeId }),
    } }
  }), [tracedGraph.nodes, activeTrace, toggleRouteTrace, relayMode, relayOutputStepID])
  const displayEdges = React.useMemo(() => tracedGraph.edges.map(edge => {
    const trace = routeTraceFromEdge(nodes, edge)
    if (!trace) return edge
    return { ...edge, style: { ...edge.style, cursor: 'pointer' }, data: { ...edge.data,
      traced: activeTrace?.nodeId === trace.nodeId && activeTrace.routeId === trace.routeId,
      onTraceRoute: () => toggleRouteTrace(trace),
    } }
  }), [tracedGraph.edges, nodes, activeTrace, toggleRouteTrace])
  const onEdgeClick = useCallback((event: React.MouseEvent, edge: WorkflowEdge) => {
    const trace = routeTraceFromEdge(nodes, edge)
    if (!trace) return
    event.stopPropagation()
    toggleRouteTrace(trace)
  }, [nodes, toggleRouteTrace])
  const [, setIsExportingImage] = React.useState(false)
  // Store latest nodes in ref to avoid dependency issues
  const nodesRef = React.useRef(nodes)
  React.useEffect(() => {
    nodesRef.current = nodes
  }, [nodes])
  const edgesRef = React.useRef(edges)
  React.useEffect(() => {
    edgesRef.current = edges
  }, [edges])

  const handleExportImage = useCallback(async (format: WorkflowImageExportFormat) => {
    if (toolbarOnly) return
    setIsExportingImage(true)
    const previousViewport = getViewport()
    try {
      const filename = workflowExportFilename(workspacePath, format)
      const flowElement = reactFlowWrapper.current?.querySelector<HTMLElement>('.react-flow')
      let result: { canceled?: boolean; filePath?: string } | null = null

      if (flowElement) {
        flowElement.setAttribute('data-flow-exporting', 'true')
        try {
          await fitView({ padding: FLOW_FIT_PADDING, duration: 0, minZoom: FLOW_FIT_MIN_ZOOM, maxZoom: FLOW_FIT_MAX_ZOOM })
          await waitForAnimationFrames(3)
          if (format === 'svg' || format === 'png') {
            const svgDataUrl = renderFlowElementToSvg(flowElement)
            const dataUrl = format === 'png' ? await svgDataUrlToPngDataUrl(svgDataUrl) : svgDataUrl
            result = await saveWorkflowImage(dataUrl, filename, format)
          } else {
            result = await captureWorkflowImage(filename, format, flowElement.getBoundingClientRect())
          }
        } finally {
          flowElement.removeAttribute('data-flow-exporting')
        }
      }

      if (!result) {
        const dataUrl = renderFlowToImage(displayNodes, displayEdges, format)
        result = await saveWorkflowImage(dataUrl, filename, format)
      }

      if (result?.canceled) return
      const location = result?.filePath ? ` to ${result.filePath}` : ''
      const formatLabel = format === 'jpeg' ? 'JPG' : format.toUpperCase()
      useChatStore.getState().addToast(`Exported flow as ${formatLabel}${location}`, 'success')
    } catch (error) {
      console.error('[WorkflowCanvas] Failed to export flow image:', error)
      useChatStore.getState().addToast(error instanceof Error ? error.message : 'Failed to export flow image', 'error')
    } finally {
      setViewport(previousViewport, { duration: 0 })
      setIsExportingImage(false)
    }
  }, [fitView, getViewport, setViewport, displayNodes, displayEdges, toolbarOnly, workspacePath])

  // Map of parent node ID to child node IDs (for grouped movement)
  const nodeGroupsRef = React.useRef<Map<string, string[]>>(new Map())
  
  // Map of child node ID to parent node ID (for quick lookup)
  const childToParentRef = React.useRef<Map<string, string>>(new Map())
  
  // Map of child node ID to relative offset from parent { dx, dy }
  const childOffsetsRef = React.useRef<Map<string, { dx: number; dy: number }>>(new Map())

  // Store current node positions before refresh (to preserve layout when saving from sidebar)
  const currentPositionsRef = React.useRef<Map<string, { x: number; y: number }>>(new Map())
  const currentOffsetsRef = React.useRef<Map<string, { parentId: string; dx: number; dy: number }>>(new Map())

  const saveCurrentLayout = useCallback(async (currentNodes: WorkflowNode[]) => {
    if (toolbarOnly) return

    const layoutPath = getLayoutFilePath()
    if (!layoutPath) return

    const nodePositions: Record<string, { x: number; y: number }> = {}
    const childOffsets: Record<string, { parentId: string; dx: number; dy: number }> = {}
    const nodeById = new Map(currentNodes.map(node => [node.id, node]))

    currentNodes.forEach(node => {
      if (node.id === 'start' || node.id === 'variables') {
        return
      }
      nodePositions[node.id] = { x: node.position.x, y: node.position.y }
    })

    childToParentRef.current.forEach((parentId, nodeId) => {
      const childNode = nodeById.get(nodeId)
      const parentNode = nodeById.get(parentId)
      if (!childNode || !parentNode) {
        return
      }
      childOffsets[nodeId] = {
        parentId,
        dx: childNode.position.x - parentNode.position.x,
        dy: childNode.position.y - parentNode.position.y
      }
    })

    const layout = {
      version: WORKFLOW_LAYOUT_VERSION,
      updatedAt: new Date().toISOString(),
      layoutDirection,
      nodePositions,
      childOffsets
    }

    try {
      await agentApi.updatePlannerFile(
        layoutPath,
        JSON.stringify(layout, null, 2),
        'Save workflow canvas layout'
      )
      console.log('[WorkflowCanvas] Saved custom layout:', Object.keys(nodePositions).length, 'positions')
    } catch (error) {
      console.error('[WorkflowCanvas] Failed to save custom layout:', error)
    }
  }, [getLayoutFilePath, layoutDirection, toolbarOnly])

  // Build node groups: map parent nodes to their child nodes (validation, learning, sub-agents)
  const buildNodeGroups = useCallback((currentNodes: WorkflowNode[]) => {
    const groups = new Map<string, string[]>()
    const childToParent = new Map<string, string>()
    const offsets = new Map<string, { dx: number; dy: number }>()

    // Helper to check if a node is a parent node type
    const isParentNode = (node: WorkflowNode): boolean => {
      return node.type === 'step' ||
             node.type === 'human_input'
    }

    // Also treat sub-agents as parent nodes (they have learning/validation children)
    const isSubAgentNode = (node: WorkflowNode): boolean => {
      return node.id.includes('-sub-agent-')
    }

    // First pass: build groups for regular and human-input parent nodes.
    currentNodes.forEach(parentNode => {
      if (!isParentNode(parentNode)) return

      const children: string[] = []
      
      // Find validation and learning nodes by parentStepId
      currentNodes.forEach(childNode => {
        if (childNode.type === 'validation') {
          const data = childNode.data as { parentStepId?: string }
          if (data.parentStepId === parentNode.id) {
            children.push(childNode.id)
            childToParent.set(childNode.id, parentNode.id)
            // Calculate relative offset
            const dx = childNode.position.x - parentNode.position.x
            const dy = childNode.position.y - parentNode.position.y
            offsets.set(childNode.id, { dx, dy })
          }
        } else if (childNode.type === 'learning') {
          const data = childNode.data as { parentStepId?: string }
          if (data.parentStepId === parentNode.id) {
            children.push(childNode.id)
            childToParent.set(childNode.id, parentNode.id)
            const dx = childNode.position.x - parentNode.position.x
            const dy = childNode.position.y - parentNode.position.y
            offsets.set(childNode.id, { dx, dy })
          }
        }
      })

      if (children.length > 0) {
        groups.set(parentNode.id, children)
      }
    })

    // Second pass: Build groups for sub-agents (they have learning/validation children)
    currentNodes.forEach(subAgentNode => {
      if (!isSubAgentNode(subAgentNode)) return

      const children: string[] = []
      
      // Find validation and learning nodes that belong to this sub-agent
      currentNodes.forEach(childNode => {
        if (childNode.type === 'validation' || childNode.type === 'learning') {
          const data = childNode.data as { parentStepId?: string }
          if (data.parentStepId === subAgentNode.id) {
            children.push(childNode.id)
            childToParent.set(childNode.id, subAgentNode.id)
            const dx = childNode.position.x - subAgentNode.position.x
            const dy = childNode.position.y - subAgentNode.position.y
            offsets.set(childNode.id, { dx, dy })
          }
        }
      })

      if (children.length > 0) {
        groups.set(subAgentNode.id, children)
      }
    })

    nodeGroupsRef.current = groups
    childToParentRef.current = childToParent
    childOffsetsRef.current = offsets
  }, [])

  // Custom onNodesChange handler that groups nodes together
  const onNodesChange = useCallback((changes: NodeChange[]) => {
    // Allow all nodes to be draggable: sub-agents, validation, learning, and parent nodes
    // These nodes can be manually positioned independently
    const filteredChanges = changes.filter(change => {
      if (change.type === 'position') {
        const nodeId = change.id
        // Allow sub-agents to be draggable (they're children but should be independently movable)
        if (nodeId.includes('-sub-agent-')) {
          return true // Allow sub-agents to be draggable
        }
        // Allow validation and learning nodes to be draggable
        const node = nodesRef.current.find(n => n.id === nodeId)
        if (node && (node.type === 'validation' || node.type === 'learning' || node.type === 'workflow-artifact')) {
          return true // Allow validation and learning nodes to be draggable
        }
        // Check if this is a child node (has a parent) - these should not be draggable
        // But we've already handled sub-agents and validation/learning above
        if (childToParentRef.current.has(nodeId)) {
          return false // Ignore position changes for other child nodes
        }
      }
      return true
    })

    // Apply filtered changes
    onNodesChangeBase(filteredChanges as NodeChange<WorkflowNode>[])

    // Check if any parent node position changed (including sub-agents, validation, learning)
    const parentPositionChanges = new Map<string, { x: number; y: number }>()
    
    filteredChanges.forEach(change => {
      if (change.type === 'position' && change.position) {
        const nodeId = change.id
        const node = nodesRef.current.find(n => n.id === nodeId)
        // Include sub-agents, validation and learning nodes as independently movable
        const isSubAgent = nodeId.includes('-sub-agent-')
        const isValidationLearningEval = node && (node.type === 'validation' || node.type === 'learning' || node.type === 'workflow-artifact')
        // Check if this is a parent node (not a child) OR a sub-agent OR validation/learning
        if (isSubAgent || isValidationLearningEval || (nodeGroupsRef.current.has(nodeId) && !childToParentRef.current.has(nodeId))) {
          parentPositionChanges.set(nodeId, { x: change.position.x, y: change.position.y })
        }
      }
    })

    // If any parent nodes moved, update their children (with cascading updates)
    if (parentPositionChanges.size > 0) {
      setNodes((nds) => {
        // First pass: update direct children
        // Note: Sub-agents SHOULD move with their parent orchestrator
        // Validation and learning nodes remain independent
        let updatedNodes = nds.map(node => {
          const parentId = childToParentRef.current.get(node.id)
          
          // Skip if this is a validation or learning node
          // These are independent and can be manually positioned
          const isValidationLearningEval = node.type === 'validation' || node.type === 'learning' || node.type === 'workflow-artifact'
          if (isValidationLearningEval) {
            return node // These nodes are independent, don't update them here
          }
          
          if (parentId && parentPositionChanges.has(parentId)) {
            const newParentPos = parentPositionChanges.get(parentId)!
            const offset = childOffsetsRef.current.get(node.id)
            if (offset) {
              return {
                ...node,
                position: {
                  x: newParentPos.x + offset.dx,
                  y: newParentPos.y + offset.dy
                }
              }
            }
          }
          return node
        })

        // Second pass: update children of nodes that moved in first pass (cascading)
        // This handles orchestrator -> sub-agents -> learning nodes
        const nodesThatMoved = new Set<string>()
        updatedNodes.forEach(node => {
          const parentId = childToParentRef.current.get(node.id)
          if (parentId && parentPositionChanges.has(parentId)) {
            nodesThatMoved.add(node.id)
          }
        })

        // Update children of nodes that moved
        // Skip validation and learning nodes (they're independent)
        updatedNodes = updatedNodes.map(node => {
          // Skip validation and learning nodes - they're independent
          const isValidationLearningEval = node.type === 'validation' || node.type === 'learning' || node.type === 'workflow-artifact'
          if (isValidationLearningEval) {
            return node
          }
          
          const parentId = childToParentRef.current.get(node.id)
          if (parentId && nodesThatMoved.has(parentId)) {
            // Find the updated parent node
            const updatedParent = updatedNodes.find(n => n.id === parentId)
            if (updatedParent) {
              const offset = childOffsetsRef.current.get(node.id)
              if (offset) {
                return {
                  ...node,
                  position: {
                    x: updatedParent.position.x + offset.dx,
                    y: updatedParent.position.y + offset.dy
                  }
                }
              }
            }
          }
          return node
        })

        return updatedNodes
      })
    }

  }, [onNodesChangeBase, setNodes])

  const onNodeDragStop = useCallback<OnNodeDrag<WorkflowNode>>(() => {
    // Let React Flow apply the final position first, then persist the current graph.
    window.setTimeout(() => {
      void saveCurrentLayout(nodesRef.current)
    }, 0)
  }, [saveCurrentLayout])

  // Single reusable function to focus/position a node at the top-left of the screen
  const focusNode = useCallback((
    nodeId: string,
    options?: {
      topPadding?: number  // Vertical padding from top (default: 50)
      delay?: number  // Delay before positioning (default: 100ms)
    }
  ) => {
    const {
      topPadding = 50,
      delay = 100
    } = options || {}

    setTimeout(() => {
      const flowNode = getNode(nodeId)
      if (flowNode) {
        const padding = 150 // Padding from left edge
        setViewport(
          {
            x: padding - flowNode.position.x, // Position on left with padding
            y: topPadding - flowNode.position.y, // Position at top with padding
            zoom: 1.0
          },
          { duration: 500 }
        )

      }
    }, delay)
  }, [getNode, setViewport])

  // Convert plan to React Flow nodes and edges (with change highlights and run callback)
  const planFlow = usePlanToFlow(plan, {
    changes,  // Pass changes to highlight modified nodes
    workspacePath,  // Pass workspace path for file opening
    selectedRunFolder: selectedRunFolder ?? undefined,  // Pass selected run folder for file opening (convert null to undefined)
    variablesManifest,  // Pass variables manifest for Variables node
    onOpenVariablesSidebar: handleOpenVariablesSidebar,  // Callback for opening variables sidebar
    isLoadingVariables,  // Whether variables are loading
    layoutDirection,  // Layout direction: 'LR' for horizontal, 'TB' for vertical
    disabled: toolbarOnly
  })

  const { nodes: initialNodes, edges: initialEdges } = planFlow

  const showStepNode = useCallback((stepId: string): boolean => {
    const nodeToShow = nodesRef.current.find(node => {
      const nodeData = node.data as { id?: string; step?: { id?: string } }
      return nodeData?.step?.id === stepId || nodeData?.id === stepId || node.id === stepId
    })

    if (!nodeToShow) {
      console.log('[WorkflowCanvas] showStepNode - no node found for stepId:', stepId)
      return false
    }

    setShowVariablesSidebar(false)
    setSelectedFlowNodeID({ workspace: workspacePath, id: nodeToShow.id })
    // The pane may be transitioning from hidden/report to flow. Focus after
    // that layout change so the node lands inside the final-sized viewport.
    focusNode(nodeToShow.id, { topPadding: 100, delay: 350 })
    return true
  }, [focusNode, workspacePath])

  useLayoutEffect(() => {
    const handlePlanStepFocus = (event: Event) => {
      const detail = (event as CustomEvent<WorkflowPlanStepFocusDetail>).detail
      if (!detail?.stepId) return
      if (detail.workspacePath && workspacePath && detail.workspacePath !== workspacePath) return
      pendingPlanStepFocusRef.current = detail.stepId
      if (!toolbarOnly && showStepNode(detail.stepId)) {
        pendingPlanStepFocusRef.current = null
      }
    }

    window.addEventListener(WORKFLOW_PLAN_STEP_FOCUS_EVENT, handlePlanStepFocus)
    return () => window.removeEventListener(WORKFLOW_PLAN_STEP_FOCUS_EVENT, handlePlanStepFocus)
  }, [showStepNode, toolbarOnly, workspacePath])

  useEffect(() => {
    const pendingStepID = pendingPlanStepFocusRef.current
    if (!pendingStepID || toolbarOnly || nodes.length === 0) return

    // Re-attempt after React Flow has received the newly enabled plan nodes.
    const timer = window.setTimeout(() => {
      if (showStepNode(pendingStepID) && pendingPlanStepFocusRef.current === pendingStepID) {
        pendingPlanStepFocusRef.current = null
      }
    }, 0)
    return () => window.clearTimeout(timer)
  }, [nodes, showStepNode, toolbarOnly])

  useEffect(() => {
    pendingPlanStepFocusRef.current = null
  }, [workspacePath])

  // Auto-focus disabled - this prevents the canvas from jumping around during workflow execution

  // Expose methods via ref
  useImperativeHandle(ref, () => ({
    refresh: async (changedStepIDs?: string[], deletedStepIDs?: string[]) => {
      await loadPlanRefresh()

      // If granular change data is provided, use it directly
      if (changedStepIDs || deletedStepIDs) {
        // The backend combines added and updated into changed_step_ids
        // For now, we'll treat all changedStepIDs as "updated" since the backend combines them
        // The visual highlighting will work correctly (blue ring for updated steps)
        const updated = changedStepIDs?.filter(id => !deletedStepIDs?.includes(id)) || []
        const deleted = deletedStepIDs || []
        const changes: PlanChanges = {
          added: [], // Backend combines added into changed_step_ids, so we can't distinguish here
          updated,
          deleted,
          hasChanges: updated.length > 0 || deleted.length > 0
        }
        // Set changes directly from granular event data
        if (changes.hasChanges) {
          setChanges(changes)
        }
        return changes
      }

      // No granular data - just refresh without setting changes
      return null
    },
    getStepCount: () => {
      // Count steps from plan data
      if (!plan?.steps) return 0
      return plan.steps.length
    },
    focusStep: (stepId: string) => {
      pendingPlanStepFocusRef.current = stepId
      if (!toolbarOnly && showStepNode(stepId)) pendingPlanStepFocusRef.current = null
    },
    resyncViewport: () => {
      // Same delay as the device-preview-resize effect above: give the
      // display:none -> visible CSS transition a frame to actually land
      // before asking React Flow to measure its wrapper.
      setTimeout(() => {
        try { void fitView({ padding: FLOW_FIT_PADDING, duration: 300, minZoom: FLOW_FIT_MIN_ZOOM, maxZoom: FLOW_FIT_MAX_ZOOM }) } catch { /* ignore */ }
      }, 60)
    }
  }), [loadPlanRefresh, plan, setChanges, showStepNode, toolbarOnly, fitView])

  // Store step ID to focus on when changes are detected (will focus after nodes update)
  React.useEffect(() => {
    if (changes?.hasChanges) {
      // Store the step ID to focus on (will be used after nodes are updated)
      const stepToFocus = changes.added?.[0] || changes.updated?.[0]
      if (stepToFocus) {
        pendingFocusStepIdRef.current = stepToFocus
      }
      
      // Clear any existing timeout
      if (highlightTimeoutRef.current) {
        clearTimeout(highlightTimeoutRef.current)
      }
      
      // Set new timeout to clear highlights
      highlightTimeoutRef.current = setTimeout(() => {
        console.log('[WorkflowCanvas] Clearing change highlights after', HIGHLIGHT_DURATION, 'ms')
        clearChanges()
      }, HIGHLIGHT_DURATION)
    }

    // Cleanup on unmount
    return () => {
      if (highlightTimeoutRef.current) {
        clearTimeout(highlightTimeoutRef.current)
      }
    }
  }, [changes, clearChanges])

  // Track previous nodes/edges to detect actual changes
  const prevNodesRef = React.useRef<typeof initialNodes>([])
  const prevEdgesRef = React.useRef<typeof initialEdges>([])
  // CRITICAL: Force header nodes to correct positions after nodes update
  // Ensure header nodes maintain correct positions (safety net in case something tries to override them)
  React.useEffect(() => {
    if (toolbarOnly) return // Skip when canvas is hidden
    if (nodes.length === 0 || initialNodes.length === 0) return
    
    const varsNode = initialNodes.find(n => n.id === 'variables')
    const startNode = initialNodes.find(n => n.id === 'start')
    
    if (!varsNode && !startNode) return
    
    // Check if any header node position has been overridden
    const currentVars = nodes.find(n => n.id === 'variables')
    const currentStart = nodes.find(n => n.id === 'start')
    
    let needsFix = false
    
    if (varsNode && currentVars && 
        (currentVars.position.x !== varsNode.position.x || currentVars.position.y !== varsNode.position.y)) {
      needsFix = true
    }
    if (startNode && currentStart && 
        (currentStart.position.x !== startNode.position.x || currentStart.position.y !== startNode.position.y)) {
      needsFix = true
    }
    
    if (needsFix) {
      // Use updateNode API to restore correct positions
      if (varsNode) updateNode('variables', { position: varsNode.position })
      if (startNode) updateNode('start', { position: startNode.position })
    }
  }, [nodes, initialNodes, updateNode, toolbarOnly])

  // Rebuild node groups when nodes change
  React.useEffect(() => {
    if (toolbarOnly) return // Skip when canvas is hidden
    hasInitializedView.current = false
  }, [toolbarOnly])

  React.useEffect(() => {
    if (toolbarOnly) return // Skip when canvas is hidden
    if (nodes.length > 0) {
      buildNodeGroups(nodes)
    }
  }, [nodes, buildNodeGroups, toolbarOnly])

  // Update nodes when plan changes (only if nodes actually changed)
  React.useEffect(() => {
    // Skip node/edge updates when the flow canvas is hidden. The saved layout
    // only applies to React Flow; a toolbar-only render should not fetch it.
    if (toolbarOnly) return

    // Compare by reference first (fast path)
    if (prevNodesRef.current === initialNodes && prevEdgesRef.current === initialEdges) {
      return // No change
    }
    
    const nodesChanged = planNodesChanged(prevNodesRef.current, initialNodes)
    const edgesChanged = planEdgesChanged(prevEdgesRef.current, initialEdges)

    if (nodesChanged) {
      // Nodes changed - will apply positions from usePlanToFlow
      console.log(`%c[WorkflowCanvas] setNodes: ${initialNodes.length} nodes (preset: ${presetQueryId?.slice(0,8)})`, 'color: #4CAF50')
      console.time(`[WorkflowCanvas] setNodes-${presetQueryId?.slice(0,8)}`)
      setNodes(initialNodes)

      // Always try to restore positions after nodes regenerate (unless layout direction changed)
      // Priority: 1) Saved layout from file, 2) Current positions (captured before refresh), 3) Auto-layout
      if (initialNodes.length > 0) {
        // Extract header node positions from initialNodes BEFORE any restoration
        // Header positions are generated; never restore stale positions from the previous serial-chain layout.
        const headerNodePositions = new Map<string, { x: number; y: number }>()
        initialNodes.forEach(node => {
          if (node.id === 'start' || node.id === 'variables') {
            headerNodePositions.set(node.id, { x: node.position.x, y: node.position.y })
          }
        })
        // Checking for saved layout...
        
        // First try to load saved layout from file
        loadSavedLayout().then(savedLayout => {
          // Use saved layout if available, otherwise use current positions (captured before refresh)
          const positionsToUse = savedLayout?.positions && savedLayout.positions.size > 0
            ? savedLayout.positions
            : currentPositionsRef.current
          const offsetsToUse = savedLayout?.offsets && savedLayout.offsets.size > 0
            ? savedLayout.offsets
            : currentOffsetsRef.current
          
          if (positionsToUse.size > 0) {
            setNodes((nds) => {
              // First, apply saved/current positions to parent nodes
              let updated = nds.map(node => {
                const savedPos = positionsToUse.get(node.id)
                
                // Header nodes are skipped from restoration (will be forced to correct positions later)

                // Apply saved position unless it's a header node (start, variables)
                // Header nodes MUST always use the enforced horizontal layout from usePlanToFlow
                if (savedPos && node.id !== 'start' && node.id !== 'variables') {
                  return { ...node, position: savedPos }
                }
                return node
              })
              
            // Build groups from original auto-layout to get parent-child relationships
              buildNodeGroups(nds)
              
              // If we have saved/current offsets, use them (version 1.1+)
              // Otherwise, fall back to calculating from original auto-layout
              // Note: Sub-agents are now saved as parent positions, not offsets
              if (offsetsToUse.size > 0) {
                // Apply offsets in multiple passes to handle cascading parent-child relationships
                // Pass 1: Apply offsets for nodes whose parent is a top-level parent (orchestrator, step, etc.)
                // Pass 2: Apply learning/validation offsets (relative to sub-agents or other parents)
                
                // First pass: Apply offsets for nodes whose parent is a top-level parent (orchestrator, step, etc.)
                // Skip sub-agents, validation and learning nodes (they're loaded from parentPositions, not offsets)
                updated = updated.map(node => {
                  // Skip sub-agents, validation and learning nodes - they're loaded from parentPositions, not offsets
                  if (node.id.includes('-sub-agent-') || 
                      node.type === 'validation' || 
                      node.type === 'learning' || 
                                            node.type === 'workflow-artifact') {
                    return node
                  }
                  
                  const savedOffset = offsetsToUse.get(node.id)
                  if (savedOffset) {
                    const parentNode = updated.find(n => n.id === savedOffset.parentId)
                    // Only apply if parent is a top-level parent (not a sub-agent)
                    if (parentNode && !parentNode.id.includes('-sub-agent-')) {
                      return {
                        ...node,
                        position: {
                          x: parentNode.position.x + savedOffset.dx,
                          y: parentNode.position.y + savedOffset.dy
                        }
                      }
                    }
                  }
                  return node
                })
                
                // Second pass: Apply offsets for nodes whose parent is a sub-agent (learning/validation nodes)
                updated = updated.map(node => {
                  const savedOffset = offsetsToUse.get(node.id)
                  if (savedOffset) {
                    const parentNode = updated.find(n => n.id === savedOffset.parentId)
                    // Only apply if parent is a sub-agent
                    if (parentNode && parentNode.id.includes('-sub-agent-')) {
                      return {
                        ...node,
                        position: {
                          x: parentNode.position.x + savedOffset.dx,
                          y: parentNode.position.y + savedOffset.dy
                        }
                      }
                    }
                  }
                  return node
                })
              } else {
                // Fallback: calculate offsets from original auto-layout (for old saved layouts)
                updated = updated.map(node => {
                  const parentId = childToParentRef.current.get(node.id)
                  if (parentId) {
                    const parentNode = updated.find(n => n.id === parentId)
                    const originalParentNode = nds.find(n => n.id === parentId)
                    const originalNode = nds.find(n => n.id === node.id)
                    
                    if (parentNode && originalParentNode && originalNode) {
                      const originalOffset = {
                        dx: originalNode.position.x - originalParentNode.position.x,
                        dy: originalNode.position.y - originalParentNode.position.y
                      }
                      
                      return {
                        ...node,
                        position: {
                          x: parentNode.position.x + originalOffset.dx,
                          y: parentNode.position.y + originalOffset.dy
                        }
                      }
                    }
                  }
                  return node
                })
              }
              
              // Rebuild groups with final positions to ensure offsets are correct for future moves
              buildNodeGroups(updated)
              
              // CRITICAL: Force header nodes to correct positions
              updated = updated.map(node => {
                if (headerNodePositions.has(node.id)) {
                  return { ...node, position: headerNodePositions.get(node.id)! }
                }
                return node
              })
              updated = enforceWorkflowHeaderClearance(updated)
              buildNodeGroups(updated)
              
              // Clear current positions after use (they've been applied)
              if (positionsToUse === currentPositionsRef.current) {
                currentPositionsRef.current.clear()
                currentOffsetsRef.current.clear()
              }
              
              return updated
            })
          } else {
            // No saved layout - force header nodes immediately
            setNodes((nds) => {
              const updated = nds.map(node => {
                if (headerNodePositions.has(node.id)) {
                  return { ...node, position: headerNodePositions.get(node.id)! }
                }
                return node
              })
              return enforceWorkflowHeaderClearance(updated)
            })
          }
        }).catch(err => {
          console.error('[WorkflowCanvas] Failed to load saved layout:', err)
          // If saved layout fails, try to use current positions
          if (currentPositionsRef.current.size > 0) {
            setNodes((nds) => {
              let updated = nds.map(node => {
                const savedPos = currentPositionsRef.current.get(node.id)
                
                // Header nodes are skipped from restoration (will be forced to correct positions later)

                // Apply saved position unless it's a header node (start, variables)
                if (savedPos && node.id !== 'start' && node.id !== 'variables') {
                  return { ...node, position: savedPos }
                }
                return node
              })
              buildNodeGroups(updated)
              
              // CRITICAL: Force header nodes to correct positions
              updated = updated.map(node => {
                if (headerNodePositions.has(node.id)) {
                  return { ...node, position: headerNodePositions.get(node.id)! }
                }
                return node
              })
              updated = enforceWorkflowHeaderClearance(updated)
              buildNodeGroups(updated)
              
              // Clear current positions after use
              currentPositionsRef.current.clear()
              currentOffsetsRef.current.clear()
              return updated
            })
          }
        })
      } else {
        // No saved layout or layout direction changed - ensure header nodes have correct positions from usePlanToFlow
        const headerNodePositions = new Map<string, { x: number; y: number }>()
        initialNodes.forEach(node => {
          if (node.id === 'start' || node.id === 'variables') {
            headerNodePositions.set(node.id, { x: node.position.x, y: node.position.y })
          }
        })
        
        if (headerNodePositions.size > 0) {
          setNodes((nds) => {
            const updated = nds.map(node => {
              if (headerNodePositions.has(node.id)) {
                return { ...node, position: headerNodePositions.get(node.id)! }
              }
              return node
            })
            return enforceWorkflowHeaderClearance(updated)
          })
          
          // Also use updateNode API to force positions
          headerNodePositions.forEach((pos, nodeId) => {
            updateNode(nodeId, { position: pos })
          })
        }
      }
      
      hasInitializedView.current = false
      
      prevNodesRef.current = initialNodes
      
      // After nodes are updated, check if we need to focus on a changed step (from backend updates)
      // Use setTimeout to ensure nodes are fully rendered in React Flow
      if (pendingFocusStepIdRef.current) {
        const stepIdToFocus = pendingFocusStepIdRef.current
        // Store focusNode in a local variable to avoid dependency issues
        const focusNodeFn = focusNode
        setTimeout(() => {
          // Find the node for this step ID - prioritize step.id over node.id for accurate matching
          // nodeData.step.id is the stable ID from plan.json.
          const nodeToFocus = initialNodes.find(n => {
            if (n.type === 'step') {
              const nodeData = n.data as StepNodeData
              const stepId = nodeData?.step?.id
              if (stepId === stepIdToFocus) {
                return true
              }
              // Fallback: match by node.id only if step.id doesn't exist (shouldn't happen for valid steps)
              if (!stepId && n.id === stepIdToFocus) {
                return true
              }
              return false
            }
            return false
          })
          
          if (nodeToFocus) {
            // Focus on the changed step (position viewport, but don't open sidebar)
            focusNodeFn(nodeToFocus.id, { topPadding: 150, delay: 0 })
            const nodeData = nodeToFocus.data as StepNodeData
            console.log('[WorkflowCanvas] Auto-focused on step that was changed by backend:', {
              stepId: stepIdToFocus,
              nodeId: nodeToFocus.id,
              stepTitle: nodeData?.step?.title,
              matchedBy: nodeData?.step?.id === stepIdToFocus ? 'step.id' : 'node.id'
            })
          } else {
            console.warn('[WorkflowCanvas] Could not find node for changed step ID:', stepIdToFocus, {
              availableNodes: initialNodes
                .filter(n => n.type === 'step')
                .map(n => {
                  const nodeData = n.data as StepNodeData
                  return {
                    nodeId: n.id,
                    stepId: nodeData?.step?.id,
                    stepTitle: nodeData?.step?.title
                  }
                })
            })
          }
          
          // Clear the pending focus
          pendingFocusStepIdRef.current = null
        }, 200) // Small delay to ensure React Flow has rendered the nodes
      }
    }
    
    if (nodesChanged) {
      console.timeEnd(`[WorkflowCanvas] setNodes-${presetQueryId?.slice(0,8)}`)
    }

    if (edgesChanged) {
      console.log(`%c[WorkflowCanvas] setEdges: ${initialEdges.length} edges (preset: ${presetQueryId?.slice(0,8)})`, 'color: #4CAF50')
      setEdges(initialEdges)
      prevEdgesRef.current = initialEdges
    }

  }, [initialNodes, initialEdges, setNodes, setEdges, focusNode, buildNodeGroups, loadSavedLayout, layoutDirection, updateNode, presetQueryId, toolbarOnly])

  // Fit the full plan on first render so the workflow shape is visible by default.
  React.useEffect(() => {
    if (toolbarOnly) return
    if (!hasInitializedView.current && !triggers.loading && nodes.length > 0) {
      const fitTimer = window.setTimeout(() => {
        window.requestAnimationFrame(() => {
          // Restore the saved pan/zoom for this workspace instead of
          // re-fitting: a refresh should keep the user where they were.
          // Tablet and embedded views keep their own framing.
          if (previewDevice !== 'tablet' && !embeddedPlanOnly) {
            const saved = readSavedViewport(getViewportStorageKey(), FLOW_FIT_MIN_ZOOM, 2)
            if (saved) {
              setViewport(saved, { duration: 0 })
              viewportStateRef.current = saved
              hasInitializedView.current = true
              return
            }
          }
          if (previewDevice === 'tablet') {
            if (triggers.jobs.length) {
              focusTriggers()
              hasInitializedView.current = true
              return
            }
            const firstNode = nodes.find(node => node.id !== 'start' && node.id !== 'variables') ?? nodes[0]
            setViewport({
              x: 48 - firstNode.position.x * 0.85,
              y: 48 - firstNode.position.y * 0.85,
              zoom: 0.85,
            }, { duration: 350 })
            viewportStateRef.current = getViewport()
            hasInitializedView.current = true
            return
          }
          const embeddedFocusNodes = embeddedPlanOnly ? nodes.slice(0, Math.min(nodes.length, 8)) : undefined
          Promise.resolve(
            fitView({
              padding: embeddedPlanOnly ? 0.12 : FLOW_FIT_PADDING,
              duration: 350,
              minZoom: embeddedPlanOnly ? 0.18 : FLOW_FIT_MIN_ZOOM,
              maxZoom: embeddedPlanOnly ? 0.7 : FLOW_FIT_MAX_ZOOM,
              nodes: embeddedFocusNodes,
            })
          ).finally(() => {
            viewportStateRef.current = getViewport()
            hasInitializedView.current = true
          })
        })
      }, 220)

      return () => window.clearTimeout(fitTimer)
    }
  }, [embeddedPlanOnly, nodes, fitView, getViewport, getViewportStorageKey, previewDevice, setViewport, toolbarOnly, triggers.loading, triggers.jobs.length, focusTriggers])

  useEffect(() => {
    if (!selectedFlowNode && !activeTrace && !selectedTriggerJob) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setSelectedTrigger(null)
        setRouteTrace(null)
        setSelectedFlowNodeID(null)
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [selectedFlowNode, activeTrace, selectedTriggerJob])

  const onNodeClick = useCallback((_: React.MouseEvent, node: WorkflowNode) => {
    if (node.type === 'workflow-trigger' || node.type === 'workflow-trigger-heading') return
    if (node.type === 'variables') {
      setShowVariablesSidebar(true)
      setSelectedFlowNodeID(null)
      return
    }
    setSelectedFlowNodeID(current => current?.workspace === workspacePath && current.id === node.id
      ? null
      : { workspace: workspacePath, id: node.id })
  }, [workspacePath])
  const onPaneClick = useCallback(() => {
    setSelectedTrigger(null)
    setRouteTrace(null)
    setSelectedFlowNodeID(null)
  }, [])

  // The toolbar (owned by WorkspaceViewHost) triggers image export through
  // this registration: export needs the React Flow instance, which only
  // exists inside this canvas.
  useEffect(() => {
    registerExportHandler(handleExportImage)
    return () => registerExportHandler(null)
  }, [registerExportHandler, handleExportImage])

  // Unified loading state - wait for ALL data before showing canvas. The host
  // derives the same shell state to hide the toolbar while this shows.
  const loadingMessages = []
  if (loading) loadingMessages.push('plan & step config')
  if (isLoadingWorkspaceState) loadingMessages.push('workspace state')

  if (flowShell === 'loading') {
    return (
      <div className="relative flex items-center justify-center h-full bg-gray-50 dark:bg-gray-900">
        {!relayMode && <div className="absolute right-3 top-3 z-20"><WorkspacePanelGuideButton topic="Plan" /></div>}
        <div className="flex flex-col items-center gap-3">
          <div className="w-8 h-8 border-2 border-gray-400 dark:border-gray-500 border-t-transparent rounded-full animate-spin" />
          <span className="text-sm text-gray-500 dark:text-gray-400">
            Loading {loadingMessages.join(' & ')}...
          </span>
          <span className="text-xs text-gray-400 dark:text-gray-500">
            Please wait while we load everything
          </span>
        </div>
      </div>
    )
  }

  // Error state - show errors from plan loading or workspace state loading.
  // "plan.json not found" is already treated as "no plan" by the host.
  const effectiveError = flowError

  if (flowShell === 'error') {
    return (
      <div className="flex items-center justify-center h-full bg-gray-50 dark:bg-gray-900">
        <div className="flex flex-col items-center gap-3 text-center max-w-md">
          <div className="w-12 h-12 rounded-full bg-red-100 dark:bg-red-900/30 flex items-center justify-center">
            <span className="text-2xl">⚠️</span>
          </div>
          <div className="flex flex-col gap-2">
            {effectiveError && (
              <span className="text-sm text-red-600 dark:text-red-400">
                <strong>{relayMode ? 'Graph error:' : 'Plan error:'}</strong> {effectiveError}
              </span>
            )}
            {workspaceStateError && (
              <div className="flex flex-col gap-2">
                <span className="text-sm text-red-600 dark:text-red-400">
                  <strong>Workspace error:</strong> {workspaceStateError}
                </span>
                {isRetryingWorkspaceState && (
                  <div className="flex items-center gap-2 text-sm text-blue-600 dark:text-blue-400">
                    <div className="w-4 h-4 border-2 border-blue-600 dark:border-blue-400 border-t-transparent rounded-full animate-spin" />
                    <span>
                      Retrying in {workspaceStateRetryCountdown !== null ? `${workspaceStateRetryCountdown} second${workspaceStateRetryCountdown !== 1 ? 's' : ''}...` : '5 seconds...'}
                    </span>
                  </div>
                )}
              </div>
            )}
          </div>
          <div className="flex items-center gap-2">
            {assistantControl}
            {!relayMode && <WorkspacePanelGuideButton topic="Plan" />}
            <button
              onClick={() => {
                loadPlanRefresh()
                refreshWorkspaceState()
              }}
              className="px-4 py-2 text-sm bg-gray-100 dark:bg-gray-700 text-gray-900 dark:text-gray-100 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
              disabled={isRetryingWorkspaceState}
            >
              {isRetryingWorkspaceState ? 'Retrying...' : 'Retry Loading'}
            </button>
          </div>
        </div>
      </div>
    )
  }

  // No plan state
  const hasPlan = !!(plan && plan.steps && plan.steps.length > 0)
  if (!hasPlan) {
    return (
      <PlanEmptyState
        kind={relayMode ? 'graph' : 'plan'}
        title={relayMode ? 'Build your Relay graph' : 'Plan your workflow'}
        description={relayMode
          ? 'Describe the input, agents, scripts, and JSON output in chat. Your graph will appear here as you build it.'
          : 'Describe what you want to accomplish in chat. Your plan will appear here when it is ready.'}
        actionLabel={relayMode ? 'Build graph in chat' : 'Build plan in chat'}
        onAction={onCreatePlan}
        secondaryActions={<>
          {assistantControl}
          {!relayMode && <WorkspacePanelGuideButton topic="Plan" />}
          <button
            type="button"
            onClick={() => void (async () => {
              if (isRefreshingPlan) return
              setIsRefreshingPlan(true)
              try { await loadPlanRefresh() } finally { setIsRefreshingPlan(false) }
            })()}
            disabled={isRefreshingPlan}
            className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={relayMode ? 'Check again for a graph' : 'Check again for a plan'}
          >
            <RefreshCw className={`h-3.5 w-3.5 ${isRefreshingPlan ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </>}
      />
    )
  }

  return (
    <div className="h-full min-h-0" ref={reactFlowWrapper}>
        {/* Canvas area — skip when toolbarOnly to avoid rendering 1000+ SVG nodes */}
        {toolbarOnly ? null : <div className="h-full min-h-0 relative flex">
          {selectedTriggerJob && <div className="absolute left-3 top-3 z-20 max-w-[calc(100%-12rem)] rounded-lg border border-primary/40 bg-background/95 px-3 py-2 text-sm shadow-lg" role="status">
            <div className="flex items-center gap-2"><span className="truncate">Trigger: {selectedTriggerJob.name}</span><button type="button" onClick={() => setSelectedTrigger(null)} className="shrink-0 rounded px-2 py-1 text-xs hover:bg-muted">Show all</button></div>
            <p className="text-xs text-muted-foreground">Saved route choices and required earlier steps. Unspecified decisions show possible paths.</p>
          </div>}
          {activeTrace && tracedRoute && (
            <div className="absolute left-3 top-3 z-20 max-w-[calc(100%-12rem)] rounded-lg border border-teal-500/50 bg-background/95 px-3 py-2 text-sm shadow-lg">
              <div className="flex items-center gap-3" role="status">
                <Route className="h-4 w-4 shrink-0 text-teal-500" />
                <span className="truncate">Tracing: {tracedRoute.route_name || tracedRoute.route_id}</span>
                <button type="button" onClick={() => setRouteTrace(null)} className="shrink-0 rounded px-2 py-1 text-xs hover:bg-muted focus-visible:outline" title="Clear route trace (Escape)">Show all</button>
              </div>
            </div>
          )}
          <div className="absolute right-3 top-3 z-20 flex items-center gap-1">
            {assistantControl}
            <button type="button" onClick={() => void fitView({ padding: FLOW_FIT_PADDING, duration: 300, minZoom: FLOW_FIT_MIN_ZOOM, maxZoom: FLOW_FIT_MAX_ZOOM })}
              className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-muted-foreground shadow-sm hover:bg-muted hover:text-foreground"
              aria-label={relayMode ? 'Fit graph to view' : 'Fit plan to view'} title={relayMode ? 'Fit graph to view' : 'Fit plan to view'}>
              <Maximize className="h-3.5 w-3.5" />
            </button>
            {!relayMode && <WorkspacePanelGuideButton topic="Plan" />}
            <button
              type="button"
              onPointerDown={event => event.stopPropagation()}
              onClick={event => {
                event.stopPropagation()
                void handlePlanRefresh()
              }}
              disabled={isRefreshingPlan}
              className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground"
              aria-label="Refresh plan"
              title="Refresh plan"
              data-testid="refresh-plan"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${isRefreshingPlan ? 'animate-spin' : ''}`} />
            </button>
          </div>
          <div className={`min-h-0 h-full transition-all duration-300 ${showVariablesSidebar ? 'mr-[450px]' : ''} ${previewDevice === 'desktop' ? 'flex-1' : previewDeviceShellClass(previewDevice)}`}>
        <ReactFlow
          className="w-full h-full bg-gray-50 dark:bg-gray-900"
          style={{ width: '100%', height: '100%' }}
          nodes={displayNodes}
          edges={displayEdges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onEdgeClick={onEdgeClick}
          onPaneClick={onPaneClick}
          onNodeDragStop={readOnly ? undefined : onNodeDragStop}
          panOnDrag
          panOnScroll
          nodesDraggable={!readOnly}
          nodesConnectable={false}
          nodesFocusable={false}
          elementsSelectable={false}
          edgesFocusable={false}
          onlyRenderVisibleElements={false}
          onViewportChange={onViewportChange}
          nodeTypes={canvasNodeTypes}
          edgeTypes={edgeTypes}
          fitView={false}
          fitViewOptions={{ padding: FLOW_FIT_PADDING, minZoom: FLOW_FIT_MIN_ZOOM, maxZoom: FLOW_FIT_MAX_ZOOM }}
          minZoom={FLOW_FIT_MIN_ZOOM}
          maxZoom={2}
          defaultViewport={{ x: 100, y: 0, zoom: 0.9 }}
          attributionPosition="bottom-right"
        >
          <Background 
            variant={BackgroundVariant.Dots} 
            gap={20} 
            size={1} 
            color="#e5e7eb"
            className="dark:!bg-gray-900"
          />
        </ReactFlow>

        {/* Plan-specific controls live inside the canvas; shared navigation and
            workflow actions remain in the workflow-level toolbar. */}
        </div>

        {selectedFlowNode && (
          <ReadOnlyStepDetailPanel
            node={selectedFlowNode}
            workspacePath={workspacePath}
            onClose={() => setSelectedFlowNodeID(null)}
          />
        )}

        {/* Variables Sidebar */}
        {showVariablesSidebar && (
          <VariablesSidebar
            workspacePath={workspacePath}
            onClose={() => setShowVariablesSidebar(false)}
            onUpdate={handleVariablesUpdate}
            showChatArea={showChatArea}
          />
        )}
      </div>}
    </div>
  )
})

// Add display name for debugging
WorkflowCanvasInner.displayName = 'WorkflowCanvasInner'

// The flow canvas is one view among several. WorkspaceViewHost owns the
// toolbar, the shared data, and the dispatch between views, and wraps this in
// its own ReactFlowProvider so report mode never repaints on flow-only
// store traffic.
export { WorkflowCanvasInner }

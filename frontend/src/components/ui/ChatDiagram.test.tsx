// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { ChatDiagram } from './ChatDiagram'
import { ChatSequence, ChatSwimlane } from './ChatProcessDiagram'

const host = document.createElement('div')
const root = createRoot(host)

let mockViewportWidth = 0
let resizeCallback: ResizeObserverCallback | null = null

const originalClientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')
const originalResizeObserver = globalThis.ResizeObserver

function setViewportWidth(width: number) {
  mockViewportWidth = width
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get() {
      return (this as HTMLElement).hasAttribute('data-diagram-viewport') ? mockViewportWidth : 0
    },
  })
}

class MockResizeObserver {
  constructor(cb: ResizeObserverCallback) {
    resizeCallback = cb
  }
  observe() {}
  unobserve() {}
  disconnect() {}
}

afterEach(() => {
  act(() => root.render(null))
  resizeCallback = null
  if (originalClientWidth) Object.defineProperty(HTMLElement.prototype, 'clientWidth', originalClientWidth)
  globalThis.ResizeObserver = originalResizeObserver
})

const fourStepFlow = {
  type: 'diagram' as const,
  title: 'Current chat visual flow',
  direction: 'horizontal' as const,
  nodes: [
    { id: 'agent', title: 'Agent', description: 'Sends typed JSON fields' },
    { id: 'tool', title: 'author_chat_visual', description: 'Validates with Zod catalog' },
    { id: 'reply', title: 'Assistant reply', description: 'Streams block in prose' },
    { id: 'ui', title: 'Rendered visual', description: 'App json-render & Slack', emphasis: true },
  ],
  edges: [
    { from: 'agent', to: 'tool' },
    { from: 'tool', to: 'reply', label: 'SpecStream block' },
    { from: 'reply', to: 'ui', label: 'progressive render' },
  ],
}

function canvasWidth() {
  const canvas = host.querySelector<HTMLElement>('[data-diagram-viewport] > div')
  return parseFloat(canvas?.style.width || '0')
}

describe('ChatDiagram responsive width and edge labels', () => {
  it('switches overflowing horizontal diagrams to vertical layout and restores horizontal on widen', async () => {
    globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
    setViewportWidth(720)
    await act(async () => root.render(<ChatDiagram spec={fourStepFlow} />))

    expect(host.querySelector('[data-diagram-direction]')?.getAttribute('data-diagram-direction')).toBe('vertical')
    expect(canvasWidth()).toBeLessThanOrEqual(720)

    // Render edge labels as HTML overlays above the SVG instead of SVG foreignObject.
    expect(host.querySelectorAll('svg foreignObject').length).toBe(0)
    const labels = Array.from(host.querySelectorAll('[data-diagram-edge-label]')).map(el => el.textContent)
    expect(labels).toEqual(['SpecStream block', 'progressive render'])

    // Widening the chat area restores the requested horizontal direction.
    setViewportWidth(1800)
    await act(async () => { resizeCallback?.([], {} as ResizeObserver) })
    expect(host.querySelector('[data-diagram-direction]')?.getAttribute('data-diagram-direction')).toBe('horizontal')
    expect(canvasWidth()).toBeLessThanOrEqual(1800)
  })

  it('keeps a 3-node horizontal diagram horizontal with compact cards when it fits', async () => {
    setViewportWidth(760)
    await act(async () => root.render(<ChatDiagram spec={{
      type: 'diagram',
      title: 'Three steps',
      direction: 'horizontal',
      nodes: [
        { id: 'a', title: 'Input' },
        { id: 'b', title: 'Check' },
        { id: 'c', title: 'Output' },
      ],
      edges: [
        { from: 'a', to: 'b' },
        { from: 'b', to: 'c' },
      ],
    }} />))

    expect(host.querySelector('[data-diagram-direction]')?.getAttribute('data-diagram-direction')).toBe('horizontal')
    expect(canvasWidth()).toBeLessThanOrEqual(760)
  })
})

describe('ChatSwimlane and ChatSequence responsive widths', () => {
  it('tightens swimlane pitch to fit a normal chat column and keeps a readable floor for many steps', async () => {
    setViewportWidth(760)
    await act(async () => root.render(<ChatSwimlane spec={{
      type: 'swimlane',
      title: 'Handoff',
      lanes: [{ id: 'person', title: 'Person' }, { id: 'agent', title: 'Agent' }],
      steps: [
        { id: 's1', lane: 'person', title: 'Ask' },
        { id: 's2', lane: 'agent', title: 'Validate' },
        { id: 's3', lane: 'agent', title: 'Format' },
        { id: 's4', lane: 'person', title: 'Review' },
      ],
    }} />))
    expect(canvasWidth()).toBeLessThanOrEqual(760)

    await act(async () => root.render(<ChatSwimlane spec={{
      type: 'swimlane',
      title: 'Long process',
      lanes: [{ id: 'person', title: 'Person' }],
      steps: Array.from({ length: 8 }, (_, i) => ({ id: `s${i + 1}`, lane: 'person', title: `Step ${i + 1}` })),
    }} />))
    expect(canvasWidth()).toBeGreaterThan(760)
  })

  it('tightens sequence pitch to fit a normal chat column and renders message labels outside SVG', async () => {
    setViewportWidth(720)
    await act(async () => root.render(<ChatSequence spec={{
      type: 'sequence',
      title: 'Review cycle',
      participants: [
        { id: 'a', title: 'User' },
        { id: 'b', title: 'Agent' },
        { id: 'c', title: 'Tool' },
        { id: 'd', title: 'UI' },
      ],
      messages: [
        { from: 'a', to: 'b', label: 'Ask' },
        { from: 'b', to: 'c', label: 'Validate' },
        { from: 'c', to: 'b', label: 'Block', reply: true },
        { from: 'b', to: 'd', label: 'Render' },
      ],
    }} />))
    expect(canvasWidth()).toBeLessThanOrEqual(720)
    expect(host.querySelectorAll('svg foreignObject').length).toBe(0)
    expect(host.querySelectorAll('[data-sequence-message]').length).toBe(4)
  })
})

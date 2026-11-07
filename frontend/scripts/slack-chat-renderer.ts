// Pure formatter: no credentials, network client, routing or agent execution.
import { readFileSync } from 'node:fs'
import { toCard, toPartialCard } from '@json-render/chat'
import { cardToBlockKit, cardToFallbackText } from '@chat-adapter/slack'
import { Card, Section, CardText as Text, type CardChild } from 'chat'
import type { Spec } from '@json-render/core'
import { compileChatVisual } from '../src/components/ui/chatVisualSpec'
import type { ChatElement } from '../src/components/ui/chatElementSpec'


function project(widget: ChatElement) {
  const elements: Spec['elements'] = {}
  const children: string[] = []
  const add = (type: string, props: Record<string, unknown>) => {
    if (type === 'Text' && typeof props.content === 'string' && props.content.length > 2800) {
      for (let i = 0; i < props.content.length; i += 2800) add(type, { ...props, content: props.content.slice(i, i + 2800) })
      return
    }
    const id = `item${children.length}`
    elements[id] = { type, props, children: [] }
    children.push(id)
  }
  switch (widget.type) {
    case 'chart':
      add('Chart', { title: widget.title, chart: { type: widget.style, categories: widget.data.map(point => point.label), series: [{ name: widget.unit || 'Value', values: widget.data.map(point => point.value) }] } })
      break
    case 'table': add('Table', { headers: widget.headers, rows: widget.rows }); break
    case 'timeline': add('Table', { headers: ['When', 'Event', 'Detail'], rows: widget.items.map(item => [item.when, item.title, item.detail || '']) }); break
    case 'details': add('Text', { content: widget.body }); break
    case 'cards':
      for (const item of widget.items) {
        add('Text', { content: item.title, style: 'bold' })
        if (item.description) add('Text', { content: item.description })
        // Private workspace images remain private; Slack cannot authenticate them.
        if (item.image?.startsWith('https://') || item.image?.startsWith('http://')) add('Image', { url: item.image, alt: item.title })
        if (item.href) add('Text', { content: `[Open](${item.href})` })
      }
      break
    case 'diagram':
      add('Table', { headers: ['Part', 'Description'], rows: widget.nodes.map(node => [node.title, [node.description, ...node.items?.map(item => `${item.title}: ${item.description || ''}`) || []].filter(Boolean).join('\n')]) })
      for (const edge of widget.edges) add('Text', { content: `${widget.nodes.find(node => node.id === edge.from)!.title} ${edge.bidirectional ? '↔' : '→'} ${widget.nodes.find(node => node.id === edge.to)!.title}${edge.label ? `: ${edge.label}` : ''}` })
      break
    case 'swimlane': add('Table', { headers: ['Who', 'Step', 'Detail'], rows: widget.steps.map(step => [widget.lanes.find(lane => lane.id === step.lane)!.title, step.title, step.description || '']) }); break
    case 'sequence': add('Table', { headers: ['From', 'To', 'Message'], rows: widget.messages.map(message => [widget.participants.find(actor => actor.id === message.from)!.title, widget.participants.find(actor => actor.id === message.to)!.title, message.label]) }); break
  }
  elements.root = { type: 'Card', props: widget.type === 'chart' ? {} : { title: widget.title }, children }
  return { root: 'root', elements }
}

function format(content: string, final: boolean) {
  const children: CardChild[] = []
  let source: string[] | null = null, marker = '', regularFence = ''
  const plain: string[] = []
  const flushText = () => {
    const body = plain.join('\n').trim()
    plain.length = 0
    // Preserve the complete answer; native Slack section text is capped at3000.
    for (let i = 0; i < body.length; i += 2800) children.push(Text(body.slice(i, i + 2800)))
  }
  const visual = (closed: boolean) => {
    const spec = compileChatVisual(source!.join('\n'), !final && !closed)
    if (!spec) {
      children.push(Text(final || closed ? 'This visual could not be displayed. Ask for a text summary.' : 'Preparing visual…'))
      return
    }
    const element = spec.elements.visual
    const projected = project({ ...element.props, type: element.type } as ChatElement)
    const card = final || closed ? toCard(projected, { maxElements: 200 }) : toPartialCard(projected, { maxElements: 200 })
    if (card) children.push(Section([...(card.title ? [Text(card.title, { style: 'bold' })] : []), ...card.children]))
  }
  for (const line of content.replace(/\r\n?/g, '\n').split('\n')) {
    const opening = line.match(/^ {0,3}(`{3,}|~{3,})[ \t]*([^\s]*)[^\r]*$/)
    if (source) {
      if (opening && opening[1][0] === marker[0] && opening[1].length >= marker.length && !opening[2]) { visual(true); source = null; marker = '' }
      else source.push(line)
    } else if (regularFence) {
      plain.push(line)
      if (opening && opening[1][0] === regularFence[0] && opening[1].length >= regularFence.length && !opening[2]) regularFence = ''
    } else if (opening && opening[2] === 'chat-ui') {
      flushText(); source = []; marker = opening[1]
    } else {
      plain.push(line)
      if (opening) regularFence = opening[1]
    }
  }
  if (source) visual(false)
  flushText()
  const card = Card({ children })
  const blocks = cardToBlockKit(card)
  if (blocks.length > 50) return { blocks: [], text: cardToFallbackText(card), error: 'message_limit' }
  return { blocks, text: cardToFallbackText(card) }
}

try {
  const request = JSON.parse(readFileSync(0, 'utf8'))
  if (typeof request.content !== 'string' || request.content.length > 200000 || typeof request.final !== 'boolean') throw new Error('input')
  process.stdout.write(JSON.stringify(format(request.content, request.final)))
} catch {
  // Never send a prop value, model spec, credential or stack back to the server.
  process.stdout.write(JSON.stringify({ error: 'visual_format_failed' }))
}

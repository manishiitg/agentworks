import { useId } from 'react'
import { ChatDiagramFrame } from './ChatDiagramFrame'
import type { ChatElement } from './chatElementSpec'

const arrowID = (value: string) => `process-arrow-${value.replace(/[^a-zA-Z0-9_-]/g, '')}`
const labelLines = (value: string, width: number) => {
  const units = Array.from(value).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 255 ? 2 : 1), 0)
  return Math.max(1, Math.ceil(units / Math.max(8, Math.floor(width / 7))))
}

export function ChatSwimlane({ spec }: { spec: Extract<ChatElement, { type: 'swimlane' }> }) {
  const arrow = arrowID(useId())
  const labelWidth = 144, pitch = 248, cardWidth = 192, cardHeight = 156
  const laneHeight = Math.max(196, ...spec.lanes.map(lane => labelLines(lane.title, labelWidth - 32) * 20 + 32))
  const width = labelWidth + spec.steps.length * pitch + 24, height = spec.lanes.length * laneHeight
  const positions = spec.steps.map((step, index) => ({ step, x: labelWidth + index * pitch + pitch / 2, y: spec.lanes.findIndex(lane => lane.id === step.lane) * laneHeight + laneHeight / 2 }))
  return <ChatDiagramFrame kind="swimlane" title={spec.title} width={width} height={height}
    description={<ol>{spec.steps.map(step => <li key={step.id}>{spec.lanes.find(lane => lane.id === step.lane)?.title}: {step.title}{step.description ? ` — ${step.description}` : ''}</li>)}</ol>}>
    {spec.lanes.map((lane, index) => <section key={lane.id} data-swimlane-lane={lane.id} aria-label={lane.title} className={`absolute left-0 border-b border-border last:border-b-0 ${index % 2 ? 'bg-muted/25' : 'bg-background/30'}`} style={{ top: index * laneHeight, width, height: laneHeight }}>
      <div className="flex h-full items-center border-r border-border px-4 text-sm font-semibold" style={{ width: labelWidth }}><span className="break-words">{lane.title}</span></div>
    </section>)}
    <svg className="absolute inset-0 text-muted-foreground/70" width={width} height={height} aria-hidden="true">
      <defs><marker id={arrow} viewBox="0 0 8 8" refX="8" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8Z" fill="currentColor" /></marker></defs>
      {positions.slice(1).map((next, index) => {
        const previous = positions[index], start = previous.x + cardWidth / 2, end = next.x - cardWidth / 2, middle = (start + end) / 2
        return <path key={next.step.id} d={`M${start},${previous.y} C${middle},${previous.y} ${middle},${next.y} ${end},${next.y}`} fill="none" stroke="currentColor" strokeWidth="1.5" markerEnd={`url(#${arrow})`} />
      })}
    </svg>
    {positions.map(({ step, x, y }, index) => <section key={step.id} data-swimlane-step={step.id} aria-label={`${index + 1}. ${spec.lanes.find(lane => lane.id === step.lane)?.title}: ${step.title}`} title={[step.title, step.description].filter(Boolean).join(' — ')} className="absolute flex flex-col gap-2 overflow-hidden rounded-2xl border border-border bg-card p-4 text-card-foreground shadow-sm" style={{ left: x - cardWidth / 2, top: y - cardHeight / 2, width: cardWidth, height: cardHeight }}>
      <span className="text-[11px] font-semibold leading-4 text-primary">{index + 1}</span>
      <div className="line-clamp-2 break-words text-sm font-semibold leading-5">{step.title}</div>
      {step.description && <div className="line-clamp-3 break-words text-xs leading-4 text-muted-foreground">{step.description}</div>}
    </section>)}
  </ChatDiagramFrame>
}

export function ChatSequence({ spec }: { spec: Extract<ChatElement, { type: 'sequence' }> }) {
  const arrow = arrowID(useId())
  const pitch = 248, headerWidth = 192, padding = 32
  const headerHeight = Math.max(76, ...spec.participants.map(actor => labelLines(actor.title, headerWidth - 32) * 20 + 24))
  const width = spec.participants.length * pitch + padding * 2
  const actorX = (id: string) => padding + spec.participants.findIndex(actor => actor.id === id) * pitch + headerWidth / 2
  let cursor = padding + headerHeight + 16
  const messages = spec.messages.map((message, index) => {
    const from = actorX(message.from), to = actorX(message.to), self = from === to
    const labelWidth = self ? 144 : Math.abs(to - from) - 24
    const label = `${index + 1}. ${message.label}`
    const labelHeight = labelLines(label, labelWidth) * 16 + 8
    const y = cursor + labelHeight + 8, top = cursor
    cursor = y + (self ? 64 : 36)
    return { message, from, to, self, label, labelWidth, labelHeight, y, top }
  })
  const height = cursor + 16
  return <ChatDiagramFrame kind="sequence" title={spec.title} width={width} height={height}
    description={<ol>{spec.messages.map((message, index) => <li key={index}>{spec.participants.find(actor => actor.id === message.from)?.title} {message.reply ? 'replies to' : 'sends to'} {spec.participants.find(actor => actor.id === message.to)?.title}: {message.label}</li>)}</ol>}>
    <svg className="absolute inset-0 text-muted-foreground/70" width={width} height={height} aria-hidden="true">
      <defs><marker id={arrow} viewBox="0 0 8 8" refX="8" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8Z" fill="currentColor" /></marker></defs>
      {spec.participants.map(actor => <line key={actor.id} x1={actorX(actor.id)} x2={actorX(actor.id)} y1={padding + headerHeight} y2={height - 16} stroke="currentColor" strokeWidth="1" strokeDasharray="4 6" opacity="0.4" />)}
      {messages.map(({ message, from, to, self, label, labelWidth, labelHeight, y, top }, index) => <g key={index} data-sequence-message={index + 1}>
        <foreignObject x={self ? from + 12 : Math.min(from, to) + 12} y={top} width={labelWidth} height={labelHeight}>
          <div title={message.label} className="flex h-full items-center justify-center bg-background/90 px-1 text-center text-xs leading-4 text-foreground"><span className="break-words">{label}</span></div>
        </foreignObject>
        <path d={self ? `M${from},${y} H${from + 128} V${y + 32} H${from}` : `M${from},${y} H${to}`} fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray={message.reply ? '5 4' : undefined} markerEnd={`url(#${arrow})`} />
      </g>)}
    </svg>
    {spec.participants.map(actor => <section key={actor.id} data-sequence-participant={actor.id} aria-label={actor.title} title={actor.title} className="absolute flex items-center justify-center rounded-2xl border border-border bg-card px-4 text-center text-sm font-semibold leading-5 text-card-foreground shadow-sm" style={{ left: actorX(actor.id) - headerWidth / 2, top: padding, width: headerWidth, height: headerHeight }}><span className="break-words">{actor.title}</span></section>)}
  </ChatDiagramFrame>
}

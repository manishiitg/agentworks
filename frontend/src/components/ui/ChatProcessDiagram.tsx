import { useId, useRef } from 'react'
import { ChatDiagramFrame, useDiagramViewportWidth } from './ChatDiagramFrame'
import type { ChatElement } from './chatElementSpec'

const arrowID = (value: string) => `process-arrow-${value.replace(/[^a-zA-Z0-9_-]/g, '')}`
const labelLines = (value: string, width: number) => {
  const units = Array.from(value).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 255 ? 2 : 1), 0)
  return Math.max(1, Math.ceil(units / Math.max(8, Math.floor(width / 7))))
}

export function ChatSwimlane({ spec }: { spec: Extract<ChatElement, { type: 'swimlane' }> }) {
  const arrow = arrowID(useId())
  const region = useRef<HTMLDivElement>(null)
  const viewportWidth = useDiagramViewportWidth(region)
  const defaultWidth = 144 + spec.steps.length * 248 + 24
  const compact = viewportWidth !== null && viewportWidth < defaultWidth
  const labelWidth = compact ? Math.min(144, Math.max(108, Math.floor(viewportWidth * 0.18))) : 144
  const rightPadding = compact ? 12 : 24
  const fitPitch = compact && spec.steps.length > 0
    ? Math.floor((viewportWidth - labelWidth - rightPadding) / spec.steps.length)
    : 248
  const pitch = Math.max(148, Math.min(248, fitPitch))
  const gap = Math.max(20, Math.min(56, Math.round(pitch * 0.22)))
  const cardWidth = pitch - gap
  const cardHeight = 156
  const laneHeight = Math.max(196, ...spec.lanes.map(lane => labelLines(lane.title, labelWidth - 24) * 20 + 32))
  const width = labelWidth + spec.steps.length * pitch + rightPadding, height = spec.lanes.length * laneHeight
  const positions = spec.steps.map((step, index) => ({ step, x: labelWidth + index * pitch + pitch / 2, y: spec.lanes.findIndex(lane => lane.id === step.lane) * laneHeight + laneHeight / 2 }))
  return <ChatDiagramFrame kind="swimlane" title={spec.title} width={width} height={height} viewportRef={region}
    description={<ol>{spec.steps.map(step => <li key={step.id}>{spec.lanes.find(lane => lane.id === step.lane)?.title}: {step.title}{step.description ? ` — ${step.description}` : ''}</li>)}</ol>}>
    {spec.lanes.map((lane, index) => <section key={lane.id} data-swimlane-lane={lane.id} aria-label={lane.title} className={`absolute left-0 border-b border-border last:border-b-0 ${index % 2 ? 'bg-muted/25' : 'bg-background/30'}`} style={{ top: index * laneHeight, width, height: laneHeight }}>
      <div className="flex h-full items-center border-r border-border px-3 text-sm font-semibold" style={{ width: labelWidth }}><span className="break-words">{lane.title}</span></div>
    </section>)}
    <svg className="pointer-events-none absolute inset-0 text-muted-foreground/70" width={width} height={height} aria-hidden="true">
      <defs><marker id={arrow} viewBox="0 0 8 8" refX="8" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8Z" fill="currentColor" /></marker></defs>
      {positions.slice(1).map((next, index) => {
        const previous = positions[index], start = previous.x + cardWidth / 2, end = next.x - cardWidth / 2, middle = (start + end) / 2
        return <path key={next.step.id} d={`M${start},${previous.y} C${middle},${previous.y} ${middle},${next.y} ${end},${next.y}`} fill="none" stroke="currentColor" strokeWidth="1.5" markerEnd={`url(#${arrow})`} />
      })}
    </svg>
    {positions.map(({ step, x, y }, index) => <section key={step.id} data-swimlane-step={step.id} aria-label={`${index + 1}. ${spec.lanes.find(lane => lane.id === step.lane)?.title}: ${step.title}`} title={[step.title, step.description].filter(Boolean).join(' — ')} className="absolute flex flex-col gap-2 overflow-hidden rounded-2xl border border-border bg-card p-3.5 text-card-foreground shadow-sm" style={{ left: x - cardWidth / 2, top: y - cardHeight / 2, width: cardWidth, height: cardHeight }}>
      <span className="text-[11px] font-semibold leading-4 text-primary">{index + 1}</span>
      <div className="line-clamp-2 break-words text-sm font-semibold leading-5">{step.title}</div>
      {step.description && <div className="line-clamp-3 break-words text-xs leading-4 text-muted-foreground">{step.description}</div>}
    </section>)}
  </ChatDiagramFrame>
}

export function ChatSequence({ spec }: { spec: Extract<ChatElement, { type: 'sequence' }> }) {
  const arrow = arrowID(useId())
  const region = useRef<HTMLDivElement>(null)
  const viewportWidth = useDiagramViewportWidth(region)
  const count = spec.participants.length
  const lastParticipant = spec.participants[count - 1]?.id
  const hasRightSelf = Boolean(lastParticipant && spec.messages.some(message => message.from === message.to && message.from === lastParticipant))
  const defaultWidth = Math.max(
    32 + (count - 1) * 248 + 192 + 32,
    hasRightSelf ? 32 + (count - 1) * 248 + 96 + 156 + 32 : 0,
  )
  const compact = viewportWidth !== null && viewportWidth < defaultWidth
  const padding = compact ? 16 : 32
  const selfOverhang = hasRightSelf ? 96 : 0
  const fitPitch = compact && count > 1
    ? Math.floor((viewportWidth - padding * 2 + 24 - selfOverhang) / count)
    : 248
  const pitch = Math.max(152, Math.min(248, fitPitch))
  const headerWidth = compact ? Math.min(192, Math.max(120, Math.min(pitch - 24, viewportWidth - padding * 2))) : 192
  const headerHeight = Math.max(76, ...spec.participants.map(actor => labelLines(actor.title, headerWidth - 24) * 20 + 24))
  const actorX = (id: string) => padding + spec.participants.findIndex(actor => actor.id === id) * pitch + headerWidth / 2
  const width = Math.max(
    padding + Math.max(0, count - 1) * pitch + headerWidth + padding,
    hasRightSelf ? actorX(lastParticipant!) + 156 + padding : 0,
  )
  let cursor = padding + headerHeight + 16
  const messages = spec.messages.map((message, index) => {
    const from = actorX(message.from), to = actorX(message.to), self = from === to
    const labelWidth = self ? 144 : Math.max(88, Math.abs(to - from) - 24)
    const label = `${index + 1}. ${message.label}`
    const labelHeight = labelLines(label, labelWidth) * 16 + 8
    const y = cursor + labelHeight + 8, top = cursor
    cursor = y + (self ? 64 : 36)
    return { message, from, to, self, label, labelWidth, labelHeight, y, top }
  })
  const height = cursor + 16
  return <ChatDiagramFrame kind="sequence" title={spec.title} width={width} height={height} viewportRef={region}
    description={<ol>{spec.messages.map((message, index) => <li key={index}>{spec.participants.find(actor => actor.id === message.from)?.title} {message.reply ? 'replies to' : 'sends to'} {spec.participants.find(actor => actor.id === message.to)?.title}: {message.label}</li>)}</ol>}>
    <svg className="pointer-events-none absolute inset-0 text-muted-foreground/70" width={width} height={height} aria-hidden="true">
      <defs><marker id={arrow} viewBox="0 0 8 8" refX="8" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8Z" fill="currentColor" /></marker></defs>
      {spec.participants.map(actor => <line key={actor.id} x1={actorX(actor.id)} x2={actorX(actor.id)} y1={padding + headerHeight} y2={height - 16} stroke="currentColor" strokeWidth="1" strokeDasharray="4 6" opacity="0.4" />)}
      {messages.map(({ message, from, to, self, y }, index) => (
        <path key={index} d={self ? `M${from},${y} H${from + 128} V${y + 32} H${from}` : `M${from},${y} H${to}`} fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray={message.reply ? '5 4' : undefined} markerEnd={`url(#${arrow})`} />
      ))}
    </svg>
    {messages.map(({ message, from, to, self, label, labelWidth, labelHeight, top }, index) => (
      <div key={index} data-sequence-message={index + 1} title={message.label}
        className="absolute flex items-center justify-center rounded-sm bg-background/95 px-1 text-center text-xs leading-4 text-foreground"
        style={{ left: self ? from + 12 : Math.min(from, to) + 12, top, width: labelWidth, height: labelHeight }}>
        <span className="break-words">{label}</span>
      </div>
    ))}
    {spec.participants.map(actor => <section key={actor.id} data-sequence-participant={actor.id} aria-label={actor.title} title={actor.title} className="absolute flex items-center justify-center rounded-2xl border border-border bg-card px-3 text-center text-sm font-semibold leading-5 text-card-foreground shadow-sm" style={{ left: actorX(actor.id) - headerWidth / 2, top: padding, width: headerWidth, height: headerHeight }}><span className="break-words">{actor.title}</span></section>)}
  </ChatDiagramFrame>
}

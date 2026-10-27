import { useState, type ReactNode } from 'react'
import { ImageOff } from 'lucide-react'
import { WorkspaceImage } from './WorkspaceImage'
import { workspaceCardImage, type ChatCard, type ChatElement } from './chatElementSpec'

function RemoteImage({ src, title }: { src: string; title: string }) {
  const [failed, setFailed] = useState(false)
  return failed
    ? <span className="flex h-full flex-col items-center justify-center gap-2 p-2 text-center text-xs text-muted-foreground"><ImageOff className="h-5 w-5" aria-hidden />Image unavailable</span>
    : <img src={src} alt={title} loading="lazy" referrerPolicy="no-referrer" onError={() => setFailed(true)} className="h-full w-full object-cover" />
}

function CardImage({ item, grid }: { item: ChatCard; grid: boolean }) {
  if (!item.image) return null
  return <div className={grid ? 'aspect-[4/3] w-full shrink-0 overflow-hidden bg-muted' : 'h-32 w-24 shrink-0 overflow-hidden rounded-xl bg-muted sm:h-36 sm:w-28'}>
    {workspaceCardImage(item.image)
      ? <WorkspaceImage path={item.image} alt={item.title} className="h-full w-full object-cover" />
      : <RemoteImage key={item.image} src={item.image} title={item.title} />}
  </div>
}

const markdownLabel = (value: string) => value.replace(/[\r\n]/g, ' ').replace(/[\\[\]`*_<>]/g, '\\$&')

export function ChatCards({ spec, renderMarkdown }: {
  spec: Extract<ChatElement, { type: 'cards' }>
  renderMarkdown: (body: string) => ReactNode
}) {
  const grid = spec.layout === 'grid'
  return <section className="not-prose my-4 min-w-0 max-w-3xl text-foreground" aria-label={spec.title} data-chat-element="cards">
    <h3 className="mb-3 break-words text-sm font-semibold">{spec.title}</h3>
    <ul className={grid ? 'm-0 grid list-none grid-cols-1 gap-4 p-0 sm:grid-cols-2' : 'm-0 list-none divide-y divide-border p-0'}>
      {spec.items.map((item, index) => <li key={index} className={grid
        ? 'min-w-0 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground'
        : 'flex min-w-0 items-start gap-4 py-5 first:pt-0 last:pb-0'}>
        <CardImage item={item} grid={grid} />
        <div className={grid ? 'min-w-0 p-4' : 'min-w-0 flex-1 py-0.5'}>
          <div className="break-words text-sm font-semibold leading-6 [&_p]:!m-0 [&_p]:!text-sm [&_a]:!font-semibold [&_a]:!text-foreground [&_a]:!no-underline hover:[&_a]:!underline">
            {item.href ? renderMarkdown(`[${markdownLabel(item.title)}](<${item.href}>)`) : item.title}
          </div>
          {item.description && <p className="mb-0 mt-1.5 whitespace-pre-wrap break-words text-sm leading-6 text-muted-foreground">{item.description}</p>}
        </div>
      </li>)}
    </ul>
  </section>
}

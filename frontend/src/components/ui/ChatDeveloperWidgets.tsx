import type { ReactNode } from 'react'
import { CircleCheck, CircleX, FileCode2, Loader2, Minus } from 'lucide-react'
import { workspaceCardImage, type ChatCheck, type ChatElement } from './chatElementSpec'

const frame = 'not-prose my-4 min-w-0 max-w-2xl overflow-hidden rounded-xl border border-border bg-muted/20 text-foreground'
const linkClass = 'min-w-0 break-words text-sm [&_p]:!m-0 [&_a]:!text-foreground [&_a]:underline'
const label = (text: string) => text.replace(/[\r\n]/g, ' ').replace(/[\\[\]`*_<>]/g, '\\$&')
// Reuse Markdown's existing authorized workspace/external navigation. Never invent
// a diff route or imply that a relative source filename is a workspace address.
const target = (href: string) => workspaceCardImage(href) ? `#workspace/${encodeURIComponent(href)}` : href
const link = (text: string, href: string) => `[${label(text)}](<${target(href)}>)`

type Markdown = (body: string) => ReactNode

export function ChatFileChanges({ spec, renderMarkdown }: { spec: Extract<ChatElement, { type: 'file_changes' }>; renderMarkdown: Markdown }) {
  return <section className={frame} aria-label={spec.title} data-chat-element="file_changes">
    <h3 className="border-b border-border px-3 py-2 text-sm font-semibold">{spec.title}</h3>
    <ul className="m-0 list-none divide-y divide-border p-0">
      {spec.files.map((file, index) => {
        const href = file.href || (workspaceCardImage(file.path) ? file.path : undefined)
        return <li key={index} className="px-3 py-2">
          <div className="flex min-w-0 items-start gap-2">
            <FileCode2 aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
            <div className={`${linkClass} flex-1 font-mono text-xs`}>{href ? renderMarkdown(link(file.path, href)) : file.path}</div>
            <span className="flex shrink-0 gap-2 text-xs tabular-nums">
              {file.added !== undefined && <span className="text-emerald-700 dark:text-emerald-400" aria-label={`${file.added} lines added`}>+{file.added}</span>}
              {file.removed !== undefined && <span className="text-red-700 dark:text-red-400" aria-label={`${file.removed} lines removed`}>−{file.removed}</span>}
            </span>
          </div>
          {file.summary && <p className="mb-0 mt-1 whitespace-pre-wrap break-words pl-6 text-xs text-muted-foreground">{file.summary}</p>}
        </li>
      })}
    </ul>
  </section>
}

const states = {
  passed: { label: 'Passed', icon: CircleCheck, color: 'text-emerald-700 dark:text-emerald-400' },
  failed: { label: 'Failed', icon: CircleX, color: 'text-red-700 dark:text-red-400' },
  running: { label: 'Running', icon: Loader2, color: 'text-muted-foreground' },
  not_run: { label: 'Not run', icon: Minus, color: 'text-muted-foreground' },
} as const

function CheckLabel({ check }: { check: ChatCheck }) {
  const state = states[check.status]
  const Icon = state.icon
  return <span className="inline-flex w-full min-w-0 items-start gap-2 align-middle">
    <Icon aria-hidden className={`mt-0.5 h-4 w-4 shrink-0 ${state.color} ${check.status === 'running' ? 'animate-spin' : ''}`} />
    <span className="min-w-0 flex-1 break-words text-sm">{check.name}</span>
    <span className={`shrink-0 text-xs ${state.color}`}>{state.label}</span>
  </span>
}

export function ChatChecks({ spec, renderMarkdown }: { spec: Extract<ChatElement, { type: 'checks' }>; renderMarkdown: Markdown }) {
  return <section className={frame} aria-label={spec.title} data-chat-element="checks">
    <h3 className="border-b border-border px-3 py-2 text-sm font-semibold">{spec.title}</h3>
    <ul className="m-0 list-none divide-y divide-border p-0">
      {spec.checks.map((check, index) => <li key={index}>
        {check.detail || check.href ? <details>
          <summary className="cursor-pointer px-3 py-2 marker:text-muted-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"><CheckLabel check={check} /></summary>
          <div className="min-w-0 border-t border-border px-3 py-2">
            {check.detail && <pre className="m-0 max-h-60 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-muted-foreground">{check.detail}</pre>}
            {check.href && <div className={`${linkClass} mt-2`}>{renderMarkdown(link('Open details', check.href))}</div>}
          </div>
        </details> : <div className="px-3 py-2"><CheckLabel check={check} /></div>}
      </li>)}
    </ul>
    <p className="m-0 border-t border-border px-3 py-2 text-[11px] text-muted-foreground">Reported in this reply · not a live status feed</p>
  </section>
}

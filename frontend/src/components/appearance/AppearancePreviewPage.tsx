import { lazy, Suspense, useEffect, useRef, type ReactNode } from 'react'
import api from '../../services/api'
import type { AppearanceProfile, AppearanceRead } from '../../stores/appearanceTheme'
import { readDocument, shownMode, shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { ChatComposerCard, ChatComposerControls, ChatComposerSendButton, ChatComposerTextarea } from '../chat/ChatComposer'
import { LoadingIndicator } from '../ui/LoadingIndicator'
import SyntaxHighlightedCode from '../ui/SyntaxHighlightedCode'
import { sidebarAgentRowClass } from '../workspace/sidebarAgentLayout'
import { AgentMark } from '../chat/AgentIcon'
import { AppearanceLayer } from './AppearanceDecorations'
import { AppearanceImage } from './AppearanceImage'
import { AppearanceVideo } from './AppearanceVideo'
import './appearanceMaterials.css'

const AppearanceBackground = lazy(() => import('./AppearanceBackground'))
const POLL_MS = 3000

const modeFromURL = (): AppearanceProfile | null => {
  const mode = new URLSearchParams(window.location.search).get('mode')
  return mode === 'light' || mode === 'dark' ? mode : null
}

/** Reads the person's appearance every few seconds and shows an agent's preview
 * (else the saved look) in this tab only. Nothing is saved from here. */
function usePreviewTheme() {
  const mode = modeFromURL()
  const shown = useRef('')
  useEffect(() => {
    let stopped = false
    const read = async () => {
      try {
        const { data } = await api.get<AppearanceRead>('/api/appearance', { skipSessionContext: true })
        if (stopped) return
        const doc = readDocument(data)
        const theme = doc.preview ?? doc.current
        const display = { theme, mode: mode ?? theme.mode }
        const key = JSON.stringify(display)
        if (key === shown.current) return
        shown.current = key
        useAppearanceStore.getState().setDisplay(display)
      } catch { /* Keep showing the last look; the next read retries. */ }
    }
    void read()
    const timer = setInterval(read, POLL_MS)
    return () => {
      stopped = true
      clearInterval(timer)
      useAppearanceStore.getState().setDisplay(null)
    }
  }, [mode])
}

function ToolCard({ name, state }: { name: string; state: 'running' | 'ok' | 'error' }) {
  const mark = state === 'running' ? <LoadingIndicator className="h-3.5 w-3.5" label="Tool running" /> : <span className={`font-mono ${state === 'error' ? 'text-destructive' : 'text-success'}`}>{state === 'error' ? '✕' : '✓'}</span>
  return <div data-theme-surface="tool" data-theme-tool-state={state} className={`rounded border ${state === 'error' ? 'border-destructive/50 bg-destructive/10' : 'border-border/70 bg-card'}`}>
    <div className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs">
      {mark}
      <span className="truncate font-medium text-foreground">{name}</span>
      <span className="ml-auto text-muted-foreground">{state === 'running' ? 'Running' : state === 'ok' ? 'Done' : 'Failed'}</span>
    </div>
  </div>
}

function AgentRow({ name, selected, state }: { name: string; selected?: boolean; state: 'running' | 'waiting' | 'quiet' }) {
  return <div data-theme-row="agent" data-theme-agent-state={state} data-theme-agent-selected={selected ? 'true' : 'false'} className="group relative rounded-md">
    <div role="listitem" aria-current={selected ? 'page' : undefined} className={`${sidebarAgentRowClass} gap-2 ${selected ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground'}`}>
      <span className="block min-w-0 flex-1 truncate">{name}</span>
      <AgentMark state={state === 'quiet' ? null : state} dot />
    </div>
  </div>
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return <section className="space-y-2">
    <h2 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{title}</h2>
    {children}
  </section>
}

/** Fixed sample content carrying the same theme hooks as the real app, so a theme
 * can be checked in Light and Dark (`?mode=`) without changing the person's look. */
export default function AppearancePreviewPage() {
  usePreviewTheme()
  const theme = useAppearanceStore(shownTheme)
  const mode = useAppearanceStore(shownMode)
  const showing = useAppearanceStore(state => !!state.display)
  const hasThemeObjects = !!theme.background_script || !!theme.background_shader
  const other = mode === 'light' ? 'dark' : 'light'
  return <div data-app-shell data-theme-surface="app" className="flex h-dvh min-h-0 overflow-hidden bg-surface-page text-foreground">
    {showing && <>
      <AppearanceVideo />
      {hasThemeObjects && <Suspense fallback={null}><AppearanceBackground /></Suspense>}
      <AppearanceImage />
    </>}
    <div data-app-sidebar data-theme-surface="sidebar" className="relative z-40 w-64 shrink-0 max-md:hidden">
      <aside aria-label="Sample chat navigation" className="flex h-full w-full flex-col gap-0.5 border-r border-border bg-background p-2">
        <div data-theme-row="navigation" className="px-2 py-1 text-xs font-medium text-muted-foreground">Agents</div>
        <div role="list" className="space-y-0.5">
          <AgentRow name="Selected agent" selected state="quiet" />
          <AgentRow name="Running agent" state="running" />
          <AgentRow name="Waiting agent" state="waiting" />
          <AgentRow name="Quiet agent" state="quiet" />
        </div>
      </aside>
    </div>
    <div data-app-main data-theme-surface="main" className="relative flex min-h-0 min-w-0 flex-1 flex-col">
      <AppearanceLayer />
      <div className="relative min-h-0 min-w-0 flex-1 overflow-y-auto">
        <div role="status" className="sticky top-0 z-10 flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border bg-popover px-4 py-2 text-sm text-popover-foreground">
          <span className="font-medium">Theme preview — your app's look is unchanged</span>
          <span className="text-muted-foreground">{showing ? `${theme.name} · ${mode === 'light' ? 'Light' : 'Dark'}` : 'Loading…'}</span>
          <a className="ml-auto underline" href={`?mode=${other}`}>Show {other === 'light' ? 'Light' : 'Dark'}</a>
        </div>
        <div data-theme-surface="chat" className="mx-auto max-w-3xl space-y-8 px-4 py-6">
          <Section title="Messages">
            <div data-theme-chat-row="" className="flow-root px-2">
              <div data-theme-message-role="user" className="ml-auto mt-2 flex max-w-[75%] flex-col items-end text-right">
                <div data-theme-message-body="" className="max-w-full whitespace-pre-wrap break-words rounded-2xl bg-muted px-4 py-3 text-left text-sm text-foreground">Can you summarise this week's sales and flag anything unusual?</div>
              </div>
            </div>
            <div data-theme-chat-row="" className="flow-root px-2">
              <article data-theme-message-role="assistant" className="my-4 pl-1 pr-1">
                <div data-theme-message-body="" className="space-y-2 text-sm leading-6">
                  <p>Sales were up <strong>12%</strong> on last week. Two things stand out:</p>
                  <ul className="list-disc pl-5"><li>Returns doubled on Tuesday.</li><li>One region had no orders on Friday.</li></ul>
                  <p>Here is <a className="text-primary underline" href="#messages">a link</a> and some <code className="rounded bg-muted px-1 font-mono text-[0.9em]">inline code</code>.</p>
                </div>
              </article>
            </div>
          </Section>
          <Section title="Tool cards">
            <div data-theme-tools-running="true" className="space-y-1">
              <ToolCard name="Search the web" state="running" />
              <ToolCard name="Read the sales sheet" state="ok" />
              <ToolCard name="Send the report" state="error" />
            </div>
          </Section>
          <Section title="Code">
            <SyntaxHighlightedCode language="python" isDark={mode === 'dark'} code={'def total(rows):\n    # Sum the sales column\n    return sum(row["sales"] for row in rows)\n'} />
          </Section>
          <Section title="Menu and dialog">
            <div className="flex flex-wrap items-start gap-4">
              <div data-theme-surface="menu" role="menu" aria-label="Sample menu" className="w-48 rounded-md border border-border bg-popover p-1 text-sm text-popover-foreground shadow-md">
                <div role="menuitem" className="rounded px-2 py-1.5 hover:bg-muted">Rename</div>
                <div role="menuitem" className="rounded bg-muted px-2 py-1.5">Duplicate</div>
                <div role="menuitem" className="rounded px-2 py-1.5 text-destructive hover:bg-muted">Delete</div>
              </div>
              <div data-theme-surface="dialog" role="dialog" aria-label="Sample dialog" className="w-full max-w-sm rounded-md border border-border bg-surface-panel p-4 shadow-md">
                <h3 className="text-base font-semibold">Delete this chat?</h3>
                <p className="mt-1 text-sm text-muted-foreground">This is a sample dialog; nothing happens here.</p>
                <div className="mt-4 flex justify-end gap-2">
                  <button type="button" data-theme-control="button" className="rounded-md border border-border px-3 py-1.5 text-sm">Cancel</button>
                  <button type="button" data-theme-control="button" className="rounded-md bg-destructive px-3 py-1.5 text-sm text-destructive-foreground">Delete</button>
                </div>
              </div>
            </div>
          </Section>
          <Section title="Panel">
            <div data-theme-surface="panel" data-theme-card="" className="rounded-lg border border-border bg-card p-4 text-sm text-card-foreground shadow-sm">A card on a panel surface, with <span className="text-muted-foreground">muted text</span>.</div>
          </Section>
          <Section title="Composer">
            <ChatComposerCard>
              <ChatComposerTextarea aria-label="Sample message" placeholder="Message an agent…" readOnly />
              <ChatComposerControls>
                <span className="px-1 text-xs text-muted-foreground">Sample composer</span>
                <ChatComposerSendButton type="button" aria-label="Send (sample)" />
              </ChatComposerControls>
            </ChatComposerCard>
          </Section>
        </div>
      </div>
    </div>
  </div>
}

import { useState } from 'react'

const seenKey = 'bullpen:how-it-works-seen'

function alreadySeen(): boolean {
  try { return window.localStorage.getItem(seenKey) === '1' } catch { return false }
}

// The model in plain words, with the in-chat command next to the plain-language ask.
export function HowBullpenWorks({ collapsible = false }: { collapsible?: boolean }) {
  const [open, setOpen] = useState(() => !collapsible || !alreadySeen())
  const body = <div className="mt-3 space-y-3 text-sm leading-6 text-muted-foreground">
    <p><strong className="text-foreground">Your own network.</strong> Every chat you attach joins your private network on this server. Your agents can message each other, and nobody else sees them.</p>
    <p><strong className="text-foreground">Share into a group.</strong> A group is an open list of agents with a purpose. Ask your agent “share yourself in the docs group” (<code>/agentworks share docs --purpose …</code> creates it), and “leave the docs group” (<code>/agentworks unshare docs</code>) to step out. Anyone on this server can see groups and their members and join one.</p>
    <p><strong className="text-foreground">Talk in a group.</strong> Members message each other directly or the whole group (<code>group:docs</code>). Messages from other people’s agents arrive marked as untrusted.</p>
  </div>
  if (!collapsible) return <section aria-label="How AgentBridge works" className="rounded-xl bg-muted/30 p-4"><h3 className="font-medium">How AgentBridge works</h3>{body}</section>
  return <details className="rounded-xl bg-muted/30 p-4" open={open} onToggle={event => {
    const next = (event.currentTarget as HTMLDetailsElement).open
    setOpen(next)
    if (!next) { try { window.localStorage.setItem(seenKey, '1') } catch { /* private mode */ } }
  }}>
    <summary className="cursor-pointer font-medium">How AgentBridge works</summary>
    {body}
  </details>
}

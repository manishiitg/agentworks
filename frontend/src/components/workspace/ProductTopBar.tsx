import type { ReactNode } from 'react'

/** The outer product header used by Goals, Crew, Code, and other surfaces. */
export function ProductTopBar({ children }: { children: ReactNode }) {
  return <div data-terminal-focus-chrome="header" className="shrink-0 border-b border-border bg-muted px-4 py-2">
    <div className="flex flex-wrap items-center justify-between gap-3 md:flex-nowrap">{children}</div>
  </div>
}

export function ProductTopBarMain({ children }: { children: ReactNode }) {
  return <div className="flex min-w-0 items-center gap-3">{children}</div>
}

export function ProductTopBarActions({ children }: { children: ReactNode }) {
  return <div className="flex shrink-0 items-center gap-2">{children}</div>
}

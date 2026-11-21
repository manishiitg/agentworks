import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'

/**
 * A log in one scrollable container, with a Copy button that copies all of it (not only what is on screen).
 * The title says what the log is; the text is shown exactly as given.
 */
export function CrewLogBox({ title, meta, text, testId }: { title: ReactNode; meta?: ReactNode; text: string; testId?: string }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1500)
    } catch { /* The browser refused the copy: the text can still be selected by hand. */ }
  }
  return <div data-testid={testId} className="overflow-hidden rounded-lg border border-border bg-muted/20">
    <div className="flex items-center justify-between gap-3 border-b border-border px-3 py-2">
      <p className="min-w-0 flex-1 text-xs font-semibold">{title}</p>
      <button type="button" onClick={() => void copy()} aria-label="Copy the whole log" title="Copy the whole log"
        className="flex shrink-0 items-center gap-1 rounded-full border border-border px-2.5 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
        {copied ? <Check className="h-3.5 w-3.5" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}{copied ? 'Copied' : 'Copy'}
      </button>
    </div>
    {meta && <p className="border-b border-border px-3 py-1.5 text-[11px] text-muted-foreground">{meta}</p>}
    <pre tabIndex={0} className="max-h-80 overflow-auto whitespace-pre-wrap break-words px-3 py-2.5 font-mono text-[11px] leading-relaxed text-muted-foreground">{text}</pre>
  </div>
}

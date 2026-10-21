import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from '../ui/Button'

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch { setCopied(false) }
  }
  return <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" title={copied ? 'Copied' : `Copy: ${label}`} onClick={() => void copy()}>
    {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
  </Button>
}

export function CommandRow({ command, label }: { command: string; label: string }) {
  return <div className="flex items-center gap-2">
    <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground" aria-label={label}>{command}</code>
    <CopyButton text={command} label={label} />
  </div>
}

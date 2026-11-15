import { useState } from 'react'
import { Check, Copy, RotateCcw } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { Button } from '../ui/Button'

export const APPEARANCE_PROMPT = `Customize my AgentWorks appearance to match this style:
[Describe the colours, fonts, spacing or background animation you want.]

Read my current theme first using settings action=appearance. Use the appearance tools, not application source files. Apply the look across the app, keep text readable and code monospace, and save it as a named theme. Keep project files and authored Artifact designs unchanged. Keep animations subtle and respect reduced motion. Tell me what changed and how to reset it.`

export default function AppearanceSettings() {
  const current = useAppearanceStore(state => state.current)
  const saving = useAppearanceStore(state => state.saving)
  const error = useAppearanceStore(state => state.error)
  const change = useAppearanceStore(state => state.change)
  const [prompt, setPrompt] = useState(APPEARANCE_PROMPT)
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState(false)
  const copy = async () => {
    try { await navigator.clipboard.writeText(prompt); setCopied(true); setCopyError(false) }
    catch { setCopyError(true) }
  }
  return <div className="max-w-2xl space-y-5">
    <p className="text-sm text-muted-foreground">Describe the look you want to any of your agents. Copy this prompt, edit the style, and paste it into a chat.</p>
    <label htmlFor="appearance-prompt" className="sr-only">Appearance prompt</label>
    <textarea id="appearance-prompt" className="min-h-64 w-full resize-y rounded-xl border border-border bg-card p-4 text-sm leading-relaxed text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={prompt} onChange={event => { setPrompt(event.target.value);setCopied(false) }} />
    <div className="flex flex-wrap items-center gap-3">
      <Button disabled={!prompt.trim()} onClick={() => void copy()}>{copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}{copied ? 'Copied' : 'Copy prompt'}</Button>
      <span role="status" className="text-xs text-muted-foreground">{copied ? 'Paste it into any agent chat.' : copyError ? 'Select the prompt above and copy it manually.' : ''}</span>
    </div>
    <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
      <span className="flex-1 text-sm text-muted-foreground">Current theme: <span className="text-foreground">{current.name}</span></span>
      <Button variant="ghost" size="sm" disabled={saving} onClick={() => void change('reset')}><RotateCcw className="h-3.5 w-3.5" />{saving ? 'Resetting…' : 'Reset to default'}</Button>
    </div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </div>
}

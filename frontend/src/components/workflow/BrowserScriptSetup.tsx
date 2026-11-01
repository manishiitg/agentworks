import { useState } from 'react'
import { Check, Code2, Copy, Loader2, Send } from 'lucide-react'
import { Button } from '../ui/Button'

const examples = {
  node: {
    install: 'curl -fsS -H "Authorization: Bearer $MCP_API_TOKEN" "$MCP_API_URL/tools/browser/packages/node" -o agentworks-playwright.tgz\nnpm install --save-dev ./agentworks-playwright.tgz @playwright/test\nnpx playwright install chromium',
    script: `import { chromium } from '@playwright/test'
import { attachLiveBrowser } from '@agentworks/playwright'

const browser = await chromium.launch()
const context = await browser.newContext()
const live = await attachLiveBrowser(context, { label: 'Node script' })
try {
  const page = await context.newPage()
  await page.goto('https://example.com')
  // Your script runs here; its browser appears in Running.
} finally {
  await live.stop()
  await browser.close()
}`,
  },
  python: {
    install: 'curl -fsS -H "Authorization: Bearer $MCP_API_TOKEN" "$MCP_API_URL/tools/browser/packages/python" -o agentworks-playwright-python.zip\npython3 -m pip install ./agentworks-playwright-python.zip\npython3 -m playwright install chromium',
    script: `from playwright.sync_api import sync_playwright
from agentworks_playwright import attach_live_browser

with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    context = browser.new_context()
    try:
        with attach_live_browser(context, label="Python script"):
            page = context.new_page()
            page.goto("https://example.com")
            # Your script runs here; its browser appears in Running.
    finally:
        browser.close()`,
  },
}

const agentBrowserExample = `agent_browser(command="status")
agent_browser(command="skills", args=["get", "core"])`

export function BrowserScriptSetup({ onSendToAgent }: { onSendToAgent?: (message: string) => Promise<unknown> }) {
  const [integration, setIntegration] = useState<'playwright' | 'agent-browser'>('playwright')
  const [language, setLanguage] = useState<'node' | 'python'>('node')
  const [copied, setCopied] = useState('')
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState('')
  async function copy(value: string, kind: string) {
    try { await navigator.clipboard.writeText(value); setCopied(kind) } catch { setCopied('') }
  }
  const example = examples[language]
  function resetFeedback() { setCopied(''); setSent(false); setError('') }
  async function sendToAgent() {
    if (!onSendToAgent || sending) return
    setSending(true); setSent(false); setError('')
    const name = language === 'node' ? 'Node.js' : 'Python'
    const message = integration === 'agent-browser' ? [
      'Use the managed AgentWorks agent_browser tool for browser work in this project. Adapt it to the current task using the project’s selected browser. First check live status and read the installed command guide:',
      '', '```text', agentBrowserExample, '```', '',
      'Follow the returned mode, authorized endpoints and tab guidance. Reuse a suitable project tab, or create a labeled tab when needed, and take a fresh snapshot before acting. Do not run the standalone agent-browser CLI through the shell, launch another browser, invent a CDP port, or change the selected browser. If the connection is unavailable, explain how to reconnect it.',
    ].join('\n') : [
      `Use the AgentWorks ${name} Playwright integration for browser scripts in this project so their live browser appears in the Browser panel under Running. Adapt this example to the current task and reuse existing project dependencies where possible. Run setup from this project's session; use its MCP_API_URL and MCP_API_TOKEN environment variables without putting credential values in code or logs.`,
      '', 'Package setup:', '```sh', example.install, '```', '', `${name} example:`,
      language === 'node' ? '```javascript' : '```python', example.script, '```',
    ].join('\n')
    try { await onSendToAgent(message); setSent(true) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not send to the agent. Try again.') }
    finally { setSending(false) }
  }
  return <details className="border-t border-border pt-3" aria-label="Use browser from code">
    <summary className="cursor-pointer text-xs text-muted-foreground"><Code2 className="mr-1.5 inline h-3.5 w-3.5" />Use from code</summary>
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap gap-1" role="group" aria-label="Browser library">{(['playwright', 'agent-browser'] as const).map(value => <Button key={value} size="sm" variant={integration === value ? 'secondary' : 'ghost'} aria-pressed={integration === value} disabled={sending} onClick={() => { setIntegration(value); resetFeedback() }}>{value === 'playwright' ? 'Playwright' : 'agent-browser'}</Button>)}</div>
      {integration === 'playwright' ? <>
        <p className="text-xs text-muted-foreground">Scripts and tests with a live browser in this panel.</p>
        <div className="flex gap-1" role="group" aria-label="Script language">{(['node', 'python'] as const).map(value => <Button key={value} size="xs" variant={language === value ? 'secondary' : 'ghost'} aria-pressed={language === value} disabled={sending} onClick={() => { setLanguage(value); resetFeedback() }}>{value === 'node' ? 'Node.js' : 'Python'}</Button>)}</div>
        <details><summary className="cursor-pointer text-xs text-muted-foreground">Setup details</summary><p className="mt-2 text-xs text-muted-foreground">Run in this project’s terminal. Its session supplies the connection; no keys go in your script.</p><pre className="mt-2 overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{example.install}</code></pre><Button size="xs" variant="ghost" onClick={() => void copy(example.install, 'install')}>{copied === 'install' ? <Check /> : <Copy />}{copied === 'install' ? 'Copied' : 'Copy install command'}</Button></details>
        <pre className="overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{example.script}</code></pre>
      </> : <>
        <p className="text-xs text-muted-foreground">Agent commands using this project’s selected browser.</p>
        <pre className="overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{agentBrowserExample}</code></pre>
        <details><summary className="cursor-pointer text-xs text-muted-foreground">Setup details</summary><p className="mt-2 text-xs text-muted-foreground">The agent uses the built-in browser tool. Check its live status and command guide before opening a website.</p></details>
      </>}
      <Button size="sm" variant="ghost" disabled={!onSendToAgent || sending} onClick={() => void sendToAgent()}>{sending ? <Loader2 className="animate-spin" /> : sent ? <Check /> : <Send />}{sending ? 'Sending…' : sent ? 'Sent to agent' : 'Send to agent'}</Button>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
      {integration === 'playwright' && <p className="text-xs text-muted-foreground">Sessions appear in Running, then Replays when a recording is available.</p>}
    </div>
  </details>
}

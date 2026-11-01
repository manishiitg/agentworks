import { useState } from 'react'
import { Check, Code2, Copy } from 'lucide-react'
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

export function BrowserScriptSetup() {
  const [language, setLanguage] = useState<'node' | 'python'>('node')
  const [copied, setCopied] = useState('')
  async function copy(value: string, kind: string) {
    try { await navigator.clipboard.writeText(value); setCopied(kind) } catch { setCopied('') }
  }
  const example = examples[language]
  return <details className="border-t border-border pt-3" aria-label="Use browser from code">
    <summary className="cursor-pointer text-xs text-muted-foreground"><Code2 className="mr-1.5 inline h-3.5 w-3.5" />Use from code</summary>
    <div className="space-y-3 pt-3">
      <p className="text-xs text-muted-foreground">Ask your agent to set this up, or run it in this project’s terminal. Its session supplies the connection; no keys go in your script.</p>
      <div className="flex gap-1" role="group" aria-label="Script language">{(['node', 'python'] as const).map(value => <Button key={value} size="xs" variant={language === value ? 'secondary' : 'ghost'} aria-pressed={language === value} onClick={() => { setLanguage(value); setCopied('') }}>{value === 'node' ? 'Node' : 'Python'}</Button>)}</div>
      <details><summary className="cursor-pointer text-xs text-muted-foreground">Install packages</summary><pre className="mt-2 overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{example.install}</code></pre><Button size="xs" variant="ghost" onClick={() => void copy(example.install, 'install')}>{copied === 'install' ? <Check /> : <Copy />}{copied === 'install' ? 'Copied' : 'Copy install command'}</Button></details>
      <pre className="overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{example.script}</code></pre>
      <Button size="xs" variant="ghost" onClick={() => void copy(example.script, 'script')}>{copied === 'script' ? <Check /> : <Copy />}{copied === 'script' ? 'Copied' : 'Copy example'}</Button>
      <p className="text-xs text-muted-foreground">Scripts and tests appear in Running, then Replays when a recording is available. Existing Playwright contexts can use the same helpers.</p>
    </div>
  </details>
}

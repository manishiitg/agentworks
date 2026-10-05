import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserWorkspacePanel } from '../../src/components/workflow/BrowserWorkspacePanel'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import type { BrowserAutomationMode } from '../../src/components/BrowserAutomationSettings'
import '../../src/index.css'
const params = new URLSearchParams(location.search)
const profile = params.get('profile') || 'code'
const workspace = profile === 'code' ? '_users/alice/Chats/Code/projects/customer-portal' : profile === 'work' ? 'Chats/Work/projects/customer-portal' : 'Workflow/customer-portal'
document.documentElement.classList.toggle('dark', params.get('theme') !== 'light')
function Fixture() {
  const [mode, setMode] = useState<BrowserAutomationMode>('auto')
  return <TooltipProvider><main style={{height:"min(760px, 100vh)", maxWidth:860}} className="mx-auto flex flex-col border border-border bg-background text-foreground"><BrowserWorkspacePanel workspacePath={workspace} profileId={profile} scopeNoun="project" browserMode={mode} onBrowserModeChange={setMode} cdpPort={9222} onCdpPortChange={() => {}} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={() => {}} onSave={() => {}} dirty={mode !== 'auto'} /></main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)

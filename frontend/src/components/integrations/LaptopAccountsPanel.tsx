import { useEffect, useState } from 'react'
import { Laptop, RefreshCw } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { LaptopConnectionSetup } from '../../products/work/CodeFilesPanel'
import { Button } from '../ui/Button'
import { SettingsCard } from '../ui/SettingsCard'

type Computer = { device_id: string; platform?: string }

/** The connection is personal; each project separately selects its access. */
export function LaptopAccountsPanel() {
  const person = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${workspace}:${person}`
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ scope: string; devices: Computer[]; error?: string }>()
  const data = result?.scope === scope ? result : undefined
  useEffect(() => {
    let active = true
    const refresh = () => {
      void agentApi.myVaultsOp({ operation: 'laptops' }).then(body => {
        const response = JSON.parse(body) as { devices?: Computer[] }
        if (active) setResult({ scope, devices: response.devices || [] })
      }).catch(() => { if (active) setResult({ scope, devices: [], error: 'Computers could not be loaded. Check the connection again.' }) })
    }
    refresh()
    window.addEventListener('focus', refresh)
    return () => { active = false; window.removeEventListener('focus', refresh) }
  }, [scope, attempt])
  return <SettingsCard icon={<Laptop className="h-4 w-4" />} title="Your computers" description="Connect once, then choose where to use it.">
    <p className="text-xs leading-5 text-muted-foreground">Choose a connected computer in any Goal or Crew’s Local laptop panel. Each project chooses its folders and terminal commands. Laptop tools are for Builder chats; workflow steps, schedules and Autopilot stay on the server. Gmail and Slack for steps are separate.</p>
    {!data && <p role="status" className="text-xs text-muted-foreground">Loading your computers…</p>}
    {data?.error && <p role="alert" className="text-xs text-destructive">{data.error}</p>}
    {data?.devices.map(device => <div key={device.device_id} className="flex items-center gap-2 rounded border border-border p-3 text-sm"><Laptop className="h-4 w-4 text-muted-foreground" /><span className="min-w-0 flex-1 truncate">{device.device_id}</span><span className="text-xs text-emerald-600 dark:text-emerald-400">Connected</span></div>)}
    {data && !data.error && data.devices.length === 0 && <p className="text-xs text-muted-foreground">No computer is connected. Start AgentWorks on your computer, then check again.</p>}
    <LaptopConnectionSetup connected={Boolean(data?.devices.length)} />
    <Button size="sm" variant="outline" onClick={() => setAttempt(value => value + 1)}><RefreshCw className="h-3.5 w-3.5" />Check connection</Button>
  </SettingsCard>
}

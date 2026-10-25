import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from '../ui/Button'
import WorkflowEmailPanel from '../workflow/WorkflowEmailPanel'
import { ProjectMcpPanel } from './ProjectMcpPanel'
import { ProjectSecretsPanel } from './ProjectSecretsPanel'
import PersonalAccountsPanel from './PersonalAccountsPanel'

export type VaultSection = 'vault' | 'connected' | 'available' | 'secrets' | 'gmail' | 'slack-account' | 'laptop'
export type ProjectSecretControls = {
  selectedSecrets: string[]
  selectedGlobalSecrets: string[]
  onSelectedSecretsChange: (names: string[]) => Promise<unknown> | void
  onSelectedGlobalSecretsChange: (names: string[]) => Promise<unknown> | void
}

/** Same existing project editors for Crew, Goals and Flows; navigation only chooses a section. */
export function ProjectVaultControls({ workspacePath, placeNoun, section = 'vault', canEdit, onAsk, laptop, onAppsSaved, ...secrets }: ProjectSecretControls & {
  workspacePath: string; placeNoun: string; section?: VaultSection; canEdit: boolean
  onAsk: (message: string) => Promise<void>; laptop?: ReactNode; onAppsSaved?: (servers: string[]) => void
}) {
  const [apps, setApps] = useState<'connected' | 'available'>(section === 'available' ? 'available' : 'connected')
  const secretRef = useRef<HTMLElement>(null)
  const googleRef = useRef<HTMLDetailsElement>(null)
  const slackRef = useRef<HTMLDetailsElement>(null)
  const laptopRef = useRef<HTMLDetailsElement>(null)
  useEffect(() => {
    if (section === 'connected' || section === 'available') setApps(section)
    const target = section === 'secrets' ? secretRef.current : section === 'gmail' ? googleRef.current : section === 'slack-account' ? slackRef.current : section === 'laptop' ? laptopRef.current : null
    if (target instanceof HTMLDetailsElement) target.open = true
    target?.scrollIntoView?.({ block: 'start' })
  }, [section])
  return <div className="space-y-5" aria-label="Project apps and secrets">
    <section aria-label="MCPs" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-semibold">Apps &amp; MCPs</h2>
        <Button size="sm" variant="outline" onClick={() => setApps(apps === 'connected' ? 'available' : 'connected')}>{apps === 'connected' ? 'Add an app' : 'Back to connected apps'}</Button>
      </div>
      <ProjectMcpPanel view={apps} workspacePath={workspacePath} placeNoun={placeNoun} canEdit={canEdit} onAsk={onAsk} onSelectionSaved={onAppsSaved} />
    </section>
    <section ref={secretRef} aria-label="Secrets" className="space-y-3 border-t border-border pt-4">
      <h2 className="font-semibold">Secrets</h2>
      <ProjectSecretsPanel workspacePath={workspacePath} placeNoun={placeNoun}
        selectedSecrets={secrets.selectedSecrets} selectedGlobalSecrets={secrets.selectedGlobalSecrets}
        onSecretChange={secrets.onSelectedSecretsChange} onGlobalSecretChange={names => secrets.onSelectedGlobalSecretsChange(names || [])} />
    </section>
    <details ref={googleRef} className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Google apps &amp; Gmail</summary><div className="pt-3"><WorkflowEmailPanel workspacePath={workspacePath} scopeNoun={placeNoun === 'Flow' ? 'relay' : 'project'} onAsk={onAsk} /></div></details>
    <details ref={slackRef} className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Slack account used here</summary><div className="pt-3"><PersonalAccountsPanel workspacePath={workspacePath} provider="slack" /></div></details>
    {laptop && <details ref={laptopRef} className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Local laptop</summary><div className="pt-3">{laptop}</div></details>}
  </div>
}

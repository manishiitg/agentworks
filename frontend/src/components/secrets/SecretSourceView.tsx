import { useCallback, useEffect, useState } from 'react'
import { secretsApi, type SecretSources } from '../../api/secrets'
import { PROJECT_SECRETS_REFRESH_EVENT } from '../../utils/secretMutationRefresh'
import { Button } from '../ui/Button'

export default function SecretSourceView({ workflowPath }: { workflowPath: string }) {
  const [snapshot, setSnapshot] = useState<SecretSources | null>(null)
  const [refresh, setRefresh] = useState(0)
  const [failed, setFailed] = useState(false)
  const reload = useCallback(() => setRefresh(value => value + 1), [])
  useEffect(() => {
    let active = true
    setSnapshot(null)
    setFailed(false)
    void secretsApi.getSecretSources(workflowPath).then(result => {
      if (active) setSnapshot(result)
    }).catch(() => { if (active) setFailed(true) })
    return () => { active = false }
  }, [workflowPath, refresh])
  useEffect(() => {
    window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT, reload)
    return () => window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT, reload)
  }, [reload])
  return <section className="space-y-2 rounded-md border border-border p-3 text-xs" aria-label="Where secrets come from">
    <div className="flex items-center justify-between gap-2">
      <p className="font-medium">Where secrets come from</p>
      <Button size="sm" variant="ghost" onClick={reload}>Refresh saved setup</Button>
    </div>
    {failed ? <p role="alert">Secret sources are unavailable. Refresh this product's Settings and try again.</p> : !snapshot ? <p>Loading saved setup…</p> : <>
      <p className="text-muted-foreground">{snapshot.note}</p>
      {snapshot.warnings.map(warning => <p role="status" key={warning}>{warning}</p>)}
      {snapshot.secrets.map(secret => <div key={secret.name} className="border-t border-border pt-2">
        <p className="break-all font-mono font-medium">{secret.display_name}</p>
        <p className="mt-1 text-muted-foreground">{secret.explanation}</p>
      </div>)}
      {snapshot.status === 'available' && snapshot.secrets.length === 0 && <p>No secrets are stored or selected in this saved setup.</p>}
    </>}
  </section>
}

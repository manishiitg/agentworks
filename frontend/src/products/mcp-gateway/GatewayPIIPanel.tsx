import { useState } from 'react'
import { FlaskConical, ShieldCheck } from 'lucide-react'
import { WorkspaceViewTabs } from '../../components/workflow/WorkspaceViewTabs'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Textarea } from '../../components/ui/Textarea'
import {
  approvePIIReview, listConnectors, listGroups, listPIIReviews, listPIIRules,
  listTools, testPII, type GatewayPIIRule,
} from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import { gatewayErrorMessage, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass = 'h-8 rounded-md border border-input bg-background px-2 text-xs'
const typeLabels: Record<string, string> = { email: 'Email addresses', phone: 'US phone numbers', ssn: 'US Social Security numbers', credit_card: 'Credit card numbers', api_key: 'Recognized API keys' }
const actionLabels: Record<string, string> = { allow: 'Allow', mask: 'Mask', block: 'Block', require_review: 'Require approval' }
const directionLabels: Record<string, string> = { input: 'Before the MCP call', output: 'MCP response', both: 'Input and response' }
const defaults = [['email', 'mask'], ['phone', 'mask'], ['ssn', 'block'], ['credit_card', 'block'], ['api_key', 'block']] as const
type RuleScope = Pick<Partial<GatewayPIIRule>, 'GroupID' | 'ConnectorID' | 'PublicName'>

export function GatewayPIIPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [rules, reviews, groups, connectors, tools] = await Promise.all([
      listPIIRules(base), listPIIReviews(base), listGroups(base), listConnectors(base), listTools(base),
    ])
    return { rules: rules.rules, reviews: reviews.reviews, groups: groups.groups, connectors: connectors.connectors, tools: tools.tools }
  }, attempt)
  const [tab, setTab] = useState('protection')
  const [testScope, setTestScope] = useState<RuleScope>({})
  const [sample, setSample] = useState('')
  const [sampleDirection, setSampleDirection] = useState('input')
  const [testResult, setTestResult] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  async function runTest() {
    setBusy(true); setActionError(null)
    try {
      const result = await testPII(base, sample, sampleDirection, {
        GroupIDs: testScope.GroupID ? [testScope.GroupID] : [], ConnectorID: testScope.ConnectorID, PublicName: testScope.PublicName,
      })
      const resultLabels: Record<string, string> = { allow: 'Allowed', mask: 'Masked', block: 'Blocked', require_review: 'Approval required' }
      const count = result.decision.match_count
      setTestResult(`${resultLabels[result.decision.action] || result.decision.action} · ${count} ${count === 1 ? 'match' : 'matches'}${result.decision.data_types.length ? ` (${result.decision.data_types.map(kind => typeLabels[kind] || kind).join(', ')})` : ''}${result.masked_preview ? `\n${result.masked_preview}` : ''}`)
    } catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  async function approveReview(id: string) {
    setBusy(true); setActionError(null)
    try { await approvePIIReview(base, id); bump() }
    catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  if (loading) return <ConsoleLoading label="Loading PII policy…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load PII policy.'} onRetry={bump} />

  const pending = data.reviews.filter(review => review.Status === 'pending')
  const groupName = (id?: string) => data.groups.find(group => group.ID === id)?.Name || id || 'All groups'
  const serverName = (id?: string) => {
    const server = data.connectors.find(connector => connector.ID === id)
    return server?.Label || server?.Provider || id || 'All MCPs'
  }
  const toolName = (name?: string) => data.tools.find(tool => tool.PublicName === name)?.UpstreamName || name || 'All tools'
  const scopeSummary = (scope: RuleScope) => [groupName(scope.GroupID), serverName(scope.ConnectorID), toolName(scope.PublicName)].join(' · ')
  const scopeFields = (scope: RuleScope, change: (next: RuleScope) => void) => <div className="mt-3 grid gap-3 sm:grid-cols-3">
    <label className="space-y-1 text-xs text-muted-foreground">Group
      <select aria-label="Group scope" className={`${selectClass} block w-full`} value={scope.GroupID || ''} onChange={event => change({ ...scope, GroupID: event.target.value })}>
        <option value="">All groups</option>
        {data.groups.map(group => <option key={group.ID} value={group.ID}>{group.Name || group.ID}</option>)}
      </select>
    </label>
    <label className="space-y-1 text-xs text-muted-foreground">MCP server
      <select aria-label="Server scope" className={`${selectClass} block w-full`} value={scope.ConnectorID || ''} onChange={event => change({ ...scope, ConnectorID: event.target.value, PublicName: '' })}>
        <option value="">All MCPs</option>
        {data.connectors.map(connector => <option key={connector.ID} value={connector.ID}>{connector.Label || connector.Provider}</option>)}
      </select>
    </label>
    <label className="space-y-1 text-xs text-muted-foreground">Tool
      <select aria-label="Tool scope" className={`${selectClass} block w-full`} value={scope.PublicName || ''} onChange={event => change({ ...scope, PublicName: event.target.value })}>
        <option value="">All tools</option>
        {data.tools.filter(tool => !scope.ConnectorID || tool.ConnectorID === scope.ConnectorID).map(tool => <option key={tool.PublicName} value={tool.PublicName}>{scope.ConnectorID ? tool.UpstreamName : `${serverName(tool.ConnectorID)} / ${tool.UpstreamName}`}</option>)}
      </select>
    </label>
  </div>

  return (
    <div className="space-y-4" data-testid="gateway-pii">
      <div className="border-b border-border">
        <WorkspaceViewTabs value={tab} onChange={value => { setTab(value); setActionError(null) }} ariaLabel="PII tabs"
          options={[{ value: 'protection', label: 'Protection', icon: ShieldCheck }, { value: 'test', label: 'Test', icon: FlaskConical }, { value: 'reviews', label: 'Reviews', count: pending.length }]} />
      </div>
      {error && <ConsoleStale message={error} onRetry={bump} />}
      {actionError && <ConsoleError message={actionError} onRetry={bump} />}
      {tab === 'protection' && <div role="tabpanel" aria-label="PII protection" className="space-y-5">
        <SettingsCard title="Default protection" description={data.rules.length ? 'Applies to MCP inputs and responses unless a custom rule overrides it.' : 'Applies to MCP inputs and responses.'}>
          <div className="divide-y divide-border">
            {defaults.map(([kind, action]) => <div key={kind} className="flex items-center justify-between gap-3 py-2.5">
              <span>{typeLabels[kind]}</span>
              <span className={`rounded px-2 py-0.5 text-[11px] font-medium ${action === 'mask' ? 'bg-primary/10 text-primary' : 'bg-muted text-foreground'}`}>{actionLabels[action]}</span>
            </div>)}
          </div>
          <details className="text-xs text-muted-foreground">
            <summary className="cursor-pointer">Coverage and limits</summary>
            <p className="mt-2 leading-relaxed">Regex and checksums scan text and JSON strings in Vault-routed calls. Images, binary results and payloads over 256 KiB are blocked. Detection can miss sensitive data. Blocking a response cannot undo the MCP action.</p>
          </details>
        </SettingsCard>
        {data.rules.length > 0 && <SettingsCard title="Custom rules" count={data.rules.length}>
          <div className="divide-y divide-border">
            {data.rules.map(saved => <div key={saved.ID} className="space-y-1 py-3">
              <div className="flex flex-wrap items-center gap-2"><span className="font-medium">{typeLabels[saved.DataType] || saved.DataType}</span><span className="rounded bg-muted px-2 py-0.5 text-[11px]">{actionLabels[saved.Action] || saved.Action}</span></div>
              <p className="text-muted-foreground">{directionLabels[saved.Direction] || saved.Direction}</p>
              <p className="break-words text-muted-foreground">{scopeSummary(saved)}</p>
            </div>)}
          </div>
        </SettingsCard>}
      </div>}
      {tab === 'test' && <div role="tabpanel" aria-label="Test PII protection">
        <SettingsCard title="Test protection" description="Uses saved rules. Samples are not stored.">
          <div className="space-y-3">
            <Textarea aria-label="PII sample" value={sample} onChange={event => { setSample(event.target.value); setTestResult(null) }} rows={4} placeholder="Enter sample text…" />
            <select aria-label="Sample direction" className={selectClass} value={sampleDirection} onChange={event => { setSampleDirection(event.target.value); setTestResult(null) }}>
              <option value="input">Before the MCP call</option><option value="output">MCP response</option>
            </select>
            <details>
              <summary className="cursor-pointer text-muted-foreground">Scope · {scopeSummary(testScope)}</summary>
              {scopeFields(testScope, scope => { setTestScope(scope); setTestResult(null) })}
            </details>
            <Button size="sm" disabled={busy || !sample} onClick={() => void runTest()}>{busy ? 'Testing…' : 'Test policy'}</Button>
            {testResult && <pre role="status" className="whitespace-pre-wrap break-all rounded-md bg-muted/30 p-3 text-xs">{testResult}</pre>}
          </div>
        </SettingsCard>
      </div>}
      {tab === 'reviews' && <div role="tabpanel" aria-label="PII reviews">
        <SettingsCard title="Pending approvals" count={pending.length}>
          {pending.length === 0 ? <ConsoleEmpty>No calls waiting for approval.</ConsoleEmpty> : <div className="divide-y divide-border">
            <p className="text-muted-foreground">Approval allows one matching retry. Sensitive values are not stored here.</p>
            {pending.map(review => <div key={review.ID} className="flex flex-wrap items-center justify-between gap-3 py-3">
              <div className="space-y-1"><p className="font-medium">{toolName(review.PublicName)}</p><p className="text-muted-foreground">{review.UserID} · {serverName(review.ConnectorID)}</p><p className="text-muted-foreground">{review.DataTypes?.map(kind => typeLabels[kind] || kind).join(', ')}</p></div>
              <Button size="sm" disabled={busy} onClick={() => void approveReview(review.ID)}>Approve retry</Button>
            </div>)}
          </div>}
        </SettingsCard>
      </div>}
    </div>
  )
}

import { useMemo, useState } from 'react'
import type {
  CostAggregate,
  CostOverview,
  CostOverviewAggregate,
  CostOverviewBot,
  CostOverviewItem,
  CostOverviewMCP,
  CostOverviewUser,
} from '../../services/api-types'
import { formatTokens } from '../workflow/costs/helpers'
import { costAgentLabel } from '../workflow/costs/CostsModelSection'

type Group = 'user' | 'workflow' | 'crew' | 'product' | 'bot' | 'mcp' | 'other'

type Row = {
  key: string
  title: string
  subtitle: string
  usage?: CostOverviewAggregate
  item?: CostOverviewItem
  user?: CostOverviewUser
  bot?: CostOverviewBot
  mcp?: CostOverviewMCP
}

const TABS: { value: Group; label: string }[] = [
  { value: 'user', label: 'Users' },
  { value: 'workflow', label: 'Workflows' },
  { value: 'crew', label: 'Crews' },
  { value: 'product', label: 'Projects' },
  { value: 'bot', label: 'Bots' },
  { value: 'mcp', label: 'MCP' },
  { value: 'other', label: 'Other' },
]

const scopeNames: Record<string, string> = {
  workflow_execution: 'Runs',
  pulse: 'Pulse',
  builder: 'Builder',
  chat: 'Conversations',
  tool: 'Tools',
}

const currency = (amount: number) => {
  if (amount > 0 && amount < 0.0001) return '<$0.0001'
  return new Intl.NumberFormat('en-US', {
    style: 'currency', currency: 'USD',
    minimumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
  }).format(amount)
}

const amountLabel = (usage: Pick<CostAggregate, 'total_cost_usd' | 'unpriced_call_count'>) =>
  usage.total_cost_usd === 0 && (usage.unpriced_call_count ?? 0) > 0 ? 'Unknown' : currency(usage.total_cost_usd)

const tokens = (usage: CostAggregate) =>
  (usage.prompt_tokens ?? 0) + (usage.completion_tokens ?? 0) + (usage.cache_read_tokens ?? 0) + (usage.cache_write_tokens ?? 0)

const orderByCost = <T extends CostAggregate>(values: T[]) => values.sort((a, b) =>
  b.total_cost_usd - a.total_cost_usd || b.call_count - a.call_count)

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="rounded-lg border border-gray-200 bg-white px-3 py-2 dark:border-gray-700 dark:bg-gray-900">
    <div className="text-[11px] text-gray-500 dark:text-gray-400">{label}</div>
    <div className="mt-0.5 text-base font-semibold tabular-nums text-gray-950 dark:text-white">{value}</div>
  </div>
}

function Breakdown({ title, rows }: {
  title: string
  rows: { key: string; label: string; usage: CostAggregate }[]
}) {
  if (rows.length === 0) return null
  return <section className="mt-5">
    <h4 className="mb-2 text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</h4>
    <div className="divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-gray-800 dark:border-gray-700">
      {rows.map(row => <div key={row.key} className="flex items-start justify-between gap-3 px-3 py-2 text-sm">
        <div className="min-w-0">
          <div className="truncate text-gray-800 dark:text-gray-200" title={row.label}>{row.label}</div>
          <div className="text-xs text-gray-500 dark:text-gray-400">
            {row.usage.call_count.toLocaleString()} calls · {formatTokens(tokens(row.usage))} tokens
            {(row.usage.unpriced_call_count ?? 0) > 0 && ` · ${row.usage.unpriced_call_count?.toLocaleString()} unpriced`}
          </div>
        </div>
        <span className="shrink-0 font-medium tabular-nums text-gray-900 dark:text-gray-100">{amountLabel(row.usage)}</span>
      </div>)}
    </div>
  </section>
}

function PricingDetail({ usage }: { usage: CostOverviewAggregate }) {
  const parts = [
    { label: 'Provider reported', value: usage.provider_actual_cost_usd ?? 0 },
    { label: 'Subscription equivalent', value: usage.subscription_shadow_cost_usd ?? 0 },
    { label: 'Token estimate', value: usage.token_estimate_cost_usd ?? 0 },
  ].filter(part => part.value > 0)
  return <section className="mt-5 rounded-lg border border-gray-200 bg-gray-50 p-3 text-sm dark:border-gray-700 dark:bg-gray-950/30">
    <h4 className="font-semibold text-gray-900 dark:text-gray-100">Pricing coverage</h4>
    {parts.length > 0 ? <dl className="mt-2 space-y-1">
      {parts.map(part => <div key={part.label} className="flex justify-between gap-3 text-gray-600 dark:text-gray-300">
        <dt>{part.label}</dt><dd className="tabular-nums">{currency(part.value)}</dd>
      </div>)}
    </dl> : <p className="mt-1 text-gray-600 dark:text-gray-300">No priced calls recorded.</p>}
    {(usage.unpriced_call_count ?? 0) > 0 && <p className="mt-2 text-gray-600 dark:text-gray-300">
      {usage.unpriced_call_count?.toLocaleString()} LLM calls have unknown cost and are excluded from the tracked amount.
    </p>}
    {(usage.subscription_shadow_cost_usd ?? 0) > 0 && <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Subscription equivalent is a usage estimate, not the subscription bill.</p>}
  </section>
}

export default function CostExplorer({ data, days, itemLabel }: {
  data: CostOverview
  days: number
  itemLabel: (item: CostOverviewItem) => string
}) {
  const [group, setGroup] = useState<Group>('user')
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const workItems = useMemo(() => data.items.filter(item => item.total_cost_usd > 0 || item.call_count > 0), [data.items])
  const workByID = useMemo(() => new Map(workItems.map(item => [item.id, item])), [workItems])
  const rowsByGroup = useMemo(() => {
    const result: Record<Group, Row[]> = { user: [], workflow: [], crew: [], product: [], bot: [], mcp: [], other: [] }
    for (const user of orderByCost([...(data.by_user || [])].filter(value => value.total_cost_usd > 0 || value.call_count > 0))) {
      result.user.push({ key: `user:${user.id}`, title: user.name, subtitle: `${user.call_count.toLocaleString()} LLM calls`, usage: user, user })
    }
    for (const item of workItems) {
      result[item.kind].push({
        key: `work:${item.id}`, title: itemLabel(item),
        subtitle: `${item.call_count.toLocaleString()} LLM calls`, usage: item, item,
      })
    }
    for (const bot of orderByCost([...(data.by_bot || [])].filter(value => value.total_cost_usd > 0 || value.call_count > 0))) {
      result.bot.push({ key: `bot:${bot.id}`, title: bot.name, subtitle: `${bot.call_count.toLocaleString()} LLM calls`, usage: bot, bot })
    }
    for (const mcp of [...(data.by_mcp || [])].filter(value => value.calls > 0).sort((a, b) => b.calls - a.calls)) {
      result.mcp.push({ key: `mcp:${mcp.server}`, title: mcp.server, subtitle: `${mcp.calls.toLocaleString()} tool calls`, mcp })
    }
    for (const kind of ['workflow', 'crew', 'product', 'other'] as const) {
      result[kind].sort((a, b) => b.usage!.total_cost_usd - a.usage!.total_cost_usd || b.usage!.call_count - a.usage!.call_count)
    }
    return result
  }, [data, itemLabel, workItems])

  const visibleTabs = TABS.filter(tab => tab.value !== 'other' || rowsByGroup.other.length > 0)
  const rows = rowsByGroup[group].filter(row => `${row.title} ${row.subtitle}`.toLowerCase().includes(search.toLowerCase()))
  const selected = rows.find(row => row.key === selectedKey) || rows[0]

  const openWork = (id: string) => {
    const item = workByID.get(id)
    if (!item) return
    setGroup(item.kind)
    setSelectedKey(`work:${id}`)
    setSearch('')
  }

  const openUser = (id: string) => {
    setGroup('user')
    setSelectedKey(`user:${id}`)
    setSearch('')
  }

  const usage = selected?.usage
  const scopeRows = selected?.item || selected?.user
    ? Object.entries(selected.item?.by_scope || selected.user?.by_scope || {}).filter(([, value]) => value.call_count > 0 || value.total_cost_usd > 0)
      .map(([key, value]) => ({ key, label: scopeNames[key] || key, usage: value }))
      .sort((a, b) => b.usage.total_cost_usd - a.usage.total_cost_usd || b.usage.call_count - a.usage.call_count)
    : []
  const modelRows = selected?.item || selected?.user
    ? Object.entries(selected.item?.by_model || selected.user?.by_model || {}).filter(([, value]) => value.call_count > 0 || value.total_cost_usd > 0)
      .map(([key, value]) => ({ key, label: `${costAgentLabel(value.provider || '', key)} · ${key}`, usage: value }))
      .sort((a, b) => b.usage.total_cost_usd - a.usage.total_cost_usd || b.usage.call_count - a.usage.call_count)
    : []

  return <section className="mt-6">
    <div role="group" aria-label="Cost view" className="flex max-w-full gap-1 overflow-x-auto border-b border-gray-200 pb-2 dark:border-gray-700">
      {visibleTabs.map(tab => <button key={tab.value} type="button" onClick={() => { setGroup(tab.value); setSelectedKey(null); setSearch('') }}
        aria-pressed={group === tab.value}
        className={`shrink-0 rounded-md px-3 py-1.5 text-sm font-medium ${group === tab.value
          ? 'bg-violet-50 text-violet-700 dark:bg-violet-500/20 dark:text-violet-200'
          : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'}`}>
        {tab.label} <span className="ml-1 text-xs opacity-70">{rowsByGroup[tab.value].length}</span>
      </button>)}
    </div>
    <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">Select a row for its full breakdown. Views group the same activity; their totals should not be added together.</p>

    <div className="mt-4 grid gap-4 lg:grid-cols-[minmax(16rem,0.85fr)_minmax(0,1.4fr)]">
      <div className="min-w-0">
        <input type="search" value={search} onChange={event => setSearch(event.target.value)}
          aria-label={`Find ${TABS.find(tab => tab.value === group)?.label.toLowerCase()}`}
          placeholder={`Find ${TABS.find(tab => tab.value === group)?.label.toLowerCase()}…`}
          className="mb-2 w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-400 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-100" />
        {rows.length === 0 ? <p className="rounded-lg border border-dashed border-gray-200 px-3 py-8 text-center text-sm text-gray-500 dark:border-gray-700">
          {search ? 'No matching entries.' : `No ${TABS.find(tab => tab.value === group)?.label.toLowerCase()} recorded in the last ${days} days.`}
        </p> : <div className="max-h-[42rem] space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-1 dark:border-gray-700">
          {rows.map(row => <button key={row.key} type="button" onClick={() => setSelectedKey(row.key)} aria-pressed={selected?.key === row.key}
            className={`flex w-full items-start justify-between gap-3 rounded-md px-3 py-2.5 text-left ${selected?.key === row.key
              ? 'bg-violet-50 ring-1 ring-violet-200 dark:bg-violet-500/15 dark:ring-violet-500/30'
              : 'hover:bg-gray-50 dark:hover:bg-gray-800/60'}`}>
            <span className="min-w-0">
              <span className="block truncate text-sm font-medium text-gray-900 dark:text-gray-100" title={row.title}>{row.title}</span>
              <span className="block text-xs text-gray-500 dark:text-gray-400">{row.subtitle}</span>
            </span>
            <span className="shrink-0 text-right text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">
              {row.mcp ? row.mcp.recorded_cost_usd > 0 ? currency(row.mcp.recorded_cost_usd) : 'Unknown' : amountLabel(row.usage!)}
              {(row.usage?.unpriced_call_count ?? 0) > 0 && <span className="block text-[11px] font-normal text-gray-500 dark:text-gray-400">{row.usage?.unpriced_call_count?.toLocaleString()} unpriced</span>}
            </span>
          </button>)}
        </div>}
      </div>

      {selected && <div className="min-w-0 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-900 sm:p-5">
        <div className="mb-4">
          <div className="text-xs font-semibold uppercase tracking-wide text-violet-600 dark:text-violet-300">Detailed summary</div>
          <h3 className="mt-1 break-words text-lg font-semibold text-gray-950 dark:text-white">{selected.title}</h3>
          <p className="text-xs text-gray-500 dark:text-gray-400">{selected.subtitle} · last {days} days</p>
        </div>

        {usage ? <>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <Metric label="Tracked cost" value={amountLabel(usage)} />
            <Metric label="LLM calls" value={usage.call_count.toLocaleString()} />
            <Metric label="Unpriced calls" value={(usage.unpriced_call_count ?? 0).toLocaleString()} />
            <Metric label="Tokens" value={formatTokens(tokens(usage))} />
          </div>
          <PricingDetail usage={usage} />
          <Breakdown title="Activity" rows={scopeRows} />
          <Breakdown title="Models" rows={modelRows} />

          {selected.user && (selected.user.by_work?.length ?? 0) > 0 && <section className="mt-5">
            <h4 className="mb-2 text-sm font-semibold text-gray-900 dark:text-gray-100">Where this user worked</h4>
            <div className="space-y-1">
              {selected.user.by_work?.map(work => <button key={work.id} type="button" onClick={() => openWork(work.id)}
                className="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 px-3 py-2 text-left text-sm hover:border-violet-300 dark:border-gray-700 dark:hover:border-violet-500/40">
                <span className="min-w-0 truncate text-gray-800 dark:text-gray-200">{workByID.get(work.id) ? itemLabel(workByID.get(work.id)!) : work.name} <span className="text-xs text-gray-500">· {work.kind}</span></span>
                <span className="shrink-0 tabular-nums text-gray-900 dark:text-gray-100">{amountLabel(work)}</span>
              </button>)}
            </div>
          </section>}

          {selected.item && (selected.item.by_user?.length ?? 0) > 0 && <section className="mt-5">
            <h4 className="mb-2 text-sm font-semibold text-gray-900 dark:text-gray-100">Users in this {selected.item.kind === 'crew' ? 'Crew' : selected.item.kind === 'workflow' ? 'workflow' : 'project'}</h4>
            <div className="space-y-1">
              {selected.item.by_user?.map(actor => <button key={actor.id} type="button" onClick={() => openUser(actor.id)}
                className="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 px-3 py-2 text-left text-sm hover:border-violet-300 dark:border-gray-700 dark:hover:border-violet-500/40">
                <span className="min-w-0 truncate text-gray-800 dark:text-gray-200">{actor.name} <span className="text-xs text-gray-500">· {actor.call_count.toLocaleString()} calls</span></span>
                <span className="shrink-0 tabular-nums text-gray-900 dark:text-gray-100">{amountLabel(actor)}</span>
              </button>)}
            </div>
          </section>}

          {selected.item && (selected.item.by_bot?.length ?? 0) > 0 && <Breakdown title="Bot activity" rows={(selected.item.by_bot || []).map(bot => ({ key: bot.id, label: bot.name, usage: bot }))} />}
          {selected.item && (selected.item.by_mcp?.length ?? 0) > 0 && <section className="mt-5">
            <h4 className="mb-2 text-sm font-semibold text-gray-900 dark:text-gray-100">MCP calls</h4>
            <div className="space-y-1 text-sm text-gray-700 dark:text-gray-300">
              {selected.item.by_mcp?.map(mcp => <div key={mcp.server} className="flex justify-between gap-3"><span>{mcp.server} · {mcp.calls.toLocaleString()} calls</span><span>{mcp.recorded_cost_usd > 0 ? currency(mcp.recorded_cost_usd) : 'Unknown'}</span></div>)}
            </div>
          </section>}
          {selected.bot && <p className="mt-4 text-xs text-gray-500 dark:text-gray-400">External channel delivery fees are not included.</p>}
          {selected.bot && <button type="button" onClick={() => openWork(selected.bot!.workflow)} className="mt-5 text-sm font-medium text-violet-700 hover:underline dark:text-violet-300">View related project or workflow →</button>}
        </> : selected.mcp && <>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            <Metric label="MCP calls" value={selected.mcp.calls.toLocaleString()} />
            <Metric label="Unpriced calls" value={selected.mcp.unpriced_calls.toLocaleString()} />
            <Metric label="Known service charge" value={selected.mcp.recorded_cost_usd > 0 ? currency(selected.mcp.recorded_cost_usd) : 'Unknown'} />
          </div>
          <p className="mt-4 text-sm leading-6 text-gray-600 dark:text-gray-300">The MCP service did not report a price for unpriced calls. Model tokens used to process tool results remain in the LLM usage totals.</p>
        </>}
      </div>}
    </div>
  </section>
}

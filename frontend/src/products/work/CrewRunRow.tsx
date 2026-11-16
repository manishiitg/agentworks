import type { ComponentProps } from 'react'
import { PageRow, RowButton } from '../../components/automation/AutomationKit'

type Detail = 'log' | 'costs' | null

/**
 * One run in the Runs list: its row, with Ask on the same line as its name. A run that does not show its own cost
 * and full log keeps Cost details and Advanced log in a line below; pressing the highlighted one hides it again.
 */
export function CrewRunRow({ showDetails, detail, onDetail, onAsk, ...row }: {
  /** Cost details and Advanced log: for runs that do not show their cost and full log themselves. */
  showDetails: boolean
  /** The section open in this run now, or null (also null when another run is the open one). */
  detail: Detail
  onDetail: (detail: Detail) => void
  onAsk: () => void | Promise<void>
} & Omit<ComponentProps<typeof PageRow>, 'action' | 'below'>) {
  const pills = <span className="flex items-center gap-2">
    <RowButton title="Ask about this run" onClick={() => void onAsk()}>Ask</RowButton>
  </span>
  const below = showDetails ? <div className="space-y-2">
    {showDetails && <div className="flex flex-wrap items-center justify-end gap-2">
      <RowButton pressed={detail === 'costs'} onClick={() => onDetail(detail === 'costs' ? null : 'costs')}>Cost details</RowButton>
      <RowButton pressed={detail === 'log'} onClick={() => onDetail(detail === 'log' ? null : 'log')}>Advanced log</RowButton>
    </div>}
  </div> : undefined
  return <PageRow {...row} action={pills} below={below} />
}

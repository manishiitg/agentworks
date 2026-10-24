import { Fragment, type ReactNode } from 'react'
import type { ChatTab } from '../../stores/useChatStore'

export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Builder' : name
}

function shortOwnerName(name: string): string {
  const words = name.split('@')[0].split('+')[0].split(/[\s._-]+/).filter(Boolean)
  if (!words.length) return name
  const first = Array.from(words[0])
  const label = first[0].toLocaleUpperCase() + first.slice(1).join('')
  return words.length > 1 ? label + ' ' + Array.from(words[words.length - 1])[0].toLocaleUpperCase() + '.' : label
}

type Owner = { id: string; name: string }
function otherOwner(tab: ChatTab): Owner | undefined {
  const metadata = tab.metadata
  const name = metadata?.agentProfileTabOwnerName?.trim()
  if (!name || metadata?.agentProfileTabCanWrite === true) return undefined
  // Missing identity must not combine two different people's tabs by display name.
  return { id: metadata?.agentProfileTabOwnerID || tab.tabId, name }
}

/** Present adjacent tabs together without changing the server's order or controls. */
export function ChatTabGroups({ tabs, children, layout = 'strip' }: {
  tabs: ChatTab[]
  children: (tab: ChatTab) => ReactNode
  layout?: 'strip' | 'menu'
}) {
  const groups: { owner?: Owner; tabs: ChatTab[] }[] = []
  for (const tab of tabs) {
    const owner = otherOwner(tab)
    const previous = groups[groups.length - 1]
    if (previous && previous.owner?.id === owner?.id) previous.tabs.push(tab)
    else groups.push({ owner, tabs: [tab] })
  }
  if (!groups.some(group => group.owner)) return <>{tabs.map(tab => <Fragment key={tab.tabId}>{children(tab)}</Fragment>)}</>

  return <>{groups.map(({ owner, tabs: groupTabs }) => <div
    key={groupTabs[0].tabId}
    role={owner ? 'group' : undefined}
    aria-label={owner ? 'Tabs owned by ' + owner.name : undefined}
    data-chat-owner-group={owner?.id}
    className={layout === 'menu' ? 'min-w-0' : 'flex shrink-0 flex-col' + (owner ? ' ml-1 border-l border-border/70 pl-2' : '')}
  >
    {owner ? <div className={'flex min-w-0 items-center gap-2 px-2 text-[10px] font-normal text-muted-foreground ' + (layout === 'menu' ? 'pb-1 pt-2' : 'h-4')}>
      <span data-chat-owner-label title={owner.name} className="max-w-40 truncate">{shortOwnerName(owner.name)}</span>
      <span aria-hidden="true" className="h-px min-w-3 flex-1 bg-border/70" />
    </div> : layout === 'strip' ? <div aria-hidden="true" className="h-4" /> : null}
    <div className={layout === 'menu' ? undefined : 'flex min-h-8 items-center gap-1 [&>*]:shrink-0'}>
      {groupTabs.map(tab => <Fragment key={tab.tabId}>{children(tab)}</Fragment>)}
    </div>
  </div>)}</>
}

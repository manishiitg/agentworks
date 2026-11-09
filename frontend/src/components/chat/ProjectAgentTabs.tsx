import { createPortal } from 'react-dom'
import { Fragment, useContext, useEffect, useLayoutEffect, useRef, useState, type DragEvent as ReactDragEvent, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, ChevronDown, GripVertical, Info, Loader2, MoreHorizontal, Pencil, Pin, PinOff, Plus, X } from 'lucide-react'
import type { AgentProfileSideChat } from '../../services/api-types'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { chatTabArrowTargetAllowed, workChatTabShortcut, WORK_SIDE_CHAT_LIMIT } from '../../products/work/workTabs'
import { workSideChatDisplayName } from '../../products/work/workProjectPresentation'
import { markFeatureUsed } from '../../utils/featureUsage'
import { recordTabReorderTelemetry } from '../../utils/chatDeliveryTelemetry'
import { AgentWorksChatTabItem } from './AgentWorksChatTabItem'
import { compactOwnerChatName, ownerShortName } from './chatTabName'
import { ProjectWorkspaceTitle } from './ProjectWorkspaceTitle'
import { MobileWorkspaceNavigationContext } from '../workspace/MobileWorkspaceNavigation'
import { openCrewAccess, openCrewAskThread, useCrewAsking, useCrewLivesOnMac, useYourComputerSummary, type CloudState } from '../../products/work/YourComputerPanel'
import { teamAgentForTab, useTeamTab, type TeamTabPresence } from '../../products/work/teamApi'
import type { ChatTabPillProps } from './ChatTabPill'
import { AgentHeaderLine, useCrewAgentsStore, useOpenAgentStore, type CrewAgentActions, type CrewAgentRow, type CrewClosedAgent } from './AgentCard'
import { useChatRuntimeActivity } from '../../hooks/useChatRuntimeActivity'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { fillAgentPurposeDraft, sendToTab } from '../../utils/agentPurposeDraft'
import { AGENT_STATE_WORD, useAgentMenuState } from './agentMenuState'
import { AgentMark, PersonPresenceDot } from './AgentIcon'
import { canCloseWorkspaceChatTab } from '../../utils/closeWorkspaceChatTab'


function AgentMenuActivity({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  return <AgentMark state={useAgentMenuState(tabId, workspaceActivity)} />
}

function AgentMenuStateWord({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  const state = useAgentMenuState(tabId, workspaceActivity)
  return state ? <span className="ml-auto shrink-0 pl-2 text-[10px] font-normal text-muted-foreground">{AGENT_STATE_WORD[state]}</span> : null
}

/** One tab-management view; each product supplies its existing server operations. */
export function ProjectAgentTabs({ tabs, listed, canonicalTabId, projectName, mainPurpose, allowSideChats, opening, surface,
  onImportLocalChat, onOpenIdentity, onOrder, onPin, onRename, onOpen, onReopen, onCloseTab, onSelectTab, projectActivity, renderAfterTab, isTabActive, onMakeInteractive,
}: {
  tabs: ChatTab[]; listed: AgentProfileSideChat[]; canonicalTabId: string; projectName: string
  /** The person's own Main chat's purpose; undefined when Main is not theirs. */
  mainPurpose?: string
  allowSideChats: boolean; opening: boolean; surface: ProductSurface
  onOrder: (keys: string[]) => Promise<void>; onPin: (key: string, pinned: boolean) => Promise<void>
  onRename: (tab: ChatTab, name: string) => Promise<boolean | void>; onOpen: (name: string) => Promise<boolean | void>
  /** Opens a listed agent that has no tab here again and switches to it; without it, onOpen by name. */
  onReopen?: (chat: AgentProfileSideChat) => Promise<void>
  onImportLocalChat?: () => void
  onOpenIdentity?: () => void
  onCloseTab: (tabId: string) => void; onSelectTab: (tabId: string) => void
  projectActivity?: 'running' | 'waiting' | null
  renderAfterTab?: (tab: ChatTab) => ReactNode; isTabActive?: (tab: ChatTab) => boolean; onMakeInteractive?: (tabId: string) => void
}) {
  const activeTabId = useChatStore(state => state.activeTabId)
  const chatTabs = useChatStore(state => state.chatTabs)
  const canonicalSessionId = useChatStore(state => state.chatTabs[canonicalTabId]?.sessionId)
  const [newTabName, setNewTabName] = useState<string | null>(null)
  const listedFor = (tab: ChatTab) => listed.find(chat => tab.metadata?.agentProfileConversationKey === chat.conversation_key || tab.sessionId === chat.session_id)
  const ownedListed = listed.filter(chat => chat.can_write !== false)
  const sideChats = tabs.filter(tab => tab.tabId !== canonicalTabId && !tab.metadata?.isViewOnly)
  const phone = useMobileViewport()
  const phoneNavigation = useContext(MobileWorkspaceNavigationContext)
  const selectTab = (tabId: string) => { onSelectTab(tabId); phoneNavigation?.onChange('chat') }
  const closeProjectTab = onCloseTab
  // Closing a Crew agent hides it (owner, 4 Nov: "Close = Hide"): it stays under Hidden agents;
  // removing it for good is "Remove agent…" in its card. Other tabs (activity views, Goal chats) still close.
  const hideOrCloseTab = (tabId: string) => {
    const tab = tabs.find(item => item.tabId === tabId)
    const key = tab && tab.tabId !== canonicalTabId && surface === 'work' && isAgentTab(tab) ? sideChatKey(tab) : undefined
    if (!key) { closeProjectTab(tabId); return }
    void pinSideChat(key, false)
    if (tabId === activeTabId && canonicalTabId) onSelectTab(canonicalTabId)
  }
  const renameSideChat = onRename
  const submitNewAgent = async (requested: string) => {
    const name = requested.trim()
    if (!name || opening) return
    try { if (await onOpen(name) !== false) { setNewTabName(null); phoneNavigation?.onChange('chat') } }
    catch (cause) { useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open a new agent.', 'error') }
  }
  const isAgentTab = (tab: ChatTab) => !tab.metadata?.isViewOnly && !tab.metadata?.isScheduledRun && !tab.metadata?.isBotRun
  const agentCount = tabs.filter(isAgentTab).length
  const sideChatKey = (tab: ChatTab) => listedFor(tab)?.conversation_key
  const sideChatName = (tab: ChatTab) => workSideChatDisplayName(listedFor(tab)?.name || tab.name)
  const canControl = (tab: ChatTab) => tab.metadata?.agentProfileTabCanWrite !== false
  // Another person's tab names that person, so two owners' main chats never look alike.
  const tabLabel = (tab: ChatTab) => {
    const owner = tab.metadata?.agentProfileTabOwnerName
    // Other people's agents sit under their name (left menu headings, tab strip groups), so the name alone (owner, 5 Nov).
    return compactOwnerChatName(tab.tabId === canonicalTabId ? 'Main' : sideChatName(tab), owner)
  }
  // The purpose icon, for the tab's owner only (owner, 3 Nov): it fills the chat box; the agent saves the line itself.
  const purposeOf = (tab: ChatTab): string | undefined => {
    if (tab.tabId === canonicalTabId) return mainPurpose
    const chat = listedFor(tab)
    return chat && chat.can_write !== false && isAgentTab(tab) ? chat.purpose || '' : undefined
  }
  const sideChatPinned = (tab: ChatTab) => listedFor(tab)?.pinned !== false
  // The strip shows the main chat and the tabs the person pinned, as many as they pin (owner, 2026-10-20); the rest are
  // in the Agents menu. The tab the person is in is always on the strip (in the last place when it is not pinned), so it
  // can be seen and dragged.
  const onStrip = (tab: ChatTab) => tab.tabId === canonicalTabId || !listedFor(tab) || sideChatPinned(tab)
  const activeTab = tabs.find(tab => tab.tabId === activeTabId)
  const mainTab = tabs.find(tab => tab.tabId === canonicalTabId) ?? activeTab
  const placeProject = mainTab?.metadata?.agentProfileId === 'work' ? mainTab.metadata?.agentProfileProjectId : undefined
  const place = useYourComputerSummary(Boolean(placeProject), placeProject)
  // On the website a Crew that lives on its owner's Mac has no tabs of its own online; a new one would be empty.
  const livesOnMac = useCrewLivesOnMac(placeProject)
  // A Crew on its owner's Mac shows the Mac's own tabs while the Mac is on; with it off, Main only (PLAT-856-509).
  const sideChatsHere = allowSideChats && place !== 'mac_off'
  // A Crew on its owner's Mac shows where it lives only as the colour of the agents arrow (owner, 3 Nov): green with the
  // Mac on, grey with it off, amber while moving. Online Crews keep a plain arrow.
  const arrowTone = place === 'mac' ? 'text-emerald-500' : place === 'mac_off' ? 'text-muted-foreground/50' : place === 'moving' ? 'text-amber-500' : ''
  // Someone else's Mac Crew (a teammate's view) names its owner's Mac, from the server's Team reading.
  const teamState = useTeamTab(placeProject)
  const team = teamState.view
  const crewOwner = team && !team.can_switch && !team.team.change_on_mac ? team.people.find(person => person.note === 'owner')?.name : undefined
  const placeWord = crewOwner
    ? place === 'mac' ? `On ${crewOwner}'s Mac` : place === 'mac_off' ? `${crewOwner}'s Mac is off` : ''
    : place === 'mac' ? 'On your Mac' : place === 'mac_off' ? 'Mac off' : place === 'moving' ? 'Moving online' : ''
  const shownTabs = activeTab && !onStrip(activeTab) ? [...tabs.filter(onStrip), activeTab] : tabs.filter(onStrip)
  // Grouped by person like the left menu (owner, 5 Nov): your own tabs first, then each other person's, by account,
  // under the server's display name.
  const tabOwnerId = (tab: ChatTab) => canControl(tab) ? '' : listedFor(tab)?.tab_owner_id || tab.metadata?.agentProfileTabOwnerID || 'someone'
  const personName = (id: string) => team?.people.find(person => person.user_id === id)?.name
    || ownerShortName(tabs.map(tab => tabOwnerId(tab) === id ? listedFor(tab)?.tab_owner_name || tab.metadata?.agentProfileTabOwnerName || '' : '').find(Boolean) || '') || 'Someone'
  // Whether a teammate's Mac is on, the server's words (crew_team_tab.go).
  const presenceOf = (id: string) => team?.people.find(person => person.user_id === id)?.presence
  const byName = (a: string, b: string) => a === '' ? -1 : b === '' ? 1 : personName(a).localeCompare(personName(b))
  const tabPeople = [...new Set(shownTabs.map(tabOwnerId))].sort(byName)
  // The header shows the same agents as the left menu (owner, 5 Nov: "why is server different from local"): yours,
  // then each teammate's shared ones under their name, with the dot for their Mac.
  const visibleTabs = tabPeople.flatMap(person => shownTabs.filter(tab => tabOwnerId(tab) === person))
  const [moreOpen, setMoreOpen] = useState(false)
  // Which agents others can message and who asked them lately (a Crew on this Mac): marks on each tab's avatar.
  const macHere = livesOnMacHere(place) && !livesOnMac
  const asking = useCrewAsking(macHere ? placeProject : undefined)
  // The Agents menu is grouped by person like the left menu (owner, 5 Nov): You, then each teammate.
  // In the Mac app the teammates' agents are real tabs too (WorkSurface lists them from the server).
  const menuPeople = [...new Set(tabs.map(tabOwnerId))].sort(byName)
  const menuGrouped = menuPeople.length > 1 || menuPeople[0] !== ''
  const menuGroups = menuPeople.map(person => ({ person, tabs: tabs.filter(tab => tabOwnerId(tab) === person) }))
  // In a Crew's strip each other person is one chip after your own tabs (owner, 6 Nov, approved mockup); their agents
  // and the server's status words open from it. Other products keep their tabs.
  const personChips = surface === 'work'
  const stripTabs = personChips ? visibleTabs.filter(tab => tabOwnerId(tab) === '') : visibleTabs
  const chipAgent = (tab: ChatTab) => {
    const row = teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, false, false)
    return { tabId: tab.tabId, name: tabLabel(tab), status: row?.place_label || row?.status_label || '', tone: row?.place_label ? row.place_tone : undefined, running: row?.activity === 'running' }
  }
  const chipPeople = personChips ? menuGroups.filter(group => group.person !== '').map(group => {
    const agents = group.tabs.filter(isAgentTab).map(chipAgent)
    return { person: group.person, agents, watching: agents.find(agent => agent.tabId === activeTabId) }
  }).filter(group => group.agents.length > 0) : []
  const agentId = (tab: ChatTab) => tab.tabId === canonicalTabId ? 'main' : sideChatKey(tab)?.split(':chat:')[1]
  // Your own agent In team, from the Team tab's server reading (the same rows in the Mac app and on the website).
  // Answer only / Make changes is the same row's server answer (When others ask).
  const shareOf = (tab: ChatTab) => {
    const row = canControl(tab) && team?.team.on ? teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, true) : undefined
    return row?.in_team ? { line: 'my team', canChange: Boolean(row.can_make_changes) } : undefined
  }
  const sharedHere = (tab: ChatTab) => Boolean(shareOf(tab))
  // Its setup icon and card rows, the server's (crew_tab_setup.go): the same Team tab row as above, yours or a teammate's.
  const setupOf = (tab: ChatTab) => teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, canControl(tab))?.setup
  const automationsAsk = team?.asks?.automations
  const agentFace = (tab: ChatTab): ChatTabPillProps['agent'] => {
    // A Crew's agents only (owner, 4 Nov); Goal and Relay chat tabs keep their status dot.
    if (surface !== 'work' || !isAgentTab(tab)) return undefined
    const id = agentId(tab)
    const share = shareOf(tab)
    const ask = id ? asking.find(item => item.tab === id) : undefined
    const purpose = purposeOf(tab)
    // A teammate's agent: its name and purpose from the server, no actions (it is theirs).
    const theirs = !canControl(tab) ? listedFor(tab) : undefined
    if (theirs) return { info: { name: tabLabel(tab), purpose: theirs.purpose, owner: personName(tabOwnerId(tab)), presence: presenceOf(tabOwnerId(tab))?.line, tabId: tab.tabId, setup: setupOf(tab) } }
    const ago = ask && (ask.active ? 'now' : `${Math.max(1, Math.round((Date.now() - Date.parse(ask.started_at)) / 60000))} min ago`)
    return {
      asking: ask?.active,
      info: purpose === undefined ? undefined : {
        name: tabLabel(tab),
        purpose,
        sharedWith: share?.line,
        canChange: share?.canChange,
        lastAsked: ask && placeProject ? { who: ask.sender, when: ago || '', active: ask.active, open: () => { void openCrewAskThread(placeProject, ask) } } : undefined,
        onSetPurpose: () => fillAgentPurposeDraft(tab.tabId),
        onSharing: placeProject && id ? () => openCrewAccess(placeProject, id) : undefined,
        onRemove: tab.tabId !== canonicalTabId && canControl(tab) ? () => closeProjectTab(tab.tabId) : undefined,
        tabId: tab.tabId,
        activity: tab.tabId === canonicalTabId ? projectActivity : undefined,
        setup: setupOf(tab),
        onAutomations: automationsAsk ? () => { sendToTab(tab.tabId, automationsAsk) } : undefined,
      },
    }
  }
  // Desktop agent placement follows the visible sidebar, including its temporary peek.
  // Phones keep the compact current-agent header and drawer list.
  const leftMenuOpen = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget !== null)
  const menuMode = surface === 'work' && (phone || leftMenuOpen)
  // The strip's own handlers for one agent, so the left menu and the rail offer the same rename, order, show/hide and
  // hide (owner, 4 Nov: "did we lose more features?"); removing for good is only in the agent card.
  const agentActions = (tab: ChatTab): CrewAgentActions => {
    const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
    const pinKey = tab.tabId === canonicalTabId ? undefined : sideChatKey(tab)
    const position = ownedListed.findIndex(chat => chat.conversation_key === key)
    const before = position > 0 ? ownedListed[position - 1].conversation_key : undefined
    const after = position >= 0 ? ownedListed[position + 1]?.conversation_key : undefined
    return {
      orderKey: key,
      renameDraft: key ? sideChatName(tab) : undefined,
      rename: key ? name => renameSideChat(tab, name) : undefined,
      moveUp: key && before ? () => { void moveSideChat(key, before) } : undefined,
      moveDown: key && after ? () => { void moveSideChat(key, after) } : undefined,
      dropHere: key ? from => { void moveSideChat(from, key) } : undefined,
      setPinned: pinKey ? pinned => { void pinSideChat(pinKey, pinned) } : undefined,
      remove: tab.tabId !== canonicalTabId && canControl(tab) ? () => closeProjectTab(tab.tabId) : undefined,
    }
  }
  // The left menu's order: shown agents (pinned, or open now) first, then the hidden ones. Cmd/Ctrl+1..5 and the arrows
  // follow it in Left menu mode, so reordering there changes the numbers.
  const agentTabs = tabs.filter(isAgentTab)
  const listedInMenu = (tab: ChatTab) => onStrip(tab) || tab.tabId === activeTabId
  const menuOrder = [...agentTabs.filter(listedInMenu), ...agentTabs.filter(tab => !listedInMenu(tab))]
  const crewAgents: CrewAgentRow[] = surface === 'work' && placeProject ? menuOrder.map((tab, index): CrewAgentRow => ({ shortcut: index < 5 ? index + 1 : undefined, tabId: tab.tabId, name: tabLabel(tab), main: tab.tabId === canonicalTabId, ownerId: canControl(tab) ? undefined : tabOwnerId(tab), owner: canControl(tab) ? undefined : personName(tabOwnerId(tab)), pinned: onStrip(tab), asking: agentFace(tab)?.asking, info: agentFace(tab)?.info, activity: tab.tabId === canonicalTabId ? projectActivity : undefined, actions: agentActions(tab) })) : []
  // Agents the server lists that have no tab here (closed), for the left menu's Closed agents list.
  const closedListed = surface === 'work' && placeProject ? listed.filter(chat => !chat.is_main && !tabs.some(tab => listedFor(tab) === chat)) : []
  // The strip's ⌄ opens every agent; it shows no count (which agents are tabs is the person's choice) and no place colour
  // (owner, 4 Nov).
  const closedAgents: CrewClosedAgent[] = closedListed.map(chat => ({ key: chat.conversation_key, name: workSideChatDisplayName(chat.name), purpose: chat.purpose,
    open: () => (onReopen ? onReopen(chat) : onOpen(chat.name)).catch(cause => { useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open this agent.', 'error') }) }))
  const crewKey = JSON.stringify([placeProject, projectName, sideChatsHere, ownedListed.map(chat => chat.conversation_key), closedListed.map(chat => [chat.conversation_key, chat.name, chat.purpose]), crewAgents.map(agent => [agent.tabId, agent.shortcut, agent.name, agent.ownerId, agent.owner, agent.pinned, agent.actions?.remove ? 1 : 0, agent.actions?.rename ? 1 : 0, agent.asking, agent.activity, agent.info?.purpose, agent.info?.sharedWith, agent.info?.lastAsked?.when, agent.info?.setup, agent.info?.presence, agent.info?.onAutomations ? 1 : 0])])
  useEffect(() => {
    if (placeProject) useCrewAgentsStore.getState().set(placeProject, projectName, crewAgents, sideChatsHere, closedAgents)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [crewKey])
  // The open tab's details, for the purpose line at the top of its chat.
  const openTab = activeTab && tabs.some(tab => tab.tabId === activeTab.tabId) ? activeTab : undefined
  const openInfo = openTab ? agentFace(openTab)?.info : undefined
  const openKey = openTab ? `${openTab.tabId}|${openInfo?.purpose ?? ''}|${openInfo?.sharedWith ?? ''}|${openInfo?.onSharing ? 1 : 0}|${JSON.stringify(openInfo?.setup ?? null)}|${openInfo?.activity ?? ''}|${openInfo?.presence ?? ''}` : ''
  useEffect(() => {
    useOpenAgentStore.getState().set(openTab?.tabId ?? null, openInfo ?? null)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openKey])
  const [actionsFor, setActionsFor] = useState<string | null>(null)
  const [reordering, setReordering] = useState(false)
  const canReorderInMenu = tabs.length > 5
  useEffect(() => { if (!moreOpen || !canReorderInMenu) setReordering(false) }, [moreOpen, canReorderInMenu])
  const [renaming, setRenaming] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const moreRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [menuPosition, setMenuPosition] = useState({ top: 0, left: 0, maxHeight: 400 })
  useLayoutEffect(() => {
    if (!moreOpen) return
    const position = () => {
      const rect = moreRef.current?.getBoundingClientRect()
      if (!rect) return
      const viewport = window.visualViewport
      const visibleTop = viewport?.offsetTop ?? 0
      const bottom = visibleTop + (viewport?.height ?? window.innerHeight)
      const top = Math.max(visibleTop + 8, Math.min(rect.bottom + 4, bottom - 120))
      setMenuPosition({ top, left: Math.max(8, Math.min(rect.right - 320, window.innerWidth - Math.min(320, window.innerWidth - 16) - 8)), maxHeight: Math.min(416, Math.max(0, bottom - top - 8)) })
    }
    position()
    menuRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
    window.addEventListener('resize', position)
    window.addEventListener('scroll', position, true)
    window.visualViewport?.addEventListener('resize', position)
    window.visualViewport?.addEventListener('scroll', position)
    return () => {
      window.removeEventListener('resize', position); window.removeEventListener('scroll', position, true)
      window.visualViewport?.removeEventListener('resize', position); window.visualViewport?.removeEventListener('scroll', position)
    }
  }, [moreOpen])
  useEffect(() => {
    if (!moreOpen) return
    const close = (event: MouseEvent) => { if (!moreRef.current?.contains(event.target as Node) && !menuRef.current?.contains(event.target as Node)) setMoreOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setMoreOpen(false); moreRef.current?.querySelector<HTMLButtonElement>('button')?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [moreOpen])
  // Dragging a side tab onto another saves the new order on the server; the strip then follows the server's order.
  const [draggingKey, setDraggingKey] = useState<string | null>(null)
  const [dropKey, setDropKey] = useState<string | null>(null)
  const drag = useRef<{ key: string; tabId: string; attemptId: string; dropped: boolean } | null>(null)
  const dragMime = 'application/x-agentworks-chat-key'
  const startDrag = (event: ReactDragEvent<HTMLDivElement>, key: string, tabId: string) => {
    if ((event.target as Element).closest('button,input')) { event.preventDefault(); return }
    // A payload is required by native browser drag/drop. A ref keeps source
    // identity stable while React renders the selected tab or drop indicator.
    event.dataTransfer.setData(dragMime, key)
    event.dataTransfer.setData('text/plain', key)
    event.dataTransfer.effectAllowed = 'move'
    drag.current = { key, tabId, attemptId: crypto.randomUUID(), dropped: false }
    setDraggingKey(key)
    if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, drag.current.attemptId, 'start', tabId)
  }
  const endDrag = () => {
    const attempt = drag.current
    if (attempt && !attempt.dropped && canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, 'cancel', attempt.tabId)
    drag.current = null; setDraggingKey(null); setDropKey(null)
  }
  const overTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    if (!drag.current || !key) return
    event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropKey(key)
  }
  const dropTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    const attempt = drag.current
    if (!attempt || !key) return
    event.preventDefault(); event.stopPropagation()
    attempt.dropped = true
    void moveSideChat(attempt.key, key, attempt)
    setDropKey(null)
  }
  const moveSideChat = async (fromKey: string, toKey: string, attempt: { key: string; tabId: string; attemptId: string; dropped: boolean } = { key: fromKey, tabId: sideChats.find(tab => sideChatKey(tab) === fromKey)?.tabId || '', attemptId: crypto.randomUUID(), dropped: true }) => {
    const keys = ownedListed.map(chat => chat.conversation_key)
    const from = keys.indexOf(fromKey)
    const to = keys.indexOf(toKey)
    const targetId = sideChats.find(tab => sideChatKey(tab) === toKey)?.tabId
    const trace = (status: 'drop' | 'no_change' | 'saved' | 'failed') => { if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, status, attempt.tabId, targetId) }
    trace('drop')
    if (from < 0 || to < 0 || from === to) { trace('no_change'); return }
    keys.splice(to, 0, ...keys.splice(from, 1))
    try {
      await onOrder(keys)
      trace('saved')
    } catch (cause) {
      trace('failed')
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not save the tab order.', 'error')
    }
  }
  const pinSideChat = async (key: string, pinned: boolean) => {
    try {
      await onPin(key, pinned)
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not pin this tab.', 'error')
    }
  }
  // Cmd/Ctrl+arrows cycle visible chats (also from the composer); numbers pick a chat.
  // Alt+Shift+T / Option+Shift+T opens one
  // (PLAT-571), only while this product is on screen and never for a key
  // the terminal already took (it marks its own Alt keys handled).
  const visibleTabsRef = useRef(visibleTabs)
  // A Crew uses the left menu's order with the menu open or closed, so ⌘2 is the same agent either way (owner, 4 Nov).
  visibleTabsRef.current = surface === 'work' && placeProject ? menuOrder.filter(listedInMenu) : visibleTabs
  const numberedTabsRef = useRef(tabs)
  numberedTabsRef.current = surface === 'work' && placeProject ? menuOrder : tabs
  useEffect(() => {
    if (!sideChatsHere) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || useProductSurfaceStore.getState().productSurface !== surface) return
      if (event.target instanceof Element && event.target.closest('.xterm')) return
      const action = workChatTabShortcut(event)
      if (!action) return
      if (action.kind === 'adjacent') {
        // The owner chose tab switching from the chat input, including drafts.
        if (!chatTabArrowTargetAllowed(event.target)) return
        const shown = visibleTabsRef.current
        const active = shown.findIndex(tab => isTabActive ? isTabActive(tab) : tab.tabId === useChatStore.getState().activeTabId)
        if (shown.length < 2 || active < 0) return
        event.preventDefault()
        markFeatureUsed('chat-tab-keys')
        onSelectTab(shown[(active + action.direction + shown.length) % shown.length].tabId)
        return
      }
      event.preventDefault()
      markFeatureUsed(action.kind === 'new' ? 'chat-tab-new' : 'chat-tab-keys')
      if (action.kind === 'new') {
        setNewTabName(current => current ?? '')
        return
      }
      const target = numberedTabsRef.current[action.index]
      if (target) onSelectTab(target.tabId)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [sideChatsHere, surface, onSelectTab, isTabActive])
  // With many tabs the strip scrolls sideways (tabs keep their width, "+" stays in view) and the
  // active tab is brought into view when it changes.
  const stripRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const active = stripRef.current?.querySelector('[data-tab-active="true"]')
    if (active && typeof (active as HTMLElement).scrollIntoView === 'function') (active as HTMLElement).scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [activeTabId, tabs.length])
  const newTabForm = sideChatsHere && newTabName !== null && (
    <form
      className="absolute inset-x-3 top-full z-40 flex items-center rounded-lg border border-border bg-background p-2 shadow-lg md:static md:shrink-0 md:border-0 md:bg-transparent md:p-0 md:shadow-none"
      onSubmit={event => { event.preventDefault(); void submitNewAgent(newTabName) }}
    >
      <input
        autoFocus
        value={newTabName}
        onChange={event => setNewTabName(event.target.value)}
        onKeyDown={event => { if (event.key === 'Escape') setNewTabName(null) }}
        onBlur={() => { if (!opening && !newTabName.trim()) setNewTabName(null) }}
        maxLength={120}
        disabled={opening}
        placeholder="Name this agent, e.g. research"
        aria-label="Agent name"
        className="h-11 w-full md:h-7 md:w-48 rounded-md border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        data-testid="work-new-chat-tab-name"
      />
    </form>
  )

  // Your own agents' menu (the caret) sits with your tabs, before the divider and the other people's chips.
  // Keep general agent actions reachable with Main alone, including when local-chat import is unavailable.
  const moreMenu = (
        <div ref={moreRef} className="relative shrink-0 max-md:min-w-0 max-md:flex-1">
          <button
            type="button"
            onClick={() => setMoreOpen(open => !open)}
            aria-expanded={moreOpen}
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-full max-md:justify-center max-md:gap-2 max-md:px-2 max-md:text-sm max-md:font-semibold max-md:text-foreground"
            title={placeWord ? `Switch agent · ${placeWord}` : 'Switch agent'}
            aria-label={`Agents: switch, reorder or close (${agentCount})${placeWord ? ` · ${placeWord}` : ''}`}
            aria-haspopup="dialog"
            data-testid="work-more-tabs"
          >
            {phone && activeTab ? <>
              <AgentMenuActivity tabId={activeTab.tabId} workspaceActivity={activeTab.tabId === canonicalTabId ? projectActivity : undefined} />
              <PhoneCrewTitle name={projectName} tabName={activeTab.tabId === canonicalTabId ? '' : tabLabel(activeTab)} />
              <ChevronDown className={`h-3.5 w-3.5 shrink-0 transition-transform ${arrowTone || 'text-muted-foreground'} ${moreOpen ? 'rotate-180' : ''}`} aria-hidden="true" />
            </> : <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />}
          </button>
          {moreOpen && createPortal(
            <div ref={menuRef} style={menuPosition} className="fixed z-[100] flex w-80 max-w-[calc(100vw-1rem)] flex-col rounded-2xl border border-border bg-popover p-1.5 shadow-lg" role="dialog" aria-label="Agents">
              <div className="flex shrink-0 items-center gap-3 px-2.5 pb-2 pt-1.5">
                <h2 className="text-sm font-semibold">Agents</h2>
                <span className="ml-auto flex items-center gap-2"><CrewPlaceBadge place={place} word={placeWord} />
                {canReorderInMenu && <button type="button" aria-pressed={reordering} onClick={() => setReordering(value => !value)} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{reordering ? 'Done' : 'Reorder'}</button>}</span>
              </div>
              {reordering && <p className="shrink-0 px-2 py-2 text-[11px] text-muted-foreground">Drag agents or use the arrows to change their order.</p>}
              <div className="workspace-menu-scroll min-h-0 overflow-y-auto overscroll-contain pt-1">
              {menuGroups.map(group => <Fragment key={`person:${group.person}`}>
              {menuGrouped && <p title={presenceOf(group.person)?.line} className="flex items-center gap-1.5 px-2.5 pb-0.5 pt-2 text-[11px] text-muted-foreground"><PersonPresenceDot presence={presenceOf(group.person)} />{group.person ? personName(group.person) : 'You'}</p>}
              {group.tabs.map(tab => {
                const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
                // Anyone may pin another person's tab on their own strip; renaming and ordering stay with its owner.
                const pinKey = tab.tabId === canonicalTabId ? undefined : sideChatKey(tab)
                const label = tabLabel(tab)
                const position = ownedListed.findIndex(chat => chat.conversation_key === key)
                return <div
                  key={tab.tabId}
                  data-chat-list-id={tab.tabId}
                  draggable={reordering && !!key}
                  onDragStart={reordering && key ? event => startDrag(event, key, tab.tabId) : undefined}
                  onDragOver={event => overTab(event, key || listed?.[0]?.conversation_key)}
                  onDrop={event => dropTab(event, key || listed?.[0]?.conversation_key)}
                  onDragEnd={endDrag}
                  className={`group flex min-h-11 items-center gap-1 rounded-xl px-1.5 ${tab.tabId === activeTabId ? 'bg-muted/70' : 'hover:bg-muted/50'} ${dropKey === key && draggingKey !== key ? 'ring-1 ring-inset ring-primary' : ''}`}
                >
                  {reordering && key ? <GripVertical aria-hidden="true" className="h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground" /> : <span className="flex w-4 shrink-0 justify-center"><AgentMenuActivity tabId={tab.tabId} workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined} /></span>}
                  {renaming === tab.tabId ? <input
                    autoFocus
                    aria-label={`New name for ${label}`}
                    value={renameDraft}
                    onChange={event => setRenameDraft(event.target.value)}
                    onKeyDown={event => {
                      if (event.key === 'Escape') { event.stopPropagation(); setRenaming(null) }
                      if (event.key === 'Enter') { void renameSideChat(tab, renameDraft).finally(() => setRenaming(null)) }
                    }}
                    onBlur={() => setRenaming(null)}
                    className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1 text-xs"
                  /> : <button
                    type="button"
                    onClick={() => { setMoreOpen(false); selectTab(tab.tabId) }}
                    title={canControl(tab) ? label : `${label} · ${personName(tabOwnerId(tab))}`}
                    className={`min-w-0 flex-1 rounded py-2 text-left text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${tab.tabId === activeTabId ? 'font-medium text-foreground' : 'text-foreground/85'}`}
                  >
                    <span className="flex min-w-0 items-center gap-2"><span className="truncate text-[13px]">{label}</span>{sharedHere(tab) && <span className="shrink-0 rounded-full bg-primary/15 px-1.5 text-[10px] font-medium leading-4 text-primary">Shared</span>}{phone && <AgentMenuStateWord tabId={tab.tabId} workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined} />}</span>
                    {!isAgentTab(tab) ? <span className="block text-[10px] font-normal text-muted-foreground">Activity</span>
                      : purposeOf(tab) ? <span className="block truncate text-[11px] font-normal text-muted-foreground">{purposeOf(tab)}</span> : null}
                  </button>}
                  {reordering && key ? <div className="flex shrink-0 items-center">
                    <button type="button" aria-label={`Move ${label} earlier`} disabled={position <= 0} onClick={() => { const target = ownedListed[position - 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowUp className="h-3.5 w-3.5" /></button>
                    <button type="button" aria-label={`Move ${label} later`} disabled={position < 0 || position === ownedListed.length - 1} onClick={() => { const target = ownedListed[position + 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowDown className="h-3.5 w-3.5" /></button>
                  </div> : <>
                  {/* Actions show on hover, or after ⋯ (owner, 3 Nov mockup): the rows stay quiet. */}
                  {renaming !== tab.tabId && <button type="button" aria-label={`More for ${label}`} aria-expanded={actionsFor === tab.tabId} onClick={() => setActionsFor(actionsFor === tab.tabId ? null : tab.tabId)} className={`h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted ${actionsFor === tab.tabId ? 'hidden' : 'inline-flex group-hover:hidden group-focus-within:hidden'}`}><MoreHorizontal className="h-3.5 w-3.5" /></button>}
                  <span className={`shrink-0 items-center ${actionsFor === tab.tabId ? 'flex' : 'hidden group-hover:flex group-focus-within:flex'}`}>
                  {purposeOf(tab) !== undefined && renaming !== tab.tabId && <button type="button" aria-label={`Change ${label}'s purpose`} title="Change purpose" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setMoreOpen(false); fillAgentPurposeDraft(tab.tabId) }}><Info className="h-3.5 w-3.5" /></button>}
                  {key && renaming !== tab.tabId && <button type="button" aria-label={`Rename ${label}`} title="Rename" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setRenameDraft(sideChatName(tab)); setRenaming(tab.tabId) }}><Pencil className="h-3.5 w-3.5" /></button>}
                  {pinKey && <button type="button" aria-pressed={sideChatPinned(tab)} aria-label={sideChatPinned(tab) ? `Unpin ${label}` : `Pin ${label} to the tab bar`} title={sideChatPinned(tab) ? 'Pinned to the tab bar. Click to unpin' : 'Pin to the tab bar'} className={`inline-flex h-7 w-7 shrink-0 items-center justify-center rounded hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring ${sideChatPinned(tab) ? 'text-foreground' : 'text-muted-foreground'}`} onClick={() => { void pinSideChat(pinKey, !sideChatPinned(tab)) }}>{sideChatPinned(tab) ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}</button>}
                  {tab.tabId !== canonicalTabId && canControl(tab) && (
                    <button
                      type="button"
                      onClick={() => { setMoreOpen(false); hideOrCloseTab(tab.tabId) }}
                      className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={surface === 'work' && isAgentTab(tab) ? `Hide ${label}` : `Close ${label}`}
                      title={surface === 'work' && isAgentTab(tab) ? "Hide agent (it stays under Hidden agents)" : "Close activity view"}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  )}
                  </span>
                  </>}
                </div>
              })}
              </Fragment>)}
              {/* Closed agents open again from here too, for people who use tabs instead of the left menu. */}
              {closedAgents.length > 0 && <>
                <p className="px-2.5 pb-0.5 pt-2 text-[11px] text-muted-foreground">Closed agents</p>
                {closedAgents.map(agent => <button key={agent.key} type="button" title={agent.purpose || undefined} onClick={() => { setMoreOpen(false); void agent.open() }}
                  className="flex min-h-10 w-full min-w-0 items-center rounded-xl px-2.5 text-left text-[13px] text-muted-foreground hover:bg-muted/50 hover:text-foreground"><span className="min-w-0 truncate">{agent.name}</span></button>)}
              </>}
              </div>
              {sideChatsHere && <button type="button" disabled={opening || ownedListed.length >= WORK_SIDE_CHAT_LIMIT} onClick={() => { setMoreOpen(false); setNewTabName('') }}
                className="mt-1 flex min-h-10 shrink-0 items-center gap-2 border-t border-border px-2.5 text-sm text-primary disabled:opacity-40"><Plus className="h-4 w-4" aria-hidden="true" />New agent</button>}
              {onImportLocalChat && <button type="button" disabled={opening} onClick={() => { setMoreOpen(false); onImportLocalChat() }} className="flex min-h-10 shrink-0 items-center gap-2 px-2.5 text-sm text-muted-foreground hover:text-foreground"><span aria-label="Claude, Codex and Cursor" className="flex shrink-0 -space-x-1">{['claude.ico', 'codex.png', 'cursor.svg'].map(icon => <span key={icon} className="grid h-5 w-5 place-items-center rounded-full bg-popover ring-2 ring-popover"><img alt="" src={`/provider-icons/${icon}`} className="h-4 w-4 object-contain" /></span>)}</span>Continue a local chat</button>}
            </div>, document.body
          )}
        </div>
      )
  const moreBeforePeople = chipPeople.length > 0 && !menuMode

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      {/* A Crew's top line is "Crew › agent ⓘ" with Left menu and Tabs alike (owner, 4 Nov). */}
      {!phone && surface !== 'work' && <ProjectWorkspaceTitle name={projectName} surface={surface} />}
      {surface === 'work' && <AgentHeaderLine tabId={openTab?.tabId} crewName={projectName} showName={phone} onOpenIdentity={onOpenIdentity} />}
      <div ref={stripRef} className={`${menuMode ? 'hidden' : 'hidden md:flex'} min-w-[84px] items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden [&>*]:shrink-0`} data-testid="work-chat-tab-strip" aria-label="Chat tabs">
      {stripTabs.map((tab, index) => {
        const person = tabOwnerId(tab)
        const groupLabel = person && (index === 0 || tabOwnerId(stripTabs[index - 1]) !== person)
          ? <span key={`person:${person}`} title={presenceOf(person)?.line} className="ml-1 flex items-center gap-1.5 text-[11px] text-muted-foreground"><span aria-hidden="true" className="h-4 w-px bg-border" /><PersonPresenceDot presence={presenceOf(person)} />{personName(person)}:</span> : null
        const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
        const targetKey = key || (tab.tabId === canonicalTabId ? listed?.[0]?.conversation_key : undefined)
        return <Fragment key={tab.tabId}>{groupLabel}<div
          data-chat-tab-id={tab.tabId}
          draggable={!!key}
          onDragStart={key ? event => startDrag(event, key, tab.tabId) : undefined}
          onDragOver={event => overTab(event, targetKey)}
          onDrop={event => dropTab(event, targetKey)}
          onDragEnd={endDrag}
          className={`flex items-center rounded border-l-2 ${dropKey === targetKey && draggingKey !== key ? 'border-primary bg-primary/10' : 'border-transparent'} ${draggingKey === key ? 'opacity-50' : ''}`}
          title={key ? 'Drag to reorder this agent' : undefined}
        ><AgentWorksChatTabItem
        tab={tab}
        isActive={isTabActive ? isTabActive(tab) : tab.tabId === activeTabId}
        canClose={tab.tabId !== canonicalTabId && tab.metadata?.isViewOnly === true && canCloseWorkspaceChatTab(tab, chatTabs)}
        isBlank={false}
        displayName={tabLabel(tab)}
        workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined}
        purpose={purposeOf(tab)}
        onSetPurpose={purposeOf(tab) === undefined ? undefined : () => fillAgentPurposeDraft(tab.tabId)}
        agent={agentFace(tab)}
        titleOverride={tab.tabId === canonicalTabId ? projectName : key ? sideChatName(tab) : undefined}
        onTabClick={selectTab}
        onCloseTab={hideOrCloseTab}
        onRenameTab={key && canControl(tab) ? renameSideChat : undefined}
        closeStopsSession={tab.metadata?.isViewOnly !== true && !!key}
        onMakeInteractive={onMakeInteractive}
      />{renderAfterTab?.(tab)}</div></Fragment>
      })}
      {moreBeforePeople && moreMenu}
      {chipPeople.length > 0 && <span aria-hidden="true" className="mx-1 h-4 w-px bg-border" />}
      {chipPeople.map(group => <PersonChip key={`person:${group.person}`} name={personName(group.person)} presence={presenceOf(group.person)}
        agents={group.agents} watching={group.watching} onPick={selectTab} />)}
      </div>
      {!moreBeforePeople && moreMenu}

      {newTabForm}
      {sideChatsHere && newTabName === null && (
        <button
          type="button"
          onClick={() => setNewTabName('')}
          disabled={opening || ownedListed.length >= WORK_SIDE_CHAT_LIMIT}
          className={`${menuMode ? 'max-md:h-10 max-md:w-10' : 'max-md:hidden'} ml-0.5 inline-flex h-7 shrink-0 items-center justify-center gap-1 rounded-md text-xs text-primary transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40 ${sideChats.length === 0 ? 'px-2' : 'w-7'}`}
          aria-label="New agent"
          title={ownedListed.length >= WORK_SIDE_CHAT_LIMIT
            ? `At most ${WORK_SIDE_CHAT_LIMIT + 1} agents per project`
            : 'New agent (⌥⇧T on Mac, Alt+Shift+T on Windows): another agent in this project. The main agent keeps Slack, WhatsApp, MCP and schedules'}
          data-testid="work-new-chat-tab"
        >
          {opening ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          {/* Labelled until the first side chat exists, then a browser-style "+" next to the tabs. */}
          {sideChats.length === 0 && <span className="hidden sm:inline">New agent</span>}
        </button>
      )}
    </div>
  )
}

type PersonChipAgent = { tabId: string; name: string; status: string; tone?: string; running?: boolean }

/** Another person's agent: the server's "working" (their Mac's relayed events, or its own queue) shows even when this
 * browser holds no live turn for it; this browser's own signal (needs you, failed, a reply to read) wins. */
function useAgentMenuStateOrServer(tabId: string, running?: boolean) {
  const own = useAgentMenuState(tabId)
  return own ?? (running ? 'running' as const : null)
}
function PersonAgentMark({ tabId, running }: { tabId: string; running?: boolean }) { return <AgentMark state={useAgentMenuStateOrServer(tabId, running)} /> }
function PersonAgentWord({ tabId, running }: { tabId: string; running?: boolean }) {
  const state = useAgentMenuStateOrServer(tabId, running)
  return state ? <span className="ml-auto shrink-0 pl-2 text-[10px] font-normal text-muted-foreground">{AGENT_STATE_WORD[state]}</span> : null
}
const STATUS_TONE: Record<string, string> = { ok: 'text-emerald-600 dark:text-emerald-400', wait: 'text-amber-600 dark:text-amber-400', server: 'text-sky-600 dark:text-sky-400' }

/** Another person in the Crew's tab strip (owner, 6 Nov, approved mockup): one chip with their initial, name, how many
 * of their agents are here and the dot for their Mac; it opens their agents with the server's status words (the Team
 * tab reading). While one of theirs is open, the chip names it. */
function PersonChip({ name, presence, agents, watching, onPick }: {
  name: string; presence?: TeamTabPresence; agents: PersonChipAgent[]; watching?: PersonChipAgent; onPick: (tabId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ top: 0, left: 0 })
  useLayoutEffect(() => {
    if (!open) return
    const rect = buttonRef.current?.getBoundingClientRect()
    if (rect) setPosition({ top: rect.bottom + 4, left: Math.max(8, Math.min(rect.left, window.innerWidth - Math.min(300, window.innerWidth - 16) - 8)) })
    menuRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const close = (event: MouseEvent) => { if (!buttonRef.current?.contains(event.target as Node) && !menuRef.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); buttonRef.current?.focus() } }
    const shut = () => setOpen(false)
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', shut)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape); window.removeEventListener('resize', shut) }
  }, [open])
  const avatar = (size: string) => <span aria-hidden="true" className={`relative grid ${size} shrink-0 place-items-center rounded-full bg-primary/15 text-[11px] font-semibold text-primary`}>
    {name.trim().charAt(0).toUpperCase() || '?'}
    {presence && <span className="absolute -bottom-0.5 -right-0.5 grid place-items-center rounded-full bg-background p-[1.5px]"><PersonPresenceDot presence={presence} className="h-2 w-2" /></span>}
  </span>
  return <>
    <button ref={buttonRef} type="button" onClick={() => setOpen(value => !value)} aria-haspopup="menu" aria-expanded={open}
      aria-label={`${name}'s agents (${agents.length})${presence ? ` · ${presence.line}` : ''}`} title={presence?.line || `${name}'s agents`}
      data-person-chip={name} data-tab-active={watching ? 'true' : undefined}
      className={`inline-flex h-7 max-w-[16rem] shrink-0 items-center gap-1.5 rounded-full border py-0.5 pl-0.5 pr-2 text-xs transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${watching ? 'border-primary text-foreground' : 'border-border text-foreground/85'}`}>
      {avatar('h-5 w-5')}
      <span className="min-w-0 truncate">{name}{watching && <> · <b className="font-semibold">{watching.name}</b></>}</span>
      {/* What their agents are doing now (owner, 6 Nov: "when their agent is online/offline/working/loading I want to see
          that"): one mark each, only while it is working, needs them, failed or has a reply not read yet. */}
      <span className="flex shrink-0 items-center gap-0.5">{agents.map(agent => <PersonAgentMark key={agent.tabId} tabId={agent.tabId} running={agent.running} />)}</span>
      <ChevronDown className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`} aria-hidden="true" />
    </button>
    {open && createPortal(
      <div ref={menuRef} style={position} role="menu" aria-label={`${name}'s agents`} className="fixed z-[100] w-[300px] max-w-[calc(100vw-1rem)] rounded-2xl border border-border bg-popover p-1.5 shadow-lg">
        <div className="flex items-start gap-2 px-2 pb-1.5 pt-1">
          {avatar('h-5 w-5')}
          <p className="min-w-0 text-xs text-muted-foreground"><span className="font-semibold text-foreground">{name}</span>{presence && <span className="block">{presence.line}</span>}</p>
        </div>
        {agents.map(agent => <button key={agent.tabId} type="button" role="menuitem" onClick={() => { setOpen(false); onPick(agent.tabId) }}
          className={`flex min-h-9 w-full min-w-0 items-start gap-2 rounded-xl px-2 py-1.5 text-left text-[13px] hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${watching?.tabId === agent.tabId ? 'bg-muted/70' : ''}`}>
          <span className="mt-1 flex w-3 shrink-0 justify-center"><PersonAgentMark tabId={agent.tabId} running={agent.running} /></span>
          {/* The name keeps its own full line; the server's status sits smaller underneath (owner, 6 Nov: names were cut to "t.." by the status). */}
          <span className="min-w-0 flex-1">
            <span className={`block truncate ${watching?.tabId === agent.tabId ? 'font-semibold' : ''}`}>{agent.name}</span>
            {agent.status && <span className={`block text-[11px] font-normal leading-snug ${STATUS_TONE[agent.tone || ''] || 'text-muted-foreground'}`}>{agent.status}</span>}
          </span>
          {watching?.tabId === agent.tabId && <span className="mt-0.5 shrink-0 text-[11px] font-normal text-muted-foreground">watching</span>}
          <span className="mt-0.5 flex shrink-0 items-center"><PersonAgentWord tabId={agent.tabId} running={agent.running} /></span>
        </button>)}
      </div>, document.body
    )}
  </>
}

const PHONE_PLACE: Record<CloudState, string> ={ mac: 'On your Mac', mac_off: 'Mac off', moving: 'Moving online…', online: 'Online', offline: 'Offline' }

/** Where the Crew lives, as the server reports it (owner, 3 Nov): a dot and a short word, at every width. */
const livesOnMacHere = (place?: CloudState) => place === 'mac' || place === 'mac_off' || place === 'offline' || place === 'moving'

function CrewPlaceBadge({ place, word }: { place?: CloudState; word?: string }) {
  if (!place) return null
  const dot = place === 'mac_off' || place === 'offline' ? 'bg-muted-foreground' : place === 'moving' ? 'bg-amber-500' : 'bg-emerald-500'
  return <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground"><span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${dot}`} />{word || PHONE_PLACE[place]}</span>
}

/** The phone chat title: the Crew's name and the agent; where it lives is the arrow's colour. */
function PhoneCrewTitle({ name, tabName }: { name: string; tabName: string }) {
  return <span className="flex min-w-0 items-center gap-1.5 truncate">
    <span className="truncate font-medium text-foreground">{name}{tabName ? ` · ${tabName}` : ''}</span>
  </span>
}

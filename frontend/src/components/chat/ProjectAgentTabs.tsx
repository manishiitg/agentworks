import { takeSidebarProjectAction, useSidebarProjectAction } from '../../products/work/sidebarProjectActions'
import { randomUUID } from '../../utils/randomUUID'
import { remoteProjectConnection } from '../../utils/remoteSessions'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { TeammateChatIdentity } from '../../products/work/TeammateChatIdentity'
import { OnlineAgentLine } from '../../products/work/OnlineAgentLine'
import { PendingAgentForks } from './PendingAgentForks'
import { LocalMcpImportDialog } from '../../products/work/LocalMcpImport'
import { ForkToTeammate } from './ForkToTeammate'
import { ContinueOnlineDialog } from './ContinueOnline'
import { continueInTerminal } from './TerminalResume'
import { forkChatTab } from '../../utils/forkChatTab'
import { createPortal } from 'react-dom'
import { Fragment, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent as ReactDragEvent, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, ChevronDown, GripVertical, Info, Loader2, MoreHorizontal, Pencil, Plus, X } from 'lucide-react'
import type { AgentProfileSideChat } from '../../services/api-types'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { workChatTabShortcut } from '../../products/work/workTabs'
import { workSideChatDisplayName } from '../../products/work/workProjectPresentation'
import { markFeatureUsed } from '../../utils/featureUsage'
import { recordTabReorderTelemetry } from '../../utils/chatDeliveryTelemetry'
import { AgentWorksChatTabItem } from './AgentWorksChatTabItem'
import { compactOwnerChatName, ownerShortName } from './chatTabName'
import { ProjectWorkspaceTitle } from './ProjectWorkspaceTitle'
import { MobileWorkspaceNavigationContext } from '../workspace/MobileWorkspaceNavigation'
import { openCrewAccess, openCrewAskThread, useCrewAsking, useCrewLivesOnMac, useYourComputerSummary, type CloudState } from '../../products/work/YourComputerPanel'
import { agentSetupForTab, createOnlineAgent, forgetOnlineProjects, teamAgentForTab, teamChanged, teamFailure, useTeamTab } from '../../products/work/teamApi'
import type { ChatTabPillProps } from './ChatTabPill'
import { AgentHeaderLine, useCrewAgentsStore, useOpenAgentStore, type CrewAgentActions, type CrewAgentRow, type CrewClosedAgent } from './AgentCard'
import { agentMessagePrompt } from './AgentMessageFrom'
import { agentTeamShareFromRow } from './AgentInTeamMark'
import { useChatRuntimeActivity } from '../../hooks/useChatRuntimeActivity'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { setUpAgentInstructions, setUpAgentPurpose, sendToTab } from '../../utils/agentPurposeDraft'
import { AGENT_STATE_WORD, useAgentMenuState } from './agentMenuState'
import { AgentMark, PersonPresenceDot } from './AgentIcon'
import { useAuthStore } from '../../stores/useAuthStore'
import { canCloseWorkspaceChatTab } from '../../utils/closeWorkspaceChatTab'


function AgentMenuActivity({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  return <AgentMark state={useAgentMenuState(tabId, workspaceActivity)} />
}

function AgentMenuStateWord({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  const state = useAgentMenuState(tabId, workspaceActivity)
  return state ? <span className="ml-auto shrink-0 pl-2 text-[10px] font-normal text-muted-foreground">{AGENT_STATE_WORD[state]}</span> : null
}

/** One tab-management view; each product supplies its existing server operations. */
export function ProjectAgentTabs({ tabs, listed, canonicalTabId, projectName, allowSideChats, opening, surface,
  onImportLocalChat, canImportMcp = false, checkingLocalImport = false, projectActionReady = true, onDiscoverSkills, onOpenIdentity, onOrder, onForkParent, onSetSidebarHidden, onRename, onOpen, onReopen, onCloseTab, onSelectTab, projectActivity, renderAfterTab, isTabActive, onMakeInteractive, hideAddAgent = false, startChat = false, importMcpRequested = false, onRequestHandled, projectId,
}: {
  tabs: ChatTab[]; listed: AgentProfileSideChat[]; canonicalTabId: string; projectName: string
  /** The person's own Main chat's purpose; undefined when Main is not theirs. */
  /** A project with no chats shows its start page; its header needs no New agent (#4439). */
  hideAddAgent?: boolean
  /** The project's start page asked for a new chat (#4439); handled once, then onRequestHandled. */
  startChat?: boolean
  /** The project's start page asked for Import MCP servers. */
  importMcpRequested?: boolean
  onRequestHandled?: () => void
  projectId?: string
  allowSideChats: boolean; opening: boolean; surface: ProductSurface
  onOrder: (keys: string[]) => Promise<void>
  /** Moves a fork out from under its agent ('') or back under one (#3504). */
  onForkParent?: (key: string, parentKey: string) => Promise<void>
  onSetSidebarHidden?: (key: string, hidden: boolean) => Promise<void>
  onRename: (tab: ChatTab, name: string) => Promise<boolean | void>; onOpen: (name: string) => Promise<boolean | void>
  /** Opens a listed agent that has no tab here again and switches to it; without it, onOpen by name. */
  onReopen?: (chat: AgentProfileSideChat) => Promise<void>
  onImportLocalChat?: () => void
  /** This Mac's MCP servers can be imported; needs no project folder, unlike chats. */
  canImportMcp?: boolean
  checkingLocalImport?: boolean
  projectActionReady?: boolean
  onDiscoverSkills?: () => void
  onOpenIdentity?: () => void
  onCloseTab: (tabId: string) => void; onSelectTab: (tabId: string) => void
  projectActivity?: 'running' | 'waiting' | null
  renderAfterTab?: (tab: ChatTab) => ReactNode; isTabActive?: (tab: ChatTab) => boolean; onMakeInteractive?: (tabId: string) => void
}) {
  const activeTabId = useChatStore(state => state.activeTabId)
  const canonicalSessionId = useChatStore(state => state.chatTabs[canonicalTabId]?.sessionId)
  const [forkTo, setForkTo] = useState<{ id: string; name: string } | null>(null)
  const [continueOnline, setContinueOnline] = useState<{ tabId: string; key: string; name: string } | null>(null)
  const [newTabName, setNewTabName] = useState<string | null>(null)
  const [addMode, setAddMode] = useState<'choose' | 'new' | 'fork' | null>(null)
  // New agent's Where it runs: 'mac', or an online project's ID the Team uses and the person owns.
  const [newWhere, setNewWhere] = useState('mac')
  const [makingOnline, setMakingOnline] = useState(false)
  const [forking, setForking] = useState(false)
  // A chat without a name (#4439): the server names it from its first message, then from a short AI title.
  const startBlankChat = async () => {
    if (opening) return
    try { if (await onOpen('') !== false) { setAddMode(null); phoneNavigation?.onChange('chat') } }
    catch (cause) { useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open a new chat.', 'error') }
  }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { if (startChat) { onRequestHandled?.(); void startBlankChat() } }, [startChat])
  const beginAddAgent = () => {
    if (surface === 'work') setAddMode('choose')
    else setNewTabName('')
  }
  useEffect(() => { setAddMode(null) }, [canonicalTabId])
  useEffect(() => {
    if (!addMode) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); setAddMode(null) } }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [addMode])
  // Main is listed, renamed, moved, hidden and removed like any agent (owner, 25 Nov, #4046: Main is not special).
  const listedByKey = useMemo(() => new Map(listed.map(chat => [chat.conversation_key, chat])), [listed])
  const mainListed = useMemo(() => listed.find(chat => chat.is_main), [listed])
  const listedFor = (tab: ChatTab) => (tab.metadata?.agentProfileConversationKey ? listedByKey.get(tab.metadata.agentProfileConversationKey) : undefined) ?? (tab.tabId === canonicalTabId ? mainListed : undefined)
  const ownedListed = listed.filter(chat => chat.can_write !== false)
  const sideChats = tabs.filter(tab => tab.tabId !== canonicalTabId && !tab.metadata?.isViewOnly)
  const phone = useMobileViewport()
  const phoneNavigation = useContext(MobileWorkspaceNavigationContext)
  const selectTab = (tabId: string) => { onSelectTab(tabId); phoneNavigation?.onChange('chat') }
  const closeProjectTab = onCloseTab
  const renameSideChat = onRename
  const submitNewAgent = async (requested: string) => {
    const name = requested.trim()
    if (!name || opening || makingOnline) return
    const online = newWhere !== 'mac' ? onlineChoices.find(item => item.project_id === newWhere) : undefined
    if (online && teamState.target) {
      setMakingOnline(true)
      try {
        const made = await createOnlineAgent(teamState.target, online.project_id, name)
        teamChanged()
        useChatStore.getState().addToast(`Created ${name} in ${online.name} on the server. It runs 24/7, even with your Mac off.`, 'success')
        setNewTabName(null); setAddMode(null); setNewWhere('mac')
        // It opens as a tab here at once: the strip lists it again and opens it (WorkSurface open-team-agent).
        if (placeProject && made.chat_id) {
          forgetOnlineProjects(placeProject)
          window.dispatchEvent(new CustomEvent('agentworks:open-team-agent', { detail: { projectId: placeProject, key: `${online.project_id}:chat:${made.chat_id}` } }))
        }
      } catch (cause) { useChatStore.getState().addToast(teamFailure(cause).message, 'error') }
      finally { setMakingOnline(false) }
      return
    }
    try { if (await onOpen(name) !== false) { setNewTabName(null); setAddMode(null); phoneNavigation?.onChange('chat') } }
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
    // Main's name is its own chat's, from the server (Main until renamed, #4030).
    // The person's own online agent names its project, so it reads apart from this Mac's agents (#4121).
    const online = tab.metadata?.agentProfileOnlineProject
    if (online) return `${sideChatName(tab)} · ${online.name}`
    return compactOwnerChatName(tab.tabId === canonicalTabId ? listedFor(tab)?.name || 'Main' : sideChatName(tab), owner)
  }
  // The purpose icon, for the tab's owner only (owner, 3 Nov): it fills the chat box; the agent saves the line itself.
  const purposeOf = (tab: ChatTab): string | undefined => {
    // Main's purpose is its own chat's, like any agent's (owner, 25 Nov, #4046: a project has no description).
    const chat = tab.tabId === canonicalTabId ? listed.find(row => row.is_main) : listedFor(tab)
    return chat && chat.can_write !== false && isAgentTab(tab) ? chat.purpose || '' : undefined
  }
  const activeTab = tabs.find(tab => tab.tabId === activeTabId)
  const mainTab = tabs.find(tab => tab.tabId === canonicalTabId) ?? activeTab
  const placeProject = projectId || (mainTab?.metadata?.agentProfileId === 'work' ? mainTab.metadata?.agentProfileProjectId : undefined)
  const place = useYourComputerSummary(Boolean(placeProject), placeProject)
  // On the website a Crew that lives on its owner's Mac has no tabs of its own online; a new one would be empty.
  const livesOnMac = useCrewLivesOnMac(placeProject)
  // A Crew on its owner's Mac shows the Mac's own tabs while the Mac is on; with it off, Main only (PLAT-856-509).
  const sideChatsHere = allowSideChats && place !== 'mac_off'
  useEffect(() => {
    const add = (event: Event) => {
      const requested = (event as CustomEvent<{ projectId: string }>).detail?.projectId
      if (surface !== 'work' || requested !== placeProject || !sideChatsHere || opening) return
      setAddMode('choose')
    }
    window.addEventListener('crew-new-agent', add)
    return () => window.removeEventListener('crew-new-agent', add)
  }, [surface, placeProject, sideChatsHere, opening, ownedListed.length])

  const canImportChat = !!onImportLocalChat && !opening
  const canDiscoverSkills = !!onDiscoverSkills && !opening
  const sidebarAction = useSidebarProjectAction(state => state.request)
  useEffect(() => {
    if (!sidebarAction || surface !== 'work' || sidebarAction.projectId !== placeProject || !projectActionReady || opening ||
      (sidebarAction.action === 'chat' && checkingLocalImport)) return
    if (!takeSidebarProjectAction(sidebarAction)) return
    if (sidebarAction.action === 'new' && sideChatsHere) setAddMode('choose')
    else if (sidebarAction.action === 'chat' && canImportChat) onImportLocalChat?.()
    else if (sidebarAction.action === 'skills' && canDiscoverSkills) onDiscoverSkills?.()
    else useChatStore.getState().addToast('That action isn’t available in this project.', 'error')
  }, [sidebarAction, surface, placeProject, projectActionReady, opening, checkingLocalImport, sideChatsHere, canImportChat, canDiscoverSkills, onImportLocalChat, onDiscoverSkills])
  useEffect(() => {
    const onImport = (event: Event) => {
      const request = (event as CustomEvent<{ projectId: string; kind: 'chat' | 'skills' }>).detail
      if (surface !== 'work' || !placeProject || request?.projectId !== placeProject) return
      if (request.kind === 'chat' && canImportChat) onImportLocalChat?.()
      if (request.kind === 'skills' && canDiscoverSkills) onDiscoverSkills?.()
    }
    window.addEventListener('crew-agent-import', onImport)
    return () => window.removeEventListener('crew-agent-import', onImport)
  }, [surface, placeProject, canImportChat, canDiscoverSkills, onImportLocalChat, onDiscoverSkills])

  // A Crew on its owner's Mac shows where it lives only as the colour of the agents arrow (owner, 3 Nov): green with the
  // Mac on, grey with it off. Online Crews keep a plain arrow.
  const arrowTone = place === 'mac' ? 'text-emerald-500' : place === 'mac_off' ? 'text-muted-foreground/50' : ''
  // Someone else's Mac Crew (a teammate's view) names its owner's Mac, from the server's Team reading.
  const teamState = useTeamTab(placeProject)
  const team = teamState.view
  // This Mac's own project: paired with its Team (localProject) or the Mac app's Crew before pairing (unbound).
  const macProject = teamState.target?.localProject || (teamState.unbound ? placeProject : undefined)
  // Online projects the Team uses that this person owns: New agent may run there instead of on the Mac.
  const onlineChoices = (team?.online_projects || []).filter(item => item.can_make_agents && item.project_id)
  const crewOwner = team && !team.can_switch && !team.team.change_on_mac ? team.people.find(person => person.note === 'owner')?.name : undefined
  const placeWord = crewOwner
    ? place === 'mac' ? `On ${crewOwner}'s Mac` : place === 'mac_off' ? `${crewOwner}'s Mac is off` : ''
    : place === 'mac' ? 'On your Mac' : place === 'mac_off' ? 'Mac off' : ''
  // Grouped by person like the left menu (owner, 5 Nov): your own tabs first, then each other person's, by account,
  // under the server's display name.
  // A read-only row that is the viewer's own (a schedule's own chat) sits with their own tabs, not under a person.
  // A server project opened in place (#3383): "me" there is this Mac's sign-in to that server.
  const localMe = useAuthStore(state => state.user?.id)
  const remoteMe = useConnectedWorkStore(state => {
    const connection = remoteProjectConnection(placeProject)
    return connection ? state.connections.find(item => item.id === connection)?.user?.id : undefined
  })
  const me = remoteMe ?? localMe
  const tabOwnerId = (tab: ChatTab) => {
    if (canControl(tab)) return ''
    const owner = listedFor(tab)?.tab_owner_id || tab.metadata?.agentProfileTabOwnerID || 'someone'
    return owner === me ? '' : owner
  }
  const personName = (id: string) => team?.people.find(person => person.user_id === id)?.name
    || ownerShortName(tabs.map(tab => tabOwnerId(tab) === id ? listedFor(tab)?.tab_owner_name || tab.metadata?.agentProfileTabOwnerName || '' : '').find(Boolean) || '') || 'Someone'
  // Whether a teammate's Mac is on, the server's words (crew_team_tab.go).
  const presenceOf = (id: string) => team?.people.find(person => person.user_id === id)?.presence
  const byName = (a: string, b: string) => a === '' ? -1 : b === '' ? 1 : personName(a).localeCompare(personName(b))
  const tabPeople = [...new Set(tabs.map(tabOwnerId))].sort(byName)
  // The header shows the same agents as the left menu (owner, 5 Nov: "why is server different from local"): yours,
  // then each teammate's shared ones under their name, with the dot for their Mac.
  const visibleTabs = tabPeople.flatMap(person => tabs.filter(tab => tabOwnerId(tab) === person))
  const [moreOpen, setMoreOpen] = useState(false)
  const [importMcp, setImportMcp] = useState(false)
  useEffect(() => { if (importMcpRequested) { onRequestHandled?.(); setImportMcp(true) } }, [importMcpRequested, onRequestHandled])
  // Which agents others can message and who asked them lately (a Crew on this Mac): marks on each tab's avatar.
  const macHere = livesOnMacHere(place) && !livesOnMac
  const asking = useCrewAsking(macHere ? placeProject : undefined)
  // The Agents menu is grouped by person like the left menu (owner, 5 Nov): You, then each teammate.
  // In the Mac app the teammates' agents are real tabs too (WorkSurface lists them from the server).
  const menuPeople = tabPeople
  const menuGrouped = menuPeople.length > 1 || menuPeople[0] !== ''
  const menuGroups = menuPeople.map(person => ({ person, tabs: tabs.filter(tab => tabOwnerId(tab) === person) }))
  // A Crew's strip shows only your own tabs; teammates are in the ⌄ menu and the header's Team button (owner, 12 Nov:
  // one Team chip, not two).
  const stripTabs = surface === 'work' ? visibleTabs.filter(tab => tabOwnerId(tab) === '') : visibleTabs
  // Another person's agent working or waiting for them, as the server reads it from their Mac (crew_team_activity.go).
  const serverActivity = (tab: ChatTab) => {
    const activity = teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, false, false)?.activity
    return activity === 'running' || activity === 'waiting' ? activity : undefined
  }
  const agentId = (tab: ChatTab) => tab.tabId === canonicalTabId ? 'main' : sideChatKey(tab)?.split(':chat:')[1]
  // Your own agent In team, from the Team tab's server reading (the same rows in the Mac app and on the website).
  const shareOf = (tab: ChatTab) => {
    const row = canControl(tab) && team?.team.on ? teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, true) : undefined
    return row?.in_team ? { line: 'my team', chosen: row.audience === 'chosen' } : undefined
  }
  const inTeamFor = (key:string|undefined, main=false) => {
    const row=teamAgentForTab(teamState,key,main,true)
    return row?.mine===true && row.in_team
  }
  const teamShareFor = (key: string | undefined, main = false) =>
    team?.team.on ? agentTeamShareFromRow(teamAgentForTab(teamState, key, main, true)) : undefined
  const sharedHere = (tab: ChatTab) => Boolean(shareOf(tab))
  // Its setup icon and card rows, the server's (crew_tab_setup.go): the same Team tab row as above, yours or a teammate's.
  const setupOf = (tab: ChatTab) => agentSetupForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, canControl(tab))
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
    if (theirs) return { info: { name: tabLabel(tab), purpose: theirs.purpose, examples: theirs.examples, owner: personName(tabOwnerId(tab)), presence: presenceOf(tabOwnerId(tab))?.line, tabId: tab.tabId, setup: setupOf(tab) } }
    const ago = ask && (ask.active ? 'now' : `${Math.max(1, Math.round((Date.now() - Date.parse(ask.started_at)) / 60000))} min ago`)
    const teamRow = placeProject ? teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, true) : undefined
    return {
      asking: ask?.active,
      info: purpose === undefined ? undefined : {
        name: tabLabel(tab),
        purpose, examples: listedFor(tab)?.examples,
        sharing: placeProject && teamRow ? { projectId: placeProject, agentKey: teamRow.key } : undefined,
        sharedWith: share?.line,
        sharedChosen: share?.chosen,
        teamShare: team?.team.on ? agentTeamShareFromRow(teamRow) : undefined,
        lastAsked: ask && placeProject ? { who: ask.sender, when: ago || '', active: ask.active, open: () => { void openCrewAskThread(placeProject, ask) } } : undefined,
        // Your own agents message it with this line, Team on or not (owner, 28 Nov: the menu must say how agents message it).
        messageLine: `Ask the "${tabLabel(tab)}" agent in ${projectName} to …`,
        messageCopy: agentMessagePrompt(tabLabel(tab), projectName),
        instructions: { set: tab.tabId === canonicalTabId ? undefined : Boolean(listedFor(tab)?.has_instructions), onEdit: () => setUpAgentInstructions(tab.tabId) },
        onSharing: placeProject && id ? () => openCrewAccess(placeProject, id) : undefined,
        onRemove: isAgentTab(tab) && canControl(tab) ? () => closeProjectTab(tab.tabId) : undefined,
        tabId: tab.tabId,
        activity: tab.tabId === canonicalTabId ? projectActivity : undefined,
        setup: setupOf(tab),
        onAddAutomation: () => { sendToTab(tab.tabId, 'Add an automation for this agent. Ask what I want it to do and when, then use the existing automation tools.') },
        onAutomations: automationsAsk ? () => { sendToTab(tab.tabId, automationsAsk) } : undefined,
      },
    }
  }
  const saveSidebarHidden = async (key: string, hidden: boolean) => {
    try { await onSetSidebarHidden?.(key, hidden) }
    catch { useChatStore.getState().addToast('Couldn’t save this sidebar change. Open the menu to try again.', 'error') }
  }
  // Share rename and ordering between the left menu and the rail.
  const agentActions = (tab: ChatTab): CrewAgentActions => {
    const key = !canControl(tab) ? undefined : sideChatKey(tab)
    const ownedSideChat = key && ownedListed.some(chat => chat.conversation_key === key)
    const position = ownedListed.findIndex(chat => chat.conversation_key === key)
    const before = position > 0 ? ownedListed[position - 1].conversation_key : undefined
    const after = position >= 0 ? ownedListed[position + 1]?.conversation_key : undefined
    const shareSource = canControl(tab) ? teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, tab.tabId === canonicalTabId, true) : undefined
    return {
      forkTo: surface === 'work' && canControl(tab) && team?.team.on && shareSource ? () => setForkTo({ id: shareSource.id, name: shareSource.name }) : undefined,
      fork: surface === 'work' && isAgentTab(tab) && canControl(tab) ? () => forkChatTab(tab.tabId) : undefined,
      // Continue online (#4520): only an own chat of a project on this Mac (paired with its Team, or not yet); an online
      // agent's tab already runs on the server. The Mac's readiness check says what is missing.
      continueOnline: surface === 'work' && isAgentTab(tab) && canControl(tab) && macProject && !tab.metadata?.agentProfileOnlineProject && (tab.metadata?.agentProfileConversationKey || tab.tabId === canonicalTabId)
        ? () => setContinueOnline({ tabId: tab.tabId, key: tab.metadata?.agentProfileConversationKey || macProject, name: tabLabel(tab) }) : undefined,
      terminal: surface === 'work' && isAgentTab(tab) && canControl(tab) && (tab.metadata?.agentProfileChatProvider || tab.metadata?.agentProfileEngine) === 'claude-code' ? () => continueInTerminal(tab.tabId) : undefined,
      orderKey: key,
      renameDraft: key ? sideChatName(tab) : undefined,
      rename: key ? name => renameSideChat(tab, name) : undefined,
      moveUp: key && before ? () => { void moveSideChat(key, before) } : undefined,
      moveDown: key && after ? () => { void moveSideChat(key, after) } : undefined,
      dropHere: key ? from => { void moveSideChat(from, key) } : undefined,
      forkParent: key && ownedSideChat && onForkParent ? parentKey => onForkParent(key, parentKey) : undefined,
      setSidebarHidden: surface === 'work' && key && ownedSideChat && onSetSidebarHidden ? hidden => { void saveSidebarHidden(key, hidden) } : undefined,
    }
  }
  // The left menu and shortcuts keep the saved order regardless of selection. Cmd/Ctrl+1..5 and the arrows
  // follow it in Left menu mode, so reordering there changes the numbers.
  // Another person's chat is listed once even when this app holds two tabs for its session (owner, 10 Nov: a teammate's
  // Main showed twice after it was opened).
  const menuOrder = tabs.filter(isAgentTab).filter((tab, index, all) => canControl(tab) || !tab.sessionId || all.findIndex(other => !canControl(other) && other.sessionId === tab.sessionId) === index)
  const sidebarOrder = new Map(listed.map((chat, index) => [chat.conversation_key, index]))
  const crewAgents: CrewAgentRow[] = surface === 'work' && placeProject ? menuOrder.map((tab, index): CrewAgentRow => {
    const chat = listedFor(tab)
    const mine = canControl(tab)
    const main = tab.tabId === canonicalTabId || chat?.is_main === true
    const face = agentFace(tab)
    return {
      key: tab.metadata?.agentProfileConversationKey, forkedFrom: chat?.forked_from, lastMessageAt: chat?.last_message_at,
      inTeam: mine && inTeamFor(tab.metadata?.agentProfileConversationKey, main),
      teamShare: mine ? teamShareFor(tab.metadata?.agentProfileConversationKey, main) : undefined,
      shortcut: index < 5 ? index + 1 : undefined, tabId: tab.tabId, name: tabLabel(tab), main,
      place: chat?.chat_location === 'mac' ? 'mac' : undefined,
      ownerId: mine ? undefined : tabOwnerId(tab), owner: mine ? undefined : personName(tabOwnerId(tab)),
      // A teammate's Agent only agent can be asked, not added to your groups (owner, 28 Nov).
      messageOnly: !mine && teamAgentForTab(teamState, tab.metadata?.agentProfileConversationKey, false, false)?.chat_share === 'agent' || undefined,
      // Hiding only affects the sidebar, never the tab, keyboard order or agent's work.
      sidebarHidden: mine && chat?.can_write !== false && chat?.pinned === false,
      sidebarOrder: chat ? sidebarOrder.get(chat.conversation_key) : undefined,
      asking: face?.asking, info: face?.info,
      activity: tab.tabId === canonicalTabId ? projectActivity : mine ? undefined : serverActivity(tab), actions: agentActions(tab),
    }
  }) : []
  // Listed agents without a tab keep the same saved hiding and position when they open.
  const openListedSet = new Set(tabs.map(listedFor).filter((chat): chat is AgentProfileSideChat => Boolean(chat)))
  const closedListed = surface === 'work' && placeProject ? listed.filter(chat => !openListedSet.has(chat)) : []
  const closedAgents: CrewClosedAgent[] = closedListed.map(chat => ({ owner: chat.can_write === false ? ownerShortName(chat.tab_owner_name || '') || 'Teammate' : undefined, forkedFrom: chat.forked_from, inTeam: chat.can_write===true && inTeamFor(chat.conversation_key,!!chat.is_main), teamShare: chat.can_write === true ? teamShareFor(chat.conversation_key, !!chat.is_main) : undefined, key: chat.conversation_key, name: workSideChatDisplayName(chat.name), purpose: chat.purpose, examples: chat.examples,
    sidebarHidden: chat.can_write !== false && chat.pinned === false, sidebarOrder: sidebarOrder.get(chat.conversation_key),
    open: () => (onReopen ? onReopen(chat) : onOpen(chat.name)).catch(cause => { useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open this agent.', 'error') }) }))
  const crewKey = JSON.stringify([placeProject, projectName, sideChatsHere, opening, canImportChat, canDiscoverSkills, ownedListed.map(chat => chat.conversation_key), closedAgents.map(agent => [agent.key, agent.owner, agent.name, agent.purpose, agent.inTeam, agent.teamShare, agent.sidebarHidden, agent.sidebarOrder]), crewAgents.map(agent => [agent.tabId, agent.key, agent.forkedFrom, agent.lastMessageAt, agent.inTeam, agent.teamShare, agent.place, agent.shortcut, agent.name, agent.ownerId, agent.owner, agent.sidebarHidden, agent.sidebarOrder, Boolean(agent.actions?.setSidebarHidden), agent.info?.onRemove ? 1 : 0, agent.actions?.rename ? 1 : 0, agent.asking, agent.activity, agent.info?.purpose, agent.info?.examples, agent.info?.sharedWith, agent.info?.sharedChosen ? 1 : 0, agent.info?.teamShare, agent.info?.lastAsked?.when, agent.info?.setup, agent.info?.presence, agent.info?.onAutomations ? 1 : 0])])
  useEffect(() => {
    if (placeProject) useCrewAgentsStore.getState().set(placeProject, projectName, crewAgents, sideChatsHere && !opening, closedAgents, { chat: canImportChat, skills: canDiscoverSkills })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [crewKey])
  // The open tab's details, for the purpose line at the top of its chat.
  const openTab = activeTab && tabs.some(tab => tab.tabId === activeTab.tabId) ? activeTab : undefined
  const openInfo = openTab ? agentFace(openTab)?.info : undefined
  // The person's own online agent (#4121) is theirs to type into, though grouped under its project.
  const watchingOther = !!openTab && tabOwnerId(openTab) !== '' && !openTab.metadata?.agentProfileOnlineProject
  const headerAgent = crewAgents.find(agent => agent.tabId === openTab?.tabId)
  const openKey = openTab ? `${openTab.tabId}|${openInfo?.purpose ?? ''}|${JSON.stringify(openInfo?.examples)}|${openInfo?.sharedWith ?? ''}|${JSON.stringify(openInfo?.teamShare ?? null)}|${openInfo?.onSharing ? 1 : 0}|${JSON.stringify(openInfo?.setup ?? null)}|${openInfo?.activity ?? ''}|${openInfo?.presence ?? ''}` : ''
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
    drag.current = { key, tabId, attemptId: randomUUID(), dropped: false }
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
  const moveSideChat = async (fromKey: string, toKey: string, attempt: { key: string; tabId: string; attemptId: string; dropped: boolean } = { key: fromKey, tabId: sideChats.find(tab => sideChatKey(tab) === fromKey)?.tabId || '', attemptId: randomUUID(), dropped: true }) => {
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
  // Cmd/Ctrl+Left/Right step to the previous/next agent and numbers pick one, within this project.
  // Alt+Shift+T / Option+Shift+T opens one
  // (PLAT-571), only while this product is on screen and never for a key
  // the terminal already took (it marks its own Alt keys handled).
  // A Crew uses the left menu's order with the menu open or closed, so ⌘2 is the same agent either way (owner, 4 Nov).
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
        // Cmd/Ctrl+Left/Right: the previous or next agent in the menu's order. While text is typed they keep their
        // editing job (line start on a Mac, previous word elsewhere); in an empty chat box they switch (PLAT-746-849).
        const field = event.target instanceof Element ? event.target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],.monaco-editor,.cm-editor') : null
        if (field && !((field instanceof HTMLTextAreaElement || field instanceof HTMLInputElement) && field.value === '')) return
        const order = numberedTabsRef.current
        if (order.length < 2) return
        const current = order.findIndex(tab => tab.tabId === useChatStore.getState().activeTabId)
        event.preventDefault()
        markFeatureUsed('chat-tab-keys')
        onSelectTab(order[(Math.max(current, 0) + action.direction + order.length) % order.length].tabId)
        return
      }
      event.preventDefault()
      markFeatureUsed(action.kind === 'new' ? 'chat-tab-new' : 'chat-tab-keys')
      if (action.kind === 'new') {
        beginAddAgent()
        return
      }
      const target = numberedTabsRef.current[action.index]
      if (target) onSelectTab(target.tabId)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [sideChatsHere, surface, onSelectTab])
  // With many tabs the strip scrolls sideways (tabs keep their width, "+" stays in view) and the
  // active tab is brought into view when it changes.
  const stripRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const active = stripRef.current?.querySelector('[data-tab-active="true"]')
    if (active && typeof (active as HTMLElement).scrollIntoView === 'function') (active as HTMLElement).scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [activeTabId, tabs.length])
  const newTabForm = surface !== 'work' && sideChatsHere && newTabName !== null && (
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
  const moreMenu = surface !== 'work' && (
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
              <PhoneCrewTitle name={projectName} tabName={tabLabel(activeTab)} />
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
                const key = !canControl(tab) ? undefined : sideChatKey(tab)
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
                  {purposeOf(tab) !== undefined && renaming !== tab.tabId && <button type="button" aria-label={`Change ${label}'s purpose`} title="Change purpose" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setMoreOpen(false); setUpAgentPurpose(tab.tabId) }}><Info className="h-3.5 w-3.5" /></button>}
                  {key && renaming !== tab.tabId && <button type="button" aria-label={`Rename ${label}`} title="Rename" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setRenameDraft(sideChatName(tab)); setRenaming(tab.tabId) }}><Pencil className="h-3.5 w-3.5" /></button>}
                  {canControl(tab) && (
                    <button
                      type="button"
                      onClick={() => { setMoreOpen(false); closeProjectTab(tab.tabId) }}
                      className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={`Close ${label}`}
                      title="Close activity view"
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
              {sideChatsHere && <button type="button" disabled={opening} onClick={() => { setMoreOpen(false); beginAddAgent() }}
                className="mt-1 flex min-h-10 shrink-0 items-center gap-2 border-t border-border px-2.5 text-sm text-primary disabled:opacity-40"><Plus className="h-4 w-4" aria-hidden="true" />New agent</button>}
              {onImportLocalChat && <button type="button" disabled={opening} onClick={() => { setMoreOpen(false); onImportLocalChat() }} className="flex min-h-10 shrink-0 items-center gap-2 px-2.5 text-sm text-muted-foreground hover:text-foreground"><span aria-label="Claude, Codex and Cursor" className="flex shrink-0 -space-x-1">{['claude.ico', 'codex.png', 'cursor.svg'].map(icon => <span key={icon} className="grid h-5 w-5 place-items-center rounded-full bg-popover ring-2 ring-popover"><img alt="" src={`/provider-icons/${icon}`} className="h-4 w-4 object-contain" /></span>)}</span>Import local chat…</button>}
              {canImportMcp && placeProject && <button type="button" data-testid="import-mcp-servers" disabled={opening} onClick={() => { setMoreOpen(false); setImportMcp(true) }} className="flex min-h-10 shrink-0 items-center gap-2 px-2.5 text-sm text-muted-foreground hover:text-foreground">Import MCP servers…</button>}
              {onDiscoverSkills && <button type="button" disabled={opening} onClick={() => { setMoreOpen(false); onDiscoverSkills() }} className="flex min-h-10 shrink-0 items-center gap-2 px-2.5 text-sm text-muted-foreground hover:text-foreground">Discover &amp; import skills…</button>}
            </div>, document.body
          )}
        </div>
      )

  const addAgentButton = sideChatsHere && (surface === 'work' || newTabName === null) && !addMode && (<span className="inline-flex shrink-0 items-center">
    <button
      type="button"
      onClick={surface === 'work' ? () => { void startBlankChat() } : beginAddAgent}
      disabled={opening}
      className={`${surface === 'work' ? 'px-2 text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-10 max-md:min-w-10' : 'ml-0.5 text-primary hover:bg-primary/10 hover:text-primary max-md:hidden'} inline-flex h-7 shrink-0 items-center justify-center gap-1 rounded-md text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40 ${surface === 'work' ? '' : sideChats.length === 0 ? 'px-2' : 'w-7'}`}
      aria-label="New agent"
      title={surface === 'work' ? 'New chat: it is named from your first message' : 'Add agent (⌥⇧T on Mac, Alt+Shift+T on Windows)'}
      data-testid="work-new-chat-tab"
    >
      {opening ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
      {/* The Crew keeps a label at desktop widths; other products retain their existing tab controls. */}
      {(surface === 'work' || sideChats.length === 0) && <span className="hidden sm:inline">New agent</span>}
    </button>
    {surface === 'work' && <button type="button" onClick={beginAddAgent} disabled={opening} aria-label="More ways to add an agent" title="New agent with a name, fork or import (⌥⇧T on Mac, Alt+Shift+T on Windows)" data-testid="work-add-agent-menu"
      className="inline-flex h-7 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40 max-md:h-10"><ChevronDown className="h-3.5 w-3.5" aria-hidden="true" /></button>}
  </span>)

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      {importMcp && placeProject && <LocalMcpImportDialog projectId={placeProject} onClose={() => setImportMcp(false)} />}
      {forkTo && <ForkToTeammate state={teamState} agentId={forkTo.id} name={forkTo.name} close={() => setForkTo(null)} />}
      {continueOnline && macProject && <ContinueOnlineDialog project={macProject} chatKey={continueOnline.key} tabId={continueOnline.tabId} name={continueOnline.name} close={() => setContinueOnline(null)} />}
      {/* Crew always uses the same current-agent header. Other products keep their tabs. */}
      {!phone && surface !== 'work' && <ProjectWorkspaceTitle name={projectName} surface={surface} />}
      {surface === 'work' && (openTab?.metadata?.agentProfileOnlineProject ? <OnlineAgentLine tab={openTab} name={sideChatName(openTab)} team={teamState} /> : watchingOther ? <TeammateChatIdentity tabId={openTab?.tabId} /> : <AgentHeaderLine agent={headerAgent} crewName={projectName} onOpenIdentity={onOpenIdentity} />)}
      {/* Viewing someone else's agent: no New agent in its header (owner, 19 Nov). The agents menu keeps it. */}
      {surface === 'work' && !watchingOther && !hideAddAgent && <>
        <span aria-hidden="true" className="mx-0.5 h-4 w-px shrink-0 bg-border" />
        {addAgentButton}
      </>}
      {/* Agent copies waiting for you sit at the header's right end (owner, 20 Nov); the Team menu shows them too. */}
      <div className="ml-auto shrink-0"><PendingAgentForks state={teamState} /></div>
      {surface !== 'work' && <div ref={stripRef} className="hidden md:flex min-w-[84px] items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden [&>*]:shrink-0" data-testid="work-chat-tab-strip" aria-label="Chat tabs">
      {stripTabs.map((tab, index) => {
        const person = tabOwnerId(tab)
        const groupLabel = person && (index === 0 || tabOwnerId(stripTabs[index - 1]) !== person)
          ? <span key={`person:${person}`} title={presenceOf(person)?.line} className="ml-1 flex items-center gap-1.5 text-[11px] text-muted-foreground"><span aria-hidden="true" className="h-4 w-px bg-border" /><PersonPresenceDot presence={presenceOf(person)} />{personName(person)}:</span> : null
        const key = !canControl(tab) ? undefined : sideChatKey(tab)
        const targetKey = key
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
        canClose={tab.tabId !== canonicalTabId && tab.metadata?.isViewOnly === true && canCloseWorkspaceChatTab(tab, useChatStore.getState().chatTabs)}
        isBlank={false}
        displayName={tabLabel(tab)}
        workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined}
        purpose={purposeOf(tab)}
        onSetPurpose={purposeOf(tab) === undefined ? undefined : () => setUpAgentPurpose(tab.tabId)}
        agent={agentFace(tab)}
        titleOverride={key ? sideChatName(tab) : tab.tabId === canonicalTabId ? tabLabel(tab) : undefined}
        onTabClick={selectTab}
        onCloseTab={closeProjectTab}
        onRenameTab={key && canControl(tab) ? renameSideChat : undefined}
        closeStopsSession={tab.metadata?.isViewOnly !== true && !!key}
        onMakeInteractive={onMakeInteractive}
      />{renderAfterTab?.(tab)}</div></Fragment>
      })}
      </div>}
      {moreMenu}

      {newTabForm}
      {addMode && createPortal(
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/40 p-4" onClick={() => { if (!forking && !opening) setAddMode(null) }}>
          <div data-theme-surface="dialog" role="dialog" aria-modal="true" aria-label={addMode === 'new' ? 'New agent' : addMode === 'choose' ? 'Add agent' : 'Fork existing agent'}
            style={{ width: '28rem', maxWidth: 'calc(100vw - 2rem)', maxHeight: 'calc(100vh - 2rem)' }}
            className="min-w-0 overflow-y-auto break-words rounded-xl border border-border bg-surface-panel p-3 shadow-xl" onClick={event => event.stopPropagation()}>
            <div className="mb-2 flex items-center justify-between gap-2">
              <h2 className="text-sm font-semibold">{addMode === 'new' ? 'New agent with a name' : addMode === 'choose' ? 'Add agent' : 'Fork existing agent'}</h2>
              <button type="button" autoFocus={addMode !== 'new'} disabled={forking || opening} aria-label="Close" onClick={() => setAddMode(null)} className="rounded p-2 hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><X className="h-4 w-4" /></button>
            </div>
            {addMode === 'new' ? <form className="space-y-3" onSubmit={event => { event.preventDefault(); void submitNewAgent(newTabName || '') }}>
              <label className="block text-sm">Agent name
                <input autoFocus value={newTabName || ''} onChange={event => setNewTabName(event.target.value)} maxLength={120} disabled={opening} placeholder="e.g. Research" aria-label="Agent name" data-testid="work-new-chat-tab-name" className="mt-1.5 h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
              </label>
              {onlineChoices.length > 0 && <fieldset className="space-y-1.5" disabled={opening || makingOnline}>
                <legend className="mb-1 text-sm font-medium">Where it runs</legend>
                {[{ id: 'mac', name: 'This Mac', line: 'Only while your Mac is on and awake' }, ...onlineChoices.map(item => ({ id: item.project_id, name: item.name, line: 'Online 24/7 on the server' }))].map(option =>
                  <label key={option.id} className={`flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2 text-sm ${newWhere === option.id ? 'border-primary bg-primary/5' : 'border-border'}`}>
                    <input type="radio" name="new-agent-where" value={option.id} checked={newWhere === option.id} onChange={() => setNewWhere(option.id)} className="mt-1" />
                    <span className="min-w-0"><span className="block font-medium">{option.id !== 'mac' && <span aria-hidden="true" className="mr-1.5 inline-block h-1.5 w-1.5 rounded-full bg-emerald-500 align-middle" />}{option.name}</span><span className="block text-xs text-muted-foreground">{option.line}</span></span>
                  </label>)}
              </fieldset>}
              <div className="flex justify-end gap-2">
                <button type="button" disabled={opening} onClick={() => { setAddMode(null); setNewTabName(null) }} className="rounded-md px-3 py-2 text-sm hover:bg-muted">Cancel</button>
                <button type="submit" disabled={opening || makingOnline || !newTabName?.trim()} className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{(opening || makingOnline) && <Loader2 className="h-4 w-4 animate-spin" />}Create agent</button>
              </div>
            </form> : addMode === 'choose' ? <div className="space-y-1">
              <button type="button" disabled={opening} onClick={() => { void startBlankChat() }} className="flex w-full items-center gap-2 rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><Plus className="h-4 w-4" /><span>New chat<span className="block text-xs text-muted-foreground">Named from your first message</span></span></button>
              <button type="button" onClick={() => { setNewTabName(''); setAddMode('new') }} className="w-full rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">New agent with a name…</button>
              <button type="button" onClick={() => setAddMode('fork')} className="w-full rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">Fork existing agent…</button>
              {onImportLocalChat && <button type="button" onClick={() => { setAddMode(null); onImportLocalChat() }} className="w-full rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><span aria-hidden="true" className="mr-2 inline-flex -space-x-1 align-middle">{['claude.ico', 'codex.png', 'cursor.svg'].map(icon => <span key={icon} className="grid h-6 w-6 place-items-center rounded-full bg-popover ring-2 ring-popover"><img alt="" src={`/provider-icons/${icon}`} className="h-4 w-4 object-contain" /></span>)}</span>Import local chat…</button>}
              {canImportMcp && placeProject && <button type="button" onClick={() => { setAddMode(null); setImportMcp(true) }} className="w-full rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">Import MCP servers…</button>}
              {onDiscoverSkills && <button type="button" onClick={() => { setAddMode(null); onDiscoverSkills() }} className="w-full rounded-lg border-t border-border px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">Discover &amp; import skills…</button>}
            </div> : <>
              <p className="mb-2 px-2 text-xs text-muted-foreground">Choose an agent to copy its conversation into a separate chat. The original stays unchanged.</p>
              <div className="max-h-72 overflow-y-auto">
                {tabs.filter(tab => isAgentTab(tab) && canControl(tab) && tab.metadata?.agentProfileConversationKey).map(tab => <button key={tab.tabId} type="button" disabled={forking} onClick={async () => {
                  setForking(true)
                  try { await forkChatTab(tab.tabId); setAddMode(null); phoneNavigation?.onChange('chat') }
                  finally { setForking(false) }
                }} className="flex w-full items-center gap-2 rounded-lg px-3 py-3 text-left text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50">{forking && <Loader2 className="h-3.5 w-3.5 animate-spin" />}<span className="truncate">{tabLabel(tab)}</span></button>)}
              </div>
            </>}
          </div>
        </div>, document.body
      )}
      {surface !== 'work' && addAgentButton}
    </div>
  )
}

const PHONE_PLACE: Record<CloudState, string> ={ mac: 'On your Mac', mac_off: 'Mac off', online: 'Online', offline: 'Offline' }

/** Where the Crew lives, as the server reports it (owner, 3 Nov): a dot and a short word, at every width. */
const livesOnMacHere = (place?: CloudState) => place === 'mac' || place === 'mac_off' || place === 'offline'

function CrewPlaceBadge({ place, word }: { place?: CloudState; word?: string }) {
  if (!place) return null
  const dot = place === 'mac_off' || place === 'offline' ? 'bg-muted-foreground' : 'bg-emerald-500'
  return <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground"><span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${dot}`} />{word || PHONE_PLACE[place]}</span>
}

/** The phone chat title: the Crew's name and the agent; where it lives is the arrow's colour. */
function PhoneCrewTitle({ name, tabName }: { name: string; tabName: string }) {
  return <span className="flex min-w-0 items-center gap-1.5 truncate">
    <span className="truncate font-medium text-foreground">{name}{tabName ? ` · ${tabName}` : ''}</span>
  </span>
}

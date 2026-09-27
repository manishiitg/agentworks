import {
  Activity,
  BellRing,
  BookMarked,
  BookOpen,
  CalendarClock,
  Cloud,
  DollarSign,
  Files,
  FileText,
  Fingerprint,
  Hand,
  Globe,
  LayoutDashboard,
  Monitor,
  Route,
  Server,
  ShieldCheck,
  Webhook,
  Zap,
  type LucideIcon,
} from 'lucide-react'

/**
 * The single registry of right-side workspace views.
 *
 * Every other list of "which views exist" -- the store's view type, its
 * normalizer, the toolbar's button groups, the canvas host's dispatch, the
 * layout's pane-visibility check, and the capabilities panel's section type --
 * derives from this array. Add a view here and the type system carries it
 * everywhere; forget a dispatch branch and `assertNever` in the canvas host
 * fails to compile.
 *
 * `kind` is what the host and layout switch on:
 *   - `canvas`     the React Flow plan (`flow`).
 *   - `preview`    the report preview (`report`) -- no React Flow tree.
 *   - `files`      the file browser.
 *   - `inspector`  a full-pane inspector (costs, logs, learnings, ...).
 *   - `capability` a section of `WorkflowCapabilitiesPanel`.
 *
 * `flow` and `report` are the two "canvas views": the store's
 * `workflowWorkspaceView` being null means "show the last canvas view the
 * user was on" (`lastCanvasView`), which is how the default builder
 * workspace works. Old persisted ids (`builder`, `log`, `soul`, `plan`) are
 * remapped by `normalizeWorkspaceViewId` and never exist as views.
 *
 * `pane` says whether opening the view means the workspace pane is showing
 * content (every kind except `files`, which the layout treats as the default
 * workspace rather than an open pane).
 */
export type WorkspaceViewKind = 'canvas' | 'preview' | 'files' | 'inspector' | 'capability'

export type WorkspaceViewDef = {
  id: string
  kind: WorkspaceViewKind
  /** Toolbar label; also the aria-label and tooltip of the toolbar button. */
  label: string
  icon: LucideIcon
  /** Which toolbar cluster the view's button belongs to, if it has one. */
  toolbarGroup?: 'views' | 'pulse' | 'capabilities'
  /** Opening this view shows the workspace pane. */
  pane: boolean
  /** The section renders inside a bounded flex column and manages its own
   * overflow, rather than letting the capabilities panel scroll it. */
  managesOwnScroll?: true
}

const VIEWS = [
  // -- canvas / preview (dispatched by WorkspaceViewHost) ------------------
  { id: 'report', kind: 'preview', label: 'Dashboard', icon: LayoutDashboard, toolbarGroup: 'views', pane: true },
  { id: 'flow', kind: 'canvas', label: 'Plan', icon: Route, toolbarGroup: 'views', pane: true },
  // -- inspectors (toolbar "views" cluster, in button order) ---------------
  { id: 'costs', kind: 'inspector', label: 'Costs', icon: DollarSign, toolbarGroup: 'views', pane: true },
  { id: 'execution-logs', kind: 'inspector', label: 'Execution logs', icon: FileText, toolbarGroup: 'views', pane: true },
  { id: 'knowledge', kind: 'inspector', label: 'Knowledge', icon: BookOpen, toolbarGroup: 'views', pane: true },
  { id: 'webhooks', kind: 'inspector', label: 'Webhooks', icon: Webhook, toolbarGroup: 'views', pane: true },
  { id: 'schedules', kind: 'inspector', label: 'Schedules', icon: CalendarClock, toolbarGroup: 'views', pane: true },
  // -- files (last button of the "views" cluster) --------------------------
  { id: 'files', kind: 'files', label: 'Files', icon: Files, toolbarGroup: 'views', pane: false },
  // -- inspectors (toolbar "pulse" cluster) --------------------------------
  // Pulse is a primary toolbar button beside Report. The remaining controls
  // are hand-built in Views/Setup for their status dots and permissions,
  // so the legacy `toolbarGroup: 'pulse'` here
  // is categorization only -- nothing filters on it the way `views` and
  // `capabilities` are filtered into their auto-rendered clusters below.
  { id: 'pulse', kind: 'inspector', label: 'Pulse', icon: Activity, toolbarGroup: 'pulse', pane: true },
  { id: 'human-actions', kind: 'inspector', label: 'Human actions', icon: Hand, toolbarGroup: 'pulse', pane: true },
  { id: 'backup', kind: 'inspector', label: 'Backup', icon: Cloud, toolbarGroup: 'pulse', pane: true },
  { id: 'publish', kind: 'inspector', label: 'Publish', icon: Globe, toolbarGroup: 'pulse', pane: true },
  { id: 'notify', kind: 'inspector', label: 'Notify', icon: BellRing, toolbarGroup: 'pulse', pane: true },
  // Access (share this workflow / manage users) opens in the pane like the
  // rest of Setup, not a modal (user request 2026-09-03). Button is hand-built
  // in the toolbar because its visibility is permission-gated.
  { id: 'access', kind: 'inspector', label: 'Access', icon: ShieldCheck, toolbarGroup: 'pulse', pane: true },
  // -- capability sections (WorkflowCapabilitiesPanel) -------------------------
  { id: 'identity', kind: 'capability', label: 'Identity', icon: Fingerprint, toolbarGroup: 'capabilities', pane: true, managesOwnScroll: true },
  { id: 'mcp', kind: 'capability', label: 'Integrations', icon: Server, toolbarGroup: 'capabilities', pane: true, managesOwnScroll: true },
  { id: 'playbooks', kind: 'capability', label: 'Workflow playbooks', icon: BookMarked, toolbarGroup: 'capabilities', pane: true, managesOwnScroll: true },
  { id: 'browser', kind: 'capability', label: 'Browser', icon: Monitor, toolbarGroup: 'views', pane: true },
  { id: 'workshop', kind: 'inspector', label: 'Automation', icon: Zap, toolbarGroup: 'views', pane: true },
] as const satisfies readonly WorkspaceViewDef[]

export type WorkspaceViewId = typeof VIEWS[number]['id']

/** A registry entry with its id narrowed to the known set and every optional
 * field present on the type, so callers can read `toolbarGroup` or
 * `managesOwnScroll` without narrowing per entry. */
export type WorkspaceView = WorkspaceViewDef & { id: WorkspaceViewId }

export const WORKSPACE_VIEWS: readonly WorkspaceView[] = VIEWS

/** Views rendered by the generic Views toolbar loop. Plan stays in this list
 * even before the workflow has steps: opening it is how a new workflow reaches
 * the existing "No Plan Yet" / "Build Plan" state. Schedules is rendered
 * separately because its button carries status affordances. */
export const PRIMARY_WORKSPACE_TOOLBAR_VIEWS = WORKSPACE_VIEWS.filter(
  view => view.toolbarGroup === 'views' && view.id !== 'schedules',
)

type ViewOfKind<K extends WorkspaceViewKind> = Extract<typeof VIEWS[number], { kind: K }>['id']

export type InspectorViewId = ViewOfKind<'inspector'> | ViewOfKind<'capability'>
export type CapabilityViewId = ViewOfKind<'capability'>
export type PreviewViewId = ViewOfKind<'preview'>
/** The two views that render in the canvas slot (Plan and Report); the store
 * remembers the last one opened as the fallback for "no explicit view". */
export type CanvasViewId = ViewOfKind<'canvas'> | ViewOfKind<'preview'>

const VIEW_BY_ID: ReadonlyMap<string, WorkspaceView> = new Map(
  WORKSPACE_VIEWS.map(view => [view.id, view] as const),
)

export function getWorkspaceView(id: WorkspaceViewId): WorkspaceView {
  return VIEW_BY_ID.get(id)!
}

export function isWorkspaceViewId(value: unknown): value is WorkspaceViewId {
  return typeof value === 'string' && VIEW_BY_ID.has(value)
}

/** Inspector-pane views: full-pane inspectors plus capability sections, which
 * the canvas host renders through the same inspector slot. */
export function isInspectorView(id: WorkspaceViewId | null | undefined): id is InspectorViewId {
  if (!id) return false
  const kind = VIEW_BY_ID.get(id)?.kind
  return kind === 'inspector' || kind === 'capability'
}

export function isCapabilityView(id: WorkspaceViewId | null | undefined): id is CapabilityViewId {
  return Boolean(id) && VIEW_BY_ID.get(id as string)?.kind === 'capability'
}

export function isPreviewView(id: WorkspaceViewId | null | undefined): id is PreviewViewId {
  return Boolean(id) && VIEW_BY_ID.get(id as string)?.kind === 'preview'
}

export function isCanvasView(id: unknown): id is CanvasViewId {
  if (typeof id !== 'string') return false
  const kind = VIEW_BY_ID.get(id)?.kind
  return kind === 'canvas' || kind === 'preview'
}

/** True when the view means the workspace pane is showing something other
 * than the default builder workspace or the file browser. */
export function isWorkspacePaneView(id: WorkspaceViewId | null | undefined): boolean {
  return Boolean(id) && VIEW_BY_ID.get(id as string)?.pane === true
}

export const CAPABILITY_VIEWS = WORKSPACE_VIEWS.filter(
  (view): view is WorkspaceView & { id: CapabilityViewId; kind: 'capability' } => view.kind === 'capability',
)

/** Legacy persisted ids that no longer exist as views of their own. `log` and
 * `soul` were the old Pulse/Soul preview, which is the report today;
 * `builder` meant "no explicit view", which is `null`. */
const LEGACY_VIEW_IDS: Record<string, WorkspaceViewId | null> = {
  soul: 'report',
  log: 'report',
  plan: 'flow',
  builder: null,
  'api-triggers': 'workshop',
  schedules: 'workshop',
  webhooks: 'workshop',
  learnings: 'knowledge',
  knowledgebase: 'knowledge',
  database: 'knowledge',
}

/** Coerce a persisted/unknown value to a view id, or null. Legacy ids are
 * remapped; anything unrecognised becomes null. */
export function normalizeWorkspaceViewId(value: unknown): WorkspaceViewId | null {
  if (value === null || value === undefined) return null
  if (typeof value !== 'string') return null
  if (value in LEGACY_VIEW_IDS) return LEGACY_VIEW_IDS[value]
  return isWorkspaceViewId(value) ? value : null
}

/** Coerce a persisted canvas-view value (current `lastCanvasView` or the old
 * `canvasViewMode`, which also allowed `log`/`soul`/`plan`) to Plan or Report. */
export function normalizeCanvasViewId(value: unknown): CanvasViewId | undefined {
  const view = normalizeWorkspaceViewId(value)
  return isCanvasView(view) ? view : undefined
}

/** Compile-time exhaustiveness helper for switches over view ids. Never
 * throws: an unhandled id is a type error, not a runtime failure. */
export function assertNeverView(_value: never): null {
  return null
}

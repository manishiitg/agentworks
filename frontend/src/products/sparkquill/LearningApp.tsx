import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import {
  Activity as PulseIcon,
  ArrowLeft,
  ArrowRight,
  BookOpen,
  Check,
  CheckCircle2,
  ChevronDown,
  ExternalLink,
  FileArchive,
  FileCode,
  FileSpreadsheet,
  FileText,
  FileType,
  Film,
  Folder,
  FolderOpen,
  Globe,
  HardDrive,
  Type,
  Image as ImageIcon,
  Info,
  LockKeyhole,
  Maximize2,
  Minimize2,
  Music,
  Presentation,
  Printer,
  RefreshCw,
  Settings as SettingsIcon,
  Sparkles,
  Star,
  Sun,
  Pin,
  PinOff,
  Bell,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Trash2,
} from 'lucide-react'
import './learning-app.css'
import {
  useSetupStore,
  useFamilyStore,
  useSparkQuillWorkspaceStore,
  useChildChatStore,
  useWhatsAppStore,
  usePinGateStore,
  type Screen,
  type ApiEngine,
  type PinnedPage,
  type QuickCommand,
  type TreeNode,
  type WsFile,
  type Activity,
  type VoiceStatus,
} from './stores'
import PlatformChat, { FAMILY_WORKSPACE, PARENT_PROFILE_ID, applyFamilyEngineToOpenTabs, startNewParentConversation, submitToParentChat, switchParentConversation, type ProductInteraction, type ProductPresentation } from './platform/PlatformChat'
import { CHILD_PROFILE_ID, CHILD_PROFILE_VERSION } from './platform/ChildPlatformChat'
import type { ParentChat } from './api/familyApi'
import { loadAgentProfileCapabilityEnabled, loadAgentProfileProviderOptions, type AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import PulseHistoryViewer from './platform/PulseHistoryViewer'
import type { ProductNotification } from '../../platform/notifications/useProductNotifications'
import ChildPlatformChat, { forgetChildChat, submitToChildChat, type ChildKickoff } from './platform/ChildPlatformChat'
import { completedSparkQuillTurns } from './platform/turnRefresh'
import { withPreviewPositionScript } from './platform/previewPosition'
import { useChatStore } from '../../stores/useChatStore'
import { api } from './api'
import { VoiceSettings } from './voice/VoiceSettings'
import { LearningModelSettings } from './LearningModelSettings'
import CodingProvidersPanel from '../../components/providers/CodingProvidersPanel'
import { useLLMStore } from '../../stores/useLLMStore'
import { readReminderSoundPref, persistReminderSoundPref } from './notifySound'
import { readVoiceAutoSendPref, persistVoiceAutoSendPref } from './voiceAutoSend'
import { buildSqAnswerText, buildSqTimerText, sanitizeSqId, sanitizeSqTimerConfigs, SQ_MAX_GAME_STATE_BYTES, sqGameStateKey, withViewerLinkBridge } from './sqOps'
import { ChatMarkdown as SharedChatMarkdown } from '../../../shared/chat/ChatRenderer'
import { ProductSurfaceSwitcher } from '../../components/ProductSurfaceSwitcher'
import { hasGatewaySSO, isSingleProductDeployment } from '../productSurfaceConfig'
import { isBrowserCDPEnabled } from '../../utils/runtimeCapabilities'
import WorkflowLiveBrowser from '../../components/workflow/WorkflowLiveBrowser'

// The child/file viewer iframe is deliberately sandbox="allow-scripts" with
// NO allow-same-origin (adding that would let a srcDoc page's script escape
// the sandbox and touch the parent page/cookies) — which makes it a
// cross-origin frame from the app's own perspective. So the app can never read
// or set that frame's scroll from outside; anything positional has to be done
// by a script injected INTO the document (see withViewerPositionScript).
function engineStatus(e: ApiEngine): { label: string; ready: boolean } {
  if (e.usable) return { label: 'Ready', ready: true }
  if (!e.runtime_available) return { label: 'Not set up', ready: false }
  if (!e.auth_configured) return { label: 'Needs sign-in', ready: false }
  return { label: 'Unavailable', ready: false }
}

// Parent-friendly presentation, keyed by the technical engine id.
// Order reflects the product preference: ChatGPT → Claude → Cursor → Pi.
const ENGINE_PRESENTATION: Record<string, { name: string; blurb: string; order: number; preferred?: boolean }> = {
  'codex-cli': { name: 'ChatGPT', blurb: 'Uses your ChatGPT account · can also create images', order: 1, preferred: true },
  'claude-code': { name: 'Claude', blurb: 'Careful, patient, step-by-step teaching', order: 2 },
  'cursor-cli': { name: 'Cursor', blurb: 'Uses your Cursor account', order: 3 },
  'pi-cli': { name: 'Pi', blurb: 'Lets you pick from many other AI models', order: 4 },
}
function pres(id: string, fallbackName: string) {
  return ENGINE_PRESENTATION[id] ?? { name: fallbackName, blurb: 'Available on this computer', order: 99, preferred: false }
}


// Child profile options — edit here to adjust the setup form.
// Targeting grades 6–12, with 4–5 also offered.
const GRADES = ['4', '5', '6', '7', '8', '9', '10', '11', '12']
const BOARDS = ['CBSE', 'ICSE / ISC (CISCE)', 'State Board', 'NIOS', 'IB', 'Cambridge / IGCSE', 'Other', 'Not sure']

// Absolute date + time label for a package, e.g. "21 Jul 2026, 5:42 PM".
function dateTimeLabel(iso?: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  return new Date(t).toLocaleString(undefined, { day: 'numeric', month: 'short', year: 'numeric', hour: 'numeric', minute: '2-digit', hour12: true })
}

// dateOnlyKey/dateOnlyLabel back the Workspace tab's "group by date" view —
// key groups by calendar day (local time, so late-evening activities don't
// slip into the next day's group), label is what's actually shown as the
// section heading.
function dateOnlyKey(iso?: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  return new Date(t).toLocaleDateString('en-CA')
}
function dateOnlyLabel(iso?: string): string {
  if (!iso) return 'Undated'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return 'Undated'
  return new Date(t).toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' })
}

// Which side of the handoff the browser should land on after a refresh.
// Without this, a refresh always falls back to Parent Mode — letting a child
// bypass the PIN gate entirely just by reloading the page. Persisted in
// localStorage (this is a single local-machine app, same as the rest of its
// state) and flipped explicitly at every real hand-off/PIN-unlock point.
const HANDOFF_SIDE_KEY = 'sparkquill.handoff-side'
function persistHandoffSide(side: 'tutor' | 'parent') {
  try { localStorage.setItem(HANDOFF_SIDE_KEY, side) } catch { /* best-effort */ }
}
function readHandoffSide(): 'tutor' | 'parent' {
  try { return localStorage.getItem(HANDOFF_SIDE_KEY) === 'tutor' ? 'tutor' : 'parent' } catch { return 'parent' }
}

// How wide the child's right-hand pane is, in px, dragged by the divider between
// her chat and her worksheet. Persisted because it's a per-child working
// preference, not a per-session one: whoever likes a big worksheet and a narrow
// chat should get that on every visit without re-dragging it.
//
// Bounds keep both panes usable — a pane dragged to nothing looks like a broken
// layout rather than a deliberate choice, and there's no affordance to get it
// back once the handle is off-screen.
const CHILD_SIDE_WIDTH_KEY = 'sparkquill.child-side-width'
const CHILD_SIDE_MIN = 320
// Half the window: the worksheet and the chat start as equals.
function childSideDefault(): number {
  const width = typeof window !== 'undefined' ? window.innerWidth : 1200
  return Math.max(CHILD_SIDE_MIN, Math.round(width / 2))
}
// The chat's floor is a PIXEL minimum, not a fraction of the window. A fraction
// looks fine on a large display and collapses on a small one — 20% of 1100px is
// 220px, which cannot hold a readable message bubble plus the composer. This is
// the width below which the chat stops being usable, so the worksheet never gets
// to claim it however wide it is asked to be.
const CHILD_CHAT_MIN = 400

// childSideMax is how wide the worksheet may get for a given window width.
// Never negative: on a very narrow window the min wins and the layout falls back
// to the single-column media query anyway.
function childSideMax(windowWidth: number): number {
  return Math.max(CHILD_SIDE_MIN, windowWidth - CHILD_CHAT_MIN)
}

function readChildSideWidth(): number {
  try {
    const n = Number(localStorage.getItem(CHILD_SIDE_WIDTH_KEY))
    return Number.isFinite(n) && n >= CHILD_SIDE_MIN ? n : childSideDefault()
  } catch { return childSideDefault() }
}
function persistChildSideWidth(px: number) {
  try { localStorage.setItem(CHILD_SIDE_WIDTH_KEY, String(Math.round(px))) } catch { /* best-effort */ }
}

// Parent Mode's own drag-to-resize for the workspace drawer — same mechanism
// as the child's worksheet, but capped at HALF the window rather than nearly
// all of it: the drawer here is a reference panel beside the conversation
// (Progress/Files), not the primary thing being read, so the chat
// should never be squeezed to a sliver the way the child's worksheet is
// allowed to claim most of the screen.
const PARENT_SIDE_WIDTH_KEY = 'sparkquill.parent-side-width.v2' // v2: the default became half the window
// 320 looked like a sane floor, but parentSideMax's own ceiling (half the
// window) drops below it on anything narrower than ~640px — the two clamps
// then collapse to the same point and the resizer can't move in either
// direction, even though it visually still drags. Low enough to leave real
// room at ordinary window widths, not just wide ones.
const PARENT_SIDE_MIN = 200
// Half the window: the conversation and the drawer start as equals (the
// previous fixed 592px favoured the chat on wide screens and crushed it on
// narrow ones).
function parentSideDefault(): number {
  const width = typeof window !== 'undefined' ? window.innerWidth : 1200
  return Math.max(PARENT_SIDE_MIN, Math.floor(width / 2))
}
function parentSideMax(windowWidth: number): number {
  return Math.max(PARENT_SIDE_MIN, Math.floor(windowWidth * 0.5))
}
function readParentSideWidth(): number {
  try {
    const n = Number(localStorage.getItem(PARENT_SIDE_WIDTH_KEY))
    return Number.isFinite(n) && n >= PARENT_SIDE_MIN ? n : parentSideDefault()
  } catch { return parentSideDefault() }
}
function persistParentSideWidth(px: number) {
  try { localStorage.setItem(PARENT_SIDE_WIDTH_KEY, String(Math.round(px))) } catch { /* best-effort */ }
}

// Reading size for the worksheet. Applied as CSS `zoom` inside the viewer
// iframe rather than a font-size override: the generated pages size everything
// in px (headings, card padding, SVG diagrams), so scaling only the body font
// would grow the paragraphs and leave headings and diagrams behind. Zoom scales
// the whole page coherently, which is what "bigger" should mean on a worksheet.
const CHILD_ZOOM_KEY = 'sparkquill.child-zoom'
const CHILD_ZOOM_STEPS = [1, 1.15, 1.35, 1.6]
function readChildZoom(): number {
  try {
    const n = Number(localStorage.getItem(CHILD_ZOOM_KEY))
    return CHILD_ZOOM_STEPS.includes(n) ? n : 1
  } catch { return 1 }
}
function persistChildZoom(z: number) {
  try { localStorage.setItem(CHILD_ZOOM_KEY, String(z)) } catch { /* best-effort */ }
}

// Reading size for the CHAT side, kept separate from the worksheet's: she may
// want big text on a dense study sheet but not on the conversation, or the
// reverse. Real DOM here, not a sandboxed iframe, so this scales the actual
// font size (via --chat-scale) rather than zooming a whole page.
const CHILD_CHAT_ZOOM_KEY = 'sparkquill.child-chat-zoom'
function readChildChatZoom(): number {
  try {
    const n = Number(localStorage.getItem(CHILD_CHAT_ZOOM_KEY))
    return CHILD_ZOOM_STEPS.includes(n) ? n : 1
  } catch { return 1 }
}
function persistChildChatZoom(z: number) {
  try { localStorage.setItem(CHILD_CHAT_ZOOM_KEY, String(z)) } catch { /* best-effort */ }
}

// Dark/light theme — follows the OS/browser's own preference (or a
// previously-stored explicit choice, from when there was an in-app toggle).
// No in-app toggle for now; kept read-only.
type Theme = 'light' | 'dark'
const THEME_KEY = 'sparkquill.theme'

function readTheme(): Theme {
  try {
    const stored = localStorage.getItem(THEME_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch { /* best-effort */ }
  return (typeof window !== 'undefined' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light'
}

// rewriteRelativeAssetURLs fixes a bug in how generated HTML pages are
// previewed: the viewer renders HTML via <iframe srcDoc={...}> (raw markup
// injected directly, not loaded as a real document at its own URL), so it has
// no base URL matching the file's actual folder on disk. A page that does
// exactly what its own skill tells it to — save an illustration next to the
// page and reference it with a plain relative `<img src="foo.png">` — silently
// fails to load the image (the browser resolves "foo.png" against the SPA's
// own URL, gets a 404, and falls back to showing the alt text in its place).
// Rewrites bare-relative src="..." references (skipping absolute/data/anchor
// URLs, which are already fine) into the real /api/workspace/raw endpoint for
// that exact file, resolved against the HTML file's own directory.
// rewriteImgSrcsRelativeTo resolves every relative src="..." in html against
// dir and points it at the raw-file API — the one place this actually
// happens, shared by the full-page viewer and a show_scene snippet alike.
function rewriteImgSrcsRelativeTo(html: string, dir: string): string {
  return html.replace(/\bsrc=(["'])(.*?)\1/gi, (whole, quote: string, ref: string) => {
    if (/^(https?:)?\/\//i.test(ref) || ref.startsWith('/') || ref.startsWith('data:') || ref.startsWith('#')) return whole
    const resolved = dir ? `${dir}/${ref}` : ref
    const url = api.rawUrl(resolved)
    return `src=${quote}${url}${quote}`
  })
}

function rewriteRelativeAssetURLs(html: string, filePath: string): string {
  if (!/^\s*<(!doctype|html)/i.test(html)) return html
  const dir = filePath.includes('/') ? filePath.slice(0, filePath.lastIndexOf('/')) : ''
  return rewriteImgSrcsRelativeTo(html, dir)
}

// withSceneResizeScript appends a tiny bootstrap script to a show_scene
// snippet so the iframe reports its own content height via postMessage —
// SceneFrame below sizes itself to that instead of a fixed guess, so content
// never gets clipped regardless of how tall a given scene turns out to be.
// (contentWindow.scrollHeight can't be read from the outside — the iframe is
// a cross-origin sandboxed frame — so the height has to self-report.)
function withSceneResizeScript(html: string): string {
  return html + `
<script>(function(){
  function report(){
    var h = document.documentElement.scrollHeight;
    parent.postMessage({ __sq: 1, op: 'scene-resize', height: h }, '*');
  }
  window.addEventListener('load', report);
  if (window.ResizeObserver) new ResizeObserver(report).observe(document.documentElement);
  setTimeout(report, 50);
  // Same SQ.choose as the shared full-page template (skills/_shared/html-design.md)
  // — a scene snippet is a SEPARATE srcDoc document, so it doesn't inherit that
  // page's own <script>; without this, a scene's SQ.choose button would throw
  // "SQ is not defined" the instant it's clicked. Disables the button immediately
  // so a slow reply can't be mistaken for a missed tap and answered twice.
  window.SQ = { choose: function (text, el) {
    if (el && el.disabled) return;
    if (el) el.disabled = true;
    parent.postMessage({ __sq: 1, op: 'choose', text: text }, '*');
  }, answer: function (qid, value, el) {
    if (el && el.disabled) return;
    if (el) el.disabled = true;
    parent.postMessage({ __sq: 1, op: 'answer', qid: qid, value: value }, '*');
  } };
})();</script>`
}

// withDiagramLib supplies JSXGraph to a generated page that draws a geometric
// figure (an angle, a circle, a labelled triangle, a graph). The page itself
// only ever contains a `<div class="jxgbox">` plus a few declarative
// JXG.JSXGraph.initBoard(...) calls — the ~1MB library is NOT written into
// every activity file; it's served once from the app's own dist (public/lib/)
// and browser-cached, so pages stay small and a second diagram costs nothing.
//
// Why the URL must be absolute: the viewer is a srcDoc iframe, whose document
// URL is about:srcdoc — relative paths have no base to resolve against and
// silently 404. That's the same reason images are rewritten to absolute
// FAMILY_API URLs (see rewriteImgSrcsRelativeTo).
//
// Why PREPEND and not append: the page's own initBoard call is inline in its
// body, so the library has to be defined before that runs — appending it (the
// way withViewerPositionScript appends) would raise "JXG is not defined".
// Injected into <head> when there is one, else at the very front.
//
// Skipped entirely for pages with no figure, so a plain worksheet never pays
// for it.
function withDiagramLib(html: string): string {
  if (!/jxgbox|JXG\./.test(html)) return html
  const tags =
    `<link rel="stylesheet" href="${api.assetUrl('lib/jsxgraph.css')}">` +
    `<script src="${api.assetUrl('lib/jsxgraphcore.js')}"></script>`
  if (/<head[^>]*>/i.test(html)) return html.replace(/<head[^>]*>/i, (m) => m + tags)
  return tags + html
}

// withViewerPositionScript keeps the child's place in her worksheet across the
// re-opens the tutor triggers, and jumps to a specific question only when the
// tutor deliberately asks for one.
//
// Why it has to run inside the frame: the viewer iframe is sandboxed
// allow-scripts WITHOUT allow-same-origin, so it is cross-origin and the parent
// can neither read nor write its scroll position. An earlier attempt restored
// scroll from outside via contentWindow.scrollY, which silently returned 0 every
// time (the read throws and is caught) — that is why re-opening a page after the
// tutor recorded an answer always jumped back to the top.
//
// So the script reports its own scroll position out to the app as it changes, and
// the app hands the last known offset back in on the next load. The iframe is
// recreated whenever srcDoc changes, so the value has to live outside it.
//
// Priority, highest first:
//  1. focusId — open_file's explicit `focus`. A deliberate act by the tutor
//     ("let's look at that money one again"), so it outranks her scroll position.
//  2. savedY — where she actually was. This is the common case: the tutor records
//     an answer, re-opens the file to refresh it, and she should not lose her
//     place. Restoring a position is strictly better than guessing a question,
//     which is why the old "scroll to the first unanswered question" behaviour was
//     dropped: it moved the page under her whenever she was reading elsewhere.
//  3. Nothing — a genuinely new file opens at the top, as expected.
function withViewerPositionScript(html: string, focusId?: string, savedY = 0, zoom = 1): string {
  const wanted = (focusId ?? '').replace(/[^A-Za-z0-9_-]/g, '')
  // Zoom shrinks the effective viewport, so a page whose .wrap is capped at a
  // fixed px width would overflow sideways instead of reflowing. Releasing that
  // cap lets the content just fill the pane at any size.
  //
  // Applied to <html>, NOT <body> — confirmed live: zooming body left the page
  // completely stuck, unable to scroll past whatever fit in one un-zoomed
  // screen's worth of height. Chromium's `zoom` grows the zoomed element's own
  // rendered box, but doesn't reliably widen its PARENT's scrollable extent to
  // match — so with body zoomed, <html> (the actual scrolling element here)
  // kept thinking the document was only as tall as it was at 100%, and
  // silently clipped everything past that with no way to reach it. Zooming
  // the root itself sidesteps the mismatch: there's no parent left to fall
  // out of sync with.
  const zoomStyle = zoom === 1 ? '' : `
<style>
  html { zoom: ${zoom}; }
  .wrap { max-width: none !important; }
</style>`
  return html + zoomStyle + `
<script>(function(){
  var wanted = ${JSON.stringify(wanted)};
  var savedY = ${Math.max(0, Math.round(savedY))};
  function restore(){
    try {
      var target = wanted ? document.getElementById(wanted) : null;
      if (target && !target.classList.contains('q')) {
        // Allow pointing at anything with an id (a study-sheet section), but
        // prefer its enclosing question so the highlight frames the whole thing.
        target = target.closest('.q') || target;
      }
      if (target) {
        target.scrollIntoView({ behavior: 'auto', block: 'center' });
        target.classList.add('is-current');
        setTimeout(function(){ target.classList.remove('is-current'); }, 2600);
        return;
      }
      if (savedY > 0) window.scrollTo(0, savedY);
    } catch (e) { /* never break the page over a nice-to-have */ }
  }
  window.addEventListener('load', restore);
  setTimeout(restore, 60);
  // Report position out so the next load can restore it. Throttled via rAF:
  // scroll fires far too often to postMessage on every event.
  var pending = false;
  window.addEventListener('scroll', function(){
    if (pending) return;
    pending = true;
    requestAnimationFrame(function(){
      pending = false;
      parent.postMessage({ __sq: 1, op: 'viewer-scroll', y: window.scrollY }, '*');
    });
  }, { passive: true });
})();</script>
<style>
  /* The frame is the page's only window, so the last thing on it must never
     sit flush against the bottom edge; a page written with no bottom margin
     (its author never sees the frame) ended with its final button touching
     the border. Room at the end, whatever the page's own styles say. */
  body{padding-bottom:40px !important}
  /* A brief, calm pulse so she can see WHERE the page landed when the tutor
     pointed at a specific question. Respects reduced-motion. */
  .q.is-current{animation:sqFocus 2.6s ease-out both}
  @keyframes sqFocus{
    0%{background:#fdeecb;box-shadow:0 0 0 6px #fdeecb}
    100%{background:transparent;box-shadow:0 0 0 6px transparent}
  }
  @media (prefers-reduced-motion:reduce){
    .q.is-current{animation:none;background:#fdeecb}
  }
</style>`
}

// StartBurst is the moment the child begins an activity: a ring of stars flies
// out from the centre, spins, and fades. Short (1.5s) and non-blocking — it sits
// over the screen with pointer-events: none so it can never swallow a tap, and it
// removes itself when finished.
//
// Two things here are deliberate, both learned from getting it wrong:
//
//  1. The timer is armed ONCE. onDone is a fresh closure on every parent render,
//     so depending on it re-armed the timeout continuously — and a child turn
//     re-renders on every streamed delta, so the burst never removed itself and
//     the stars just sat on the page. The callback is held in a ref instead.
//  2. Each star's direction is a static inline transform on a wrapper, NOT a
//     custom property read inside @keyframes. Animating a transform built from
//     var() is fragile; keeping the keyframe free of var() means the only thing
//     animating is a plain translate/rotate/scale, which always works.
//
// Purely decorative, so it is skipped entirely under prefers-reduced-motion
// rather than shown frozen.
function StartBurst({ onDone }: { onDone: () => void }) {
  const doneRef = useRef(onDone)
  doneRef.current = onDone
  useEffect(() => {
    const t = window.setTimeout(() => doneRef.current(), 1500)
    return () => window.clearTimeout(t)
  }, [])
  const count = 10
  return (
    <div className="fl-start-burst" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => (
        <span
          key={i}
          className="fl-start-burst-ray"
          style={{ transform: `rotate(${(360 / count) * i}deg)` }}
        >
          <Star
            className="fl-start-burst-star"
            size={i % 3 === 0 ? 30 : 20}
            fill="currentColor"
            strokeWidth={1}
            style={{ animationDelay: `${(i % 5) * 0.06}s` }}
          />
        </span>
      ))}
      <Sparkles className="fl-start-burst-core" size={54} />
    </div>
  )
}

// SceneFrame renders one show_scene snippet, auto-sized to its actual content
// height (see withSceneResizeScript) instead of a fixed height that would
// either clip taller scenes or leave dead space under shorter ones. Each
// instance only reacts to resize reports from its OWN iframe (matched via
// the message event's source window), so multiple scenes in the same thread
// don't interfere with each other.
// activityDir: the current activity folder, so a find_image picture the tutor
// references with a plain relative `<img src="filename.png">` resolves — the
// scene's own srcDoc has no such path otherwise, so the image silently
// rendered as a broken-image glyph (confirmed live: the tutor called
// find_image, got a real picture back, and it still never appeared).
// SCENE_MAX_HEIGHT bounds one scene's footprint in the scrolling chat feed —
// not a content cap (nothing above it is lost: the iframe scrolls internally
// by default, and no ancestor sets overflow:hidden), just how tall a single
// inline turn is allowed to make itself before the rest of the conversation
// gets pushed out of view. Raised from the original 520 now that scenes are
// meant to include real interactivity (games, simulations), which genuinely
// wants more room than a passive diagram did.
const SCENE_MAX_HEIGHT = 720

function SceneFrame({ html, activityDir }: { html: string; activityDir: string }) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [rawHeight, setRawHeight] = useState(160)
  useEffect(() => {
    const onMsg = (e: MessageEvent) => {
      if (e.source !== ref.current?.contentWindow) return
      const m = e.data
      if (m && typeof m === 'object' && m.__sq === 1 && m.op === 'scene-resize' && typeof m.height === 'number') {
        setRawHeight(Math.max(m.height, 80))
      }
    }
    window.addEventListener('message', onMsg)
    return () => window.removeEventListener('message', onMsg)
  }, [])
  const resolved = rewriteImgSrcsRelativeTo(html, activityDir)
  // A child should never have to discover a cut-off scene by noticing a faint
  // scrollbar — when content is genuinely taller than the cap, say so visibly.
  const clipped = rawHeight > SCENE_MAX_HEIGHT
  return (
    <div className="fl-scene-card">
      <iframe ref={ref} className="fl-scene-frame" title="Scene" sandbox="allow-scripts" style={{ height: Math.min(rawHeight, SCENE_MAX_HEIGHT) }} srcDoc={withSceneResizeScript(withViewerLinkBridge(withDiagramLib(resolved)))} />
      {clipped && <div className="fl-scene-more" aria-hidden="true">scroll for more ↓</div>}
    </div>
  )
}

// ActivityItemPreview shows an activity item's actual content — HTML in a
// sandboxed iframe, Markdown rendered in-page — at real (readable) size, in a
// short scrollable box, so a parent can read a test/guide's real content in
// place rather than just a filename. Falls back to a plain icon+name row
// while loading, or for anything else (images/PDF already have their own
// list glyph, and aren't a page to peek into). `large` gives it noticeably
// more room — used when it's the only item in the activity, so there's
// nothing else competing for space and more of the real content shows at once.
function ActivityItemPreview({ path, name, large, refreshKey }: { path: string; name: string; large?: boolean; refreshKey: number }) {
  const [content, setContent] = useState<{ kind: 'html' | 'md'; text: string } | null>(null)
  const frameRef = useRef<HTMLIFrameElement>(null)
  const scrollYRef = useRef(0)
  useEffect(() => {
    const onMsg = (e: MessageEvent) => {
      if (e.source !== frameRef.current?.contentWindow) return
      const m = e.data as { __sq?: number; op?: string; y?: number } | null
      if (m?.__sq === 1 && m.op === 'preview-scroll' && typeof m.y === 'number') scrollYRef.current = m.y
    }
    window.addEventListener('message', onMsg)
    return () => window.removeEventListener('message', onMsg)
  }, [])
  useEffect(() => {
    let cancelled = false
    api.readFile(path)
      .then((d) => {
        if (cancelled) return
        const raw = d.content ?? ''
        if (/^\s*<(!doctype|html)/i.test(raw)) setContent({ kind: 'html', text: rewriteRelativeAssetURLs(raw, path) })
        else if (/\.(md|markdown)$/i.test(path)) setContent({ kind: 'md', text: raw })
        else setContent(null)
      })
      .catch(() => { if (!cancelled) setContent(null) })
    return () => { cancelled = true }
  }, [path, refreshKey])
  const previewSrcDoc = useMemo(
    () => content?.kind === 'html' ? withPreviewPositionScript(content.text, scrollYRef.current) : '',
    [content],
  )
  if (!content) {
    return (
      <div className="fl-file-item-row">
        <FileGlyph name={name} size={15} />
        <span>{labelFromFilename(name).label}</span>
      </div>
    )
  }
  return (
    <div className={`fl-item-preview${large ? ' is-large' : ''}`}>
      {content.kind === 'html' ? (
        <iframe ref={frameRef} className="fl-item-preview-frame" title="" sandbox="allow-scripts" srcDoc={previewSrcDoc} />
      ) : (
        <div className="fl-item-preview-md"><Markdown text={content.text} /></div>
      )}
    </div>
  )
}

// workspaceRelativePath converts a link the agent wrote pointing at a
// workspace file — usually a full filesystem path like
// "/Users/x/.sunlit-learning/workspace/Math/Fractions/foo.md", occasionally
// already-relative like "Math/Fractions/foo.md" — into the workspace-relative
// form the viewer (/api/workspace/file?path=...) expects. Returns null for
// anything that isn't a workspace file link (http(s) links, mailto, anchors,
// absolute web paths), so those keep their normal browser behavior. Subject
// names are arbitrary (not a fixed set of roots like the old shared/parent/
// child split), so anything schemeless and non-absolute is treated as a
// workspace-relative path rather than matching a fixed prefix list.
function workspaceRelativePath(href: string): string | null {
  const marker = '/workspace/'
  const i = href.lastIndexOf(marker)
  if (i !== -1) return href.slice(i + marker.length)
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('#') || href.startsWith('/')) return null
  return href
}

// ChatLink intercepts clicks on links the agent wrote pointing at a
// workspace file so they open in the right-side viewer in-app, instead of
// performing a real browser navigation to a raw filesystem path (which the
// dev/packaged server can't serve, and which was breaking "open this file"
// requests). Anything else (real http(s) links) behaves normally.
function ChatLink({ href, children }: { href?: string; children?: React.ReactNode }) {
  const setDrawerTab = useSparkQuillWorkspaceStore((s) => s.setDrawerTab)
  const setViewerPath = useSparkQuillWorkspaceStore((s) => s.setViewerPath)
  const setViewerImageList = useSparkQuillWorkspaceStore((s) => s.setViewerImageList)
  const setViewerRefreshKey = useSparkQuillWorkspaceStore((s) => s.setViewerRefreshKey)
  const rel = href ? workspaceRelativePath(href) : null
  if (rel) {
    return (
      <a
        href={href}
        onClick={(e) => {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return // let cmd/ctrl/middle-click etc. behave normally
          e.preventDefault()
          setDrawerTab('files')
          setViewerImageList([])
          setViewerPath(rel)
          setViewerRefreshKey((k) => k + 1)
        }}
      >
        {children}
      </a>
    )
  }
  return <a href={href} target="_blank" rel="noreferrer">{children}</a>
}

// SparkQuill adds workspace-link behavior, while rendering itself is shared
// with other chat products such as Video Studio.
function Markdown({ text }: { text: string }) {
  return <SharedChatMarkdown text={text} theme={readTheme() === 'dark' ? 'dark' : 'light'} linkComponent={ChatLink} />
}



// formatBytes renders a byte count the way a person reads it. Sizes here are
// for keeping an eye on how the workspace grows, so one decimal past KB is
// plenty of precision.
function formatBytes(bytes?: number): string {
  if (!bytes || bytes < 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let i = 0
  while (value >= 1024 && i < units.length - 1) { value /= 1024; i++ }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[i]}`
}

// FileTree renders the workspace as an expandable tree (AgentWorks-style). Files
// are clickable to open in the viewer; .meta.json is hidden as noise. Each entry
// carries its size on disk — a folder's is the recursive total — so it's visible
// at a glance which part of the workspace is actually growing.
function FileTree({ nodes, onOpen, depth = 0, expanded, onToggle }: {
  nodes: TreeNode[]
  onOpen: (path: string) => void
  depth?: number
  expanded: Record<string, boolean>
  onToggle: (path: string, open: boolean) => void
}) {
  const visible = nodes.filter((n) => !n.name.startsWith('.') && !n.name.endsWith('.meta.json'))
  if (visible.length === 0) return null
  return (
    <ul className="fl-tree">
      {visible.map((n) => (
        <li key={n.path}>
          {n.type === 'dir' ? (
            <details
              open={expanded[n.path] ?? depth < 1}
              onToggle={(e) => onToggle(n.path, e.currentTarget.open)}
            >
              <summary className="fl-tree-dir">
                <Folder className="fl-tree-icon is-closed" size={15} />
                <FolderOpen className="fl-tree-icon is-open" size={15} />
                <span>{n.name}</span>
                <span className="fl-tree-size">{formatBytes(n.size)}</span>
              </summary>
              {n.children && <FileTree nodes={n.children} onOpen={onOpen} depth={depth + 1} expanded={expanded} onToggle={onToggle} />}
            </details>
          ) : (
            <button className="fl-tree-file" type="button" onClick={() => onOpen(n.path)}>
              <FileGlyph name={n.name} size={14} />
              <span>{n.name}</span>
              <span className="fl-tree-size">{formatBytes(n.size)}</span>
            </button>
          )}
        </li>
      ))}
    </ul>
  )
}
const IMAGE_PATH_RE = /\.(png|jpe?g|gif|webp|svg|bmp)$/i

// isPrintable — the viewer shows a print button for documents worth printing:
// HTML pages (tests, study material, reports) and Markdown (which some tests /
// package items are). Images/PDFs use the browser's own controls; other files
// aren't previewed.
function isPrintable(path: string): boolean {
  return /\.(html?|md|markdown)$/i.test(path)
}

// formatJSONText pretty-prints a .json/.jsonl file's raw text for the viewer;
// falls back to the raw text unchanged if it doesn't parse (e.g. JSONL).
function formatJSONText(content: string): string {
  try {
    return JSON.stringify(JSON.parse(content), null, 2)
  } catch {
    return content
  }
}

// printFile prints a viewer file. HTML opens in a new tab that auto-prints
// (?print=1 on the raw endpoint) — robust: it doesn't depend on the generated
// HTML embedding a print handler (a skill can forget to) and isn't blocked by
// the viewer iframe's sandbox. Markdown/text is rendered in-page as React, so it
// falls back to printViewerContent (CSS-isolated window.print).
function printFile(path: string) {
  if (/\.(html?)$/i.test(path)) {
    window.open(api.rawUrl(path, { print: true }), '_blank', 'noopener,noreferrer')
  } else {
    printViewerContent()
  }
}

// printViewerContent prints the open viewer's in-page rendered content (Markdown
// / plain text) by flagging the document and letting an @media print rule
// isolate .fl-viewer-md / .fl-viewer-pre before printing.
function printViewerContent() {
  const root = document.documentElement
  root.classList.add('fl-printing')
  const cleanup = () => { root.classList.remove('fl-printing'); window.removeEventListener('afterprint', cleanup) }
  window.addEventListener('afterprint', cleanup)
  window.print()
}

// FileGlyph renders a file-type icon coloured by extension, so the workspace
// shows a PDF/Word/PowerPoint/Excel/image/archive at a glance rather than one
// generic sheet-of-paper for everything.
function fileGlyphFor(name: string): { Icon: typeof FileText; kind: string } {
  const ext = (name.split('.').pop() || '').toLowerCase()
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'heic'].includes(ext)) return { Icon: ImageIcon, kind: 'image' }
  if (ext === 'pdf') return { Icon: FileType, kind: 'pdf' }
  if (['doc', 'docx', 'rtf', 'odt'].includes(ext)) return { Icon: FileText, kind: 'doc' }
  if (['ppt', 'pptx', 'odp'].includes(ext)) return { Icon: Presentation, kind: 'ppt' }
  if (['xls', 'xlsx', 'csv', 'ods', 'tsv'].includes(ext)) return { Icon: FileSpreadsheet, kind: 'sheet' }
  if (['zip', 'tar', 'gz', 'tgz', 'rar', '7z'].includes(ext)) return { Icon: FileArchive, kind: 'zip' }
  if (['html', 'htm'].includes(ext)) return { Icon: FileCode, kind: 'html' }
  if (['mp4', 'mov', 'avi', 'mkv', 'webm'].includes(ext)) return { Icon: Film, kind: 'video' }
  if (['mp3', 'wav', 'm4a', 'aac', 'ogg', 'flac'].includes(ext)) return { Icon: Music, kind: 'audio' }
  return { Icon: FileText, kind: 'file' }
}
function FileGlyph({ name, size = 16 }: { name: string; size?: number }) {
  const { Icon, kind } = fileGlyphFor(name)
  return <Icon size={size} className={`fl-glyph fl-glyph-${kind}`} />
}

// FileMetaPanel renders the sidecar metadata (<path>.meta.json) the process-file
// skill writes for a filed document — what Quill understood the file to be. Only
// the parent-meaningful fields are shown, in plain language (no raw JSON, no
// paths), consistent with the rest of the parent UI.
function FileMetaPanel({ meta }: { meta: Record<string, unknown> }) {
  const str = (k: string): string => (typeof meta[k] === 'string' ? (meta[k] as string).trim() : '')
  const concepts = Array.isArray(meta.key_concepts)
    ? (meta.key_concepts as unknown[]).filter((c): c is string => typeof c === 'string' && c.trim() !== '')
    : []
  const summary = str('summary')
  const subject = str('subject')
  const topic = str('topic')
  const type = str('type')
  const extractedText = str('extracted_text')
  const chips = [subject, topic, type].filter(Boolean)
  if (!summary && chips.length === 0 && concepts.length === 0 && !extractedText) return null
  return (
    <div className="fl-meta-panel">
      <p className="fl-meta-title"><Info size={13} /> What Quill knows about this file</p>
      {chips.length > 0 && (
        <div className="fl-meta-chips">
          {chips.map((c, i) => <span key={i} className="fl-meta-chip">{c}</span>)}
        </div>
      )}
      {summary && <p className="fl-meta-summary">{summary}</p>}
      {concepts.length > 0 && (
        <div className="fl-meta-concepts">
          {concepts.map((c, i) => <span key={i} className="fl-meta-concept">{c}</span>)}
        </div>
      )}
      {extractedText && (
        <details className="fl-meta-extracted">
          <summary>See exactly what Quill read from this file</summary>
          <pre className="fl-meta-extracted-text">{extractedText}</pre>
        </details>
      )}
    </div>
  )
}

// NonPreviewableFile is shown for files the browser can't display inline (Word,
// PowerPoint, spreadsheets, archives, …). We deliberately don't try to convert
// them — just show what Quill knows about the file (its metadata, if any) and a
// Download button to open it on the device. Keeps preview simple: images, PDFs,
// and HTML/text render inline; everything else is metadata + download.
function NonPreviewableFile({ path, meta }: { path: string; meta: Record<string, unknown> | null }) {
  const name = path.split('/').pop() || path
  return (
    <div className="fl-nopreview">
      <div className="fl-nopreview-head">
        <FileGlyph name={name} size={34} />
        <span className="fl-nopreview-name">{name}</span>
      </div>
      {meta ? (
        <FileMetaPanel meta={meta} />
      ) : (
        <p className="fl-note">This kind of file can’t be shown here — download it to open on your device.</p>
      )}
      <a
        className="fl-download-btn"
        href={api.rawUrl(path, { download: true })}
        download={name}
      >
        Download
      </a>
    </div>
  )
}

// labelFromFilename turns a bare filename like
// "2026-07-21-fractions-revision-worksheet.md" into a date + human label.
// Filenames are sometimes auto-generated noise (WhatsApp Image ..., s02.png),
// so the label prefers the date-stripped name, falling back to
// "Photo"/"File" for image uploads whose name carries no information at all.
function labelFromFilename(filename: string): { date?: string; label: string } {
  const nameNoExt = filename.replace(/\.[a-z0-9]+$/i, '')
  const dateMatch = nameNoExt.match(/^(\d{4}-\d{2}-\d{2})[-_](.+)$/)
  const date = dateMatch ? dateMatch[1] : undefined
  let rawLabel = (dateMatch ? dateMatch[2] : nameNoExt).replace(/[-_]+/g, ' ').trim()
  if (!rawLabel || /^(whatsapp image|img\d*|s\d+|image\d*|photo\d*)\b/i.test(rawLabel)) {
    rawLabel = IMAGE_PATH_RE.test(filename) ? 'Photo' : 'File'
  }
  return { date, label: rawLabel }
}

// parseMaterialPath reads a materials/<subject>/<topic>/<file> path (the only
// remaining path shape the UI needs to reverse-derive subject/topic from —
// generated content now lives in Activity objects, which already carry their
// own subject/topic/items instead of encoding them in a path).
function parseMaterialPath(p: string): { subject?: string; topic?: string; date?: string; label: string } {
  const parts = p.split('/')
  const rest = parts.slice(1) // drop "materials"
  const filename = rest[rest.length - 1] || p
  const subject = rest.length >= 1 ? rest[0] : undefined
  const topic = rest.length >= 3 ? rest[1] : undefined
  return { subject, topic, ...labelFromFilename(filename) }
}

export default function LearningApp() {
  // "New chat" for the parent conversation lives in the composer (ChatInput
  // offers it when the profile declares runtime.capabilities.new_conversation)
  // and announces it; the chat is remounted (key) after the server rotates
  // the conversation.
  const newChatBusyRef = useRef(false)
  const [parentChatEpoch, setParentChatEpoch] = useState(0)
  useEffect(() => {
    const onNewChat = (e: Event) => {
      const detail = (e as CustomEvent<{ profileId?: string; engine?: string }>).detail
      if (detail?.profileId !== PARENT_PROFILE_ID || newChatBusyRef.current) return
      newChatBusyRef.current = true
      // The engine is chosen once, here, for the fresh conversation; no
      // model yet, so it starts on the parent's own product.yaml default for
      // it. selectEngine drops any leftover parent_model from the old
      // engine along with it (this event only ever fires for the parent —
      // the guard above).
      const engineChoice = detail.engine ? api.selectEngine('parent', detail.engine).catch(() => undefined) : Promise.resolve()
      engineChoice
        .then(() => startNewParentConversation())
        .catch(() => undefined)
        .finally(() => { if (detail.engine) applyFamilyEngineToOpenTabs('parent', detail.engine); setParentChatEpoch((n) => n + 1); newChatBusyRef.current = false })
    }
    window.addEventListener('agentworks:product-new-conversation', onNewChat)
    return () => window.removeEventListener('agentworks:product-new-conversation', onNewChat)
  }, [])
  // Activities the parent pinned to the top of the Activities tab. Stored in
  // the workspace (state/pinned-activities.json) so it follows the family,
  // not the browser; pinned cards show in their own section above the groups.
  const [pinnedActivityDirs, setPinnedActivityDirs] = useState<string[]>([])
  useEffect(() => {
    let cancelled = false
    api.loadState('pinned-activities')
      .then((d) => { const dirs = (d as { dirs?: unknown } | null)?.dirs; if (!cancelled && Array.isArray(dirs)) setPinnedActivityDirs(dirs.filter((x): x is string => typeof x === 'string')) })
      .catch(() => undefined)
    return () => { cancelled = true }
  }, [])
  const toggleActivityPin = (dir: string) => {
    setPinnedActivityDirs((cur) => {
      const next = cur.includes(dir) ? cur.filter((d) => d !== dir) : [dir, ...cur]
      void api.saveState('pinned-activities', { dirs: next }).catch(() => undefined)
      return next
    })
  }
  // The composer's model switcher (ChatInput, product surfaces) announces a
  // choice; SparkQuill keeps engine as the family's shared setting
  // (family.json) so it holds across relaunches, but the model is saved
  // under whichever role's composer this came from — see FamilyFile's
  // comment on why parent and child must not share one model field.
  useEffect(() => {
    const onEngine = (e: Event) => {
      const detail = (e as CustomEvent<{ profileId?: string; engine?: string; modelId?: string }>).detail
      if (!detail?.engine || (detail.profileId !== PARENT_PROFILE_ID && detail.profileId !== 'sparkquill-child')) return
      const { engine, modelId } = detail
      const role = detail.profileId === PARENT_PROFILE_ID ? 'parent' : 'child'
      api.selectEngine(role, engine, modelId).catch(() => undefined).finally(() => applyFamilyEngineToOpenTabs(role, engine, modelId))
    }
    window.addEventListener('agentworks:product-engine-selected', onEngine)
    return () => window.removeEventListener('agentworks:product-engine-selected', onEngine)
  }, [])
  // Messages Quill sent the parent (notify_user: a check-in's summary, a
  // heads-up). They stay on screen until the parent dismisses them; the
  // dismissals are remembered per event id so a relaunch does not bring a
  // message back. A message that arrives while the app is open also goes to
  // the desktop as a native notification (the shell's preload `notify`).
  const NOTICE_DISMISSED_KEY = 'sparkquill.dismissed-notices'
  const [notices, setNotices] = useState<ProductNotification[]>([])
  const [dismissedNotices, setDismissedNotices] = useState<string[]>(() => {
    try { const raw = localStorage.getItem(NOTICE_DISMISSED_KEY); const arr = raw ? JSON.parse(raw) : []; return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string') : [] } catch { return [] }
  })
  const seenNoticesRef = useRef<Set<string> | null>(null)
  const onPlatformNotifications = useCallback((all: ProductNotification[]) => {
    setNotices(all)
    if (seenNoticesRef.current === null) {
      // First batch is history (hydrated with the conversation), not news.
      seenNoticesRef.current = new Set(all.map((n) => n.id))
      return
    }
    const seen = seenNoticesRef.current
    for (const n of all) {
      if (seen.has(n.id)) continue
      seen.add(n.id)
      const shell = (window as Window & { sparkquill?: { notify?: (title: string, body: string) => void } }).sparkquill
      shell?.notify?.(n.title || 'Quill', n.message)
    }
  }, [])
  const dismissNotice = (id: string) => {
    setDismissedNotices((cur) => {
      const next = cur.includes(id) ? cur : [...cur, id].slice(-200)
      try { localStorage.setItem(NOTICE_DISMISSED_KEY, JSON.stringify(next)) } catch { /* best-effort */ }
      return next
    })
  }
  const visibleNotices = notices.filter((n) => !dismissedNotices.includes(n.id))
  const [theme] = useState<Theme>(readTheme)
  // The main frontend forces the document to dark (ThemeProvider). While this
  // surface is mounted the family's choice wins, and the shared chat inside it
  // follows the same root class; what ThemeProvider set comes back on unmount.
  useEffect(() => {
    const root = document.documentElement
    const previous = { className: root.className, dataTheme: root.dataset.theme, colorScheme: root.style.colorScheme }
    const docTheme = theme === 'dark' ? 'dark' : 'light'
    root.classList.remove('light', 'dark')
    root.classList.add(docTheme)
    root.dataset.theme = docTheme
    root.style.colorScheme = docTheme
    return () => {
      root.className = previous.className
      if (previous.dataTheme === undefined) delete root.dataset.theme
      else root.dataset.theme = previous.dataTheme
      root.style.colorScheme = previous.colorScheme
    }
  }, [theme])

  const screen = useSetupStore((s) => s.screen)
  const setScreen = useSetupStore((s) => s.setScreen)
  const engines = useSetupStore((s) => s.engines)
  const setEngines = useSetupStore((s) => s.setEngines)
  const enginesState = useSetupStore((s) => s.enginesState)
  const setEnginesState = useSetupStore((s) => s.setEnginesState)
  const engine = useSetupStore((s) => s.engine)
  const setEngine = useSetupStore((s) => s.setEngine)
  const setTestMessage = useSetupStore((s) => s.setTestMessage)

  // The composer's quick menus come from the product (product.yaml
  // `commands:`); the standalone backend serves its own fixed list.
  const [quickCommands, setQuickCommands] = useState<{ parent: QuickCommand[]; child: QuickCommand[] }>({ parent: [], child: [] })
  useEffect(() => {
    let alive = true
    api.commands().then((c) => { if (alive) setQuickCommands(c) }).catch(() => {})
    return () => { alive = false }
  }, [])
  const refreshEngines = useCallback(() => {
    let cancelled = false
    setEnginesState('loading')
    api.engines()
      .then((data) => {
        if (cancelled) return
        const sorted = [...data].sort((a, b) => pres(a.id, a.name).order - pres(b.id, b.name).order)
        setEngines(sorted)
        // Only supply a DEFAULT for a genuinely fresh install — never override an
        // already-known choice. This used to fire unconditionally, racing the
        // /api/setup effect below (which loads the family's real saved engine):
        // /api/setup is a fast state-file read, while this call does live runtime
        // detection across four CLIs and reliably resolves second, so it was
        // silently clobbering the real selection back to whichever engine happens
        // to rank first in ENGINE_PRESENTATION's hardcoded order — confirmed live,
        // every actual conversation kept running on the real saved engine (its own
        // session is pinned to it independent of this picker) while Settings showed
        // a completely different one as "active". Read the CURRENT store value at
        // call time, not a value captured when this effect was created, since
        // /api/setup may resolve either before or after this one.
        if (!useSetupStore.getState().engine) {
          const firstReady = sorted.find((item) => item.usable) ?? sorted[0]
          if (firstReady) setEngine(firstReady.id)
        }
        setEnginesState('ready')
      })
      .catch(() => { if (!cancelled) setEnginesState('error') })
    return () => { cancelled = true }
  }, [setEngine, setEngines, setEnginesState])
  useEffect(() => refreshEngines(), [refreshEngines])

  const childName = useFamilyStore((s) => s.childName)
  const setChildName = useFamilyStore((s) => s.setChildName)
  const grade = useFamilyStore((s) => s.grade)
  const setGrade = useFamilyStore((s) => s.setGrade)
  const board = useFamilyStore((s) => s.board)
  const setBoard = useFamilyStore((s) => s.setBoard)
  // Before actually switching into Child Mode, ask the parent whether to
  // continue Myra's existing conversation or start a brand-new one — handing
  // off an activity often means "just carry on the same chat", not a fresh
  // start, so this is the parent's call rather than a silent guess.
  const [pendingChildEntry, setPendingChildEntry] = useState<{ dir: string; greetingText: string } | null>(null)
  // Bumped by every performHandoff call, captured by its own response. If a
  // second "Give to child" fires (a different activity, clicked before the
  // first request finished) before this one's response lands, its generation
  // no longer matches — the response is discarded instead of kicking off a
  // chat for an activity the parent already navigated away from. Without
  // this, whichever request happened to resolve LAST won regardless of which
  // was actually clicked last, since fetch responses aren't guaranteed to
  // arrive in request order.
  const handoffGenerationRef = useRef(0)
  // The workspace's TRUE size on disk, from /api/workspace/tree — including
  // what the listing hides (see workspaceTreeResponse), so the number in the
  // Files tab is the one worth watching for growth.
  const [treeTotalSize, setTreeTotalSize] = useState(0)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const openProviders = () => useLLMStore.getState().setShowLLMModal(true)
  const closeProviders = () => { useLLMStore.getState().setShowLLMModal(false); refreshEngines() }
  // Voice settings — the tier catalog is computed server-side against THIS
  // machine's hardware (see /api/voice/status), so the UI never has to guess
  // what an Intel vs Apple Silicon Mac can actually run.
  const [voiceStatus, setVoiceStatus] = useState<VoiceStatus | null>(null)
  // Off by default; a parent opts in here because Quill can take anywhere
  // from a few seconds to several minutes to reply, and a child who's
  // wandered off in the meantime has no other way to know a reply landed.
  // Per-device (localStorage), not a family policy.
  const [childReminderSound, setChildReminderSound] = useState(() => readReminderSoundPref())
  const toggleChildReminderSound = (on: boolean) => {
    setChildReminderSound(on)
    persistReminderSoundPref(on)
  }
  // Off by default; a parent opts in here. On, finishing a voice recording
  // (in the child's chat) sends it right away instead of leaving it in the
  // composer to review and send by hand.
  const [childVoiceAutoSend, setChildVoiceAutoSend] = useState(() => readVoiceAutoSendPref())
  const toggleChildVoiceAutoSend = (on: boolean) => {
    setChildVoiceAutoSend(on)
    persistVoiceAutoSendPref(on)
  }
  const [goalPopoverOpen, setGoalPopoverOpen] = useState(false)
  // Secrets (credentials the parent saves for Quill's tools, e.g. a school
  // portal login) — settings-form only, never through chat, so a value typed
  // here never touches the model or the persisted conversation transcript.
  const [secretNames, setSecretNames] = useState<string[]>([])
  const [secretNameDraft, setSecretNameDraft] = useState('')
  const [secretValueDraft, setSecretValueDraft] = useState('')
  const [savingSecret, setSavingSecret] = useState(false)
  const [deletingSecret, setDeletingSecret] = useState<string | null>(null)
  const waOpen = useWhatsAppStore((s) => s.waOpen)
  const setWaOpen = useWhatsAppStore((s) => s.setWaOpen)
  const [connectorSection, setConnectorSection] = useState<'whatsapp' | 'browser'>('whatsapp')
  // WhatsApp pairing is offered only when the parent profile declares the
  // whatsapp capability in its product.yaml (the platform's connector then
  // routes the parent's own chat to Quill); without it the Connectors panel
  // is just the browser.
  const [waEnabled, setWaEnabled] = useState(false)
  useEffect(() => {
    let cancelled = false
    loadAgentProfileCapabilityEnabled(PARENT_PROFILE_ID, 'whatsapp').then((enabled) => {
      if (cancelled) return
      setWaEnabled(enabled)
      if (!enabled) setConnectorSection('browser')
    })
    return () => { cancelled = true }
  }, [])
  // One WhatsApp per account: accounts holds the linked phone (at most one);
  // pairing is its QR while none is linked.
  const [waStatus, setWaStatus] = useState<{ accounts: { jid: string; connected: boolean }[]; pairing: { qr_available: boolean; qr_expires_at?: string }; voice_transcription?: { enabled: boolean; installed: boolean; installing: boolean; model_size_mb: number; available: boolean; error?: string } } | null>(null)
  const [voiceToggling, setVoiceToggling] = useState(false)
  const [waQrNonce, setWaQrNonce] = useState(0)
  const [unpairingJid, setUnpairingJid] = useState<string | null>(null)
  const [browserStatus, setBrowserStatus] = useState<{ cli_installed: boolean } | null>(null)
  const [browserCopied, setBrowserCopied] = useState(false)
  const [pulseConfig, setPulseConfig] = useState<{ enabled: boolean; cadence_hours: number; last_run_at?: string; last_session_id?: string; watch_sites?: string[]; preferred_hour: number; preferred_hour_set: boolean } | null>(null)
  const [savingPulse, setSavingPulse] = useState(false)
  const [watchSitesDraft, setWatchSitesDraft] = useState('')
  const [pulseSaved, setPulseSaved] = useState(false)
  const [pulsePopoverOpen, setPulsePopoverOpen] = useState(false)
  const [pulseRunning, setPulseRunning] = useState(false)
  const [pulseRunError, setPulseRunError] = useState<string | null>(null)
  const [pulseHistoryOpen, setPulseHistoryOpen] = useState(false)
  // Read-only view of one activity's conversation with the tutor, opened
  // from the rail. Only for a genuinely new activity, whose session lives in
  // chat_history. One migrated from the old standalone app has a
  // legacy-conversation.json instead — its product.json session_id is one
  // the migration invented, with nothing in chat_history behind it, so it
  // stays a static (non-clickable) row rather than opening to "0 events".
  const [viewingActivityChat, setViewingActivityChat] = useState<{ sessionId: string; title: string; dir: string } | null>(null)
  const [clearingPulseHistory, setClearingPulseHistory] = useState(false)
  const [clearPulseHistoryError, setClearPulseHistoryError] = useState<string | null>(null)
  const clearPulseHistory = () => {
    if (!window.confirm('Clear the check-in history? This starts the next check-in with no memory of past ones.')) return
    setClearingPulseHistory(true)
    setClearPulseHistoryError(null)
    api.resetCheckinHistory()
      .then(() => { setPulseConfig((c) => (c ? { ...c, last_session_id: undefined } : c)); setPulseHistoryOpen(false) })
      .catch((err) => setClearPulseHistoryError(err instanceof Error ? err.message : 'Could not clear the history.'))
      .finally(() => setClearingPulseHistory(false))
  }
  // "Chats" toolbar popover: every activity (each is one child conversation),
  // newest first, with per-item delete and a bulk "older than N days" sweep —
  // the missing cleanup mechanism for old activities. Independent of which
  // drawer tab is open (the Activities tab's own fetch is gated on that).
  // The chats rail on the left, like a chat app's sidebar: the parent's
  // earlier chats with Quill (open one, delete one, start a new one) and the
  // child's activity chats with their cleanup. Its open state is a per-device
  // convenience remembered in the browser.
  const [chatsRailOpen, setChatsRailOpen] = useState<boolean>(() => {
    try { return window.localStorage.getItem('sparkquill.chats-rail') === 'open' } catch { return false }
  })
  const toggleChatsRail = () => {
    setChatsRailOpen((open) => {
      try { window.localStorage.setItem('sparkquill.chats-rail', open ? 'closed' : 'open') } catch { /* private mode */ }
      return !open
    })
  }
  const [parentChats, setParentChats] = useState<ParentChat[]>([])
  const [switchingChat, setSwitchingChat] = useState(false)
  const [parentChatsError, setParentChatsError] = useState<string | null>(null)
  const [loadingChats, setLoadingChats] = useState(false)
  const [deleteOlderDays, setDeleteOlderDays] = useState('30')
  const [deletingChatDirs, setDeletingChatDirs] = useState<string[]>([])
  const [deleteChatsError, setDeleteChatsError] = useState<string | null>(null)
  const wsFiles = useSparkQuillWorkspaceStore((s) => s.wsFiles)
  const setWsFiles = useSparkQuillWorkspaceStore((s) => s.setWsFiles)
  const allFiles = useSparkQuillWorkspaceStore((s) => s.allFiles)
  const setAllFiles = useSparkQuillWorkspaceStore((s) => s.setAllFiles)
  // Which activity's conversation is currently loaded into childMessages.
  // Keyed by dir rather than a plain "have we resumed once" flag: the bound
  // activity CHANGES underneath the child whenever the parent runs
  // open_activity, and a once-ever guard left the previous activity's chat on
  // screen under the new activity's name.
  const loadedActivityDirRef = useRef<string | null>(null)
  const parentLabel = useFamilyStore((s) => s.parentLabel)
  const setParentLabel = useFamilyStore((s) => s.setParentLabel)

  const wsRefreshKey = useSparkQuillWorkspaceStore((s) => s.wsRefreshKey)
  const setWsRefreshKey = useSparkQuillWorkspaceStore((s) => s.setWsRefreshKey)
  const treeNodes = useSparkQuillWorkspaceStore((s) => s.treeNodes)
  const setTreeNodes = useSparkQuillWorkspaceStore((s) => s.setTreeNodes)
  // Reflect the workspace file system in the drawer (materials the agent can
  // read). Refetches when entering the chat and after each upload/tool event.
  // The child's own conversation resume lives in a separate effect below,
  // keyed off /api/child/activity instead of scanning the tree — there is no
  // longer a single flat child/conversations/ folder to walk.
  useEffect(() => {
    if (screen !== 'parent' && screen !== 'tutor') return
    let cancelled = false
    api.tree()
      .then((data) => {
        if (cancelled) return
        // Accepts both the current {nodes,total_size} object and the older bare
        // array, so a packaged frontend built before the size fields still works
        // against a newer server (and vice versa).
        const nodes = Array.isArray(data) ? data : (data?.nodes ?? [])
        setTreeTotalSize(Array.isArray(data) ? 0 : (data?.total_size ?? 0))
        const files: { path: string; name: string }[] = []
        const walk = (ns: TreeNode[]) => ns?.forEach((n) => {
          if (n.type === 'file') files.push({ path: n.path, name: n.name })
          if (n.children) walk(n.children)
        })
        walk(nodes)
        setTreeNodes(nodes)
        const mats: WsFile[] = files
          .filter((f) => f.path.includes('/materials/') && !f.name.endsWith('.meta.json'))
          .map((f) => {
            const parts = f.path.split('/')
            const mi = parts.indexOf('materials')
            return { path: f.path, name: f.name, scope: parts[0] || '', subject: parts[mi + 1] || '', topic: parts[mi + 2] || '' }
          })
        setWsFiles(mats)
        setAllFiles(files.map((f) => f.path))
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [screen, setAllFiles, setTreeNodes, setWsFiles, wsRefreshKey])
  const drawerOpen = true // right side always open
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const childIframeRef = useRef<HTMLIFrameElement>(null)

  // Child-mode split: how much room her worksheet gets versus the chat. Two ways
  // to set it, both writing the same persisted preference: dragging the divider
  // between the panes (fine-grained, and its hit area is deliberately much wider
  // than the hairline it draws), or the widen/restore button in the worksheet
  // toolbar for the coarse "normal ⇄ as big as it goes" jump.
  const [childSideWidthPref, setChildSideWidth] = useState(readChildSideWidth)
  // Track the window width so the stored preference can be re-clamped when the
  // window shrinks. Without this, widening on a large display and then resizing
  // smaller would leave the chat below its minimum (or push it off-screen), since
  // the preference is an absolute pixel value.
  const [windowWidth, setWindowWidth] = useState(() => window.innerWidth)
  useEffect(() => {
    const onResize = () => setWindowWidth(window.innerWidth)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  // The preference is what the child chose; this is what the layout can honour.
  const childSideWidth = Math.min(Math.max(childSideWidthPref, CHILD_SIDE_MIN), childSideMax(windowWidth))
  // Set-and-save in one place, clamped to what the layout allows, so the drag
  // handle, its arrow keys and the toolbar button can't drift apart.
  const commitChildSideWidth = (px: number) => {
    const next = Math.min(Math.max(px, CHILD_SIDE_MIN), childSideMax(windowWidth))
    setChildSideWidth(next)
    persistChildSideWidth(next)
  }
  const childBodyRef = useRef<HTMLDivElement>(null)
  const [childResizing, setChildResizing] = useState(false)
  // Reading size for the worksheet, cycled by the toolbar's "Aa" button and
  // remembered — a child who needs bigger text needs it every session, not
  // once.
  const [childZoom, setChildZoom] = useState(readChildZoom)
  const cycleChildZoom = () => {
    const i = CHILD_ZOOM_STEPS.indexOf(childZoom)
    const next = CHILD_ZOOM_STEPS[(i + 1) % CHILD_ZOOM_STEPS.length]
    setChildZoom(next)
    persistChildZoom(next)
  }
  const [childChatZoom, setChildChatZoom] = useState(readChildChatZoom)
  const cycleChildChatZoom = () => {
    const i = CHILD_ZOOM_STEPS.indexOf(childChatZoom)
    const next = CHILD_ZOOM_STEPS[(i + 1) % CHILD_ZOOM_STEPS.length]
    setChildChatZoom(next)
    persistChildChatZoom(next)
  }
  // Pointer-events (not mouse) so the same handler covers trackpad, mouse and
  // touch. Width is measured from the body's RIGHT edge rather than by
  // accumulating deltas, so the panel edge tracks the finger exactly and can't
  // drift after a clamp at either end.
  const startChildResize = (e: React.PointerEvent<HTMLDivElement>) => {
    const body = childBodyRef.current
    if (!body) return
    e.preventDefault()
    ;(e.currentTarget as HTMLDivElement).focus({ preventScroll: true })
    const right = body.getBoundingClientRect().right
    const max = childSideMax(windowWidth)
    let last = childSideWidth
    setChildResizing(true)
    const move = (ev: PointerEvent) => {
      last = Math.min(Math.max(right - ev.clientX, CHILD_SIDE_MIN), max)
      setChildSideWidth(last)
    }
    const end = () => {
      setChildResizing(false)
      // Persist once at the end, not on every move — this writes localStorage.
      persistChildSideWidth(last)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', end)
      window.removeEventListener('pointercancel', end)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
  }
  // Parent Mode's own resizer — same pointer-tracking approach as the
  // child's, clamped to parentSideMax's 50%-of-window ceiling instead.
  const [parentSideWidthPref, setParentSideWidth] = useState(readParentSideWidth)
  const parentSideWidth = Math.min(Math.max(parentSideWidthPref, PARENT_SIDE_MIN), parentSideMax(windowWidth))
  const commitParentSideWidth = (px: number) => {
    const next = Math.min(Math.max(px, PARENT_SIDE_MIN), parentSideMax(windowWidth))
    setParentSideWidth(next)
    persistParentSideWidth(next)
  }
  const parentBodyRef = useRef<HTMLDivElement>(null)
  const [parentResizing, setParentResizing] = useState(false)
  const startParentResize = (e: React.PointerEvent<HTMLDivElement>) => {
    const body = parentBodyRef.current
    if (!body) return
    e.preventDefault()
    ;(e.currentTarget as HTMLDivElement).focus({ preventScroll: true })
    const right = body.getBoundingClientRect().right
    const max = parentSideMax(windowWidth)
    let last = parentSideWidth
    setParentResizing(true)
    const move = (ev: PointerEvent) => {
      last = Math.min(Math.max(right - ev.clientX, PARENT_SIDE_MIN), max)
      setParentSideWidth(last)
    }
    const end = () => {
      setParentResizing(false)
      persistParentSideWidth(last)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', end)
      window.removeEventListener('pointercancel', end)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
  }
  // "Wide" once past the midpoint between default and max, so the button's icon
  // always shows which way the next tap will move it.
  const childSideWide = childSideWidth > (childSideDefault() + childSideMax(windowWidth)) / 2
  const drawerTab = useSparkQuillWorkspaceStore((s) => s.drawerTab)
  const setDrawerTab = useSparkQuillWorkspaceStore((s) => s.setDrawerTab)
  // The ONE activity the child is currently bound to (/api/child/activity) —
  // the child workspace shows only this, not every activity ever created.
  const childActivity = useChildChatStore((s) => s.childActivity)
  const setChildActivity = useChildChatStore((s) => s.setChildActivity)
  const childViewerPath = useChildChatStore((s) => s.childViewerPath)
  const setChildViewerPath = useChildChatStore((s) => s.setChildViewerPath)
  // Bumped whenever open_file fires, even for the SAME path — Quill re-opens
  // the child's own active/ copy after editing it to add a progress note, and
  // a same-string setChildViewerPath wouldn't otherwise trigger a refetch.
  const [childViewerRefreshKey, setChildViewerRefreshKey] = useState(0)
  // A handoff's opening message for the platform child chat, until it is sent.
  const [childKickoff, setChildKickoff] = useState<ChildKickoff | null>(null)
  const onChildKickoffSent = useCallback((id: number) => setChildKickoff((cur) => (cur?.id === id ? null : cur)), [])
  // Optional element id the tutor asked us to scroll to inside the opened page
  // (open_file's `focus`). Empty = let the viewer pick the first unanswered question.
  const [childViewerFocus, setChildViewerFocus] = useState('')
  const [startBurst, setStartBurst] = useState(false)
  // Last known scroll offset per file, reported out of the sandboxed iframe (see
  // withViewerPositionScript). A ref, not state: it updates on every scroll frame
  // and must never trigger a re-render, which would reload the iframe and destroy
  // the very position being tracked.
  const childViewerPathRef = useRef<string | null>(null)
  const childViewerScrollRef = useRef<Record<string, number>>({})
  useEffect(() => {
    const onMsg = (ev: MessageEvent) => {
      if (ev.source !== childIframeRef.current?.contentWindow) return
      const m = ev.data as { __sq?: number; op?: string; y?: number } | null
      if (!m || m.__sq !== 1 || m.op !== 'viewer-scroll' || typeof m.y !== 'number') return
      const path = childViewerPathRef.current
      if (path) childViewerScrollRef.current[path] = m.y
    }
    window.addEventListener('message', onMsg)
    return () => window.removeEventListener('message', onMsg)
  }, [])
  const childViewerContent = useChildChatStore((s) => s.childViewerContent)
  // Reading childViewerScrollRef.current DURING render (instead of only here,
  // memoized) would re-bake the latest scroll position into srcDoc on every
  // unrelated re-render of this component (e.g. each composer keystroke) —
  // React would then see a changed srcDoc string and reload the iframe,
  // replaying the position-restore highlight/scroll for no reason. Memoizing
  // on the intentional inputs only means the live-updating ref is read once
  // per genuine content/focus/path/zoom change, not once per render.
  const childViewerSrcDoc = useMemo(
    () => withViewerPositionScript(
      withViewerLinkBridge(withDiagramLib(childViewerContent?.content ?? '')),
      childViewerFocus,
      childViewerScrollRef.current[childViewerPath ?? ''] ?? 0,
      childZoom,
    ),

    [childViewerContent, childViewerFocus, childZoom, childViewerPath],
  )
  const setChildViewerContent = useChildChatStore((s) => s.setChildViewerContent)
  const childTreeRefreshKey = useChildChatStore((s) => s.childTreeRefreshKey)
  const setChildTreeRefreshKey = useChildChatStore((s) => s.setChildTreeRefreshKey)
  const filesSubjectFilter = useSparkQuillWorkspaceStore((s) => s.filesSubjectFilter)
  const setFilesSubjectFilter = useSparkQuillWorkspaceStore((s) => s.setFilesSubjectFilter)
  const filesGroupBy = useSparkQuillWorkspaceStore((s) => s.filesGroupBy)
  const setFilesGroupBy = useSparkQuillWorkspaceStore((s) => s.setFilesGroupBy)
  const activities = useSparkQuillWorkspaceStore((s) => s.activities)
  const setActivities = useSparkQuillWorkspaceStore((s) => s.setActivities)
  useEffect(() => {
    if (!chatsRailOpen) return
    let cancelled = false
    setLoadingChats(true)
    api.activities()
      .then((d) => { if (!cancelled) setActivities(d ?? []) })
      .catch(() => undefined)
      .finally(() => { if (!cancelled) setLoadingChats(false) })
    return () => { cancelled = true }
  }, [chatsRailOpen, setActivities])
  // The parent's chats with Quill: refetched when the rail opens and after
  // the live conversation changes (new chat, or an earlier one reopened) —
  // a new chat gets its title from its first message, so also a little
  // after the conversation remounts.
  const loadParentChats = useCallback(() => {
    api.listParentChats()
      .then((chats) => { setParentChats(chats); setParentChatsError(null) })
      .catch((err) => setParentChatsError(err instanceof Error ? err.message : 'Could not load chats.'))
  }, [])
  useEffect(() => {
    if (!chatsRailOpen) return
    loadParentChats()
    const later = window.setTimeout(loadParentChats, 15_000)
    return () => window.clearTimeout(later)
  }, [chatsRailOpen, parentChatEpoch, loadParentChats])
  const openParentChat = (chat: ParentChat) => {
    if (chat.current || switchingChat) return
    setSwitchingChat(true)
    switchParentConversation(chat.session_id)
      .then(() => setParentChatEpoch((n) => n + 1))
      .catch((err) => setParentChatsError(err instanceof Error ? err.message : 'Could not open that chat.'))
      .finally(() => setSwitchingChat(false))
  }
  const deleteParentChat = (chat: ParentChat) => {
    if (chat.current || !window.confirm(`Permanently delete "${chat.title}"? This cannot be undone.`)) return
    api.deleteParentChat(chat.session_id)
      .then(() => setParentChats((cur) => cur.filter((c) => c.session_id !== chat.session_id)))
      .catch((err) => setParentChatsError(err instanceof Error ? err.message : 'Could not delete that chat.'))
  }
  // Engine choice for a fresh chat — the composer's own NewChatControl asked
  // this before it was removed as a duplicate of this button; a new
  // conversation is the one moment the engine can be picked at all (locked
  // for the rest of that chat's life), so the rail's button must still ask.
  const [newChatEngines, setNewChatEngines] = useState<AgentProfileProviderOption[]>([])
  useEffect(() => {
    loadAgentProfileProviderOptions(PARENT_PROFILE_ID).then(setNewChatEngines).catch(() => undefined)
  }, [])
  const [newChatEnginePopoverOpen, setNewChatEnginePopoverOpen] = useState(false)
  const startNewChatFromRail = (engine?: string) => {
    setNewChatEnginePopoverOpen(false)
    window.dispatchEvent(new CustomEvent('agentworks:product-new-conversation', { detail: { profileId: PARENT_PROFILE_ID, engine } }))
  }
  const onNewChatButtonClick = () => {
    if (newChatEngines.length > 1) { setNewChatEnginePopoverOpen((v) => !v); return }
    startNewChatFromRail(newChatEngines[0]?.id)
  }
  const deleteChats = (dirs: string[], confirmMessage: string) => {
    if (dirs.length === 0 || !window.confirm(confirmMessage)) return
    setDeleteChatsError(null)
    setDeletingChatDirs((cur) => [...cur, ...dirs])
    api.deleteActivities(dirs)
      .then(() => setActivities((cur) => cur.filter((a) => !dirs.includes(a.dir))))
      .catch((err) => setDeleteChatsError(err instanceof Error ? err.message : 'Could not delete.'))
      .finally(() => setDeletingChatDirs((cur) => cur.filter((d) => !dirs.includes(d))))
  }
  const deleteOldChats = () => {
    const days = Number(deleteOlderDays)
    if (!Number.isFinite(days) || days <= 0) { setDeleteChatsError('Enter a positive number of days.'); return }
    const cutoff = Date.now() - days * 86_400_000
    const dirs = activities.filter((a) => a.created_at && new Date(a.created_at).getTime() < cutoff).map((a) => a.dir)
    if (dirs.length === 0) { setDeleteChatsError(`No chats older than ${days} days.`); return }
    deleteChats(dirs, `Permanently delete ${dirs.length} chat${dirs.length === 1 ? '' : 's'} older than ${days} days? This cannot be undone.`)
  }
  const viewerPath = useSparkQuillWorkspaceStore((s) => s.viewerPath)
  const setViewerPath = useSparkQuillWorkspaceStore((s) => s.setViewerPath)
  const viewerRefreshKey = useSparkQuillWorkspaceStore((s) => s.viewerRefreshKey)
  const setViewerRefreshKey = useSparkQuillWorkspaceStore((s) => s.setViewerRefreshKey)
  const viewerImageList = useSparkQuillWorkspaceStore((s) => s.viewerImageList)
  const setViewerImageList = useSparkQuillWorkspaceStore((s) => s.setViewerImageList)
  // The dir of an activity opened via open_activity (the whole activity
  // overview). Can be set ALONGSIDE viewerPath (not just instead of it):
  // clicking an item inside the activity view sets viewerPath without
  // clearing this, so viewerPath's own "back" button falls through to the
  // activity view again instead of the raw file list — viewerPath simply
  // takes render priority over viewerActivityDir whenever both are set.
  const [viewerActivityDir, setViewerActivityDir] = useState<string | null>(null)
  const viewerContent = useSparkQuillWorkspaceStore((s) => s.viewerContent)
  const setViewerContent = useSparkQuillWorkspaceStore((s) => s.setViewerContent)
  const viewerScrollRef = useRef<Record<string, number>>({})
  useEffect(() => {
    const onMsg = (ev: MessageEvent) => {
      if (ev.source !== iframeRef.current?.contentWindow) return
      const m = ev.data as { __sq?: number; op?: string; y?: number } | null
      if (m?.__sq === 1 && m.op === 'viewer-scroll' && typeof m.y === 'number' && viewerPath) {
        viewerScrollRef.current[viewerPath] = m.y
      }
    }
    window.addEventListener('message', onMsg)
    return () => window.removeEventListener('message', onMsg)
  }, [viewerPath])
  const viewerSrcDoc = useMemo(
    () => withViewerPositionScript(
      withViewerLinkBridge(withDiagramLib(viewerContent?.content ?? '')),
      undefined,
      viewerScrollRef.current[viewerPath ?? ''] ?? 0,
    ),
    [viewerContent, viewerPath],
  )
  const [viewerMeta, setViewerMeta] = useState<Record<string, unknown> | null>(null)
  const [metaOpen, setMetaOpen] = useState(false)
  // Which activity's goal (the parent's own instructions for that activity)
  // is currently revealed via its (i) button — collapsed by default.
  const [expandedActivity, setExpandedActivity] = useState<string | null>(null)
  // Which folders in the "all files" tree the user has explicitly opened or
  // closed, keyed by path — survives the FileTree unmounting when a file is
  // opened for viewing (drawerTab's ternary swaps to the viewer branch, then
  // back), so opening a file and returning no longer collapses everything
  // back to the default top-level-only view. Absent from this map = use the
  // component's own default (top level open, everything nested closed).
  const [treeExpanded, setTreeExpanded] = useState<Record<string, boolean>>({})
  const mapRefreshKey = useSparkQuillWorkspaceStore((s) => s.mapRefreshKey)
  const setMapRefreshKey = useSparkQuillWorkspaceStore((s) => s.setMapRefreshKey)
  const progressHtml = useSparkQuillWorkspaceStore((s) => s.progressHtml)
  const setProgressHtml = useSparkQuillWorkspaceStore((s) => s.setProgressHtml)
  const booting = useSetupStore((s) => s.booting)
  const setBooting = useSetupStore((s) => s.setBooting)
  const bootError = useSetupStore((s) => s.bootError)
  const setBootError = useSetupStore((s) => s.setBootError)
  const pin = useSetupStore((s) => s.pin)
  const setPin = useSetupStore((s) => s.setPin)
  const pinConfirm = useSetupStore((s) => s.pinConfirm)
  const setPinConfirm = useSetupStore((s) => s.setPinConfirm)
  const pinError = useSetupStore((s) => s.pinError)
  const setPinError = useSetupStore((s) => s.setPinError)
  const saving = useSetupStore((s) => s.saving)
  const setSaving = useSetupStore((s) => s.setSaving)
  // Child→Parent is gated by the parent PIN so a child can't reach answer keys.
  const pinGate = usePinGateStore((s) => s.pinGate)
  const setPinGate = usePinGateStore((s) => s.setPinGate)
  const gateValue = usePinGateStore((s) => s.gateValue)
  const setGateValue = usePinGateStore((s) => s.setGateValue)
  const gateError = usePinGateStore((s) => s.gateError)
  const setGateError = usePinGateStore((s) => s.setGateError)

  // Pages the parent pinned as tabs (an exam tracker, a date sheet…). Kept
  // in the workspace's per-key state, the same file Quill's pin_page writes,
  // so both sides see one list. Reloaded after every turn (Quill may have
  // pinned or unpinned) and whenever the parent toggles a pin here.
  const [pins, setPins] = useState<PinnedPage[]>([])
  const loadPins = useCallback(() => {
    api.loadState('pins')
      .then((raw) => {
        const list = (raw as { pins?: unknown } | null)?.pins
        setPins(Array.isArray(list) ? list.filter((p): p is PinnedPage => !!p && typeof (p as PinnedPage).path === 'string' && typeof (p as PinnedPage).title === 'string') : [])
      })
      .catch(() => {})
  }, [])
  const savePins = useCallback((next: PinnedPage[]) => {
    setPins(next)
    api.saveState('pins', { pins: next }).catch(() => {})
  }, [])
  const togglePin = useCallback((path: string) => {
    const already = pins.some((p) => p.path === path)
    if (already) {
      savePins(pins.filter((p) => p.path !== path))
      if (drawerTab === `pin:${path}`) setDrawerTab('progress')
      return
    }
    const name = path.split('/').pop() ?? path
    const title = name.replace(/\.html?$/i, '').replace(/[-_]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
    savePins([...pins, { path, title }])
  }, [pins, savePins, drawerTab, setDrawerTab])
  // What the shared chat hands back in platform mode: the product events the
  // workspace panel reacts to. Same reactions as the turn result path below.
  const onPlatformInteraction = useCallback((e: ProductInteraction) => {
    if (e.kind === 'family_updated') {
      const child = e.payload.child as { name?: string; grade?: string; board?: string } | undefined
      if (child?.name) setChildName(child.name)
      if (child?.grade) setGrade(child.grade)
      if (child?.board) setBoard(child.board)
      if (typeof e.payload.parent_label === 'string') setParentLabel(e.payload.parent_label)
    }
    if (e.kind === 'pins_updated') loadPins()
    if (e.kind === 'activity_created' || e.kind === 'family_updated') setMapRefreshKey((k) => k + 1)
  }, [loadPins, setMapRefreshKey])
  const onPlatformPresentation = useCallback((p: ProductPresentation) => {
    const rel = (raw: unknown) => String(raw ?? '').replace(/^.*?Chats\/SparkQuill\//, '')
    if (p.kind === 'document.file' && typeof p.payload.path === 'string') {
      setDrawerTab('files'); setViewerImageList([]); setViewerActivityDir(null); setViewerPath(rel(p.payload.path)); setViewerRefreshKey((k) => k + 1)
    } else if (p.kind === 'sparkquill.activity' && typeof p.payload.dir === 'string') {
      const dir = rel(p.payload.dir)
      setDrawerTab('files'); setViewerPath(null); setViewerActivityDir(dir); setExpandedActivity(dir); setMapRefreshKey((k) => k + 1)
    }
  }, [setDrawerTab, setMapRefreshKey])
  // Child Mode: a page Quill opens lands in the child's viewer.
  const onChildPresentation = useCallback((p: ProductPresentation) => {
    if (p.kind !== 'document.file' || typeof p.payload.path !== 'string') return
    const path = String(p.payload.path).replace(/^.*?Chats\/SparkQuill\//, '')
    setChildViewerFocus(typeof p.payload.focus === 'string' ? p.payload.focus : '')
    setChildViewerPath(path)
    setChildViewerRefreshKey((k) => k + 1)
  }, [])
  // Editing an existing activity often writes files without calling open_file
  // or open_activity again. Refresh the current previews when that chat turn
  // ends, including an activity overview whose item paths have not changed.
  useEffect(() => useChatStore.subscribe((current, previous) => {
    const finished = completedSparkQuillTurns(current.chatTabs, previous.chatTabs)
    if (!finished.parent && !finished.child) return
    setMapRefreshKey((k) => k + 1)
    setWsRefreshKey((k) => k + 1)
    setViewerRefreshKey((k) => k + 1)
    if (finished.parent) setChildTreeRefreshKey((k) => k + 1)
    if (finished.child) setChildViewerRefreshKey((k) => k + 1)
  }), [setChildTreeRefreshKey, setMapRefreshKey, setViewerRefreshKey, setWsRefreshKey])
  const childSceneDir = childActivity?.dir ?? ''
  const renderChildScene = useCallback((html: string) => <SceneFrame html={html} activityDir={childSceneDir} />, [childSceneDir])
  useEffect(() => { loadPins() }, [loadPins, mapRefreshKey])
  // The pinned page on screen, loaded when its tab is opened or a turn ends.
  const pinnedPath = drawerTab.startsWith('pin:') ? drawerTab.slice(4) : ''
  const [pinnedHtml, setPinnedHtml] = useState<string | null>(null)
  useEffect(() => {
    if (!pinnedPath) return
    let cancelled = false
    setPinnedHtml(null)
    api.readFile(pinnedPath)
      .then((d) => { if (!cancelled) setPinnedHtml(d.content ?? '') })
      .catch(() => { if (!cancelled) setPinnedHtml('') })
    return () => { cancelled = true }
  }, [pinnedPath, mapRefreshKey])

  // Load the real, agent-generated reports/progress.html for the Progress tab
  // — a single living document, rendered directly (not a link the parent has
  // to click through to).
  useEffect(() => {
    if (drawerTab !== 'progress') return
    let cancelled = false
    api.readFile('reports/progress.html')
      .then((d) => { if (!cancelled) setProgressHtml(d.content ?? '') })
      .catch(() => { if (!cancelled) setProgressHtml('') })
    return () => { cancelled = true }
  }, [drawerTab, mapRefreshKey, setProgressHtml])

  // Every activity, structured — refetched whenever the Files/Uploaded tab is
  // open or a turn just completed (Quill may have created or added to one).
  // Gated on the drawer tab as a whole, deliberately loosely: open_activity
  // can jump straight to an activity's detail view from anywhere, and with a
  // narrower gate an activity Quill had just created and opened could show
  // "no longer available" simply because this fetch never ran.
  useEffect(() => {
    if (drawerTab !== 'files' && drawerTab !== 'uploaded') return
    let cancelled = false
    api.activities()
      .then((d) => { if (!cancelled) setActivities(d ?? []) })
      .catch(() => { if (!cancelled) setActivities([]) })
    return () => { cancelled = true }
  }, [drawerTab, mapRefreshKey, setActivities])

  // Poll real WhatsApp pairing status while the connector modal's WhatsApp
  // section is open — refreshes the QR (it's short-lived) until paired.
  useEffect(() => {
    if (!waOpen || connectorSection !== 'whatsapp') return
    let cancelled = false
    const poll = () => {
      api.whatsappStatus()
        .then((d) => {
          if (cancelled) return
          setWaStatus(d)
          setWaQrNonce((n) => n + 1)
        })
        .catch(() => {})
    }
    poll()
    const id = window.setInterval(poll, 3000)
    return () => { cancelled = true; window.clearInterval(id) }
  }, [waOpen, connectorSection])

  // Browser connector — just a one-time CLI-install check; whether a CDP
  // Chrome is actually reachable is decided by agent-browser itself per call.
  useEffect(() => {
    if (!waOpen || connectorSection !== 'browser') return
    let cancelled = false
    api.browserStatus()
      .then((d) => { if (!cancelled) setBrowserStatus(d) })
      .catch(() => { if (!cancelled) setBrowserStatus({ cli_installed: false }) })
    return () => { cancelled = true }
  }, [waOpen, connectorSection])

  // Pulse config — loaded on entering the parent screen (so the header pill
  // reflects real status right away) and refreshed whenever Settings or the
  // pill's own popover opens.
  useEffect(() => {
    if (screen !== 'parent' && !settingsOpen && !pulsePopoverOpen) return
    let cancelled = false
    api.pulseConfig()
      .then((d) => {
        if (cancelled) return
        setPulseConfig(d)
        setWatchSitesDraft((d.watch_sites || []).join('\n'))
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [screen, settingsOpen, pulsePopoverOpen])

  // Voice tier catalog — loaded whenever Settings opens. Cheap (a sysctl read
  // plus two LookPath calls), so it's refetched each time rather than cached:
  // installing a model elsewhere should be reflected on the next open.
  const refreshVoiceStatus = useCallback(() => {
    api.voiceStatus()
      .then((d) => setVoiceStatus(d))
      .catch(() => {})
  }, [])
  useEffect(() => {
    if (!settingsOpen) return
    refreshVoiceStatus()
    // Poll while a model is actually downloading (live progress) OR still
    // warming up in the background (see voice_worker.go's proactive
    // startup warm-up) — otherwise "Warming up…" would sit stale until the
    // parent happened to close and reopen Settings. Idle Settings with
    // nothing in flight shouldn't hit the server every couple seconds though.
    const allTiers = voiceStatus?.stt_tiers ?? []
    const anyInstalling = allTiers.some((t) => t.installing)
    const anyWarming = allTiers.some((t) => t.installed && t.warm === false)
    if (!anyInstalling && !anyWarming) return
    const id = window.setInterval(refreshVoiceStatus, 1500)
    return () => window.clearInterval(id)
  }, [settingsOpen, refreshVoiceStatus, voiceStatus])

  // Secret names (never values) — loaded whenever Settings is opened.
  useEffect(() => {
    if (!settingsOpen) return
    let cancelled = false
    api.secrets()
      .then((names) => { if (!cancelled) setSecretNames(names) })
      .catch(() => { if (!cancelled) setSecretNames([]) })
    return () => { cancelled = true }
  }, [settingsOpen])

  const saveSecret = () => {
    const name = secretNameDraft.trim()
    const value = secretValueDraft.trim()
    if (!name || !value) return
    setSavingSecret(true)
    api.saveSecret(name, value)
      .then((names) => {
        setSecretNames(names)
        setSecretNameDraft('')
        setSecretValueDraft('')
      })
      .finally(() => setSavingSecret(false))
  }

  const deleteSecret = (name: string) => {
    setDeletingSecret(name)
    api.deleteSecret(name)
      .then((names) => setSecretNames(names))
      .finally(() => setDeletingSecret(null))
  }


  const savePulseConfig = (patch: { enabled?: boolean; cadence_hours?: number; watch_sites?: string[]; preferred_hour?: number; preferred_hour_set?: boolean }) => {
    setSavingPulse(true)
    api.savePulseConfig(patch)
      .then((d) => setPulseConfig(d))
      .catch(() => {})
      .finally(() => setSavingPulse(false))
  }

  // Runs Pulse right now (regardless of the recurring toggle) — used to test
  // it without waiting for the ticker. Fires the request, then polls config
  // and watches last_run_at change to know when the real turn (which can
  // take a few minutes) has finished.
  const runPulseNow = () => {
    const before = pulseConfig?.last_run_at
    setPulseRunError(null)
    setPulseRunning(true)
    api.runPulse()
      .then(({ ok, error }) => {
        if (!ok) { setPulseRunError(error || 'Could not start.'); setPulseRunning(false); return }
        const poll = (attempt: number) => {
          if (attempt > 300) { setPulseRunning(false); setPulseRunError('Taking longer than expected — check back shortly.'); return }
          api.pulseConfig()
            .then((cfg) => {
              setPulseConfig(cfg)
              if (cfg.last_run_at && cfg.last_run_at !== before) {
                setPulseRunning(false)
              } else {
                window.setTimeout(() => poll(attempt + 1), 4000)
              }
            })
            .catch(() => window.setTimeout(() => poll(attempt + 1), 4000))
        }
        window.setTimeout(() => poll(0), 4000)
      })
      .catch(() => { setPulseRunError('Could not reach SparkQuill.'); setPulseRunning(false) })
  }

  // The ONE activity the child is currently bound to — replaces the old
  // scoped-tree scan + package-manifest derivation entirely. Also resumes the
  // child's own conversation (now the activity's own conversation.json)
  // exactly once, the same "don't silently cold-start on refresh" fix the
  // parent thread has above.
  useEffect(() => {
    if (screen !== 'parent' && screen !== 'tutor') return
    let cancelled = false
    api.childActivity()
      .then((act) => {
        if (cancelled) return
        setChildActivity(act)
        if (!act) return
        // Reload whenever the bound activity changes, not just once ever.
        // Skipped mid-turn: replacing the thread under an in-flight send would
        // drop the optimistic message and the reply being streamed into it.
        if (loadedActivityDirRef.current === act.dir) return
        loadedActivityDirRef.current = act.dir
      })
      .catch(() => { if (!cancelled) setChildActivity(null) })
    return () => { cancelled = true }
  }, [childTreeRefreshKey, screen, setChildActivity])

  // The moment a distinct activity is bound (a fresh handoff, or resuming on
  // reload), show its first item — the same "don't wait on the model to
  // remember to call open_file" guarantee the old handoff-response filePath
  // gave, without threading a file path through the handoff call itself.
  // Always force it (not just when nothing is open yet): childViewerPath can
  // still hold a PREVIOUS activity's last-viewed file at this point, and
  // without overriding it the child would keep seeing that old file's
  // content instead of the newly handed-off activity's own.
  const autoOpenedActivityRef = useRef<string | null>(null)
  useEffect(() => {
    if (screen !== 'tutor' || !childActivity) return
    if (autoOpenedActivityRef.current === childActivity.dir) return
    autoOpenedActivityRef.current = childActivity.dir
    const first = childActivity.items[0]
    if (first) { setChildViewerPath(first.path); setChildViewerRefreshKey((k) => k + 1) }
  }, [childActivity, screen, setChildViewerPath])

  // Load the selected file for the child's own inline viewer.
  //
  // Re-opening the SAME file (Quill re-calls open_file after recording an
  // answer) reloads the iframe's document and resets its scroll to the top.
  // That used to be handled by capturing contentWindow.scrollY before the
  // refresh and restoring it after — which never worked: the iframe is
  // sandboxed WITHOUT allow-same-origin, so the read throws, gets caught, and
  // returns 0. The page reliably jumped to the top.
  // withViewerPositionScript now handles this from INSIDE the frame, restoring
  // the offset she was actually at (reported out of the frame as she scrolls).
  useEffect(() => { childViewerPathRef.current = childViewerPath }, [childViewerPath])
  useEffect(() => {
    if (!childViewerPath) { setChildViewerContent(null); return }
    let cancelled = false
    setChildViewerContent(null)
    api.readFile(childViewerPath)
      .then((d) => { if (!cancelled) setChildViewerContent({ isText: !!d.is_text, content: d.is_text ? rewriteRelativeAssetURLs(d.content ?? '', childViewerPath) : (d.content ?? '') }) })
      .catch(() => { if (!cancelled) setChildViewerContent({ isText: false, content: '' }) })
    return () => { cancelled = true }
  }, [childViewerPath, childViewerRefreshKey, setChildViewerContent])

  // Load the selected file for the drawer's Files viewer.
  useEffect(() => {
    if (!viewerPath) { setViewerContent(null); return }
    let cancelled = false
    setViewerContent(null)
    api.readFile(viewerPath)
      .then((d) => { if (!cancelled) setViewerContent({ isText: !!d.is_text, content: d.is_text ? rewriteRelativeAssetURLs(d.content ?? '', viewerPath) : (d.content ?? '') }) })
      .catch(() => { if (!cancelled) setViewerContent({ isText: false, content: '' }) })
    return () => { cancelled = true }
  }, [setViewerContent, viewerPath, viewerRefreshKey])

  // Probe for the file's metadata sidecar (<path>.meta.json — written by the
  // process-file skill when Quill files an upload: subject, topic, type, a short
  // summary, key concepts). When present, the viewer shows an info button that
  // reveals it, so the parent can see what Quill understood a document to be.
  useEffect(() => {
    setViewerMeta(null)
    setMetaOpen(false)
    if (!viewerPath || viewerPath.endsWith('.meta.json')) return
    let cancelled = false
    api.readFile(viewerPath + '.meta.json')
      .then((d) => {
        if (cancelled || !d || !d.is_text || !d.content) return
        try { setViewerMeta(JSON.parse(d.content) as Record<string, unknown>) } catch { /* not valid meta; ignore */ }
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [viewerPath, viewerRefreshKey])

  // Left/Right arrow keys step through the image list the viewer was opened
  // from (e.g. the Uploaded Material thumbnail grid for the current subject).
  useEffect(() => {
    if (!viewerPath || !IMAGE_PATH_RE.test(viewerPath) || viewerImageList.length < 2) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
      const idx = viewerImageList.indexOf(viewerPath)
      if (idx === -1) return
      const dir = e.key === 'ArrowLeft' ? -1 : 1
      const next = (idx + dir + viewerImageList.length) % viewerImageList.length
      setViewerPath(viewerImageList[next])
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [setViewerPath, viewerImageList, viewerPath])


  // Bridge for interactive HTML: the sandboxed viewer iframe posts SQ.save/load
  // messages; the app persists them to a workspace file (child/attempts) so the
  // child's answers survive reloads and Quill can read them later. 'choose' is
  // the newer op a show_scene snippet's button posts to offer a real choice —
  // treated exactly like the child typing/tapping that text, so Quill actually
  // sees and responds to whichever one she picks (see scene_tool.go).
  //
  // Answer widgets and timers ride the same bridge. 'answer' submits a widget
  // answer as her next turn (formatted `qid: value`). Timers are host-owned:
  // the iframe is recreated on every tutor re-open, so page-side timeouts
  // would die constantly — the page only posts 'timer-config' and renders its
  // own countdown display, while the host arms the deadline and, on expiry,
  // submits a [Timer] system line plus a 'timer-fired' post back so the page
  // can lock its inputs. Timer ops arm from the child's own worksheet frame
  // only: a parent previewing the same page must never start her clocks.
  const sqTimerRef = useRef(new Map<string, { timeout: number; activity: string }>())
  useEffect(() => {
    const timers = sqTimerRef.current
    const activityOf = (path: string | null | undefined) => {
      const parts = String(path ?? '').split('/').filter(Boolean)
      return parts.length > 1 ? parts[parts.length - 2] : ''
    }
    const clearSqTimer = (key: string) => {
      const entry = timers.get(key)
      if (entry) window.clearTimeout(entry.timeout)
      timers.delete(key)
    }
    const onMsg = (e: MessageEvent) => {
      const m = e.data
      if (!m || typeof m !== 'object' || (m as { __sq?: unknown }).__sq !== 1) return
      const msg = m as { op?: string; key?: string; id?: string; data?: unknown; text?: string; qid?: unknown; value?: unknown; timers?: unknown; message?: unknown; url?: unknown }
      const fromFrame = (selector: string) => Array.from(document.querySelectorAll<HTMLIFrameElement>(selector))
        .some((frame) => e.source === frame.contentWindow)
      const fromParentPage = () => e.source === iframeRef.current?.contentWindow || fromFrame('.fl-item-preview-frame, .fl-map-frame')
      const fromChildPage = () => e.source === childIframeRef.current?.contentWindow || fromFrame('.fl-scene-frame')
      const gameStateKey = () => sqGameStateKey(childViewerPathRef.current, msg.key)
      if (msg.op === 'game-save' && e.source === childIframeRef.current?.contentWindow) {
        const key = gameStateKey()
        if (!key) return
        let size = 0
        try { size = JSON.stringify(msg.data)?.length ?? 0 } catch { return }
        if (size === 0 || size > SQ_MAX_GAME_STATE_BYTES) return
        api.saveState(key, msg.data).catch(() => {})
      } else if (msg.op === 'game-load' && (fromChildPage() || fromParentPage())) {
        const source = e.source as Window | null
        const reply = (data: unknown) => source?.postMessage({ __sq: 1, op: 'game-loaded', id: msg.id, data }, '*')
        const key = e.source === childIframeRef.current?.contentWindow ? gameStateKey() : null
        if (!key) { reply(null); return }
        api.loadState(key).then(reply).catch(() => reply(null))
      } else if (msg.op === 'save' && typeof msg.key === 'string') {
        api.saveState(msg.key, msg.data).catch(() => {})
      } else if (msg.op === 'load' && typeof msg.key === 'string') {
        api.loadState(msg.key)
          .then((data) => iframeRef.current?.contentWindow?.postMessage({ __sq: 1, op: 'loaded', id: msg.id, data: data ?? null }, '*'))
          .catch(() => iframeRef.current?.contentWindow?.postMessage({ __sq: 1, op: 'loaded', id: msg.id, data: null }, '*'))
      } else if (msg.op === 'choose' && typeof msg.text === 'string') {
        if (fromChildPage()) submitToChildChat(msg.text)
        else if (fromParentPage()) submitToParentChat(msg.text)
      } else if (msg.op === 'answer') {
        const text = buildSqAnswerText(msg.qid, msg.value)
        if (!text) return
        if (fromChildPage()) {
          // Answering stops that question's clock, if one was running.
          clearSqTimer(`${activityOf(childViewerPathRef.current)}::${sanitizeSqId(msg.qid)}`)
          submitToChildChat(text)
        } else if (fromParentPage()) {
          submitToParentChat(text)
        }
      } else if (msg.op === 'timer-config') {
        if (e.source !== childIframeRef.current?.contentWindow) return
        const activity = activityOf(childViewerPathRef.current)
        for (const t of sanitizeSqTimerConfigs(msg.timers)) {
          const key = `${activity}::${t.qid}`
          if (timers.has(key)) continue // re-open resumes; never resets
          const timeout = window.setTimeout(() => {
            timers.delete(key)
            // She may have moved to another activity since arming; an expiry
            // belongs to its own test, never to whatever is open now.
            if (activityOf(childViewerPathRef.current) !== activity) return
            submitToChildChat(buildSqTimerText(t.qid))
            childIframeRef.current?.contentWindow?.postMessage({ __sq: 1, op: 'timer-fired', qid: t.qid }, '*')
          }, t.seconds * 1000)
          timers.set(key, { timeout, activity })
        }
      } else if (msg.op === 'timer-cancel') {
        if (e.source !== childIframeRef.current?.contentWindow) return
        clearSqTimer(`${activityOf(childViewerPathRef.current)}::${sanitizeSqId(msg.qid)}`)
      } else if (msg.op === 'sq-error' && typeof msg.message === 'string') {
        console.error('[sq page]', msg.message.slice(0, 500))
      } else if (msg.op === 'open' && typeof msg.url === 'string' && /^https?:\/\//i.test(msg.url)) {
        // From the viewer link bridge: an external link the page wanted to
        // follow. Opened in a real tab — navigating the srcDoc frame itself
        // would replace the report with the linked site.
        window.open(msg.url, '_blank', 'noopener')
      }
    }
    window.addEventListener('message', onMsg)
    return () => {
      window.removeEventListener('message', onMsg)
      timers.forEach((entry) => window.clearTimeout(entry.timeout))
      timers.clear()
    }
  }, [])

  // On launch, ask the agent server where onboarding stands. If setup is complete
  // we land straight in the chat; otherwise resume at the right step.
  useEffect(() => {
    let cancelled = false
    const load = (attempt: number) => {
      api.setup()
        .then((data) => {
          if (cancelled) return
          if (data.engine) setEngine(data.engine)
          if (data.child) {
            if (data.child.name) setChildName(data.child.name)
            if (data.child.grade) setGrade(data.child.grade)
            if (data.child.board) setBoard(data.child.board)
          }
          if (data.parent_label) setParentLabel(data.parent_label)
          const step = data.next_step
          if (step === 'done') setScreen(readHandoffSide() === 'tutor' ? 'tutor' : 'parent')
          else if (step === 'pin') setScreen('pin')
          else if (step === 'child') setScreen('child')
          else setScreen('engine')
          setBooting(false)
        })
        .catch(() => {
          if (cancelled) return
          // Never fall back to onboarding on a transient failure — retry, then
          // show an explicit error so completed setup is not lost visually.
          if (attempt < 4) window.setTimeout(() => load(attempt + 1), 500)
          else { setBootError(true); setBooting(false) }
        })
    }
    load(0)
    return () => { cancelled = true }
  }, [setBoard, setBootError, setBooting, setChildName, setEngine, setGrade, setParentLabel, setScreen])

  const selectedEngine = engines.find((item) => item.id === engine)
  const initial = childName.trim().slice(0, 1).toUpperCase() || 'M'

  const move = (next: Screen) => {
    setScreen(next)
  }

  // Verify the parent PIN before returning to Parent Mode from the child screen.
  const submitPinGate = () => {
    setGateError('')
    api.verifyPin(gateValue)
      .then((data) => {
        if (data.ok) { setPinGate(false); setGateValue(''); persistHandoffSide('parent'); move('parent') }
        else setGateError('That PIN isn’t right.')
      })
      .catch(() => setGateError('Could not check the PIN.'))
  }

  // After the engine is saved, ask setup() where onboarding really stands:
  // a fresh family goes on to the child step, but a family that already has
  // a child and PIN (set up before the engine step existed) lands straight
  // in the chat instead of re-entering both.
  const persistEngineAndContinue = () => {
    if (!selectedEngine) return
    setSaving(true)
    api.setup().then(state => state.engine === selectedEngine.id ? undefined : api.selectEngine('parent', selectedEngine.id))
      .then(() => api.setup())
      .then((state) => {
        applyFamilyEngineToOpenTabs('parent', selectedEngine.id, state.parent_model, state.parent_reasoning_effort, state.connection_id)
        if (state.next_step === 'done') move(readHandoffSide() === 'tutor' ? 'tutor' : 'parent')
        else if (state.next_step === 'pin') move('pin')
        else move('child')
      })
      .catch(() => move('child'))
      .finally(() => setSaving(false))
  }

  const createChildAndContinue = () => {
    if (!childName.trim()) return
    setSaving(true)
    api.saveChild({ name: childName, grade, board }).finally(() => { setSaving(false); move('pin') })
  }

  const savePinAndContinue = () => {
    setPinError('')
    if (!/^\d{4,8}$/.test(pin)) { setPinError('Use 4–8 digits.'); return }
    if (pin !== pinConfirm) { setPinError('The two PINs don’t match.'); return }
    setSaving(true)
    api.setPin(pin)
      .then((data) => { if (data.error) { setPinError(data.error); return } persistHandoffSide('parent'); move('parent') })
      .catch(() => setPinError('Could not save the PIN.'))
      .finally(() => setSaving(false))
  }


  // Real WhatsApp connection (whatsmeow QR pairing) — see whatsapp_bot.go.
  // Once paired, incoming messages in the linked account's own "Message
  // Yourself" chat are handled directly by the backend event handler; there
  // is no frontend send path for real WhatsApp messages.
  const unpairWhatsApp = (jid: string) => {
    if (!window.confirm(`Unlink this WhatsApp number (+${jid})? You can always re-pair by scanning a new QR code.`)) return
    setUnpairingJid(jid)
    api.whatsappUnpair(jid)
      .then(() => {
        setWaStatus((cur) => (cur ? { ...cur, accounts: cur.accounts.filter((a) => a.jid !== jid) } : cur))
        setWaQrNonce((n) => n + 1)
      })
      .finally(() => setUnpairingJid(null))
  }

  // Toggles on-device WhatsApp voice-note transcription (Parakeet, Apple
  // Silicon only). Enabling kicks off the shared MLX voice install if it
  // isn't already there — the same install that also powers the "most
  // natural" read-aloud voice; the status poll above picks up "installing" →
  // "installed" as it progresses. Disabling does NOT delete anything: doing
  // so would silently break read-aloud too, since they share one
  // environment. Deleting it is only ever a deliberate action via a tier's
  // own "Remove" button in Settings → Voice.
  const toggleVoiceTranscription = (enabled: boolean) => {
    setVoiceToggling(true)
    api.whatsappVoice(enabled)
      .then((d) => {
        setWaStatus((cur) => (cur ? { ...cur, voice_transcription: d } : cur))
      })
      .finally(() => setVoiceToggling(false))
  }



  // Enter Child Mode after a handoff response. new_session decides whether the
  // child continues their existing conversation (still the same activity) or
  // starts a clean one (a different activity — per-handoff resume only makes
  // sense while it's genuinely the same one). The activity's own first item
  // (if any) is opened automatically by the auto-open effect above once
  // childActivity reflects this handoff — no need to thread a file path
  // through the handoff call itself.
  const enterChildModeAfterHandoff = (newSession: boolean, greeting: string, dir: string) => {
    persistHandoffSide('tutor')
    setScreen('tutor')
    setChildTreeRefreshKey((k) => k + 1)
    // Resume (newSession === false) only ever targets the activity already
    // open in this session: the child chat keeps its own conversation per
    // activity, so there's nothing to send here — just the screen switch
    // above, and the child sees exactly where they left off.
    // A fresh session is kicked off with the greeting, shown in the chat like
    // any message; the activity's goal is already in activity.json, which the
    // tutor reads first, so only the greeting goes.
    if (newSession) setChildKickoff({ id: Date.now(), dir, text: greeting })
  }

  // handoffGreeting is what the child's chat "says" to kick off a handoff — it
  // reads like the child speaking to Quill, so it uses parentLabel ("mom",
  // "dad", a name) when known, falling back to "parent" until Quill has asked.
  const handoffGreeting = (what: string) => `My ${parentLabel || 'parent'} just ${what}. Can you help me get started?`

  // Does the real API call: bind the child to this activity, switch into
  // child mode, and kick off Quill — it opens the activity and guides the
  // child. No filename/path is shown; Quill composes everything the child
  // reads. resume asks the backend to keep Myra's existing conversation going
  // instead of its own same-activity heuristic.
  const performHandoff = (dir: string, greetingText: string, resume: boolean) => {
    const myGeneration = ++handoffGenerationRef.current
    api.handoff(dir, resume)
      // "Start fresh" on the platform means a new conversation for the
      // activity, not a kickoff appended to the old one. Rotate it before
      // the child screen opens, so the screen opens the new session.
      .then((data) => (data.new_session ? api.resetChildConversation(dir).then(() => { forgetChildChat(dir); return data }) : data))
      .then((data) => {
        if (!data.dir) return
        // A newer handoff has started since this one was fired (a different
        // activity, clicked before this request finished) — its own response
        // will apply instead, so bail out here rather than starting a chat
        // for an activity the parent already navigated away from.
        if (myGeneration !== handoffGenerationRef.current) return
        enterChildModeAfterHandoff(!!data.new_session, handoffGreeting(greetingText), data.dir)
      })
      .catch(() => {})
  }

  // Same handoff, but re-triggered from the Files browser (create_learning_activity
  // already did the equivalent when the activity was made) — e.g. to hand off
  // an activity made earlier in the conversation. Only ASK continue-vs-fresh
  // when this is genuinely the SAME activity Myra is already partway through
  // (childActivity.dir, loaded for the assignment pill) — a different activity
  // is unambiguously a fresh handoff, no need to ask. First-ever handoff has no
  // childActivity yet, so it's fresh too.
  // title names the REAL activity in the greeting ("set up 'X' for me..."),
  // not a generic "something new" phrase that reads the same for every activity.
  const startActivityHandoff = (dir: string, title: string) => {
    const greetingText = `set up "${title}" for me to work on`
    if (childActivity?.dir === dir) {
      setPendingChildEntry({ dir, greetingText })
      return
    }
    // Every activity keeps its own conversation, so an activity she worked
    // on before deserves the same continue-or-fresh question as the current
    // one; only a never-opened one is silently fresh.
    api.loadChildConversation(dir)
      .then((c) => { if ((c?.messages?.length ?? 0) > 0) setPendingChildEntry({ dir, greetingText }); else performHandoff(dir, greetingText, false) })
      .catch(() => performHandoff(dir, greetingText, false))
  }

  // Runs the actual handoff once the parent has answered continue-vs-fresh.
  const confirmChildEntry = (resume: boolean) => {
    const entry = pendingChildEntry
    if (!entry) return
    setPendingChildEntry(null)
    performHandoff(entry.dir, entry.greetingText, resume)
  }



  if (booting) {
    return (
      <main className="learning-app" data-theme={theme}>
        <div className="fl-boot"><img src="/sparkquill-loader.svg" alt="" width={76} height={76} /><p>Starting SparkQuill…</p></div>
      </main>
    )
  }

  if (bootError) {
    return (
      <main className="learning-app" data-theme={theme}>
        <div className="fl-boot">
          <img src="/sparkquill-mark.svg" alt="" width={64} height={64} />
          <p>Couldn’t reach SparkQuill on this computer.</p>
          <button className="primary-button" type="button" onClick={() => window.location.reload()}>Try again</button>
        </div>
      </main>
    )
  }

  if (screen === 'parent') {
    return (
      <main className="learning-app" data-theme={theme}>
        <div
          ref={parentBodyRef}
          className={`fl-shell${parentResizing ? ' is-resizing' : ''}`}
          data-rail={chatsRailOpen ? 'open' : 'closed'}
          data-drawer={drawerOpen ? 'open' : 'closed'}
          style={{ ['--parent-side-w' as string]: `${Math.round(parentSideWidth)}px` }}
        >
          <aside className="fl-rail" aria-label="Chats" aria-hidden={!chatsRailOpen}>
            <div className="fl-new-wrap">
              <button className="fl-new" type="button" onClick={onNewChatButtonClick} disabled={switchingChat}>
                <Plus size={16} /> New chat
              </button>
              {newChatEnginePopoverOpen && (
                <>
                  <div className="fl-new-engine-backdrop" onClick={() => setNewChatEnginePopoverOpen(false)} />
                  <div className="fl-new-engine-popover" role="menu">
                    <p className="fl-new-engine-label">Start with</p>
                    {newChatEngines.map((engine) => (
                      <button key={engine.id} type="button" role="menuitem" onClick={() => startNewChatFromRail(engine.id)}>
                        {engine.label || engine.id}
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
            <div className="fl-rail-scroll">
              <div className="fl-rail-group">
                <p className="fl-rail-label">Chats with Quill</p>
                {parentChatsError && <p className="fl-rail-empty">{parentChatsError}</p>}
                {parentChats.length === 0 && !parentChatsError ? (
                  <p className="fl-rail-empty">No chats yet.</p>
                ) : (
                  parentChats.map((chat) => (
                    <div key={chat.session_id} className={`fl-rail-row${chat.current ? ' is-active' : ''}`}>
                      <button
                        type="button"
                        className={`fl-rail-item${chat.current ? ' is-active' : ''}`}
                        onClick={() => openParentChat(chat)}
                        disabled={switchingChat}
                        title={chat.current ? 'This chat' : 'Open this chat'}
                      >
                        <span className="fl-rail-item-title">{chat.title}</span>
                        <span className="fl-rail-item-when">{chat.current ? 'Now' : dateTimeLabel(chat.updated_at) || ''}</span>
                      </button>
                      {!chat.current && (
                        <button
                          type="button"
                          className="fl-rail-delete"
                          aria-label={`Delete ${chat.title}`}
                          title="Delete this chat"
                          onClick={() => deleteParentChat(chat)}
                        >
                          <Trash2 size={14} />
                        </button>
                      )}
                    </div>
                  ))
                )}
              </div>
              <div className="fl-rail-group">
                <p className="fl-rail-label">{childName || 'Your child'}’s activity chats</p>
                <div className="fl-rail-cleanup">
                  <span>Older than</span>
                  <input
                    type="number"
                    min={1}
                    aria-label="Days"
                    value={deleteOlderDays}
                    onChange={(e) => setDeleteOlderDays(e.target.value)}
                  />
                  <span>days</span>
                  <button type="button" onClick={deleteOldChats}>Delete</button>
                </div>
                {deleteChatsError && <p className="fl-rail-empty">{deleteChatsError}</p>}
                {loadingChats && activities.length === 0 ? (
                  <p className="fl-rail-empty">Loading…</p>
                ) : activities.length === 0 ? (
                  <p className="fl-rail-empty">No activity chats yet.</p>
                ) : (
                  activities.map((act) => {
                    // A migrated activity's product.json session_id was
                    // invented by the migration with nothing behind it in
                    // chat_history — legacy_conversation being set marks
                    // that, so its row stays static rather than opening to
                    // an empty "0 events" view.
                    const onOpen = act.session_id && !act.legacy_conversation
                      ? () => setViewingActivityChat({ sessionId: act.session_id!, title: act.title, dir: `${FAMILY_WORKSPACE}/${act.dir}` })
                      : null
                    return (
                    <div key={act.dir} className="fl-rail-row">
                      {onOpen ? (
                        <button
                          type="button"
                          className="fl-rail-item"
                          onClick={onOpen}
                          title={`See ${childName || 'her'} and the tutor's conversation`}
                        >
                          <span className="fl-rail-item-title">{act.title}</span>
                          <span className="fl-rail-item-when">{dateTimeLabel(act.created_at) || 'Undated'}</span>
                        </button>
                      ) : (
                        <div className="fl-rail-item is-static" title={act.legacy_conversation ? 'This activity predates the conversation viewer' : undefined}>
                          <span className="fl-rail-item-title">{act.title}</span>
                          <span className="fl-rail-item-when">{dateTimeLabel(act.created_at) || 'Undated'}</span>
                        </div>
                      )}
                      <button
                        type="button"
                        className="fl-rail-delete"
                        aria-label={`Delete ${act.title}`}
                        title="Delete this chat"
                        disabled={deletingChatDirs.includes(act.dir)}
                        onClick={() => deleteChats([act.dir], `Permanently delete "${act.title}"? This cannot be undone.`)}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    )
                  })
                )}
              </div>
            </div>
          </aside>
          <section className="fl-center">
            <div className="fl-toolbar">
              <div className="fl-toolbar-left">
                <button className="fl-icon-btn fl-rail-toggle" type="button" aria-label={chatsRailOpen ? 'Hide chats' : 'Show chats'} title={chatsRailOpen ? 'Hide chats' : 'Show chats'} aria-pressed={chatsRailOpen} onClick={toggleChatsRail}>
                  {chatsRailOpen ? <PanelLeftClose size={18} /> : <PanelLeftOpen size={18} />}
                </button>
                {!isSingleProductDeployment() && <ProductSurfaceSwitcher />}
                <img className="fl-header-logo" src="/sparkquill-mark.svg" alt="" width={30} height={30} />
                <div className="fl-toolbar-title">
                  <strong className="fl-brand-word">Spark<span>Quill</span></strong>
                  <span>{childName || 'Your child'}{grade ? ` · Grade ${grade}` : ''}{board ? ` · ${board}` : ''}</span>
                </div>
              </div>
              <div className="fl-toolbar-right">
                <div className="fl-pulse-wrap">
                  <button
                    className="fl-pulse-pill"
                    type="button"
                    aria-label="Check-in"
                    title="Check-in"
                    onClick={() => setPulsePopoverOpen((v) => !v)}
                  >
                    <PulseIcon size={14} />
                    <span>Check-in</span>
                    <span className={`fl-dot ${pulseConfig?.enabled ? 'is-ready' : ''}`} />
                  </button>
                  {pulsePopoverOpen && (
                    <>
                    <div className="fl-pulse-backdrop" onClick={() => setPulsePopoverOpen(false)} />
                    <div className="fl-pulse-popover" role="dialog">
                      <div className="fl-pulse-popover-head">
                        <PulseIcon size={15} />
                        <span>Check-in</span>
                        <span className={`fl-pulse-badge ${pulseConfig?.enabled ? 'is-on' : 'is-off'}`}>
                          {pulseConfig?.enabled ? 'On' : 'Off'}
                        </span>
                        <button type="button" className="fl-pulse-popover-close" onClick={() => setPulsePopoverOpen(false)} aria-label="Close">×</button>
                      </div>
                      <div className="fl-pulse-body">
                        <div className="fl-pulse-col">
                          <p className="fl-pulse-popover-desc">Quill checks in on its own now and then: it reviews recent activity, updates what it remembers about you both, and sends you one short summary.</p>
                          <button
                            type="button"
                            className="fl-pulse-toggle"
                            disabled={savingPulse || !pulseConfig}
                            onClick={() => savePulseConfig({ enabled: !pulseConfig?.enabled })}
                          >
                            <span className={`fl-pulse-toggle-track ${pulseConfig?.enabled ? 'is-on' : ''}`}>
                              <span className="fl-pulse-toggle-thumb" />
                            </span>
                            {pulseConfig?.enabled ? 'Turn off' : 'Turn on'}
                          </button>
                            <div className="fl-pulse-popover-meta">
                              <span>Checks every</span>
                              <span>{pulseConfig ? `${pulseConfig.cadence_hours} hours` : '…'}</span>
                            </div>
                          <div className="fl-pulse-popover-meta">
                            <span>Last check-in</span>
                            <span>{pulseConfig?.last_run_at ? new Date(pulseConfig.last_run_at).toLocaleString() : 'Not yet'}</span>
                          </div>
                          {pulseConfig?.enabled && (
                            <div className="fl-pulse-popover-meta">
                              <span>Next check-in</span>
                              <span>
                                {pulseConfig.last_run_at
                                  ? (() => {
                                      // Mirrors the backend's own due-check (pulse.go
                                      // startPulseTicker): the cadence window alone can
                                      // land at any hour, so PreferredHour only ever
                                      // pushes it LATER, same day, never earlier — a
                                      // cadence that elapses at 2pm with a preferred
                                      // hour of 8am still fires right away.
                                      const next = new Date(new Date(pulseConfig.last_run_at).getTime() + pulseConfig.cadence_hours * 3600_000)
                                      if (pulseConfig.preferred_hour_set && next.getHours() < pulseConfig.preferred_hour) {
                                        next.setHours(pulseConfig.preferred_hour, 0, 0, 0)
                                      }
                                      return next.toLocaleString()
                                    })()
                                  : `within ${pulseConfig.cadence_hours}h`}
                              </span>
                            </div>
                          )}
                          <button
                            type="button"
                            className="fl-pulse-run-now"
                            disabled={pulseRunning || !pulseConfig}
                            onClick={runPulseNow}
                          >
                            {pulseRunning ? 'Running… (a few minutes)' : 'Run now (test it)'}
                          </button>
                          {pulseRunError && <p className="fl-pulse-run-error">{pulseRunError}</p>}
                          <div className="fl-pulse-history-actions">
                            <button
                              type="button"
                              className="fl-pulse-history-link"
                              disabled={!pulseConfig?.last_session_id}
                              onClick={() => setPulseHistoryOpen(true)}
                            >
                              View check-in history
                            </button>
                            <button
                              type="button"
                              className="fl-pulse-history-link is-danger"
                              disabled={!pulseConfig?.last_session_id || clearingPulseHistory}
                              onClick={clearPulseHistory}
                            >
                              {clearingPulseHistory ? 'Clearing…' : 'Clear history'}
                            </button>
                          </div>
                          {clearPulseHistoryError && <p className="fl-pulse-run-error">{clearPulseHistoryError}</p>}
                        </div>

                        <div className="fl-pulse-col">
                          <p className="fl-pulse-config-hint">Websites to check (optional) — any pages Quill should look at: a school portal, a class site, anything. One per line. Uses your signed-in browser (Connectors → Browser).</p>
                          <textarea
                            className="fl-pulse-config-input"
                            rows={3}
                            placeholder={"https://portal.myraschool.edu/assignments\nhttps://classroom.google.com/..."}
                            value={watchSitesDraft}
                            onChange={(e) => { setWatchSitesDraft(e.target.value); setPulseSaved(false) }}
                          />
                        </div>
                      </div>
                      <button
                        type="button"
                        className={`fl-pulse-save ${pulseSaved ? 'is-saved' : ''}`}
                        disabled={savingPulse}
                        onClick={() => {
                          const sites = watchSitesDraft.split(/[\n,]/).map((s) => s.trim()).filter(Boolean)
                          savePulseConfig({ watch_sites: sites })
                          setPulseSaved(true)
                        }}
                      >
                        {savingPulse ? 'Saving…' : pulseSaved ? 'Saved ✓' : 'Save'}
                      </button>
                    </div>
                    </>
                  )}
                </div>
                <button className="fl-icon-btn" type="button" aria-label="Settings" title="Settings" onClick={() => setSettingsOpen(true)}>
                  <SettingsIcon size={18} />
                </button>
                <button className="fl-whatsapp-btn" type="button" aria-label="WhatsApp" title="SparkQuill on WhatsApp" onClick={() => setWaOpen(true)}>
                  <svg viewBox="0 0 24 24" width="19" height="19" fill="currentColor" aria-hidden="true"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51l-.57-.01c-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.71.306 1.263.489 1.694.626.712.226 1.36.194 1.872.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
                </button>
              </div>
            </div>

            {visibleNotices.length > 0 && (
              <div className="fl-notices" role="region" aria-label="Messages from Quill">
                {visibleNotices.map((n) => (
                  <div key={n.id} className="fl-notice" role="status">
                    <Bell size={16} className="fl-notice-icon" />
                    <div className="fl-notice-body">
                      <strong>{n.title || 'From Quill'}</strong>
                      <p>{n.message}</p>
                    </div>
                    <button type="button" className="fl-notice-close" aria-label="Dismiss" title="Dismiss" onClick={() => dismissNotice(n.id)}>×</button>
                  </div>
                ))}
              </div>
            )}

            {(
              <PlatformChat
                key={parentChatEpoch}
                title="SparkQuill"
                childName={childName}
                theme={theme === 'dark' ? 'dark' : 'light'}
                commands={quickCommands.parent}
                landing={(
                  <div className="fl-thread">
                    <div className="fl-msg is-agent">
                      <span className="fl-msg-avatar is-sun"><Sun size={18} /></span>
                      <div className="fl-msg-col"><div className="fl-bubble">Hi! I’m Quill, {childName || 'your child'}’s learning guide. Tell me what {childName || 'your child'} is working on, or ask me to explain progress, make study material, or create a test.</div></div>
                    </div>
                  </div>
                )}
                onInteraction={onPlatformInteraction}
                onPresentation={onPlatformPresentation}
                onNotifications={onPlatformNotifications}
              />
            )}
          </section>
          <div
            className="fl-parent-resizer"
            role="separator"
            aria-orientation="vertical"
            aria-label="Drag to resize the workspace panel"
            aria-valuenow={Math.round(parentSideWidth)}
            aria-valuemin={PARENT_SIDE_MIN}
            aria-valuemax={Math.round(parentSideMax(windowWidth))}
            tabIndex={0}
            onPointerDown={startParentResize}
            onKeyDown={(e) => {
              const step = e.shiftKey ? 80 : 24
              if (e.key === 'ArrowLeft') { e.preventDefault(); commitParentSideWidth(parentSideWidth + step) }
              else if (e.key === 'ArrowRight') { e.preventDefault(); commitParentSideWidth(parentSideWidth - step) }
              else if (e.key === 'Home') { e.preventDefault(); commitParentSideWidth(parentSideDefault()) }
            }}
          >
            <span className="fl-parent-resizer-grip" aria-hidden="true" />
          </div>

          <aside className="fl-drawer" aria-label="Learning workspace">
            {!((drawerTab === 'files' || drawerTab === 'allfiles' || drawerTab === 'uploaded') && viewerPath) && (
              <div className="fl-drawer-tabs" role="tablist" aria-label="Workspace views">
                <button role="tab" aria-selected={drawerTab === 'progress'} className={drawerTab === 'progress' ? 'is-active' : ''} type="button" onClick={() => setDrawerTab('progress')}>Progress</button>
                {pins.map((p) => (
                  <button key={p.path} role="tab" aria-selected={drawerTab === `pin:${p.path}`} className={drawerTab === `pin:${p.path}` ? 'is-active' : ''} type="button" title={p.path} onClick={() => setDrawerTab(`pin:${p.path}`)}>{p.title}</button>
                ))}
                <button role="tab" aria-selected={drawerTab === 'files'} className={drawerTab === 'files' ? 'is-active' : ''} type="button" onClick={() => setDrawerTab('files')}>Activities</button>
                <button role="tab" aria-selected={drawerTab === 'uploaded'} className={drawerTab === 'uploaded' ? 'is-active' : ''} type="button" onClick={() => setDrawerTab('uploaded')}>Uploaded</button>
                {/* Browsing every raw file is a power-user escape hatch, not a
                    peer of the four content views — an icon keeps it one tap
                    away without competing with them for attention. */}
                <button
                  type="button"
                  className={`fl-icon-btn fl-allfiles-btn${drawerTab === 'allfiles' ? ' is-active' : ''}`}
                  aria-label="Browse all files"
                  aria-pressed={drawerTab === 'allfiles'}
                  title="Browse all files"
                  onClick={() => setDrawerTab(drawerTab === 'allfiles' ? 'files' : 'allfiles')}
                >
                  <FolderOpen size={15} />
                </button>
                <button
                  type="button"
                  className="fl-icon-btn fl-refresh-btn"
                  aria-label="Refresh workspace"
                  title="Refresh"
                  onClick={() => { setWsRefreshKey((k) => k + 1); setMapRefreshKey((k) => k + 1) }}
                >
                  <RefreshCw size={15} />
                </button>
                {/* Watch Quill's browser live while it's checking a site (e.g.
                    signing in to a school portal) — same viewer AgentWorks uses. */}
                <button
                  type="button"
                  className={`fl-icon-btn fl-browser-btn${drawerTab === 'browser' ? ' is-active' : ''}`}
                  aria-label="Watch live browser"
                  aria-pressed={drawerTab === 'browser'}
                  title="Watch live browser"
                  onClick={() => setDrawerTab(drawerTab === 'browser' ? 'progress' : 'browser')}
                >
                  <Globe size={15} />
                </button>
              </div>
            )}

            <div className="fl-drawer-scroll">
              {drawerTab === 'assets' && (
                <>
                  {(() => {
                    if (wsFiles.length === 0) {
                      return <p className="fl-note">No materials yet. Use the attach button to add photos or PDFs — they’ll appear here, organized by subject and topic, for Quill to read.</p>
                    }
                    const groups: Record<string, WsFile[]> = {}
                    wsFiles.forEach((f) => { const k = f.subject || 'General'; (groups[k] = groups[k] || []).push(f) })
                    return Object.entries(groups).map(([subj, files]) => (
                      <section key={subj} className="fl-asset-group">
                        <p className="fl-drawer-label">{subj}</p>
                        {files.map((f) => (
                          <div key={f.path} className="fl-asset">
                            <span className="fl-asset-icon"><FileGlyph name={f.name} size={17} /></span>
                            <span className="fl-asset-body"><strong>{f.name}</strong><small>{f.topic || 'material'}</small></span>
                          </div>
                        ))}
                      </section>
                    ))
                  })()}
                  <p className="fl-callout"><span className="fl-dot is-ready" /> Materials live in the family workspace on this computer. Quill reads them to explain progress and create study material.</p>
                </>
              )}

              {pinnedPath && (
                <>
                  <div className="fl-viewer-bar">
                    <span className="fl-viewer-name">{pins.find((p) => p.path === pinnedPath)?.title ?? pinnedPath}</span>
                    <button className="fl-viewer-back" type="button" title="Remove this tab (the page stays in the workspace)" onClick={() => togglePin(pinnedPath)}><PinOff size={15} /> Unpin</button>
                  </div>
                  {pinnedHtml === null ? (
                    <p className="fl-note">Loading…</p>
                  ) : pinnedHtml === '' ? (
                    <p className="fl-note">This page is missing from the workspace. Unpin it, or ask Quill to make it again.</p>
                  ) : (
                    <iframe className="fl-map-frame" title={pinnedPath} sandbox="allow-scripts" srcDoc={withViewerLinkBridge(withDiagramLib(pinnedHtml))} />
                  )}
                </>
              )}

              {drawerTab === 'progress' && (
                <>
                  {progressHtml === null ? (
                    <p className="fl-note">Loading the progress report…</p>
                  ) : progressHtml === '' || progressHtml.includes('living report grows as') ? (
                    <p className="fl-note">The progress report hasn't been built yet — ask Quill to "update the progress report" once there's some real activity to show.</p>
                  ) : (
                    <iframe className="fl-map-frame" title="Progress report" sandbox="allow-scripts" srcDoc={withViewerLinkBridge(withDiagramLib(progressHtml))} />
                  )}
                </>
              )}

              {drawerTab === 'browser' && (
                <div className="fl-browser-view">
                  <WorkflowLiveBrowser workspacePath={FAMILY_WORKSPACE} scopeNoun="project" profileId={PARENT_PROFILE_ID} onLearn={message=>{submitToParentChat(message)}} minimal />
                </div>
              )}

              {(drawerTab === 'files' || drawerTab === 'allfiles' || drawerTab === 'uploaded') && viewerPath ? (
                <div className="fl-viewer">
                  <div className="fl-viewer-bar">
                    {/* Clearing only viewerPath (never viewerActivityDir here) means
                        "back" naturally falls through to the activity-detail
                        branch below when this file was opened by clicking an
                        item INSIDE that activity — returning to the activity,
                        not the raw file list, exactly as browsing normally
                        expects. A file opened from the general list (where
                        viewerActivityDir is already null) still falls through to
                        that list, unchanged. */}
                    <button className="fl-viewer-back" type="button" onClick={() => setViewerPath(null)}><ArrowLeft size={15} /> Activities</button>
                    <span className="fl-viewer-name">{viewerPath.split('/').pop()}</span>
                    {/\.html?$/i.test(viewerPath) && (
                      <button className="fl-viewer-back" type="button" title={pins.some((p) => p.path === viewerPath) ? 'Remove from the tabs at the top' : 'Keep this page as a tab at the top'} onClick={() => togglePin(viewerPath)}>
                        {pins.some((p) => p.path === viewerPath) ? <><PinOff size={15} /> Unpin</> : <><Pin size={15} /> Pin</>}
                      </button>
                    )}
                    {viewerActivityDir && (() => {
                      const act = activities.find((a) => a.dir === viewerActivityDir)
                      return act ? (
                        <button className="fl-give-to-child" type="button" onClick={() => startActivityHandoff(act.dir, act.title)}>
                          Give to {childName || 'child'}
                        </button>
                      ) : null
                    })()}
                    <button
                      className="fl-icon-btn"
                      type="button"
                      aria-label="Refresh"
                      title="Reload this file"
                      onClick={() => setViewerRefreshKey((k) => k + 1)}
                    >
                      <RefreshCw size={14} />
                    </button>
                    {viewerMeta && (
                      <button
                        className={`fl-icon-btn${metaOpen ? ' is-active' : ''}`}
                        type="button"
                        aria-label="About this file"
                        aria-pressed={metaOpen}
                        title="What Quill knows about this file"
                        onClick={() => setMetaOpen((v) => !v)}
                      >
                        <Info size={14} />
                      </button>
                    )}
                    {isPrintable(viewerPath) && (
                      <button
                        className="fl-icon-btn"
                        type="button"
                        aria-label="Print"
                        title="Print this page"
                        onClick={() => printFile(viewerPath)}
                      >
                        <Printer size={14} />
                      </button>
                    )}
                  </div>
                  {metaOpen && viewerMeta && <FileMetaPanel meta={viewerMeta} />}
                  {/* key={viewerPath} forces a fresh mount (replaying the
                      fl-viewer-body CSS reveal animation below) every time a
                      different file opens, even if the previous file was the
                      same content TYPE (e.g. one .md to another). */}
                  <div key={viewerPath} className="fl-viewer-body">
                  {/\.(png|jpe?g|gif|webp|svg|bmp)$/i.test(viewerPath) ? (
                    <img className="fl-viewer-img" src={api.rawUrl(viewerPath)} alt={viewerPath.split('/').pop() || ''} />
                  ) : /\.pdf$/i.test(viewerPath) ? (
                    // PDFs render in the browser's native viewer (with its own
                    // zoom/page controls) — the raw endpoint serves them inline
                    // with an application/pdf content type, so a plain iframe
                    // pointed straight at it is all it takes. No sandbox here: it
                    // would disable the built-in PDF viewer, and the bytes are our
                    // own workspace file, not untrusted HTML.
                    <iframe className="fl-viewer-frame" title="PDF preview" src={api.rawUrl(viewerPath)} />
                  ) : /\.(mp4|webm|mov|m4v)$/i.test(viewerPath) ? (
                    <video className="fl-viewer-media" controls src={api.rawUrl(viewerPath)} />
                  ) : /\.(mp3|wav|m4a|aac|ogg|oga|flac|opus)$/i.test(viewerPath) ? (
                    <audio className="fl-viewer-media" controls src={api.rawUrl(viewerPath)} />
                  ) : !viewerContent ? (
                    <p className="fl-note">Loading…</p>
                  ) : !viewerContent.isText ? (
                    <NonPreviewableFile path={viewerPath} meta={viewerMeta} />
                  ) : (viewerPath.endsWith('.html') || viewerPath.endsWith('.htm')) ? (
                    <iframe ref={iframeRef} className="fl-viewer-frame" title="File preview" sandbox="allow-scripts" srcDoc={viewerSrcDoc} />
                  ) : (viewerPath.endsWith('.md') || viewerPath.endsWith('.markdown')) ? (
                    <div className="fl-viewer-md"><Markdown text={viewerContent.content} /></div>
                  ) : (viewerPath.endsWith('.json') || viewerPath.endsWith('.jsonl')) ? (
                    <pre className="fl-viewer-pre">{formatJSONText(viewerContent.content)}</pre>
                  ) : (
                    <pre className="fl-viewer-pre">{viewerContent.content}</pre>
                  )}
                  </div>
                </div>
              ) : (drawerTab === 'files' || drawerTab === 'allfiles' || drawerTab === 'uploaded') && viewerActivityDir ? (() => {
                const act = activities.find((a) => a.dir === viewerActivityDir)
                if (!act) return <p className="fl-note">That activity is no longer available.</p>
                const expanded = expandedActivity === act.dir
                return (
                  <div className="fl-viewer">
                    <div className="fl-viewer-bar">
                      <button className="fl-viewer-back" type="button" onClick={() => setViewerActivityDir(null)}><ArrowLeft size={15} /> Activities</button>
                      <span className="fl-viewer-name">{act.title}</span>
                    </div>
                    <div key={viewerActivityDir} className="fl-viewer-body fl-package-detail">
                      <div className="fl-package-detail-head">
                        <div>
                          <h2>{act.title}</h2>
                          <p className="fl-note">
                            {act.items.length > 0 ? `${act.items.length} part${act.items.length === 1 ? '' : 's'}` : 'Adaptive practice'}
                            {dateTimeLabel(act.created_at) ? ` · ${dateTimeLabel(act.created_at)}` : ''}
                          </p>
                        </div>
                        <div className="fl-package-detail-actions">
                          {act.items.length > 0 && (
                            <button
                              type="button"
                              className="fl-package-toggle"
                              aria-expanded={expanded}
                              aria-label={expanded ? 'Hide contents' : 'See what’s inside'}
                              title={expanded ? 'Hide contents' : 'See what’s inside'}
                              onClick={() => setExpandedActivity((cur) => (cur === act.dir ? null : act.dir))}
                            >
                              <ChevronDown size={14} className={expanded ? 'is-open' : ''} />
                            </button>
                          )}
                          {(act.items.length === 0 || expanded) && (
                            <button className="fl-give-to-child" type="button" onClick={() => startActivityHandoff(act.dir, act.title)}>
                              Give to {childName || 'child'}
                            </button>
                          )}
                        </div>
                      </div>
                      {(act.items.length === 0 || expanded) && act.goal && <p className="fl-package-goal"><strong>Goal:</strong> {act.goal}</p>}
                      {expanded && act.items.length > 0 && (
                        <div className="fl-package-detail-items">
                          {act.items.map((item) => (
                            <div key={item.path} className="fl-file-item fl-package-item has-preview">
                              <ActivityItemPreview path={item.path} name={item.name} large={act.items.length === 1} refreshKey={mapRefreshKey} />
                              <button
                                type="button"
                                className="fl-item-open-btn"
                                aria-label={`Open ${item.name}`}
                                title="Open"
                                onClick={() => { setViewerImageList([]); setViewerPath(item.path); setViewerRefreshKey((k) => k + 1) }}
                              >
                                <ExternalLink size={14} />
                              </button>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                )
              })() : drawerTab === 'allfiles' ? (
                treeNodes.length === 0 ? <p className="fl-note">No files yet.</p> : (
                  <>
                    <p className="fl-tree-total">
                      <HardDrive size={13} />
                      <span>{formatBytes(treeTotalSize || treeNodes.reduce((sum, n) => sum + (n.size ?? 0), 0))} on disk</span>
                    </p>
                    <FileTree
                      nodes={treeNodes}
                      onOpen={(p) => { setViewerImageList([]); setViewerPath(p) }}
                      expanded={treeExpanded}
                      onToggle={(p, open) => setTreeExpanded((prev) => ({ ...prev, [p]: open }))}
                    />
                  </>
                )
              ) : drawerTab === 'files' ? (
                <>
                  {(() => {
                    // Hierarchy: subject -> topic -> activity -> item. Every piece of
                    // generated content IS an activity now, so this groups the
                    // structured /api/activities objects directly — no path-parsing.
                    // Raw uploads have their own "Uploaded" tab; the progress page/
                    // progress report have their own dedicated tabs, so they're not
                    // duplicated here.
                    const subjectsList = Array.from(new Set(activities.filter((a) => a.subject).map((a) => a.subject!))).sort()
                    const relevant = activities.filter((a) => !filesSubjectFilter || a.subject === filesSubjectFilter)
                    const pinnedSet = new Set(pinnedActivityDirs)
                    const pinnedActs = pinnedActivityDirs.map((dir) => relevant.find((a) => a.dir === dir)).filter((a): a is Activity => !!a)
                    const unpinned = relevant.filter((a) => !pinnedSet.has(a.dir))

                    const bySubject = new Map<string, Map<string, Activity[]>>()
                    const unplaced: Activity[] = []
                    unpinned.forEach((a) => {
                      if (!a.subject) { if (!filesSubjectFilter) unplaced.push(a); return }
                      if (!bySubject.has(a.subject)) bySubject.set(a.subject, new Map())
                      const topics = bySubject.get(a.subject)!
                      const topicKey = a.topic || '—'
                      if (!topics.has(topicKey)) topics.set(topicKey, [])
                      topics.get(topicKey)!.push(a)
                    })
                    // List every part, numbered, so the parent can open and preview
                    // any one of them directly — not just see a summary count.
                    const renderActivities = (acts: Activity[]) => acts.map((act) => {
                      const expanded = expandedActivity === act.dir
                      const openable = act.items.length > 0
                      const isCurrent = childActivity?.dir === act.dir
                      // Details show for an expanded activity, and always for an
                      // adaptive one — it has no items to expand, so its goal
                      // IS the activity.
                      const showDetails = expanded || !openable
                      return (
                        <div key={act.dir} className={`fl-act${expanded ? ' is-expanded' : ''}${isCurrent ? ' is-current' : ''}`}>
                          <button
                            type="button"
                            className="fl-act-head"
                            aria-expanded={openable ? expanded : undefined}
                            disabled={!openable}
                            onClick={() => setExpandedActivity((cur) => (cur === act.dir ? null : act.dir))}
                          >
                            <span className="fl-act-title">{act.title}</span>
                            {openable && <ChevronDown size={15} className={`fl-act-chev${expanded ? ' is-open' : ''}`} />}
                          </button>
                          <div className="fl-act-row">
                            {isCurrent && <span className="fl-act-mode is-live">With {childName || 'your child'} now</span>}
                            <span className="fl-act-sub">
                              {openable ? `${act.items.length} part${act.items.length === 1 ? '' : 's'}` : 'Adaptive'}
                              {dateTimeLabel(act.created_at) ? ` · ${dateTimeLabel(act.created_at)}` : ''}
                            </span>
                            <button
                              className={`fl-act-pin${pinnedSet.has(act.dir) ? ' is-on' : ''}`}
                              type="button"
                              aria-label={pinnedSet.has(act.dir) ? 'Unpin this activity' : 'Pin this activity to the top'}
                              title={pinnedSet.has(act.dir) ? 'Unpin from the top' : 'Pin to the top'}
                              onClick={() => toggleActivityPin(act.dir)}
                            >
                              {pinnedSet.has(act.dir) ? <PinOff size={14} /> : <Pin size={14} />}
                            </button>
                            <button
                              className="fl-give-to-child"
                              type="button"
                              onClick={() => startActivityHandoff(act.dir, act.title)}
                            >
                              Give to {childName || 'child'}
                            </button>
                          </div>
                          {showDetails && act.goal && <p className="fl-package-goal"><strong>Goal:</strong> {act.goal}</p>}
                          {expanded && act.items.map((item) => (
                            <div key={item.path} className="fl-file-item fl-package-item has-preview">
                              <ActivityItemPreview path={item.path} name={item.name} large={act.items.length === 1} refreshKey={mapRefreshKey} />
                              <button
                                type="button"
                                className="fl-item-open-btn"
                                aria-label={`Open ${item.name}`}
                                title="Open"
                                onClick={() => { setViewerImageList([]); setViewerPath(item.path) }}
                              >
                                <ExternalLink size={14} />
                              </button>
                            </div>
                          ))}
                        </div>
                      )
                    })
                    // "By date" groups the SAME relevant/filtered activity set
                    // by calendar day instead of subject/topic — most-recent
                    // day first, activities within a day newest-first too.
                    const byDate = new Map<string, Activity[]>()
                    unpinned.forEach((a) => {
                      const key = dateOnlyKey(a.created_at)
                      if (!byDate.has(key)) byDate.set(key, [])
                      byDate.get(key)!.push(a)
                    })
                    const dateGroups = Array.from(byDate.entries()).sort((a, b) => b[0].localeCompare(a[0]))
                    dateGroups.forEach(([, acts]) => acts.sort((a, b) => (b.created_at || '').localeCompare(a.created_at || '')))

                    return (
                      <div className="fl-workspace">
                        {subjectsList.length > 0 && (
                          <div className="fl-subject-bar" role="group" aria-label="Filter by subject">
                            <button
                              type="button"
                              className={filesSubjectFilter === '' ? 'is-active' : ''}
                              onClick={() => setFilesSubjectFilter('')}
                            >
                              All
                            </button>
                            {subjectsList.map((s) => (
                              <button
                                key={s}
                                type="button"
                                className={filesSubjectFilter === s ? 'is-active' : ''}
                                onClick={() => setFilesSubjectFilter(filesSubjectFilter === s ? '' : s)}
                              >
                                {s}
                              </button>
                            ))}
                          </div>
                        )}
                        <div className="fl-ws-groupby" role="group" aria-label="Group activities by">
                          <button type="button" className={filesGroupBy === 'subject' ? 'is-active' : ''} onClick={() => setFilesGroupBy('subject')}>By subject</button>
                          <button type="button" className={filesGroupBy === 'date' ? 'is-active' : ''} onClick={() => setFilesGroupBy('date')}>By date</button>
                        </div>
                        {pinnedActs.length > 0 && (
                          <section className="fl-ws-subject is-pinned">
                            <h3 className="fl-ws-subject-name"><Pin size={13} /> Pinned<span>{pinnedActs.length}</span></h3>
                            <div className="fl-ws-topic">{renderActivities(pinnedActs)}</div>
                          </section>
                        )}
                        {filesGroupBy === 'date' ? (
                          dateGroups.length === 0 ? (
                            <p className="fl-note">Nothing here yet. Ask Quill to make study material or a test.</p>
                          ) : (
                            dateGroups.map(([key, acts]) => (
                              <section key={key || 'undated'} className="fl-ws-subject">
                                <h3 className="fl-ws-subject-name">
                                  {dateOnlyLabel(acts[0]?.created_at)}
                                  <span>{acts.length}</span>
                                </h3>
                                <div className="fl-ws-topic">{renderActivities(acts)}</div>
                              </section>
                            ))
                          )
                        ) : bySubject.size === 0 && unplaced.length === 0 ? (
                          <p className="fl-note">Nothing here yet. Ask Quill to make study material or a test.</p>
                        ) : (
                          <>
                            {Array.from(bySubject.entries()).map(([subj, topics]) => (
                              <section key={subj} className="fl-ws-subject">
                                <h3 className="fl-ws-subject-name">
                                  {subj}
                                  <span>{Array.from(topics.values()).reduce((n, a) => n + a.length, 0)}</span>
                                </h3>
                                {Array.from(topics.entries()).map(([top, acts]) => (
                                  <div key={top} className="fl-ws-topic">
                                    {top !== '—' && <p className="fl-ws-topic-name">{top}</p>}
                                    {renderActivities(acts)}
                                  </div>
                                ))}
                              </section>
                            ))}
                            {unplaced.length > 0 && (
                              <section className="fl-ws-subject">
                                <h3 className="fl-ws-subject-name">General<span>{unplaced.length}</span></h3>
                                <div className="fl-ws-topic">{renderActivities(unplaced)}</div>
                              </section>
                            )}
                          </>
                        )}
                      </div>
                    )
                  })()}
                </>
              ) : drawerTab === 'uploaded' ? (() => {
                // Raw parent-uploaded material (materials/<subject>/<topic>/...) —
                // its own tab, separate from Quill-generated activities.
                type Entry = { path: string; date?: string; label: string }
                const usable = allFiles.filter((p) => !p.endsWith('.meta.json') && !p.startsWith('skills/') && !p.includes('/conversations/') && !p.endsWith('child-profile.json'))
                const classified = usable
                  .filter((p) => p === 'materials' || p.startsWith('materials/'))
                  .map((p) => ({ p, ...parseMaterialPath(p) }))
                const subjectsList = Array.from(new Set(classified.filter((f) => f.subject).map((f) => f.subject!))).sort()
                const relevant = classified.filter((f) => !filesSubjectFilter || f.subject === filesSubjectFilter)

                // Subject -> Topic -> entries. A file with no topic (loose in
                // materials/<subject>/) falls into that subject's own
                // "General" bucket rather than a fake topic group.
                const bySubject = new Map<string, Map<string, Entry[]>>()
                const general: Entry[] = []
                relevant.forEach((f) => {
                  const entry: Entry = { path: f.p, date: f.date, label: f.label }
                  if (!f.subject) { general.push(entry); return }
                  if (!bySubject.has(f.subject)) bySubject.set(f.subject, new Map())
                  const topics = bySubject.get(f.subject)!
                  const topicKey = f.topic || ''
                  if (!topics.has(topicKey)) topics.set(topicKey, [])
                  topics.get(topicKey)!.push(entry)
                })
                const byDateDesc = (a: Entry, b: Entry) => (b.date || '').localeCompare(a.date || '')
                const renderEntries = (entries: Entry[]) => {
                  const sorted = [...entries].sort(byDateDesc)
                  const imagePaths = sorted.filter((e) => IMAGE_PATH_RE.test(e.path)).map((e) => e.path)
                  return (
                    <div className="fl-thumb-grid">
                      {sorted.map((e) => (
                        IMAGE_PATH_RE.test(e.path) ? (
                          <button key={e.path} type="button" className="fl-thumb-item" onClick={() => { setViewerImageList(imagePaths); setViewerPath(e.path) }}>
                            <img className="fl-thumb-img" src={api.rawUrl(e.path)} alt="" loading="lazy" />
                            <span className="fl-thumb-caption">{e.label}{e.date ? ` · ${e.date}` : ''}</span>
                          </button>
                        ) : (
                          <button key={e.path} type="button" className="fl-file-item" onClick={() => { setViewerImageList([]); setViewerPath(e.path) }}>
                            <FileGlyph name={e.path} size={16} />
                            <span>{e.label}{e.date ? ` · ${e.date}` : ''}</span>
                          </button>
                        )
                      ))}
                    </div>
                  )
                }
                return (
                  <>
                    {subjectsList.length > 0 && (
                      <select
                        className="fl-subject-select"
                        aria-label="Filter by subject"
                        value={filesSubjectFilter}
                        onChange={(e) => setFilesSubjectFilter(e.target.value)}
                      >
                        <option value="">All subjects</option>
                        {subjectsList.map((s) => <option key={s} value={s}>{s}</option>)}
                      </select>
                    )}
                    {bySubject.size === 0 && general.length === 0 ? (
                      <p className="fl-note">No uploaded material yet. Use the attach button to add photos or documents — they’ll appear here.</p>
                    ) : (
                      <>
                        {Array.from(bySubject.entries()).map(([subj, topics]) => {
                          const topicEntries = Array.from(topics.entries()).filter(([t]) => t !== '')
                          const loose = topics.get('') || []
                          return (
                            <section key={subj} className="fl-asset-group">
                              <p className="fl-drawer-label">{subj}</p>
                              {topicEntries.length > 0 ? (
                                <>
                                  {topicEntries.map(([topic, entries]) => (
                                    <div key={topic} className="fl-asset-subgroup">
                                      <p className="fl-drawer-sublabel">{topic}</p>
                                      {renderEntries(entries)}
                                    </div>
                                  ))}
                                  {loose.length > 0 && (
                                    <div className="fl-asset-subgroup">
                                      <p className="fl-drawer-sublabel">General</p>
                                      {renderEntries(loose)}
                                    </div>
                                  )}
                                </>
                              ) : (
                                renderEntries(loose)
                              )}
                            </section>
                          )
                        })}
                        {general.length > 0 && (
                          <section className="fl-asset-group">
                            <p className="fl-drawer-label">General</p>
                            {renderEntries(general)}
                          </section>
                        )}
                      </>
                    )}
                  </>
                )
              })() : null}
            </div>
          </aside>

          {waOpen && (
            <div className="fl-wa-backdrop" role="dialog" aria-modal="true" onClick={() => setWaOpen(false)}>
              <div className="fl-connectors" onClick={(e) => e.stopPropagation()}>
                <div className="fl-wa-head">
                  <span className="fl-wa-title">Connectors</span>
                  <button className="fl-wa-close" type="button" onClick={() => setWaOpen(false)} aria-label="Close">×</button>
                </div>
                <div className="fl-connectors-body">
                  <nav className="fl-connectors-nav">
                    {waEnabled && (
                      <button type="button" className={connectorSection === 'whatsapp' ? 'is-active' : ''} onClick={() => setConnectorSection('whatsapp')}>WhatsApp</button>
                    )}
                    <button type="button" className={connectorSection === 'browser' ? 'is-active' : ''} onClick={() => setConnectorSection('browser')}>Browser</button>
                  </nav>
                  <div className="fl-connectors-panel">
                    {connectorSection === 'whatsapp' ? (
                      <div className="fl-connector-card">
                        {(waStatus?.accounts?.length ?? 0) > 0 && (
                          <>
                            <p className="fl-connector-status is-connected">
                              ✓ WhatsApp linked
                            </p>
                            <ul className="fl-wa-account-list">
                              {waStatus!.accounts.map((a) => (
                                <li key={a.jid} className="fl-wa-account-row">
                                  <span>+{a.jid}{!a.connected ? ' (reconnecting…)' : ''}</span>
                                  <button
                                    className="fl-ghost-btn"
                                    type="button"
                                    onClick={() => unpairWhatsApp(a.jid)}
                                    disabled={unpairingJid === a.jid}
                                  >
                                    {unpairingJid === a.jid ? 'Unlinking…' : 'Unlink'}
                                  </button>
                                </li>
                              ))}
                            </ul>
                            <div className="fl-wa-howto">
                              <p className="fl-wa-howto-title">How to chat with Quill on WhatsApp</p>
                              <ol className="fl-note" style={{ paddingLeft: '1.2em', margin: '6px 0 0' }}>
                                <li>Open WhatsApp on your phone.</li>
                                <li>At the top, search for your own name — the chat labelled <strong>“(You)”</strong> or <strong>“Message yourself”</strong>.</li>
                                <li>Type anything there, like <em>“How is {childName || 'your child'} doing this week?”</em> — Quill reads it and replies right in that same chat.</li>
                                <li>Send a photo of a worksheet or homework and Quill files it, just like the attach button here. A voice note works too — Quill reads it back to you right away so you know it heard you correctly.</li>
                                <li>Type <strong>@child</strong> to talk as {childName || 'your child'} with the tutor in the activity you last gave — the very same conversation as in the app — and <strong>@parent</strong> to come back to Quill.</li>
                              </ol>
                              <p className="fl-note" style={{ marginTop: '8px' }}>That’s it — it works just like texting. You can also send a photo of {childName || 'your child'}’s worksheet there and Quill will look at it. Quill only ever answers in your own “message yourself” chat — never in your chats with other people.</p>
                            </div>
                            {waStatus?.voice_transcription && (
                              <div className="fl-wa-voice">
                                <div className="fl-wa-voice-row">
                                  <div>
                                    <p className="fl-wa-voice-title">Understand voice notes</p>
                                    <p className="fl-note">
                                      {(() => {
                                        const vt = waStatus.voice_transcription!
                                        const sizeLabel = vt.model_size_mb >= 1000 ? `${(vt.model_size_mb / 1000).toFixed(1)}GB` : `${vt.model_size_mb}MB`
                                        if (!vt.available) return 'Needs a newer Mac (2020 or later) — not available on this computer.'
                                        if (vt.installing) return `Setting this up on your computer (~${sizeLabel}, one-time) — this can take several minutes on a home connection…`
                                        if (vt.enabled && vt.installed) return `On — voice notes are transcribed right on this computer (~${sizeLabel} used). Nothing is sent to the cloud for this. English only.`
                                        return `Let Quill understand voice notes you send on WhatsApp — English only. Transcribed entirely on this computer, a one-time ~${sizeLabel} download, no ongoing cost.`
                                      })()}
                                    </p>
                                    {waStatus.voice_transcription.error && (
                                      <p className="fl-note fl-wa-voice-error">Couldn’t set this up: {waStatus.voice_transcription.error}</p>
                                    )}
                                  </div>
                                  <label className="fl-toggle">
                                    <input
                                      type="checkbox"
                                      checked={waStatus.voice_transcription.enabled}
                                      disabled={voiceToggling || waStatus.voice_transcription.installing || !waStatus.voice_transcription.available}
                                      onChange={(e) => toggleVoiceTranscription(e.target.checked)}
                                    />
                                    <span className="fl-toggle-slider" />
                                  </label>
                                </div>
                              </div>
                            )}
                          </>
                        )}
                        {(waStatus?.accounts?.length ?? 0) === 0 && (
                          <div className="fl-wa-add-another">
                            <p className="fl-note">
                              Scan this code with WhatsApp on your phone: <strong>Settings → Linked Devices → Link a Device.</strong>
                            </p>
                            {waStatus?.pairing?.qr_available ? (
                              <img className="fl-wa-qr" src={api.whatsappPairImageUrl(waQrNonce)} alt="WhatsApp pairing QR code" />
                            ) : (
                              <div className="fl-wa-qr is-loading">Preparing QR…</div>
                            )}
                            <p className="fl-note">The code refreshes automatically every 30 seconds until scanned.</p>
                          </div>
                        )}
                      </div>
                    ) : (
                      <div className="fl-connector-card">
                        <p className="fl-connector-status" style={browserStatus?.cli_installed ? { color: 'var(--fl-green, #2e7d32)' } : undefined}>
                          {!isBrowserCDPEnabled() ? 'Open the managed browser below' : browserStatus === null ? 'Checking…' : browserStatus.cli_installed ? '✓ Ready' : 'Open browser to check setup'}
                        </p>
                        <p className="fl-note">For things like school portals — assignments, report cards, uploaded books — the safest way for Quill to check them is to use a browser you're already signed into, so it never needs your password.</p>
                        {isBrowserCDPEnabled() && <div className="fl-install-steps">
                          <p className="fl-note"><strong>One-time setup:</strong> copy this, paste it into the Terminal app on your Mac, and press Enter.</p>
                          <div className="fl-code-row">
                            <pre className="fl-code-block"><code>curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash</code></pre>
                            <button
                              type="button"
                              className="fl-ghost-btn"
                              onClick={() => {
                                navigator.clipboard.writeText("curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash")
                                setBrowserCopied(true)
                                window.setTimeout(() => setBrowserCopied(false), 2000)
                              }}
                            >
                              {browserCopied ? 'Copied!' : 'Copy'}
                            </button>
                          </div>
                          <p className="fl-note">A new browser window opens on its own once it's done.</p>
                        </div>}
                        <button type="button" className="fl-ghost-btn" onClick={()=>{setWaOpen(false);setDrawerTab('browser')}}>Open browser to sign in or teach a task</button>
                        <p className="fl-note">Open the browser panel, take control and sign into the school portal there. Return control when finished so Quill can use the same signed-in browser. Sign in before starting a teaching session.</p>
                        {isBrowserCDPEnabled() && browserStatus && !browserStatus.cli_installed && (
                          <p className="fl-note">(Also needed once: ask whoever set this computer up to run <code>npm install -g agent-browser@latest</code>.)</p>
                        )}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </div>
          )}

          {pendingChildEntry && (
            <div className="fl-signoff-backdrop" role="dialog" aria-modal="true" aria-labelledby="fl-continue-title" onClick={() => setPendingChildEntry(null)}>
              <div className="fl-signoff-card" onClick={(e) => e.stopPropagation()}>
                <div className="fl-signoff-icon"><BookOpen size={22} /></div>
                <h2 id="fl-continue-title">Continue {childName || 'her'} chat, or start fresh?</h2>
                <p>You're about to switch to {childName || 'your child'}'s screen. Should Quill pick up where that conversation left off, or begin a brand-new one? Starting fresh means Quill forgets the earlier chat about this activity; her saved work on the pages stays.</p>
                <div className="fl-signoff-actions">
                  <button className="fl-ghost-btn" type="button" onClick={() => confirmChildEntry(false)}>Start fresh</button>
                  <button className="primary-button" type="button" onClick={() => confirmChildEntry(true)}>Continue her chat</button>
                </div>
              </div>
            </div>
          )}

          {pulseHistoryOpen && pulseConfig?.last_session_id && (
            <PulseHistoryViewer sessionId={pulseConfig.last_session_id} onClose={() => setPulseHistoryOpen(false)} />
          )}

          {viewingActivityChat && (
            <PulseHistoryViewer
              sessionId={viewingActivityChat.sessionId}
              title={viewingActivityChat.title}
              agentProfileId={CHILD_PROFILE_ID}
              agentProfileVersion={CHILD_PROFILE_VERSION}
              workspacePath={viewingActivityChat.dir}
              onClose={() => setViewingActivityChat(null)}
            />
          )}

          <CodingProvidersPanel isOpen={showProviders} onClose={closeProviders} product="sparkquill" />
          {settingsOpen && (
            <div className="fl-settings-backdrop" role="dialog" aria-modal="true" onClick={() => setSettingsOpen(false)}>
              <div className="fl-settings" onClick={(e) => e.stopPropagation()}>
                <div className="fl-settings-head">
                  <span className="fl-settings-title">Settings</span>
                  <button className="fl-wa-close" type="button" onClick={() => setSettingsOpen(false)} aria-label="Close">×</button>
                </div>
                <div className="fl-settings-body">
                  <p className="fl-drawer-label">Providers and accounts</p>
                  <p className="fl-note">Connect your AI account, check its status, or install a provider on this computer.</p>
                  <button type="button" className="fl-ghost-btn" onClick={openProviders}>Manage providers and accounts</button>
                  <LearningModelSettings key={engine} engine={engine} childName={childName} onEngineChange={setEngine} />

                  <VoiceSettings status={voiceStatus} childName={childName} onRefresh={refreshVoiceStatus} />

                  <p className="fl-drawer-label" style={{ marginTop: '20px' }}>Reply sound</p>
                  <div className="fl-wa-voice-row">
                    <div>
                      <p className="fl-note">Play a short chime on {childName || 'your child'}’s device when Quill’s reply is ready — Quill can take anywhere from a few seconds to several minutes, and this catches a reply that arrived while they’ve wandered off.</p>
                    </div>
                    <label className="fl-toggle">
                      <input
                        type="checkbox"
                        checked={childReminderSound}
                        onChange={(e) => toggleChildReminderSound(e.target.checked)}
                      />
                      <span className="fl-toggle-slider" />
                    </label>
                  </div>

                  <p className="fl-drawer-label" style={{ marginTop: '20px' }}>Voice auto-send</p>
                  <div className="fl-wa-voice-row">
                    <div>
                      <p className="fl-note">When {childName || 'your child'} finishes talking, send it right away instead of leaving it for them to press send themselves.</p>
                    </div>
                    <label className="fl-toggle">
                      <input
                        type="checkbox"
                        checked={childVoiceAutoSend}
                        onChange={(e) => toggleChildVoiceAutoSend(e.target.checked)}
                      />
                      <span className="fl-toggle-slider" />
                    </label>
                  </div>

                  <p className="fl-drawer-label" style={{ marginTop: '20px' }}>Secrets</p>
                  <p className="fl-note">Credentials Quill's tools can use — e.g. a school portal login. Saved here, never through chat, so a value you type below never appears in any saved conversation. Quill only ever sees the name, never the value.</p>
                  {secretNames.length > 0 && (
                    <ul className="fl-wa-account-list">
                      {secretNames.map((name) => (
                        <li key={name} className="fl-wa-account-row">
                          <span>{name}</span>
                          <button
                            className="fl-ghost-btn"
                            type="button"
                            onClick={() => deleteSecret(name)}
                            disabled={deletingSecret === name}
                          >
                            {deletingSecret === name ? 'Removing…' : 'Remove'}
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                  <div className="form-row">
                    <label>
                      <span>Name</span>
                      <input
                        type="text"
                        placeholder="e.g. school portal password"
                        value={secretNameDraft}
                        onChange={(e) => setSecretNameDraft(e.target.value)}
                      />
                    </label>
                    <label>
                      <span>Value</span>
                      <input
                        type="password"
                        placeholder="the credential itself"
                        value={secretValueDraft}
                        onChange={(e) => setSecretValueDraft(e.target.value)}
                        onKeyDown={(e) => { if (e.key === 'Enter') saveSecret() }}
                      />
                    </label>
                  </div>
                  <button
                    type="button"
                    className="fl-ghost-btn"
                    onClick={saveSecret}
                    disabled={savingSecret || !secretNameDraft.trim() || !secretValueDraft.trim()}
                  >
                    {savingSecret ? 'Saving…' : 'Save secret'}
                  </button>
                  {hasGatewaySSO() && (
                    <div style={{ marginTop: '24px' }}>
                      <p className="fl-drawer-label">Account</p>
                      <button className="fl-ghost-btn" type="button" onClick={() => window.location.assign('/logout')}>
                        Sign out
                      </button>
                    </div>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </main>
    )
  }

  if (screen === 'tutor') {
    return (
      <main className="learning-app" data-theme={theme}>
        <div className="fl-child">
          <div
            ref={childBodyRef}
            className={`fl-child-body${childResizing ? ' is-resizing' : ''}`}
            style={{ ['--child-side-w' as string]: `${Math.round(childSideWidth)}px` }}
          >
            <section className="fl-child-chat" style={{ ['--chat-scale' as string]: childChatZoom }}>
              <header className="fl-child-top">
                <div className="fl-child-top-row">
                  <div className="fl-child-id">
                    {!isSingleProductDeployment() && <ProductSurfaceSwitcher />}
                    <img className="fl-header-logo" src="/sparkquill-mark.svg" alt="" width={22} height={22} />
                    {/* The subtitle is a welcome line for an empty header; once an
                        assignment pill is showing, the room it took is worth more
                        than the greeting, and the pill already says what's next. */}
                    <div className="fl-child-hi"><strong>Hi {childName || 'Maya'}!</strong>{!childActivity?.title && <small>Let’s keep learning together</small>}</div>
                  </div>
                  <div className="fl-child-top-right">
                    {/* Chat reading size — same cycle as the worksheet's button but
                        its own preference, since the two sides are read differently. */}
                    <button
                      className={`fl-icon-btn fl-zoom-btn${childChatZoom > 1 ? ' is-on' : ''}`}
                      type="button"
                      aria-label={`Chat text size: ${Math.round(childChatZoom * 100)}% — tap for bigger`}
                      title={childChatZoom > 1 ? `Chat text ${Math.round(childChatZoom * 100)}% — tap for bigger` : 'Make the chat text bigger'}
                      onClick={cycleChildChatZoom}
                    >
                      <Type size={14} />
                      {childChatZoom > 1 && <span className="fl-zoom-badge">{Math.round(childChatZoom * 100)}%</span>}
                    </button>
                    <button className="fl-parent-return" type="button" title="Parent Mode" onClick={() => { setGateValue(''); setGateError(''); setPinGate(true) }}><LockKeyhole size={13} /><span>Parent Mode</span></button>
                  </div>
                </div>
                {/* Its own row: the pill's title needs the header's full width to
                    read without truncating, which sharing a row with the greeting
                    and the right-side buttons never left it. */}
                {childActivity?.title && (() => {
                  const hasInfo = !!childActivity.goal
                  return (
                    <div className="fl-child-assignment-wrap">
                      {goalPopoverOpen && <div className="fl-menu-backdrop" onClick={() => setGoalPopoverOpen(false)} />}
                      <button
                        type="button"
                        className="fl-child-assignment-pill"
                        aria-expanded={hasInfo ? goalPopoverOpen : undefined}
                        aria-label={hasInfo ? `${childActivity.title} — show the goal` : childActivity.title}
                        onClick={() => hasInfo && setGoalPopoverOpen((v) => !v)}
                      >
                        <BookOpen size={14} />
                        <span>{childActivity.title}</span>
                        {hasInfo && <ChevronDown size={13} className={goalPopoverOpen ? 'is-open' : ''} />}
                      </button>
                      {goalPopoverOpen && hasInfo && (
                        <div className="fl-child-goal-popover" role="dialog">
                          {childActivity.goal && <p><strong>Goal</strong>{childActivity.goal}</p>}
                        </div>
                      )}
                    </div>
                  )
                })()}
              </header>
              {(
                childActivity?.dir ? (
                  <ChildPlatformChat
                    activityDir={childActivity.dir}
                    title={childActivity.title || 'Activity'}
                    childName={childName}
                    theme={theme === 'dark' ? 'dark' : 'light'}
                    commands={quickCommands.child}
                    kickoff={childKickoff}
                    onKickoffSent={onChildKickoffSent}
                    onPresentation={onChildPresentation}
                    renderScene={renderChildScene}
                  />
                ) : (
                  <p className="fl-note" style={{ padding: 24 }}>No activity yet — ask your {parentLabel || 'parent'} to give you one.</p>
                )
              )}
            </section>
            <div
              className="fl-child-resizer"
              role="separator"
              aria-orientation="vertical"
              aria-label="Drag to resize the worksheet"
              aria-valuenow={Math.round(childSideWidth)}
              aria-valuemin={CHILD_SIDE_MIN}
              aria-valuemax={Math.round(childSideMax(windowWidth))}
              tabIndex={0}
              onPointerDown={startChildResize}
              // Keyboard equivalent — a drag handle that only works with a
              // pointer is unreachable for anyone tabbing, and this is the only
              // fine-grained control (the toolbar button is coarse: default vs max).
              onKeyDown={(e) => {
                const step = e.shiftKey ? 80 : 24
                if (e.key === 'ArrowLeft') { e.preventDefault(); commitChildSideWidth(childSideWidth + step) }
                else if (e.key === 'ArrowRight') { e.preventDefault(); commitChildSideWidth(childSideWidth - step) }
                else if (e.key === 'Home') { e.preventDefault(); commitChildSideWidth(childSideDefault()) }
              }}
            >
              <span className="fl-child-resizer-grip" aria-hidden="true" />
            </div>
            <aside className="fl-child-side">
              <div className="fl-child-side-scroll">
              {childViewerPath ? (
                <div className="fl-viewer">
                  <div className="fl-viewer-bar">
                    <button className="fl-viewer-back" type="button" onClick={() => setChildViewerPath(null)}><ArrowLeft size={15} /> Back</button>
                    <span className="fl-viewer-name">{labelFromFilename(childViewerPath.split('/').pop() || childViewerPath).label}</span>
                    {/* Reading size. Cycles through the steps and wraps back to
                        normal, so one button covers both directions — simpler
                        than a +/- pair on a child's toolbar, and the current
                        step is shown on the button itself. */}
                    <button
                      className={`fl-icon-btn fl-zoom-btn${childZoom > 1 ? ' is-on' : ''}`}
                      type="button"
                      aria-label={`Text size: ${Math.round(childZoom * 100)}% — tap for bigger`}
                      title={childZoom > 1 ? `Text size ${Math.round(childZoom * 100)}% — tap for bigger` : 'Make the text bigger'}
                      onClick={cycleChildZoom}
                    >
                      <Type size={14} />
                      {childZoom > 1 && <span className="fl-zoom-badge">{Math.round(childZoom * 100)}%</span>}
                    </button>
                    {/* Widen/restore the worksheet, beside refresh and print.
                        Complements the drag divider between the panes: the
                        button is a coarse two-state jump (normal ⇄ as wide as
                        possible), the divider is for anything in between. */}
                    <button
                      className={`fl-icon-btn fl-widen-btn${childSideWide ? ' is-on' : ''}`}
                      type="button"
                      aria-label={childSideWide ? 'Shrink the worksheet' : 'Widen the worksheet'}
                      title={childSideWide ? 'Give the chat more room' : 'Give the worksheet more room'}
                      aria-pressed={childSideWide}
                      onClick={() => {
                        commitChildSideWidth(childSideWide ? childSideDefault() : childSideMax(windowWidth))
                      }}
                    >
                      {childSideWide ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
                    </button>
                    <button
                      className="fl-icon-btn"
                      type="button"
                      aria-label="Refresh"
                      title="Reload this page"
                      onClick={() => setChildViewerRefreshKey((k) => k + 1)}
                    >
                      <RefreshCw size={14} />
                    </button>
                    {isPrintable(childViewerPath) && (
                      <button
                        className="fl-icon-btn"
                        type="button"
                        aria-label="Print"
                        title="Print this page"
                        onClick={() => printFile(childViewerPath)}
                      >
                        <Printer size={14} />
                      </button>
                    )}
                  </div>
                  <div key={childViewerPath} className="fl-viewer-body">
                  {IMAGE_PATH_RE.test(childViewerPath) ? (
                    <img className="fl-viewer-img" src={api.rawUrl(childViewerPath)} alt="" />
                  ) : /\.pdf$/i.test(childViewerPath) ? (
                    <iframe className="fl-viewer-frame" title="PDF preview" src={api.rawUrl(childViewerPath)} />
                  ) : /\.(mp4|webm|mov|m4v)$/i.test(childViewerPath) ? (
                    <video className="fl-viewer-media" controls src={api.rawUrl(childViewerPath)} />
                  ) : /\.(mp3|wav|m4a|aac|ogg|oga|flac|opus)$/i.test(childViewerPath) ? (
                    <audio className="fl-viewer-media" controls src={api.rawUrl(childViewerPath)} />
                  ) : !childViewerContent ? (
                    <p className="fl-note">Loading…</p>
                  ) : !childViewerContent.isText ? (
                    <NonPreviewableFile path={childViewerPath} meta={null} />
                  ) : (childViewerPath.endsWith('.html') || childViewerPath.endsWith('.htm')) ? (
                    <iframe
                      ref={childIframeRef}
                      className="fl-viewer-frame"
                      title="Preview"
                      sandbox="allow-scripts"
                      srcDoc={childViewerSrcDoc}
                    />
                  ) : childViewerPath.endsWith('.md') ? (
                    <div className="fl-viewer-md"><Markdown text={childViewerContent.content} /></div>
                  ) : (childViewerPath.endsWith('.json') || childViewerPath.endsWith('.jsonl')) ? (
                    <pre className="fl-viewer-pre">{formatJSONText(childViewerContent.content)}</pre>
                  ) : (
                    <pre className="fl-viewer-pre">{childViewerContent.content}</pre>
                  )}
                  </div>
                </div>
              ) : (
                <>
                  {(() => {
                    // Show ONLY the current activity (/api/child/activity) — the one the
                    // parent most recently handed off — plus the child's own saved work
                    // (its attempts/ folder). Not every activity ever created.
                    const currentItems = childActivity?.items ?? []
                    const attempts = childActivity?.attempts ?? []
                    if (!childActivity && attempts.length === 0) {
                      return <p className="fl-child-note"><Sparkles size={15} /> Ask Quill what to work on next!</p>
                    }
                    return (
                      <>
                        {currentItems.length > 0 ? (
                          <section className="fl-asset-group">
                            <p className="fl-drawer-label">From your parent</p>
                            <div className="fl-child-package">
                              <div className="fl-package-title"><BookOpen size={16} /><span>{childActivity?.title || 'Your activity'}<small>{currentItems.length} part{currentItems.length === 1 ? '' : 's'}{dateTimeLabel(childActivity?.created_at) ? ` · ${dateTimeLabel(childActivity?.created_at)}` : ''}</small></span></div>
                              {currentItems.map((item, i) => (
                                <button key={item.path} type="button" className="fl-file-item fl-package-item" onClick={() => { setChildViewerFocus(''); setChildViewerPath(item.path) }}>
                                  <span className="fl-package-step">{i + 1}</span>
                                  <FileGlyph name={item.name} size={15} />
                                  <span>{labelFromFilename(item.name).label}</span>
                                </button>
                              ))}
                            </div>
                          </section>
                        ) : childActivity ? (
                          // Instruction-only activity (no files): kick off the live activity in chat.
                          <section className="fl-asset-group">
                            <p className="fl-drawer-label">From your parent</p>
                            <button type="button" className="fl-file-item is-package" onClick={() => {
                              if (!window.matchMedia('(prefers-reduced-motion: reduce)').matches) setStartBurst(true)
                              setChildViewerPath(null)
                              submitToChildChat(`Let's start ${childActivity?.title || 'my activity'}!`)
                            }}>
                              <BookOpen size={16} /><span>{childActivity?.title || 'Your activity'}<small>Adaptive practice{dateTimeLabel(childActivity?.created_at) ? ` · ${dateTimeLabel(childActivity?.created_at)}` : ''}</small></span>
                            </button>
                          </section>
                        ) : null}
                        {attempts.length > 0 && (
                          <section className="fl-asset-group">
                            <p className="fl-drawer-label">Your work</p>
                            {attempts.map((item) => {
                              const { label, date } = labelFromFilename(item.name)
                              return (
                                <button key={item.path} type="button" className="fl-file-item" onClick={() => setChildViewerPath(item.path)}>
                                  <FileText size={16} /><span>{label}{date ? ` · ${date}` : ''}</span>
                                </button>
                              )
                            })}
                          </section>
                        )}
                      </>
                    )
                  })()}
                </>
              )}
              </div>
            </aside>
          </div>
          {pinGate && (
            <div className="fl-signoff-backdrop" role="dialog" aria-modal="true" aria-labelledby="fl-gate-title">
              <div className="fl-signoff-card">
                <span className="fl-signoff-icon"><LockKeyhole size={22} /></span>
                <h2 id="fl-gate-title">Enter parent PIN</h2>
                <p>Parent Mode is protected. Enter your PIN to return.</p>
                <input
                  className="fl-gate-input"
                  type="password"
                  inputMode="numeric"
                  autoFocus
                  value={gateValue}
                  onChange={(e) => setGateValue(e.target.value.replace(/\D/g, '').slice(0, 8))}
                  onKeyDown={(e) => { if (e.key === 'Enter') submitPinGate() }}
                  placeholder="PIN"
                />
                {gateError && <p className="pin-error"><LockKeyhole size={16} /> {gateError}</p>}
                <div className="fl-signoff-actions">
                  <button className="fl-ghost-btn" type="button" onClick={() => { setPinGate(false); setGateValue('') }}>Cancel</button>
                  <button className="primary-button" type="button" onClick={submitPinGate} disabled={!gateValue}>Unlock <ArrowRight size={18} /></button>
                </div>
              </div>
            </div>
          )}
        </div>
      </main>
    )
  }

  return (
    <main className="learning-app" data-theme={theme}>
      <header className="learning-header">
        <div className="learning-brand">
          {!isSingleProductDeployment() && <ProductSurfaceSwitcher />}
          <img className="brand-mark" src="/sparkquill-mark.svg" alt="" width={30} height={30} />
          <span className="brand-word">Spark<strong>Quill</strong></span>
        </div>
        <div className="setup-progress" aria-label={`Setup step ${screen === 'engine' ? '1' : screen === 'child' ? '2' : '3'} of 3`}>
          <span className="setup-step-name">{screen === 'engine' ? 'Learning helper' : screen === 'child' ? 'Your child' : 'Parent PIN'}</span>
          <span className="setup-step-count">{screen === 'engine' ? '1' : screen === 'child' ? '2' : '3'} of 3</span>
          <span className="setup-progress-track" aria-hidden="true">
            <i className="is-complete" />
            <i className={screen === 'child' || screen === 'pin' ? 'is-complete' : ''} />
            <i className={screen === 'pin' ? 'is-complete' : ''} />
          </span>
        </div>
      </header>

      <section className={`learning-stage is-${screen}`}>
        {screen === 'engine' && (
          <section className="learning-panel setup-panel">
            <CodingProvidersPanel isOpen={showProviders} onClose={closeProviders} product="sparkquill" />
            <span className="eyebrow">01 · Choose your learning helper</span>
            <h1>Pick the AI that will help your child learn.</h1>
            <p className="fl-lead">Connect an AI account to power lessons, hints, and practice.</p>
            <button type="button" className="fl-ghost-btn" onClick={openProviders}>Manage providers and accounts</button>

            <LearningModelSettings engine={engine} childName={childName} onEngineChange={id => { setEngine(id); refreshEngines() }} />
            <div className="setup-footer">
              <p>{selectedEngine && engineStatus(selectedEngine).ready ? <><CheckCircle2 size={18} /> Your AI account is ready.</> : <>Connect and select an AI account to continue.</>}</p>
              <button className="primary-button" onClick={persistEngineAndContinue} type="button" disabled={!selectedEngine || !engineStatus(selectedEngine).ready || saving}>Continue <ArrowRight size={18} /></button>
            </div>
          </section>
        )}

        {screen === 'child' && (
          <section className="learning-panel setup-panel">
            <span className="eyebrow">02 · Add your child</span>
            <h1>Create one calm learning space.</h1>
            <p className="fl-lead">Tell the learning guide just enough to make each session feel personal.</p>
            <div className="child-form-card">
              <label>
                <span>Name or nickname</span>
                <input value={childName} onChange={(event) => setChildName(event.target.value)} />
              </label>
              <div className="form-row">
                <label>
                  <span>Grade</span>
                  <select value={grade} onChange={(event) => setGrade(event.target.value)}>
                    {GRADES.map((g) => <option key={g} value={g}>Grade {g}</option>)}
                  </select>
                </label>
                <label>
                  <span>School board</span>
                  <select value={board} onChange={(event) => setBoard(event.target.value)}>
                    {BOARDS.map((b) => <option key={b} value={b}>{b}</option>)}
                  </select>
                </label>
              </div>
              <div className="profile-preview">
                <span className="avatar-preview">{initial}</span>
                <span><strong>{childName || 'Your child'}</strong><small>Grade {grade} · {board} · English</small></span>
              </div>
            </div>
            <div className="setup-footer">
              <p><LockKeyhole size={18} /> Next, set a parent PIN.</p>
              <div className="setup-actions">
                <button className="setup-back" onClick={() => move('engine')} type="button"><ArrowLeft size={16} /> Back</button>
                <button className="primary-button" onClick={createChildAndContinue} type="button" disabled={!childName.trim() || saving}>Continue <ArrowRight size={18} /></button>
              </div>
            </div>
          </section>
        )}

        {screen === 'pin' && (
          <section className="learning-panel setup-panel">
            <span className="eyebrow">03 · Set a parent PIN</span>
            <h1>Create your parent PIN.</h1>
            <p className="fl-lead">This keeps Parent Mode — your notes, answer keys, grading, and settings — separate from {childName || 'your child'}’s space on this shared computer.</p>
            <div className="child-form-card">
              <div className="form-row">
                <label>
                  <span>Parent PIN</span>
                  <input type="password" inputMode="numeric" value={pin} onChange={(event) => setPin(event.target.value.replace(/\D/g, '').slice(0, 8))} placeholder="4–8 digits" />
                </label>
                <label>
                  <span>Confirm PIN</span>
                  <input type="password" inputMode="numeric" value={pinConfirm} onChange={(event) => setPinConfirm(event.target.value.replace(/\D/g, '').slice(0, 8))} placeholder="Re-enter" />
                </label>
              </div>
              <p className="pin-hint">You’ll enter this to return to Parent Mode after handing the computer to {childName || 'your child'}.</p>
            </div>
            <div className="setup-footer">
              <p>{pinError ? <span className="pin-error"><LockKeyhole size={18} /> {pinError}</span> : <><LockKeyhole size={18} /> Only you should know this PIN.</>}</p>
              <div className="setup-actions">
                <button className="setup-back" onClick={() => move('child')} type="button"><ArrowLeft size={16} /> Back</button>
                <button className="primary-button" onClick={savePinAndContinue} type="button" disabled={saving}>Enter SparkQuill <ArrowRight size={18} /></button>
              </div>
            </div>
          </section>
        )}
      </section>
      {startBurst && <StartBurst onDone={() => setStartBurst(false)} />}
    </main>
  )
}

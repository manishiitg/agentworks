import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ArrowLeft, ArrowRight, X } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import type { WalkthroughSurface } from '../../utils/onboarding'

type WalkthroughStep = {
  selector?: string
  title: string
  body: string
  example?: string
}

const PRODUCT_SWITCHER_STEP: WalkthroughStep = {
  selector: '[aria-label="Switch product"]',
  title: 'Choose a workspace',
  body: 'Switch between the products available to you here.',
}

const productSwitcherBody = () => {
  const available = document.querySelector('[aria-label="Switch product"]')?.getAttribute('data-tour-products')?.split(' ') ?? []
  const descriptions: Record<string, string> = {
    agentworks: 'Goals for repeatable work with a success metric',
    work: 'Crew for a specialist teammate that remembers a project',
    code: 'Code for a private coding workspace with files, an editor and a terminal',
  }
  const choices = available.map(id => descriptions[id]).filter(Boolean)
  return choices.length > 0
    ? `Use ${choices.join('; ')}. Switch between the products available to you here.`
    : PRODUCT_SWITCHER_STEP.body
}

const OVERVIEW_STEPS: WalkthroughStep[] = [
  {
    title: 'What are Goals for?',
    body: 'Goals is for repeatable work with a measurable result. Tell agents the outcome and metric; they build and run an automation, show progress, and ask for decisions when needed.',
    example: 'Review new support requests daily and keep urgent response time under one hour.',
  },
  PRODUCT_SWITCHER_STEP,
  {
    selector: '[data-tour="workflow-add-edit"]',
    title: 'Open or create an automation',
    body: 'Choose an automation from the name menu, or use the plus button to create one. Its workspace walkthrough will start when you open it.',
  },
  {
    selector: '[data-tour="global-activity"]',
    title: 'Activity',
    body: 'Use this button to get back to updates and pending decisions from anywhere in Goals.',
  },
  {
    selector: '[data-tour="activity-feed"]',
    title: 'Your Activity home',
    body: 'Find updates and decisions from all your automations here.',
  },
  {
    selector: '[data-tour="global-schedules"]',
    title: 'Schedules',
    body: 'Review and manage scheduled runs across all your automations.',
  },
  {
    selector: '[data-tour="global-providers"]',
    title: 'Providers',
    body: 'Connect your coding provider accounts here. Choose which provider and account an automation uses in its Runs on setting.',
  },
  {
    selector: '[data-tour="active-work-switcher"]',
    title: 'Running work',
    body: 'When work is active, this button shows what is running or waiting for input and lets you jump to it.',
  },
]

const AUTOMATION_STEPS: WalkthroughStep[] = [
  {
    title: 'From goal to running automation',
    body: 'This workspace turns a measurable goal into work agents can run. Describe the outcome in chat, review the plan, and use the dashboard to see the result.',
    example: 'Check new support requests each morning and flag urgent ones within an hour.',
  },
  {
    selector: '[data-tour="workflow-add-edit"]',
    title: 'Current automation',
    body: 'This name tells you which automation is open. Use it to switch automations, create another one, or edit the selected one.',
  },
  {
    selector: '[data-tour="workflow-chat-pane"]',
    title: 'Build in chat',
    body: 'Ask the builder to create or change the automation here. The conversation stays with this automation.',
  },
  {
    selector: '[data-tour="chat-input-box"]',
    title: 'Describe the work',
    body: 'Type your request here. Use @ to refer to files and / to browse commands when you need them.',
  },
  {
    selector: '[data-tour="chat-browser-tools"]',
    title: 'Browser access',
    body: 'Turn on browser access when the agent needs to inspect or operate a website.',
  },
  {
    selector: '[data-tour="chat-send-controls"]',
    title: 'Attach and send',
    body: 'Attach files and send your request here. You can also send a follow-up while work is running.',
  },
  {
    selector: '[data-tour="workflow-dashboard"]',
    title: 'Dashboard',
    body: 'Open the automation’s report or dashboard. The agent can update these documents as it works.',
  },
  {
    selector: '[data-tour="workflow-views"]',
    title: 'Views',
    body: 'Open Pulse, the actions waiting on you, and recent activity from this group.',
  },
  {
    selector: '[data-tour="workflow-operations"]',
    title: 'Operations',
    body: 'Find the plan, browser, files, costs, execution logs, backup, publishing, and notifications here.',
  },
  {
    selector: '[data-tour="workflow-setup"]',
    title: 'Setup',
    body: 'Manage the automation’s identity, integrations, playbooks, and access here.',
  },
  {
    selector: '[data-tour="workflow-canvas-pane"]',
    title: 'Workspace pane',
    body: 'The selected plan, report, file browser, or settings view appears beside chat.',
  },
]

const EMPTY_AUTOMATION_STEPS: WalkthroughStep[] = [
  {
    title: 'Choose a goal',
    body: 'Goals uses automations to pursue repeatable, measurable outcomes. Open one to continue, or create one by describing the outcome you want and how you will measure success.',
    example: 'Qualify incoming leads each day and report how many are ready for follow-up.',
  },
  PRODUCT_SWITCHER_STEP,
  {
    selector: '[data-tour="automation-empty-state"]',
    title: 'Start with an automation',
    body: 'No automation is open yet. Choose one from the top bar or create a new one to start building.',
  },
  {
    selector: '[data-tour="workflow-add-edit"]',
    title: 'Choose or create',
    body: 'Open the name menu to choose an automation. Use the plus button beside it to create one. Its own workspace guide will start when you open it.',
  },
  {
    selector: '[data-tour="global-activity"]',
    title: 'Activity',
    body: 'Check updates and pending decisions from all your automations here.',
  },
  {
    selector: '[data-tour="global-providers"]',
    title: 'Providers',
    body: 'Add your provider login or key here, or use an account shared with you. Choose the automation’s provider and account through Runs on when creating it or in Setup later.',
  },
]

const EMPTY_CREW_STEPS: WalkthroughStep[] = [
  {
    title: 'What is Crew for?',
    body: 'Crew is for ongoing work with specialist AI teammates. Give each member a project and role; it keeps that project’s chat, memory, and tools so you can pick up where you left off.',
    example: 'A research member gathers sources and drafts a weekly brief for one client.',
  },
  PRODUCT_SWITCHER_STEP,
  {
    selector: '[data-tour="crew-empty-state"]',
    title: 'Your Crew starts here',
    body: 'Create a Crew member to keep a project’s chat, files, memory, and tools together.',
  },
  {
    selector: '[data-tour="crew-create"]',
    title: 'Create a Crew member',
    body: 'Give it a name and purpose. Once it opens, a separate guide will show you its workspace.',
  },
  {
    selector: '[data-tour="crew-selector"]',
    title: 'Switch Crew members',
    body: 'Use this menu to open another Crew member or add a new one later.',
  },
  {
    selector: '[data-tour="global-providers"]',
    title: 'Providers',
    body: 'Add your provider login or key here, or use an account shared with you. Choose your Crew’s provider and account through Runs on when creating it or in Setup later.',
  },
]

const CREW_STEPS: WalkthroughStep[] = [
  {
    title: 'A teammate that remembers this project',
    body: 'This Crew member is a specialist AI teammate. Its role, project memory, files, and tools stay available across chats, making it useful for work that continues over time.',
    example: 'Ask it to research a topic, draft a plan, then continue that plan later.',
  },
  {
    selector: '[data-tour="crew-selector"]',
    title: 'Current Crew member',
    body: 'Switch Crew members or create another one from this menu.',
  },
  {
    selector: '[data-tour="crew-chat"]',
    title: 'Work together in chat',
    body: 'Ask this Crew member to research, write, build, or continue project work. Its conversations stay with this Crew.',
  },
  {
    selector: '[data-tour="chat-input-box"]',
    title: 'Describe the work',
    body: 'Type your request here. Use @ to refer to project files and / to browse commands.',
  },
  {
    selector: '[data-tour="chat-send-controls"]',
    title: 'Attach and send',
    body: 'Add files or context, then send your request. You can follow up while work is running.',
  },
  {
    selector: '[data-tour="workflow-dashboard"]',
    title: 'Dashboard',
    body: 'Open this Crew member’s dashboard and reports beside chat.',
  },
  {
    selector: '[data-tour="work-tools"]',
    title: 'Workspace tools',
    body: 'Open memory, files, browser, automation, and other available views from this toolbar.',
  },
  {
    selector: '[data-tour="crew-workspace"]',
    title: 'Workspace pane',
    body: 'The selected dashboard, file, memory, or setup view appears here.',
  },
]

const EMPTY_CODE_STEPS: WalkthroughStep[] = [
  {
    title: 'What is Code for?',
    body: 'Code gives you a private workspace with files, an editor, a terminal and a coding agent on the team server. Share it with teammates when ready. Server admins and Code reviewers have logged, read-only access.',
    example: 'Ask the agent to build a feature, run its tests and fix a failing check.',
  },
  {
    selector: '[data-tour="code-empty-state"]',
    title: 'Your workspaces start here',
    body: 'Create a workspace to keep a piece of work’s chat, files and connections together.',
  },
  {
    selector: '[data-tour="code-create"]',
    title: 'Create a workspace',
    body: 'Give it a name and use Runs on to choose the coding provider and account it will use. Once it opens, a separate guide shows you around.',
  },
  {
    selector: '[data-tour="global-providers"]',
    title: 'Providers',
    body: 'Add your own provider login or key, or use an account shared with you. Choose it in Runs on when creating a workspace; you can change it later in Setup.',
  },
  {
    selector: '[data-tour="global-mcp"]',
    title: 'Connect an AI agent',
    body: 'Admins and reviewers: connect your own AI agent to this server, for example to review workspaces.',
  },
  {
    selector: '[data-tour="global-users"]',
    title: 'Add your team',
    body: 'Admins: add people by email. They sign in with Google, and you choose what each person can open.',
  },
]

const CODE_STEPS: WalkthroughStep[] = [
  {
    title: 'Your private workspace',
    body: 'Chat with the agent, keep your files, and connect your own apps here. Only you can open it; administrators and reviewers can inspect it read-only, with every view logged.',
    example: 'Ask it to research a topic, then turn the notes into a document.',
  },
  {
    selector: '[data-tour="crew-selector"]',
    title: 'Current workspace',
    body: 'Use the name menu to open another workspace or create a new one. Each workspace keeps its own chats, files and connections.',
  },
  {
    selector: '[data-tour="crew-chat"]',
    title: 'Work together in chat',
    body: 'Ask for anything: write, analyse, research, build or automate. You can follow up while the agent is working.',
  },
  {
    selector: '[data-tour="chat-input-box"]',
    title: 'Describe the work',
    body: 'Type your request here. Use @ to refer to files and / to browse commands; to add a command of your own, ask the agent to create it.',
  },
  {
    selector: '[data-tour="chat-send-controls"]',
    title: 'Attach and send',
    body: 'Add files or context, then send your request.',
  },
  {
    selector: '[data-tour="work-tools"]',
    title: 'Workspace tools',
    body: 'Open files, the browser and costs, and Setup for sharing, your connected apps (MCPs, each with your own sign-in), your secrets and models.',
  },
  {
    selector: '[data-tour="crew-workspace"]',
    title: 'Workspace pane',
    body: 'The selected file, view or setup page appears here.',
  },
  {
    selector: '[data-tour="global-providers"]',
    title: 'Providers',
    body: 'Manage provider logins and keys here. Add my account creates a private account unless you share it. Select the workspace’s provider and account in Setup; Costs shows recorded spend across your work.',
  },
]

const PROVIDERS_STEPS: WalkthroughStep[] = [
  {
    title: 'Connect the account your work runs on',
    body: 'Providers manages coding agent logins and keys. Goals, Crew and Code select a provider and account through Runs on when you create them, or in their setup later.',
  },
  {
    selector: '[data-tour="providers-list"]',
    title: 'Choose a provider',
    body: 'Select a coding provider to see its accounts. Connected means it is ready; Needs authentication means it needs a login or key; Not installed means its runtime needs installing.',
  },
  {
    selector: '[data-tour="provider-accounts"]',
    title: 'Your accounts and shared accounts',
    body: 'Add my account signs in your own login or saves your key. It stays private unless you share it. Shared with you lists accounts someone has granted you; the server account is managed by an administrator.',
  },
  {
    selector: '[data-tour="providers-costs"]',
    title: 'Costs across your work',
    body: 'Open Costs to review recorded spend by provider, account and work. This is measured run cost; your provider subscription and remaining plan allowance are separate.',
  },
  {
    selector: '[aria-label="Refresh provider status"]',
    title: 'Refresh connection status',
    body: 'After signing in or installing a provider, refresh its status here. Then return to your workspace and choose its provider and account.',
  },
]

const STEPS_BY_SURFACE: Record<WalkthroughSurface, WalkthroughStep[]> = {
  overview: OVERVIEW_STEPS,
  'empty-automation': EMPTY_AUTOMATION_STEPS,
  automation: AUTOMATION_STEPS,
  'empty-crew': EMPTY_CREW_STEPS,
  crew: CREW_STEPS,
  'empty-code': EMPTY_CODE_STEPS,
  code: CODE_STEPS,
  providers: PROVIDERS_STEPS,
}

const SURFACE_LABELS: Record<WalkthroughSurface, { product: string; section: string; aria: string }> = {
  overview: { product: 'Goals', section: 'Getting started', aria: 'Goals getting started walkthrough' },
  'empty-automation': { product: 'Goals', section: 'Choose an automation', aria: 'Empty automation walkthrough' },
  automation: { product: 'Goals', section: 'Automation', aria: 'Automation workspace walkthrough' },
  'empty-crew': { product: 'Crew', section: 'Getting started', aria: 'Empty Crew walkthrough' },
  crew: { product: 'Crew', section: 'Workspace', aria: 'Crew workspace walkthrough' },
  'empty-code': { product: 'Code', section: 'Getting started', aria: 'Empty Code walkthrough' },
  code: { product: 'Code', section: 'Workspace', aria: 'Code workspace walkthrough' },
  providers: { product: 'Providers', section: 'Accounts and costs', aria: 'Providers walkthrough' },
}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)
const visibleTargetForSelector = (selector: string): Element | null => {
  const candidates = Array.from(document.querySelectorAll(selector))
  return candidates.find(element => {
    const rect = element.getBoundingClientRect()
    const styles = window.getComputedStyle(element)
    const crewChat = element.closest('[data-tour="crew-chat"]')
    // On narrow layouts the workspace can leave chat as a thin, unusable strip.
    // Skip its descendants until the pane is wide enough to interact with.
    if (crewChat && crewChat.getBoundingClientRect().width < 120) return false
    return rect.width > 0 &&
      rect.height > 0 &&
      rect.right > 0 && rect.left < window.innerWidth &&
      rect.bottom > 0 && rect.top < window.innerHeight &&
      styles.display !== 'none' &&
      styles.visibility !== 'hidden' &&
      styles.opacity !== '0'
  }) ?? null
}
const isStepAvailable = (step: WalkthroughStep) => !step.selector || Boolean(visibleTargetForSelector(step.selector))
const visibleStepIndices = (steps: WalkthroughStep[]) => steps
  .map((step, index) => isStepAvailable(step) ? index : -1)
  .filter(index => index >= 0)

interface WorkflowWalkthroughProps {
  isOpen: boolean
  onClose: () => void
  openToken?: number
  surface: WalkthroughSurface
}

export const WorkflowWalkthrough: React.FC<WorkflowWalkthroughProps> = ({ isOpen, onClose, openToken = 0, surface }) => {
  const steps = STEPS_BY_SURFACE[surface]
  const surfaceLabel = SURFACE_LABELS[surface]
  const [progress, setProgress] = useState({ surface, openToken, index: 0 })
  // A new surface must render its first step immediately, before effects run.
  const stepIndex = progress.surface === surface && progress.openToken === openToken ? progress.index : 0
  const panelRef = useRef<HTMLDivElement>(null)
  const [panelMeasuredHeight, setPanelMeasuredHeight] = useState(228)
  const [viewport, setViewport] = useState({ width: window.innerWidth, height: window.innerHeight })
  const [targetRect, setTargetRect] = useState<DOMRect | null>(null)

  const findStep = useCallback((startIndex: number, direction: 1 | -1) => {
    if (direction === 1) {
      for (let index = Math.max(0, startIndex); index < steps.length; index += 1) {
        if (isStepAvailable(steps[index])) return index
      }
      return -1
    }
    for (let index = Math.min(steps.length - 1, startIndex); index >= 0; index -= 1) {
      if (isStepAvailable(steps[index])) {
        return index
      }
    }
    return -1
  }, [steps])

  const goToStep = useCallback((direction: 1 | -1) => {
    const nextIndex = findStep(stepIndex + direction, direction)
    if (nextIndex >= 0) setProgress({ surface, openToken, index: nextIndex })
  }, [findStep, openToken, stepIndex, surface])

  useEffect(() => {
    if (!isOpen) return
    const firstIndex = findStep(0, 1)
    setProgress({ surface, openToken, index: firstIndex >= 0 ? firstIndex : 0 })
    setTargetRect(null)
  }, [findStep, isOpen, openToken, surface])

  const updateTarget = useCallback(() => {
    const selector = steps[stepIndex]?.selector
    if (!selector) {
      setTargetRect(null)
      return
    }
    const target = visibleTargetForSelector(selector)
    if (target) {
      const rect = target.getBoundingClientRect()
      setTargetRect(previous => previous &&
        previous.left === rect.left && previous.top === rect.top &&
        previous.width === rect.width && previous.height === rect.height
        ? previous : rect)
      return
    }
    const nextIndex = findStep(stepIndex + 1, 1)
    if (nextIndex >= 0 && nextIndex !== stepIndex) {
      setProgress({ surface, openToken, index: nextIndex })
    } else {
      const previousIndex = findStep(stepIndex - 1, -1)
      if (previousIndex >= 0 && previousIndex !== stepIndex) {
        setProgress({ surface, openToken, index: previousIndex })
        return
      }
      const firstIndex = findStep(0, 1)
      if (firstIndex >= 0 && firstIndex !== stepIndex) {
        setProgress({ surface, openToken, index: firstIndex })
        return
      }
      setTargetRect(null)
    }
  }, [findStep, openToken, stepIndex, steps, surface])

  useEffect(() => {
    if (!isOpen) return
    if (!isStepAvailable(steps[stepIndex])) {
      const firstIndex = findStep(0, 1)
      if (firstIndex >= 0) {
        setProgress({ surface, openToken, index: firstIndex })
      }
    }
  }, [findStep, isOpen, openToken, stepIndex, steps, surface])

  useEffect(() => {
    if (!isOpen) return
    updateTarget()
    const handleResize = () => {
      setViewport({ width: window.innerWidth, height: window.innerHeight })
      updateTarget()
    }
    window.addEventListener('resize', handleResize)
    window.addEventListener('scroll', updateTarget, true)
    // Changing pages or opening an automation can replace tour targets without
    // causing a resize or scroll event.
    const interval = window.setInterval(updateTarget, 300)
    return () => {
      window.removeEventListener('resize', handleResize)
      window.removeEventListener('scroll', updateTarget, true)
      window.clearInterval(interval)
    }
  }, [isOpen, updateTarget])

  // This guide allows interaction with the highlighted page control. Move
  // focus into it on opening, but do not trap focus or hijack page arrow keys.
  useLayoutEffect(() => {
    if (!isOpen) return
    const launcher = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setViewport({ width: window.innerWidth, height: window.innerHeight })
    panelRef.current?.querySelector<HTMLButtonElement>('[data-tour-primary]')?.focus({ preventScroll: true })
    return () => {
      const returnTarget = launcher?.isConnected && launcher !== document.body
        ? launcher
        : document.querySelector<HTMLElement>('[aria-label^="Account:"]')
      returnTarget?.focus({ preventScroll: true })
    }
  }, [isOpen, openToken, surface])

  useLayoutEffect(() => {
    if (!isOpen || !panelRef.current) return
    const panel = panelRef.current
    const measure = () => {
      const height = panel.getBoundingClientRect().height
      if (height > 0) setPanelMeasuredHeight(previous => previous === height ? previous : height)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(panel)
    return () => observer.disconnect()
  }, [isOpen, openToken, stepIndex, surface])

  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
      if (!panelRef.current?.contains(event.target as Node)) return
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault()
        goToStep(event.key === 'ArrowLeft' ? -1 : 1)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [goToStep, isOpen, onClose])

  if (!isOpen) return null

  const visibleIndices = visibleStepIndices(steps)
  const visiblePosition = visibleIndices.indexOf(stepIndex)
  const displayedPosition = visiblePosition >= 0 ? visiblePosition : 0
  const step = steps[visibleIndices[displayedPosition]]
  const stepNumber = step ? displayedPosition + 1 : 0
  const stepTotal = visibleIndices.length
  const isFirstVisibleStep = displayedPosition === 0
  const isLastVisibleStep = stepTotal === 0 || displayedPosition === stepTotal - 1
  const panelWidth = Math.min(380, viewport.width - 24)
  const panelHeight = Math.min(panelMeasuredHeight, viewport.height - 24)
  // Top-bar menus open below their triggers. Keep the guide beside them so
  // people can open the automation picker while its step is highlighted.
  const topBarTarget = targetRect && targetRect.top < 80 && targetRect.height < 80
  const panelBesideTarget = topBarTarget && (
    targetRect.right + panelWidth + 64 <= viewport.width - 12 ||
    targetRect.left - panelWidth - 24 >= 12
  )
  const panelLeft = panelBesideTarget && targetRect
    ? targetRect.right + panelWidth + 64 <= viewport.width - 12
      ? targetRect.right + 64
      : targetRect.left - panelWidth - 24
    : targetRect
    ? clamp(targetRect.left, 12, viewport.width - panelWidth - 12)
    : clamp((viewport.width - panelWidth) / 2, 12, viewport.width - panelWidth - 12)
  const panelTop = !targetRect
    ? viewport.height / 2
    : panelBesideTarget
    ? clamp(targetRect.bottom + 10, 12, viewport.height - panelHeight - 12)
    : targetRect.bottom + panelHeight + 16 > viewport.height
      ? clamp(targetRect.top - panelHeight - 14, 12, viewport.height - panelHeight - 12)
      : clamp(targetRect.bottom + 14, 12, viewport.height - panelHeight - 12)

  return (
    <ModalPortal>
      <div className="fixed inset-0 z-[10000] pointer-events-none">
        {targetRect && (
          <div
            className="fixed rounded-xl border-2 border-primary transition-all duration-150 motion-reduce:transition-none"
            style={{
              left: targetRect.left - 6,
              top: targetRect.top - 6,
              width: targetRect.width + 12,
              height: targetRect.height + 12,
              boxShadow: '0 0 0 9999px rgba(8, 13, 24, 0.56)',
            }}
          />
        )}
        <div
          ref={panelRef}
          className="fixed pointer-events-auto overflow-y-auto rounded-2xl border border-border bg-popover p-5 text-popover-foreground shadow-2xl"
          style={{ left: panelLeft, top: panelTop, transform: targetRect ? undefined : 'translateY(-50%)', width: panelWidth, maxHeight: viewport.height - 24 }}
          role="dialog"
          aria-label={surfaceLabel.aria}
          aria-describedby="workflow-walkthrough-description"
          data-testid="workflow-walkthrough-dialog"
        >
          <div className="flex items-start justify-between gap-3">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
              {surfaceLabel.product} <span className="mx-1 text-border">/</span> {surfaceLabel.section}
            </div>
            <button
              onClick={onClose}
              className="-mr-1 -mt-1 rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              aria-label="Close walkthrough"
              data-testid="workflow-walkthrough-close"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="mt-2 flex items-center justify-between gap-3" aria-live="polite" aria-atomic="true">
            <h3 className="text-base font-semibold leading-6 text-foreground">{step?.title ?? 'Explore Goals'}</h3>
            {stepTotal > 0 && <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{stepNumber} of {stepTotal}</span>}
          </div>
          <p id="workflow-walkthrough-description" className="mt-2 text-sm leading-5 text-muted-foreground">
            {step === PRODUCT_SWITCHER_STEP ? productSwitcherBody() : step?.body ?? 'This part of the interface is still loading. You can reopen the walkthrough from your account menu.'}
          </p>
          {step?.example && (
            <div className="mt-3 rounded-lg bg-muted/60 px-3 py-2 text-xs leading-5 text-muted-foreground">
              <span className="font-semibold text-foreground">Example:</span> {step.example}
            </div>
          )}
          {stepTotal > 0 && (
            <div className="mt-4 flex gap-1" aria-hidden="true">
              {visibleIndices.map((index, position) => (
                <span key={index} className={`h-1 flex-1 rounded-full ${position <= displayedPosition ? 'bg-primary' : 'bg-muted'}`} />
              ))}
            </div>
          )}
          <div className="mt-5 flex items-center justify-between gap-2">
            <button type="button" onClick={onClose} className="rounded-md px-1 py-1.5 text-xs text-muted-foreground hover:text-foreground">
              Skip tour
            </button>
            <div className="flex items-center gap-2">
            <button
              onClick={() => goToStep(-1)}
              disabled={isFirstVisibleStep}
              data-testid="workflow-walkthrough-back"
              className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
            >
              <ArrowLeft className="h-3.5 w-3.5" />
              Back
            </button>
            {isLastVisibleStep ? (
              <button
                onClick={onClose}
                data-testid="workflow-walkthrough-done"
                data-tour-primary="true"
                className="rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90"
              >
                Finish
              </button>
            ) : (
              <button
                onClick={() => goToStep(1)}
                data-testid="workflow-walkthrough-next"
                data-tour-primary="true"
                className="inline-flex items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90"
              >
                Next
                <ArrowRight className="h-3.5 w-3.5" />
              </button>
            )}
            </div>
          </div>
        </div>
      </div>
    </ModalPortal>
  )
}

export default WorkflowWalkthrough

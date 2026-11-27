import type { AppearanceBorder, AppearanceBorderSurface, AppearanceProfile, AppearanceTheme } from '../stores/appearanceTheme'

// One projection for shared primitives; no per-screen state subscriptions or DOM
// scans. Authored documents, native terminal content and code keep their design.
const excluded = ':not(:where(.force-light,.force-light *,[data-report-theme],[data-report-theme] *,[data-theme-surface="code"],[data-theme-surface="code"] *,[data-theme-surface="terminal"],[data-theme-surface="terminal"] *))'
const scope = (selector: string) => `:where(html[data-appearance]) ${selector}${excluded}`
const borderTargets: Record<AppearanceBorderSurface, string> = {
  panel: ':is([data-theme-surface="panel"],[data-theme-surface="dialog"],[data-theme-surface="menu"],[data-theme-surface="popover"]):not([data-theme-card])',
  card: '[data-theme-card]',
  composer: '[data-chat-composer-card]',
  input: ':is([data-theme-control="input"],[data-theme-control="textarea"],[data-theme-control="select"])',
  selected_agent: '[data-theme-row="agent"][data-theme-agent-selected="true"]',
  running_agent: '[data-theme-row="agent"]:is([data-theme-agent-state="running"],:has([data-theme-agent-state="running"]))',
}

/** Values come from the same validated appearance document as colours/materials. */
export function appearanceControlCSS(theme: AppearanceTheme, profile: AppearanceProfile): string {
  const rules: string[] = []
  const colors = theme.colors[profile] ?? {}
  const icon = theme.icons
  const iconStyle: string[] = []
  if (icon?.stroke_width !== undefined) iconStyle.push(`stroke-width:${icon.stroke_width}`)
  if (icon?.scale !== undefined) iconStyle.push(`scale:${icon.scale}`)
  if (icon?.style) iconStyle.push(`stroke-linecap:${icon.style === 'round' ? 'round' : 'square'}`, `stroke-linejoin:${icon.style === 'round' ? 'round' : 'miter'}`)
  if (colors.icon) iconStyle.push(`color:${colors.icon}`)
  if (iconStyle.length) rules.push(`${scope('svg.lucide')}{${iconStyle.join(';')}}`)
  // An icon on a filled button (send, primary, destructive) keeps the button's own text colour, so it stays readable.
  if (colors.icon) rules.push(`${scope(':is([data-theme-action="send"],.bg-primary,.bg-destructive) svg.lucide')}{color:currentColor}`)

  for (const [surface, target] of Object.entries(borderTargets) as [AppearanceBorderSurface, string][]) {
    const border: AppearanceBorder = surface === 'card'
      ? { ...theme.borders?.panel, ...theme.borders?.card }
      : theme.borders?.[surface] ?? {}
    const color = colors[`${surface}_border`] ?? (surface === 'card' ? colors.panel_border : undefined)
    const declarations: string[] = []
    if (border.width_px !== undefined) {
      declarations.push(`border-width:${border.width_px}px`)
      if (border.width_px > 0 && !border.style) declarations.push('border-style:solid')
    } else if (color && (surface === 'selected_agent' || surface === 'running_agent')) declarations.push('border-width:1px', 'border-style:solid')
    if (border.radius_px !== undefined) declarations.push(`border-radius:${border.radius_px}px`)
    if (border.style) declarations.push(`border-style:${border.style}`)
    if (color) declarations.push(`border-color:${color}`)
    if (declarations.length) rules.push(`${scope(target)}{${declarations.join(';')}}`)
  }
  const density = theme.layout?.density
  const chat = theme.chat
  // Keep the virtualizer's scrolling/measuring element full-width: only its
  // measured content row gets a responsive reading column.
  if (chat?.reading_width_px !== undefined) rules.push(`${scope('[data-theme-chat-row]')}{max-width:${chat.reading_width_px}px;width:100%;margin-inline:auto}`)
  const spacing = chat?.spacing_px ?? (density === 'compact' ? 4 : density === 'roomy' ? 20 : density === 'normal' ? 12 : undefined)
  if (spacing !== undefined) rules.push(`${scope('[data-theme-message-role]')}{margin-block:${spacing}px}`)
  for (const role of ['user', 'assistant'] as const) {
    const style = chat?.[`${role}_style`]
    const background = colors[`${role}_background`]
    const foreground = colors[`${role}_foreground`]
    if (!style && !background && !foreground) continue
    // Long user messages have a pre-existing outer card; keep one surface, not
    // a card inside a card. Attachments/actions remain outside the styled body.
    if (role === 'user' && style) rules.push(`${scope('[data-theme-message-role="user"]')}{background-color:transparent;border-width:0;padding:0}`)
    const declarations: string[] = []
    if (style) {
      declarations.push(`border-radius:${style === 'bubble' ? 18 : style === 'card' ? 8 : 0}px`,
        `padding:${style === 'plain' ? '0' : '12px 16px'}`,
        `border:${style === 'card' ? '1px solid hsl(var(--border))' : '0'}`,
        `background-color:${style === 'plain' ? 'transparent' : background ?? (role === 'user' ? 'hsl(var(--muted))' : 'hsl(var(--card))')}`)
    } else if (background) declarations.push(`background-color:${background}`)
    if (foreground) declarations.push(`color:${foreground}`)
    rules.push(`${scope(`[data-theme-message-role="${role}"] [data-theme-message-body]`)}{${declarations.join(';')}}`)
  }
  if (density) {
    const [gap, height] = density === 'compact' ? [2, 28] : density === 'roomy' ? [8, 44] : [4, 36]
    // Never shrink phone tap targets or resize authored report/terminal islands.
    rules.push(`@media(min-width:768px){${scope(':is([data-theme-row="navigation"],[data-theme-row="project"],[data-theme-row="agent"],[role="menuitem"])')}{padding-block:${gap}px;min-height:${height}px}}`)
  }
  return rules.join('\n')
}

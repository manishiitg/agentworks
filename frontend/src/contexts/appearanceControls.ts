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
  return rules.join('\n')
}

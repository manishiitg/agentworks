import type { ReactNode } from 'react'
import type { ChatElement as ElementSpec } from './chatElement'

const frame = 'not-prose my-4 min-w-0 max-w-xl rounded-xl border border-border bg-muted/20 text-foreground'
const summary = 'cursor-pointer break-words px-4 py-3 text-sm font-medium marker:text-muted-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring'
const number = (value: number) => new Intl.NumberFormat(undefined, { notation: 'compact', maximumSignificantDigits: 3 }).format(value)

function SmallChart({ spec }: { spec: Extract<ElementSpec, { type: 'chart' }> }) {
  const { data, style, unit } = spec
  // Include zero for honest bar lengths and a stable baseline, including
  // negative/all-zero series. Units and exact values remain available below.
  const low = Math.min(0, ...data.map(point => point.value))
  const high = Math.max(0, ...data.map(point => point.value))
  const span = high - low || 1
  const y = (value: number) => 144 - ((value - low) / span) * 120
  const step = 280 / data.length
  const x = (index: number) => 56 + step * (index + 0.5)
  const baseline = y(0)
  const labelEvery = Math.max(1, Math.ceil(data.length / 5))
  return (
    <figure className={frame} data-chat-element="chart">
      <figcaption className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-4 pt-4">
        <span className="break-words text-sm font-semibold">{spec.title}</span>
        {unit && <span className="break-words text-xs text-muted-foreground">{unit}</span>}
      </figcaption>
      <svg viewBox="0 0 360 186" className="block w-full" role="img" aria-label={`${spec.title}${unit ? ` (${unit})` : ''}; ${data.map(point => `${point.label}: ${point.value}`).join(', ')}`}>
        {[0, 0.5, 1].map(fraction => {
          const value = low + span * fraction
          return <g key={fraction}>
            <line x1="56" x2="336" y1={y(value)} y2={y(value)} className="stroke-current text-muted-foreground/20" />
            <text x="48" y={y(value) + 4} textAnchor="end" className="fill-current text-muted-foreground" fontSize="10">{number(value)}</text>
          </g>
        })}
        <line x1="56" x2="336" y1={baseline} y2={baseline} className="stroke-current text-muted-foreground/40" />
        <g className="text-sky-600 dark:text-sky-400">
          {style === 'line' && <polyline points={data.map((point, index) => `${x(index)},${y(point.value)}`).join(' ')} fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" />}
          {data.map((point, index) => <g key={index}>
            {style === 'bar'
              ? <rect x={x(index) - step * 0.3} y={Math.min(baseline, y(point.value))} width={step * 0.6} height={Math.abs(baseline - y(point.value))} rx="2" fill="currentColor"><title>{point.label}: {point.value}{unit ? ` ${unit}` : ''}</title></rect>
              : <circle cx={x(index)} cy={y(point.value)} r="3" fill="currentColor"><title>{point.label}: {point.value}{unit ? ` ${unit}` : ''}</title></circle>}
            {(index % labelEvery === 0 || index === data.length - 1) && <text x={x(index)} y="166" textAnchor="middle" fontSize="10" className="fill-current text-muted-foreground">{point.label.length > 10 ? `${point.label.slice(0, 9)}…` : point.label}</text>}
          </g>)}
        </g>
      </svg>
      <details className="border-t border-border text-xs">
        <summary className={`${summary} !py-2 !text-xs text-muted-foreground`}>View values</summary>
        <div className="max-h-64 overflow-auto px-4 pb-3">
          <table className="w-full text-left text-xs">
            <caption className="sr-only">{spec.title}{unit ? ` (${unit})` : ''}</caption>
            <thead><tr><th scope="col" className="py-1 font-medium">Label</th><th scope="col" className="py-1 text-right font-medium">Value{unit ? ` (${unit})` : ''}</th></tr></thead>
            <tbody>{data.map((point, index) => <tr key={index} className="border-t border-border/60"><th scope="row" className="break-words py-1 font-normal">{point.label}</th><td className="py-1 text-right tabular-nums">{String(point.value)}</td></tr>)}</tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}

export function ChatElement({ spec, renderMarkdown }: { spec: ElementSpec; renderMarkdown: (body: string) => ReactNode }) {
  if (spec.type === 'chart') return <SmallChart spec={spec} />
  if (spec.type === 'details') return (
    <details className={frame} data-chat-element="details">
      <summary className={summary}>{spec.title}</summary>
      <div className="min-w-0 border-t border-border px-4 py-3">{renderMarkdown(spec.body)}</div>
    </details>
  )
  return (
    <section className={`${frame} p-4`} data-chat-element="timeline" aria-label={spec.title}>
      <div className="mb-4 break-words text-sm font-semibold">{spec.title}</div>
      <ol className="m-0 list-none p-0">
        {spec.items.map((item, index) => <li key={index} className="relative ml-1 border-l border-border pb-5 pl-5 last:border-transparent last:pb-0">
          <span aria-hidden="true" className="absolute -left-1 top-1 h-2 w-2 rounded-full bg-sky-600 dark:bg-sky-400" />
          <div className="break-words text-xs text-muted-foreground">{item.when}</div>
          <div className="mt-0.5 break-words text-sm font-medium">{item.title}</div>
          {item.detail && <div className="mt-1 whitespace-pre-wrap break-words text-sm text-muted-foreground">{item.detail}</div>}
        </li>)}
      </ol>
    </section>
  )
}

import type { ReactNode } from 'react'
import { Check } from 'lucide-react'

/** One choice of several, as a row of joined buttons (instead of radio buttons). */
export function Segmented<T extends string>({ label, value, options, onChange, disabled }: {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  disabled?: boolean
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex flex-wrap rounded-lg border border-gray-200 bg-gray-50 p-0.5 dark:border-gray-700 dark:bg-gray-800/60">
      {options.map(option => {
        const on = option.value === value
        return (
          <button key={option.value} type="button" role="radio" aria-checked={on} disabled={disabled} onClick={() => onChange(option.value)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-50 ${on ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-white' : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100'}`}>
            {option.label}
          </button>
        )
      })}
    </div>
  )
}

/** A pick that can be turned on and off, as a rounded chip (instead of a checkbox). */
export function Chip({ on, onClick, disabled, children, title }: { on: boolean; onClick: () => void; disabled?: boolean; children: ReactNode; title?: string }) {
  return (
    <button type="button" aria-pressed={on} disabled={disabled} onClick={onClick} title={title}
      className={`inline-flex max-w-full items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors disabled:opacity-50 ${on
        ? 'border-violet-500 bg-violet-600 text-white hover:bg-violet-500'
        : 'border-gray-300 bg-white text-gray-700 hover:border-violet-400 hover:text-gray-900 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:text-white'}`}>
      {on && <Check className="h-3 w-3 shrink-0" aria-hidden="true" />}
      <span className="min-w-0 truncate">{children}</span>
    </button>
  )
}

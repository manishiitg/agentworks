import { useId, useState } from "react"
import { Eye, EyeOff } from "lucide-react"

import { Input } from "./Input"
import { Label } from "./label"

export interface SecretFieldProps {
  label: string
  required?: boolean
  hint?: React.ReactNode
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  /** Tooltip shown when disabled (permission reason). */
  disabledTitle?: string
  autoComplete?: string
  name?: string
}

/**
 * A standard secret/token field for settings forms: label, reveal toggle,
 * and hint line. One design for every credential input.
 */
export function SecretField({
  label,
  required = false,
  hint,
  value,
  onChange,
  placeholder,
  disabled = false,
  disabledTitle,
  autoComplete = "off",
  name,
}: SecretFieldProps) {
  const [shown, setShown] = useState(false)
  const inputID = useId()
  return (
    <div>
      <Label htmlFor={inputID} className="mb-2 block">
        {label} {required && <span className="text-red-500">*</span>}
      </Label>
      <div className="relative" title={disabled ? disabledTitle : undefined}>
        <Input
          id={inputID}
          type={shown ? "text" : "password"}
          value={value}
          onChange={event => onChange(event.target.value)}
          disabled={disabled}
          placeholder={placeholder}
          autoComplete={autoComplete}
          name={name}
          className="pr-10"
        />
        <button
          type="button"
          onClick={() => setShown(shown => !shown)}
          disabled={disabled}
          aria-label={shown ? `Hide ${label}` : `Show ${label}`}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
        >
          {shown ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
      {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

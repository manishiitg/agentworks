import * as React from "react"

import { cn } from "@/lib/utils"

export interface SwitchProps extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> {
  checked: boolean
  onCheckedChange?: (checked: boolean) => void
}

/**
 * The one toggle switch for settings forms. A native button with
 * role="switch", so it needs no extra dependency and stays keyboard
 * operable. Colors follow the theme (primary when on), never hardcoded
 * blue/gray.
 */
const Switch = React.forwardRef<HTMLButtonElement, SwitchProps>(
  ({ className, checked, onCheckedChange, disabled, ...props }, ref) => {
    return (
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        data-state={checked ? "checked" : "unchecked"}
        disabled={disabled}
        onClick={event => {
          if (disabled) return
          props.onClick?.(event)
          onCheckedChange?.(!checked)
        }}
        ref={ref}
        {...props}
        className={cn(
          "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border border-transparent p-0.5 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50",
          checked ? "justify-end bg-primary" : "justify-start bg-muted",
          className
        )}
      >
        <span className={cn("pointer-events-none block h-5 w-5 rounded-full shadow-sm", checked ? "bg-primary-foreground" : "bg-muted-foreground")} />
      </button>
    )
  }
)
Switch.displayName = "Switch"

export { Switch }

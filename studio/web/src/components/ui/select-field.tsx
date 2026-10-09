// SelectField is the shared Select (./select, Base UI) for a plain list
// of string values — the form controls' one select (plan H3): the
// options as data, the value and its change as strings, the trigger
// labelled and carrying the value (data-value) so a test or a script
// can read it without opening the list.
import type { ReactNode } from "react"
import { cn } from "cn"

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export type SelectOption = {
  value: string
  label: ReactNode
  title?: string
  disabled?: boolean
  /** Extra data-* attributes for the option's element. */
  data?: Record<`data-${string}`, string | undefined>
}

export function SelectField({
  value,
  onValueChange,
  options,
  label,
  disabled,
  className,
  attrs,
}: {
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  /** The control's accessible name. */
  label: string
  disabled?: boolean
  className?: string
  /** Extra data-* attributes for the trigger. */
  attrs?: Record<`data-${string}`, string | undefined>
}) {
  const shown = (v: unknown) => options.find((o) => o.value === v)?.label ?? String(v ?? "")
  return (
    <Select
      value={value}
      disabled={disabled}
      onValueChange={(v) => onValueChange(typeof v === "string" ? v : String(v ?? ""))}
    >
      <SelectTrigger
        size="sm"
        aria-label={label}
        data-value={value}
        className={cn(
          "h-7 max-w-full min-w-0 rounded-md px-2 py-1 text-xs data-[size=sm]:h-7",
          className
        )}
        {...attrs}
      >
        <SelectValue className="min-w-0 truncate">{shown}</SelectValue>
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false} align="start">
        {options.map((o, i) => (
          <SelectItem
            key={`${i}:${o.value}`}
            value={o.value}
            disabled={o.disabled}
            title={o.title}
            data-value={o.value}
            className="text-xs"
            {...o.data}
          >
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

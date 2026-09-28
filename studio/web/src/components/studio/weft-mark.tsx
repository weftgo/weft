// The weft loop mark from the site header (plan §5.6): one thread in
// plain weave through three warps. currentColor-adjacent via CSS vars
// so it adapts to both themes; the thread stays vermilion.
import { cn } from "cn"

export function WeftMark({ className }: { className?: string }) {
  return (
    <svg
      width="24"
      height="24"
      viewBox="0 0 32 32"
      aria-hidden="true"
      className={className}
    >
      <path
        d="M4 11H28M4 16H28M4 21H28"
        fill="none"
        stroke="var(--muted-foreground)"
        strokeWidth="2.4"
        strokeLinecap="round"
      />
      <path
        d="M6.2 5V8.9M6.2 13.1V18.9M6.2 23.1V24A3.27 3.27 0 0 0 12.73 24M12.73 24V18.1M12.73 13.9V8A3.27 3.27 0 0 1 19.27 8M19.27 8V13.9M19.27 18.1V24A3.27 3.27 0 0 0 25.8 24M25.8 24V23.1M25.8 18.9V13.1M25.8 8.9V5"
        fill="none"
        stroke="var(--background)"
        strokeWidth="4.8"
        strokeLinecap="round"
      />
      <path
        d="M6.2 5V8.9M6.2 13.1V18.9M6.2 23.1V24A3.27 3.27 0 0 0 12.73 24M12.73 24V18.1M12.73 13.9V8A3.27 3.27 0 0 1 19.27 8M19.27 8V13.9M19.27 18.1V24A3.27 3.27 0 0 0 25.8 24M25.8 24V23.1M25.8 18.9V13.1M25.8 8.9V5"
        fill="none"
        stroke="var(--thread)"
        strokeWidth="2.2"
        strokeLinecap="round"
      />
    </svg>
  )
}

/** The wordmark block: mark + "weft" in sans 600 + mono "studio" chip. */
export function WeftWordmark({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2", className)}>
      <WeftMark />
      <span className="text-[15px] font-semibold tracking-tight">weft</span>
      <span className="rounded-full border border-border px-2 py-0.5 font-mono text-[11px] text-muted-foreground">
        studio
      </span>
    </span>
  )
}

// A toast's actions (plan H5, lib/notify.ts): "open" is a router link
// built by lib/links.ts — a real href, so it middle-clicks and copies
// like every other Studio link — and the rest are buttons. Rendered
// inside the shell's <Toaster>, which sits inside the router.
import { Link } from "@tanstack/react-router"

import type { StudioLink } from "@/lib/links"

export type ToastAction =
  | { label: string; link: StudioLink; onClick?: never }
  | { label: string; onClick: () => void; link?: never }

const cls =
  "rounded border px-2 py-0.5 font-mono text-[11px] text-foreground hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"

export function ToastActions({ actions }: { actions: ToastAction[] }) {
  return (
    <span className="ml-auto flex shrink-0 items-center gap-1.5" data-toast-actions="">
      {actions.map((a) =>
        a.link ? (
          <Link key={a.label} {...a.link} className={cls}>
            {a.label}
          </Link>
        ) : (
          <button key={a.label} type="button" className={cls} onClick={a.onClick}>
            {a.label}
          </button>
        )
      )}
    </span>
  )
}

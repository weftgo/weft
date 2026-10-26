// A page gated on a capability when GET /api/meta failed: the read's
// error in lib/honesty.ts's words, never "this Studio has no …" — a
// failed read is no verdict about what the Studio serves.
import { CAPS_UNREAD } from "@/lib/honesty"
import { STATUS_TEXT } from "@/lib/status-class"

export function CapsUnread({ error }: { error: string }) {
  return (
    <div
      className="mx-auto max-w-lg space-y-2 py-24 text-center"
      data-caps-unread
    >
      <p
        className={`text-sm ${STATUS_TEXT.error}`}
      >{`${CAPS_UNREAD}: ${error}`}</p>
    </div>
  )
}

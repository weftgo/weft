// Truncation honesty (B9, D5): a result the loop cut is the honesty
// table's badge — a result cap's cut is truncated (cause result_cap,
// the bytes named), a call the max_tokens step never ran is max_tokens
// with no fix (the loop already retried). The markers come from
// weft/loop.go (see lib/events.ts — the regexes and the unrun call's
// words live there beside their tests); the panel draws the same.
import { truncation, UNRUN_CALL_REASON } from "@/lib/events"
import { resultCapReason } from "@/lib/honesty"
import { HoleBadge } from "@/components/studio/hole-badge"

export function TruncationBadge({ content }: { content: string }) {
  const cut = truncation(content)
  if (!cut) return null
  return cut.kind === "bytes" ? (
    <HoleBadge
      hole="truncated"
      cause="result_cap"
      reason={resultCapReason(cut.bytes)}
    />
  ) : (
    <HoleBadge hole="max_tokens" reason={UNRUN_CALL_REASON} fix="" />
  )
}

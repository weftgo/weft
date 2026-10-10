// The step card's Wire tab (ADR 0028 §12, plan A6.2): per attempt,
// the provider request and response as they crossed the wire — read
// from the requests row's raw (A6.1), never a second route. Method and
// URL, the headers, the body (JSON pretty-printed, base64 said as
// binary), the status; what the recorder withheld (credential headers,
// query parameters) is listed, what the 256 KiB cap cut and a body not
// read to its end are badged. A row without raw shows its own
// raw_badge, reason and fix; a token that may not read prompts sees
// the hidden badge — never an empty tab.
import { Download } from "lucide-react"

import type { Holed, RawSide, RequestRow } from "@/lib/api"
import { download, pretty } from "@/lib/json"
import { bytes } from "@/lib/summarize"
import { hasWire } from "@/lib/requests"
import {
  headerRows,
  isJSONMedia,
  WIRE_INCOMPLETE,
  WIRE_INCOMPLETE_REASON,
  wireCutReason,
  wireFileName,
  withheld,
} from "@/lib/wire"
import { kib } from "@/lib/honesty"
import { CodeWin, CopyButton } from "@/components/studio/codewin"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Button } from "@/components/ui/button"

function Headers({ h, label }: { h: RawSide["headers"]; label: string }) {
  const rows = headerRows(h)
  if (!rows.length)
    return <p className="font-mono text-[11px] text-faint">no headers</p>
  return (
    <table className="w-full font-mono text-[11px]" aria-label={label} data-wire-headers>
      <tbody>
        {rows.map(([k, v], i) => (
          <tr key={`${k}\u0000${i}`} className="align-top">
            <th scope="row" className="pr-3 text-left font-normal text-muted-foreground whitespace-nowrap">
              {k}
            </th>
            <td className="break-all">{v}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Body({ side, title }: { side: RawSide; title: string }) {
  if (side.encoding === "base64") {
    return (
      <div className="flex items-center gap-2 font-mono text-[11px]" data-wire-binary>
        <span className="text-muted-foreground">binary, {bytes(side.bytes)} (base64)</span>
        <CopyButton text={side.body} label={`copy the ${title} (base64)`} className="text-muted-foreground" />
      </div>
    )
  }
  if (side.body === "")
    return <p className="font-mono text-[11px] text-faint">empty body</p>
  return <CodeWin title={title} text={side.body} json={isJSONMedia(side.media_type) ? undefined : false} />
}

/** The cuts a side carries: the cap's, a body not read to its end. */
function Cuts({ side }: { side: RawSide }) {
  const cut = side.truncated_bytes ?? 0
  if (side.complete === false) {
    return (
      <HoleBadge
        hole="truncated"
        reason={WIRE_INCOMPLETE_REASON}
        fix=""
        note={cut > 0 ? `${WIRE_INCOMPLETE} (${kib(cut)} unread)` : WIRE_INCOMPLETE}
      />
    )
  }
  if (cut > 0)
    return <HoleBadge hole="truncated" reason={wireCutReason(cut)} fix="" note={`${kib(cut)} cut`} />
  return null
}

function SideBlock({
  kind,
  side,
  withheldWords,
}: {
  kind: "request" | "response"
  side: RawSide
  withheldWords: string[]
}) {
  const line =
    kind === "request"
      ? `${side.method ?? ""} ${side.url ?? ""}`.trim()
      : `${side.status ?? ""}${side.media_type ? ` · ${side.media_type}` : ""}`
  return (
    <section className="space-y-1.5" aria-label={kind} data-wire-side={kind}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">{kind}</span>
        <span className="break-all font-mono text-[11px]" data-wire-line>
          {line}
        </span>
        {kind === "request" && side.media_type ? (
          <span className="font-mono text-[11px] text-faint">{side.media_type}</span>
        ) : null}
        <span className="font-mono text-[10px] text-faint tabular-nums">{bytes(side.bytes)}</span>
        <Cuts side={side} />
      </div>
      {withheldWords.map((w) => (
        <p key={w} className="font-mono text-[11px] text-muted-foreground" data-wire-withheld>
          {w}
        </p>
      ))}
      <Headers h={side.headers} label={`${kind} headers`} />
      <Body side={side} title={`${kind} body`} />
    </section>
  )
}

/** One attempt's exchange, or its row's hole. */
function AttemptWire({ row, runId, label }: { row: RequestRow; runId: string; label?: string }) {
  if (!hasWire(row)) {
    return (
      <div data-wire-attempt={row.attempt} className="space-y-1">
        {label ? <span className="font-mono text-[11px] text-faint">{label}</span> : null}
        <HoleBadge
          hole={row.raw_badge || "not_recorded"}
          cause={row.raw_badge ? undefined : "raw_off"}
          reason={row.raw_reason}
          fix={row.raw_fix}
          detail
        />
      </div>
    )
  }
  const raw = row.raw
  const reqWithheld = [withheld(raw.redacted_headers, "header"), withheld(raw.redacted_query, "query parameter")].filter(
    (w): w is string => w !== null
  )
  return (
    <div data-wire-attempt={row.attempt} className="space-y-3">
      <div className="flex items-center gap-2">
        {label ? <span className="font-mono text-[11px] text-faint">{label}</span> : null}
        <Button
          variant="ghost"
          size="xs"
          className="ml-auto font-mono text-[11px]"
          onClick={() => download(wireFileName(runId, raw), pretty({ request: raw.request, response: raw.response }))}
        >
          <Download data-slot="icon" />
          download .json
        </Button>
      </div>
      <SideBlock kind="request" side={raw.request} withheldWords={reqWithheld} />
      {raw.response ? (
        <SideBlock kind="response" side={raw.response} withheldWords={[]} />
      ) : (
        <section aria-label="response" data-wire-side="response">
          <span className="eyebrow">response</span>{" "}
          <span className="font-mono text-[11px] text-muted-foreground">
            none: the exchange got no response (a transport error)
          </span>
        </section>
      )}
    </div>
  )
}

/**
 * WireView is the Wire tab: the step's attempts, each its own block,
 * labelled as the Request pane labels them; `env` is the whole record's
 * hole (hidden for a read-scoped token).
 */
export function WireView({ rows, runId, env }: { rows: RequestRow[]; runId: string; env?: Holed }) {
  if (env?.badge)
    return (
      <div data-wire="" className="pt-2">
        <HoleBadge hole={env.badge} reason={env.reason} fix={env.fix} detail />
      </div>
    )
  return (
    <div data-wire="" className="space-y-4 pt-2">
      {rows.map((r) => (
        <AttemptWire key={r.index} row={r} runId={runId} label={rows.length > 1 ? `attempt ${r.attempt}` : undefined} />
      ))}
    </div>
  )
}

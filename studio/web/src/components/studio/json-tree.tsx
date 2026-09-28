// A collapsible JSON tree for the raw explorer's document view: every
// object and array folds, strings are clipped inline with the full
// value one click away, and each node's path is copyable. Keeps a
// 200 KB run document readable where a flat pre would not.
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import { pretty } from "@/lib/json"
import { CopyButton } from "@/components/studio/codewin"

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function Primitive({ value }: { value: unknown }) {
  const [full, setFull] = useState(false)
  if (typeof value === "string") {
    const clipped = !full && value.length > 120
    return (
      <span
        className={`text-code-str ${clipped ? "cursor-pointer" : ""}`}
        title={clipped ? "click to expand" : undefined}
        onClick={clipped ? () => setFull(true) : undefined}
      >
        {JSON.stringify(clipped ? `${value.slice(0, 120)}…` : value)}
        {clipped ? (
          <span className="text-code-mut"> +{value.length - 120}</span>
        ) : null}
      </span>
    )
  }
  if (typeof value === "number")
    return <span className="text-ev-tool-code">{String(value)}</span>
  if (typeof value === "boolean" || value === null)
    return <span className="text-ev-error">{String(value)}</span>
  return <span className="text-code-mut">{String(value)}</span>
}

function summary(v: unknown): string {
  if (Array.isArray(v)) return `[${v.length}]`
  if (isObj(v)) {
    const keys = Object.keys(v)
    return `{${keys.length}} ${keys.slice(0, 4).join(", ")}${keys.length > 4 ? ", …" : ""}`
  }
  return ""
}

export function JsonNode({
  name,
  value,
  depth,
  openDepth,
  path,
  last,
}: {
  name?: string
  value: unknown
  depth: number
  /** Nodes shallower than this start open. */
  openDepth: number
  path: string
  last: boolean
}) {
  const container = Array.isArray(value) || isObj(value)
  const [open, setOpen] = useState(depth < openDepth)
  const key =
    name !== undefined ? (
      <span className="text-code-ink">
        {Array.isArray(value) || isObj(value) ? name : JSON.stringify(name)}
        <span className="text-code-mut">: </span>
      </span>
    ) : null
  if (!container) {
    return (
      <div className="pl-4 leading-relaxed">
        {key}
        <Primitive value={value} />
        {last ? "" : <span className="text-code-mut">,</span>}
      </div>
    )
  }
  const entries: [string, unknown][] = Array.isArray(value)
    ? value.map((v, i) => [String(i), v])
    : Object.entries(value)
  const [o, c] = Array.isArray(value) ? ["[", "]"] : ["{", "}"]
  return (
    <div className={depth > 0 ? "pl-4" : ""}>
      <div className="group/node flex items-center gap-1 leading-relaxed">
        <button
          type="button"
          className="-ml-4 flex w-4 items-center justify-center text-code-mut hover:text-code-ink"
          aria-label={open ? "collapse" : "expand"}
          aria-expanded={open}
          onClick={() => setOpen((x) => !x)}
        >
          <ChevronRight
            className={`size-3 transition-transform ${open ? "rotate-90" : ""}`}
          />
        </button>
        <span
          className="cursor-pointer select-none"
          onClick={() => setOpen((x) => !x)}
        >
          {key}
          <span className="text-code-mut">{o}</span>
          {!open ? (
            <span className="text-code-mut"> {summary(value)} </span>
          ) : null}
          {!open ? (
            <span className="text-code-mut">
              {c}
              {last ? "" : ","}
            </span>
          ) : null}
        </span>
        <span className="opacity-0 group-hover/node:opacity-100">
          <CopyButton text={pretty(value)} label={`copy ${path || "root"}`} />
        </span>
      </div>
      {open ? (
        <>
          {entries.map(([k, v], i) => (
            <JsonNode
              key={k}
              name={Array.isArray(value) ? undefined : k}
              value={v}
              depth={depth + 1}
              openDepth={openDepth}
              path={path ? `${path}.${k}` : k}
              last={i === entries.length - 1}
            />
          ))}
          <div className="leading-relaxed text-code-mut">
            {c}
            {last ? "" : ","}
          </div>
        </>
      ) : null}
    </div>
  )
}

export function JsonTree({
  value,
  openDepth = 2,
  className = "",
}: {
  value: unknown
  openDepth?: number
  className?: string
}) {
  return (
    <div className={`pl-4 font-mono text-[12.5px] ${className}`}>
      <JsonNode value={value} depth={0} openDepth={openDepth} path="" last />
    </div>
  )
}

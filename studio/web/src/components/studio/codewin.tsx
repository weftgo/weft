// The code window (site .codewin): dark in both themes, the only
// shadow in the UI. Machine output lives here — Geist Mono. Inside a
// run page the window is a working surface, not a hero: a slim bar
// with the title, the size, a copy button, and a height cap that
// keeps a 60 KB tool result from becoming the page (expand to see
// it all). JSON text is pretty-printed and coloured.
import { Check, ChevronsDownUp, ChevronsUpDown, Copy } from "lucide-react"
import { useMemo, useState } from "react"

import { copyText, pretty, tokenize, tryJSON } from "@/lib/json"
import { bytes } from "@/lib/summarize"
import { Button } from "@/components/ui/button"

export function CopyButton({
  text,
  className = "",
  label = "copy",
}: {
  text: string
  className?: string
  label?: string
}) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className={`text-code-mut hover:text-code-ink ${className}`}
      aria-label={label}
      title={label}
      onClick={(e) => {
        e.stopPropagation()
        void copyText(text).then((ok) => {
          if (!ok) return
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        })
      }}
    >
      {done ? <Check data-slot="icon" /> : <Copy data-slot="icon" />}
    </Button>
  )
}

const tokenClass: Record<string, string> = {
  key: "text-code-ink",
  string: "text-code-str",
  number: "text-code-num",
  literal: "text-ev-error",
  punct: "text-code-mut",
  ws: "",
}

/** Coloured JSON: the tokenizer's spans, nothing else. */
export function JsonText({ text }: { text: string }) {
  const toks = useMemo(() => tokenize(text), [text])
  return (
    <>
      {toks.map((t, i) =>
        t.kind === "ws" ? (
          t.text
        ) : (
          <span key={i} className={tokenClass[t.kind]}>
            {t.text}
          </span>
        )
      )}
    </>
  )
}

/** Lines past this many are folded behind "show all". */
const FOLD_LINES = 24

export function CodeWin({
  title,
  text,
  copy = true,
  json,
  dots = false,
  fold = true,
  className = "",
  tone,
}: {
  title?: string
  text: string
  copy?: boolean
  /** Force JSON colouring; by default JSON-looking text is detected. */
  json?: boolean
  /** The site's three dots — for the raw explorer's top windows. */
  dots?: boolean
  /** Cap the height at FOLD_LINES with an expand control. */
  fold?: boolean
  className?: string
  /** A tint for the body text: errors read mustard, results moss. */
  tone?: "error" | "result"
}) {
  const parsed = useMemo(
    () => (json === false ? null : tryJSON(text)),
    [json, text]
  )
  const shown = parsed !== null ? pretty(parsed) : text
  const lines = shown.split("\n").length
  const size = new TextEncoder().encode(text).length
  const [open, setOpen] = useState(false)
  const folded = fold && !open && lines > FOLD_LINES
  return (
    <div className={`codewin ${className}`}>
      <div className="codewin-bar">
        {dots ? (
          <>
            <span className="codewin-dot" />
            <span className="codewin-dot" />
            <span className="codewin-dot on" />
          </>
        ) : null}
        {title ? (
          <span className="truncate font-mono text-[11px] text-code-mut">
            {title}
          </span>
        ) : null}
        <span className="ml-auto shrink-0 font-mono text-[10px] text-code-mut tabular-nums">
          {parsed !== null ? "json · " : ""}
          {bytes(size)}
          {lines > 1 ? ` · ${lines} lines` : ""}
        </span>
        {fold && lines > FOLD_LINES ? (
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-code-mut hover:text-code-ink"
            aria-label={open ? "collapse" : "show all"}
            title={open ? "collapse" : `show all ${lines} lines`}
            onClick={() => setOpen((o) => !o)}
          >
            {open ? (
              <ChevronsDownUp data-slot="icon" />
            ) : (
              <ChevronsUpDown data-slot="icon" />
            )}
          </Button>
        ) : null}
        {copy ? <CopyButton text={shown} /> : null}
      </div>
      <pre
        className={`${folded ? "max-h-[26rem]" : ""} ${
          tone === "error"
            ? "text-ev-error"
            : tone === "result" && parsed === null
              ? "text-code-str"
              : ""
        }`}
        data-folded={folded ? "" : undefined}
      >
        <code>{parsed !== null ? <JsonText text={shown} /> : shown}</code>
      </pre>
      {folded ? (
        <button
          type="button"
          className="codewin-more"
          onClick={() => setOpen(true)}
        >
          show all {lines} lines · {bytes(size)}
        </button>
      ) : null}
    </div>
  )
}

/** Pretty-printed JSON in a code window. */
export function JsonWin({
  title,
  value,
  className = "",
  dots,
  fold,
}: {
  title?: string
  value: unknown
  className?: string
  dots?: boolean
  fold?: boolean
}) {
  const text = pretty(value)
  return (
    <CodeWin
      title={title}
      text={text}
      json
      className={className}
      dots={dots}
      fold={fold}
    />
  )
}

// The code window (site .codewin): dark in both themes, three dots,
// the only shadow in the UI. Machine output lives here — Geist Mono.
import { Check, Copy } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="text-code-mut hover:text-code-ink"
      aria-label="copy"
      onClick={() => {
        try {
          void navigator.clipboard.writeText(text)
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        } catch {
          // clipboard unavailable — the text is selectable anyway
        }
      }}
    >
      {done ? <Check data-slot="icon" /> : <Copy data-slot="icon" />}
    </Button>
  )
}

export function CodeWin({
  title,
  text,
  copy = true,
  className = "",
}: {
  title?: string
  text: string
  copy?: boolean
  className?: string
}) {
  return (
    <div className={`codewin ${className}`}>
      <div className="codewin-bar">
        <span className="codewin-dot" />
        <span className="codewin-dot" />
        <span className="codewin-dot on" />
        {title ? (
          <span className="ml-2 truncate font-mono text-[11px] text-code-mut">
            {title}
          </span>
        ) : null}
        {copy ? (
          <span className="ml-auto">
            <CopyButton text={text} />
          </span>
        ) : null}
      </div>
      <pre>
        <code>{text}</code>
      </pre>
    </div>
  )
}

/** Pretty-printed JSON in a code window. */
export function JsonWin({
  title,
  value,
  className = "",
}: {
  title?: string
  value: unknown
  className?: string
}) {
  const text = JSON.stringify(value, null, 2)
  return <CodeWin title={title} text={text} className={className} />
}

// A GenAI span's messages as the OTel GenAI conventions carry them
// (lib/genai.ts genaiChat): role-labelled, their parts drawn — text,
// reasoning, a tool call's name and arguments, a tool result. The trace
// page's chat view and a derived run's story (docs/record-contract.md
// §9) draw them with this one renderer.
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import type { ChatMessage, ChatPart } from "@/lib/genai"
import { CODE_SURFACE, JsonTree } from "@/components/studio/json-tree"
import { STATUS_TEXT } from "@/lib/status-class"

const ROLE_TONE: Record<string, string> = {
  system: "border-dashed bg-secondary/30",
  user: "bg-secondary/50",
  assistant: "border-thread/30 bg-thread/5",
  tool: "bg-background",
}

function PartView({ part }: { part: ChatPart }) {
  switch (part.kind) {
    case "text":
      return <div className="text-xs whitespace-pre-wrap">{part.text}</div>
    case "reasoning":
      return (
        <div className="text-xs whitespace-pre-wrap text-muted-foreground italic">
          {part.text}
        </div>
      )
    case "tool_call":
      // As the run page's calls: the name, the arguments folded.
      return (
        <details
          className="font-mono text-[11px]"
          data-chat-tool-call={part.name}
        >
          <summary className="cursor-pointer">
            <span className={`${STATUS_TEXT.tool}`}>
              ⚙ {part.name || "(unnamed tool)"}
            </span>
            {part.id ? <span className="text-faint"> · {part.id}</span> : null}
          </summary>
          <div className="pt-1">
            <JsonTree
              value={part.arguments ?? null}
              openDepth={2}
              className={CODE_SURFACE}
            />
          </div>
        </details>
      )
    case "tool_result":
      return (
        <div className="font-mono text-[11px]">
          <span className="text-faint">
            result{part.id ? ` · ${part.id}` : ""}
          </span>
          {typeof part.response === "string" ? (
            <div className="text-xs whitespace-pre-wrap">{part.response}</div>
          ) : (
            <JsonTree
              value={part.response ?? null}
              openDepth={2}
              className={CODE_SURFACE}
            />
          )}
        </div>
      )
    default:
      return (
        <div className="font-mono text-[11px]">
          <span className="text-faint">{part.type}</span>
          <JsonTree value={part.value} openDepth={1} />
        </div>
      )
  }
}

/** Role-labelled messages; with `fold`, the earlier inputs behind one
 * toggle (each model call's input repeats the conversation so far). */
export function ChatMessages({
  messages,
  problems = [],
  fold = false,
}: {
  messages: ChatMessage[]
  problems?: string[]
  fold?: boolean
}) {
  const [open, setOpen] = useState(false)
  const lastInput = messages.reduce(
    (n, m, i) => (m.side === "input" ? i : n),
    -1
  )
  const hidden = fold && !open ? Math.max(0, lastInput) : 0
  return (
    <div className="space-y-2">
      {hidden > 0 ? (
        <button
          type="button"
          className="flex items-center gap-1 font-mono text-[11px] text-thread-ink hover:underline"
          aria-expanded={false}
          onClick={() => setOpen(true)}
        >
          <ChevronRight className="size-3" data-slot="icon" />
          {hidden} earlier {hidden === 1 ? "message" : "messages"}
        </button>
      ) : null}
      <ol className="space-y-2" aria-label="messages">
        {messages.slice(hidden).map((m, i) => (
          <li
            key={i + hidden}
            data-chat-role={m.role}
            data-chat-side={m.side}
            className={`max-w-2xl space-y-1 rounded-lg border px-3 py-2 ${ROLE_TONE[m.role] ?? "bg-background"}`}
          >
            <div className="flex items-center gap-2 font-mono text-[10px] tracking-wide text-muted-foreground uppercase">
              <span>{m.role}</span>
              {m.finishReason ? (
                <span className="normal-case">· {m.finishReason}</span>
              ) : null}
            </div>
            {m.parts.length ? (
              m.parts.map((p, j) => <PartView key={j} part={p} />)
            ) : (
              <span className="font-mono text-[11px] text-faint">
                (no parts)
              </span>
            )}
          </li>
        ))}
      </ol>
      {problems.map((p) => (
        <p key={p} className={`font-mono text-[11px] ${STATUS_TEXT.bad}`}>
          {p} — the raw value is in the attributes
        </p>
      ))}
    </div>
  )
}

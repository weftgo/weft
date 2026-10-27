// The trace page's GenAI chat view (plan H7): a span's messages as the
// OTel GenAI conventions carry them, read from either key family —
//   - the current one: gen_ai.input.messages / gen_ai.output.messages
//     (JSON arrays of {role, parts[, finish_reason]}), beside
//     gen_ai.system_instructions (a JSON array of parts);
//   - the legacy one: gen_ai.prompt / gen_ai.completion (plain text),
//     their indexed forms gen_ai.prompt.N.role|content and
//     gen_ai.completion.N.role|content (with
//     gen_ai.{prompt,completion}.N.tool_calls.M.id|name|arguments — a
//     prompt's are the assistant turns of the history, as OpenLLMetry
//     writes them — and gen_ai.prompt.N.tool_call_id, the id a tool
//     result answers), and the
//     event-era gen_ai.content.prompt / gen_ai.content.completion.
// The current family wins when both are present. Nothing is
// reconstructed: a span carrying neither reads as not recorded. Plain
// TypeScript — no React, no DOM.

/** One part of a message, as the chat view draws it. */
export type ChatPart =
  | { kind: "text"; text: string }
  | { kind: "reasoning"; text: string }
  | { kind: "tool_call"; id: string; name: string; arguments: unknown }
  | { kind: "tool_result"; id: string; response: unknown }
  /** A part type the view does not draw itself (a blob, a uri, a
   * file, a type from a newer convention): shown as its JSON. */
  | { kind: "other"; type: string; value: unknown }

export interface ChatMessage {
  /** system, user, assistant, tool — or whatever role the span named. */
  role: string
  parts: ChatPart[]
  /** Which side of the call it was on: what the model was sent, or
   * what it answered. */
  side: "input" | "output"
  finishReason?: string
}

export interface GenAIChat {
  /** Which key family the messages came from; null when the span
   * carries none. */
  family: "semconv" | "legacy" | null
  messages: ChatMessage[]
  /** The keys read (the attributes list keeps them all, raw). */
  keys: string[]
  /** A value the view could not parse, said rather than dropped. */
  problems: string[]
}

export const INPUT_KEY = "gen_ai.input.messages"
export const OUTPUT_KEY = "gen_ai.output.messages"
export const SYSTEM_KEY = "gen_ai.system_instructions"

/** The operations that are model calls (the chat view's spans). */
const MODEL_OPS = new Set(["chat", "text_completion", "generate_content"])

/** isModelCall: the span is a GenAI model call (its operation). */
export function isModelCall(attrs: Record<string, unknown>): boolean {
  const op = attrs["gen_ai.operation.name"]
  return typeof op === "string" && MODEL_OPS.has(op)
}

/** A JSON attribute: an exporter sends a string; a store may hand
 * back the parsed value. */
function jsonValue(v: unknown): { ok: true; value: unknown } | { ok: false } {
  if (typeof v !== "string") return { ok: true, value: v }
  try {
    return { ok: true, value: JSON.parse(v) as unknown }
  } catch {
    return { ok: false }
  }
}

function str(v: unknown): string {
  return typeof v === "string"
    ? v
    : v === undefined || v === null
      ? ""
      : JSON.stringify(v)
}

function part(p: unknown): ChatPart {
  if (typeof p === "string") return { kind: "text", text: p }
  if (!p || typeof p !== "object")
    return { kind: "other", type: typeof p, value: p }
  const o = p as Record<string, unknown>
  const type = typeof o.type === "string" ? o.type : ""
  switch (type) {
    case "text":
      return { kind: "text", text: str(o.content ?? o.text) }
    case "reasoning":
      return { kind: "reasoning", text: str(o.content ?? o.text) }
    case "tool_call":
      return {
        kind: "tool_call",
        id: str(o.id),
        name: str(o.name),
        arguments:
          typeof o.arguments === "string"
            ? parsedOrText(o.arguments)
            : o.arguments,
      }
    case "tool_call_response":
      return {
        kind: "tool_result",
        id: str(o.id),
        response: o.response ?? o.result,
      }
    default:
      return { kind: "other", type: type || "unknown", value: p }
  }
}

/** Arguments sent as a JSON string read as their JSON when they parse. */
function parsedOrText(s: string): unknown {
  try {
    return JSON.parse(s) as unknown
  } catch {
    return s
  }
}

function semconvMessages(
  v: unknown,
  side: "input" | "output",
  key: string,
  problems: string[]
): ChatMessage[] {
  const parsed = jsonValue(v)
  if (!parsed.ok || !Array.isArray(parsed.value)) {
    problems.push(`${key} is not a JSON array of messages`)
    return []
  }
  return parsed.value.map((m): ChatMessage => {
    const o = (m && typeof m === "object" ? m : {}) as Record<string, unknown>
    const parts = Array.isArray(o.parts)
      ? o.parts.map(part)
      : o.content !== undefined
        ? [
            part(
              typeof o.content === "string"
                ? { type: "text", content: o.content }
                : o.content
            ),
          ]
        : []
    const msg: ChatMessage = {
      role: str(o.role) || (side === "input" ? "user" : "assistant"),
      parts,
      side,
    }
    if (typeof o.finish_reason === "string" && o.finish_reason)
      msg.finishReason = o.finish_reason
    return msg
  })
}

const INDEXED =
  /^gen_ai\.(prompt|completion)\.(\d+)\.(role|content|tool_call_id)$/
const INDEXED_CALL =
  /^gen_ai\.(prompt|completion)\.(\d+)\.tool_calls\.(\d+)\.(id|name|arguments)$/

function legacyMessages(
  attrs: Record<string, unknown>,
  keys: string[]
): ChatMessage[] {
  const out: ChatMessage[] = []
  // The indexed forms first: they carry roles.
  type Slot = {
    role?: string
    content?: string
    toolCallID?: string
    calls: Map<number, { id?: string; name?: string; arguments?: string }>
  }
  const sides: Record<"prompt" | "completion", Map<number, Slot>> = {
    prompt: new Map(),
    completion: new Map(),
  }
  const slot = (side: "prompt" | "completion", n: number): Slot => {
    let s = sides[side].get(n)
    if (!s) sides[side].set(n, (s = { calls: new Map() }))
    return s
  }
  for (const [k, v] of Object.entries(attrs)) {
    const m = INDEXED.exec(k)
    if (m) {
      keys.push(k)
      const s = slot(m[1] as "prompt" | "completion", Number(m[2]))
      if (m[3] === "role") s.role = str(v)
      else if (m[3] === "tool_call_id") s.toolCallID = str(v)
      else s.content = str(v)
      continue
    }
    const c = INDEXED_CALL.exec(k)
    if (c) {
      keys.push(k)
      const s = slot(c[1] as "prompt" | "completion", Number(c[2]))
      const n = Number(c[3])
      const call = s.calls.get(n) ?? {}
      call[c[4] as "id" | "name" | "arguments"] = str(v)
      s.calls.set(n, call)
    }
  }
  for (const side of ["prompt", "completion"] as const) {
    const slots = [...sides[side].entries()].sort((a, b) => a[0] - b[0])
    for (const [, s] of slots) {
      const parts: ChatPart[] = []
      // A tool message answering a call: its content is that result.
      if (s.toolCallID !== undefined)
        parts.push({
          kind: "tool_result",
          id: s.toolCallID,
          response: s.content ?? "",
        })
      else if (s.content) parts.push({ kind: "text", text: s.content })
      for (const [, call] of [...s.calls.entries()].sort((a, b) => a[0] - b[0]))
        parts.push({
          kind: "tool_call",
          id: call.id ?? "",
          name: call.name ?? "",
          arguments:
            call.arguments !== undefined
              ? parsedOrText(call.arguments)
              : undefined,
        })
      out.push({
        role:
          s.role ||
          (s.toolCallID !== undefined
            ? "tool"
            : side === "prompt"
              ? "user"
              : "assistant"),
        parts,
        side: side === "prompt" ? "input" : "output",
      })
    }
  }
  if (out.length) return out
  // The plain keys: one text each, no roles beyond the side.
  for (const [side, names] of [
    ["input", ["gen_ai.prompt", "gen_ai.content.prompt"]],
    ["output", ["gen_ai.completion", "gen_ai.content.completion"]],
  ] as const) {
    for (const k of names) {
      const v = attrs[k]
      if (typeof v === "string" && v) {
        keys.push(k)
        out.push({
          role: side === "input" ? "user" : "assistant",
          parts: [{ kind: "text", text: v }],
          side,
        })
        break
      }
    }
  }
  return out
}

/**
 * genaiChat reads a span's GenAI messages: the current key family when
 * any of its keys is present, else the legacy one, else none (family
 * null — the view says not recorded; nothing is reconstructed).
 */
export function genaiChat(attrs: Record<string, unknown>): GenAIChat {
  const problems: string[] = []
  const keys: string[] = []
  if (INPUT_KEY in attrs || OUTPUT_KEY in attrs || SYSTEM_KEY in attrs) {
    const messages: ChatMessage[] = []
    if (SYSTEM_KEY in attrs) {
      keys.push(SYSTEM_KEY)
      const parsed = jsonValue(attrs[SYSTEM_KEY])
      if (parsed.ok && Array.isArray(parsed.value))
        messages.push({
          role: "system",
          parts: parsed.value.map(part),
          side: "input",
        })
      else if (typeof attrs[SYSTEM_KEY] === "string")
        messages.push({
          role: "system",
          parts: [{ kind: "text", text: attrs[SYSTEM_KEY] }],
          side: "input",
        })
    }
    if (INPUT_KEY in attrs) {
      keys.push(INPUT_KEY)
      messages.push(
        ...semconvMessages(attrs[INPUT_KEY], "input", INPUT_KEY, problems)
      )
    }
    if (OUTPUT_KEY in attrs) {
      keys.push(OUTPUT_KEY)
      messages.push(
        ...semconvMessages(attrs[OUTPUT_KEY], "output", OUTPUT_KEY, problems)
      )
    }
    return { family: "semconv", messages, keys, problems }
  }
  const messages = legacyMessages(attrs, keys)
  return { family: messages.length ? "legacy" : null, messages, keys, problems }
}

/** The honesty line's reason when a model call carries no messages. */
export const NO_MESSAGES_REASON =
  "this span carries no gen_ai messages (gen_ai.input.messages / gen_ai.output.messages, or the legacy gen_ai.prompt / gen_ai.completion): content capture was off, or the instrumentation keeps messages off its spans (weft's own chat spans do: a weft run's messages are on its run page)"

/** The event-era GenAI log events (OTel's gen_ai.<role>.message and
 * gen_ai.choice): an OTLP log record whose EventName is one of them
 * carries one message as its body's JSON. */
const EVENT_ROLE = /^gen_ai\.(system|user|assistant|tool)\.message$/

/**
 * eventMessage reads a GenAI log event (UX-H-10) — an app log row whose
 * event_name is gen_ai.<role>.message or gen_ai.choice — as one
 * message: the role from the name (gen_ai.choice: the message's own,
 * else assistant, on the output side), its content as text or parts,
 * an assistant's tool_calls ({id, function: {name, arguments}}) and a
 * tool message's id as their parts, a choice's finish_reason. A body
 * that is not JSON is the message's text. Undefined for any other row:
 * a plain log line. With no event_name the row's event.name attribute
 * (older SDKs) names the event. Nothing is read that the row did not carry — the
 * logs route's own rules (a read token reads no logs) stand.
 */
export function eventMessage(
  name: string | undefined,
  body: string,
  attrs?: Record<string, unknown>
): ChatMessage | undefined {
  // An older SDK sends the name as the event.name attribute, the
  // record's EventName empty (U33.2-fix).
  const ev = attrs?.["event.name"]
  const eventName = name || (typeof ev === "string" ? ev : "")
  const m = eventName ? EVENT_ROLE.exec(eventName) : null
  const choice = eventName === "gen_ai.choice"
  if (!m && !choice) return undefined
  const parsed = jsonValue(body)
  const o = (
    parsed.ok && parsed.value && typeof parsed.value === "object"
      ? parsed.value
      : { content: body }
  ) as Record<string, unknown>
  const msg = (
    choice && o.message && typeof o.message === "object" ? o.message : o
  ) as Record<string, unknown>
  const role = m ? m[1] : str(msg.role) || "assistant"
  const parts: ChatPart[] = []
  const c = msg.content
  if (role === "tool" && msg.id !== undefined)
    parts.push({ kind: "tool_result", id: str(msg.id), response: c ?? "" })
  else if (Array.isArray(c)) parts.push(...c.map(part))
  else if (c !== undefined && c !== null && c !== "") parts.push(part(c))
  if (Array.isArray(msg.tool_calls))
    for (const raw of msg.tool_calls as unknown[]) {
      const tc = (raw && typeof raw === "object" ? raw : {}) as Record<
        string,
        unknown
      >
      const fn = (
        tc.function && typeof tc.function === "object" ? tc.function : tc
      ) as Record<string, unknown>
      parts.push({
        kind: "tool_call",
        id: str(tc.id),
        name: str(fn.name),
        arguments:
          typeof fn.arguments === "string"
            ? parsedOrText(fn.arguments)
            : fn.arguments,
      })
    }
  const out: ChatMessage = {
    role,
    parts,
    side: choice ? "output" : "input",
  }
  if (typeof o.finish_reason === "string" && o.finish_reason)
    out.finishReason = o.finish_reason
  return out
}

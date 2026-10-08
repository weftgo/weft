// The whole integration: one import from the npm package. Importing
// the root entry defines <weft-devtools> and mounts the dock (the
// package's panel.js, the same bytes as /studio/panel.js); mount()
// replaces that self-mounted dock with one configured here.
//
// The other form needs no code at all: `import "@weftgo/devtools"`
// alone mounts the dock with the endpoint taken from
// <meta name="weft:endpoint" content="/studio/"> (or the page's own
// directory), scoped by the data-weft-scope marker in index.html; it
// removes itself silently when Studio does not answer, where mount()
// shows one "Studio not reachable" line instead.
import { mount } from "@weftgo/devtools"

// /studio/ is studio-local's embedded Studio, proxied by vite.config.ts
// in dev (same origin: no token, no CORS). VITE_WEFT_STUDIO points the
// panel anywhere else.
mount({ endpoint: import.meta.env.VITE_WEFT_STUDIO ?? "/studio/" })

// The fake chat: one POST /run per question, the reply rendered as
// text. studio-local's /run answers with the Weft-Scope header, which
// the panel reads on loopback to follow the turn.
const log = document.querySelector<HTMLDivElement>("#log")!
const form = document.querySelector<HTMLFormElement>("#ask")!

function line(cls: string, text: string) {
  const p = document.createElement("p")
  p.className = cls
  p.textContent = text
  log.append(p)
}

form.addEventListener("submit", async (ev) => {
  ev.preventDefault()
  const input = form.elements.namedItem("text") as HTMLInputElement
  const text = input.value.trim() || "where is order 42?"
  input.value = ""
  line("you", `> ${text}`)
  try {
    const res = await fetch("/run", { method: "POST", body: text })
    line("bot", (await res.text()).trim())
  } catch (err) {
    line("bot", `error: ${String(err)}`)
  }
})

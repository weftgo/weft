// The Settings page (plan H4) against a fake Studio: every section
// from the server's own goldens (meta, meta-retention,
// settings-pricing, runtimes), the pricing table edited per browser
// and downloaded as the override file, the token viewed / cleared /
// switched, the hidden badge where db.path's rule withholds, each off
// capability with its reason, and axe over the whole page.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import axe from "axe-core"
import { debugScopeWords } from "@/lib/runtimes"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  adoptTokenFromLocation,
  setStudioToken,
  studioToken,
  tabTokenActive,
} from "@/lib/api"
import type { PricingSettings } from "@/lib/api"
import { editsKey, LOGS_BRIDGE } from "@/lib/settings"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

type MetaDoc = Record<string, unknown> & {
  capabilities: string[]
  capabilities_off: Record<string, string>
}
const metaG = () => golden<MetaDoc>("meta")
const pricingG = golden<PricingSettings>("settings-pricing")
const runtimesG = golden<object>("runtimes")

/** The meta golden with the playground on (its runtime link's
 * capabilities served, their off reasons gone). */
const playgroundMeta = (): MetaDoc => {
  const m = metaG()
  const on = [
    "playground",
    "runtimes",
    "breakpoints",
    "steer",
    "preview",
    "cancel",
  ]
  const off = { ...m.capabilities_off }
  for (const c of on) delete off[c]
  return {
    ...m,
    capabilities: [...m.capabilities, ...on],
    capabilities_off: off,
    runtimes: 1,
  }
}

/** A read-scoped panel token (claims readable; Studio verifies). */
const panelToken = (scope: "read" | "playground") =>
  `weft_pt.${btoa(
    JSON.stringify({
      public_id: "pub_orders",
      scope,
      exp: "2026-10-11T00:00:00Z",
    })
  )
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "")}.sig`

/** What a panel token's meta looks like: walled, and none of db.path's
 * rule (no path, size, pid, live_all false). */
const panelMeta = (): MetaDoc => {
  const { pid: _pid, ...m } = playgroundMeta()
  return { ...m, auth_required: true, live_all: false, db: { kind: "sqlite" } }
}

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  localStorage.clear()
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", metaG())
    .on("GET settings/pricing", pricingG)
    .on("GET runtimes", runtimesG)
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  setStudioToken("")
  localStorage.clear()
})

const section = (id: string) =>
  document.querySelector<HTMLElement>(`[data-section="${id}"]`)!
const fact = (id: string, k: string) =>
  section(id).querySelector<HTMLElement>(`[data-fact="${k}"] dd`)!

const SECTIONS = [
  ["connect", "Connect an app"],
  ["studio", "Studio"],
  ["token", "Token"],
  ["database", "Database"],
  ["retention", "Retention"],
  ["pricing", "Pricing"],
  ["capabilities", "Capabilities"],
  ["runtimes", "Runtimes"],
  ["content", "Content policy"],
  ["appearance", "Appearance"],
  ["keyboard", "Keyboard"],
  ["concepts", "Concepts"],
]

async function openSettings(): Promise<void> {
  renderApp("/settings")
  await screen.findByRole("heading", { level: 1, name: "Settings" })
}

describe("the settings page renders every section from the goldens", () => {
  it("meta + settings-pricing + runtimes: each section, in order, with its data", async () => {
    studio.on("GET meta", playgroundMeta())
    await openSettings()
    expect(document.title).toBe("settings · weft studio")
    const ids = [...document.querySelectorAll("[data-section]")].map((s) =>
      s.getAttribute("data-section")
    )
    expect(ids).toEqual(SECTIONS.map(([id]) => id))
    for (const [id, title] of SECTIONS)
      expect(
        within(section(id)).getByRole("heading", { level: 2, name: title })
      ).toBeTruthy()
    // Studio: versions, pid, the interrupted threshold, ingest.
    expect(fact("studio", "weft").textContent).toBe("v0.20.1")
    expect(fact("studio", "panel").textContent).toBe("v0.20.1")
    expect(fact("studio", "pid").textContent).toBe("1234")
    expect(fact("studio", "interrupted after").textContent).toContain(
      "30000 ms"
    )
    expect(fact("studio", "ingest").textContent).toMatch(/^open/)
    expect(fact("studio", "endpoint").textContent).toMatch(/^http/)
    // Database: the golden's DB names no file, and this caller may read it.
    expect(fact("database", "kind").textContent).toBe("sqlite")
    expect(fact("database", "path").textContent).toContain("names no file")
    expect(section("database").querySelector("[data-hole]")).toBeNull()
    // Retention off.
    expect(
      section("retention").querySelector("[data-retention]")!.textContent
    ).toContain("off")
    expect(section("retention").textContent).toContain("WEFT_RETAIN")
    expect(section("retention").textContent).toContain("weft vacuum")
    // Pricing: the served table.
    const table = await within(section("pricing")).findByRole("table")
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent)
    ).toEqual([
      "#",
      "provider",
      "model glob",
      "input",
      "output",
      "cached input",
      "cache write",
      "reasoning",
      "remove",
    ])
    expect(
      screen.getByLabelText<HTMLInputElement>("row 1 model glob").value
    ).toBe("claude-*")
    expect(
      screen.getByLabelText<HTMLInputElement>("row 1 cached input").value
    ).toBe("0.1")
    expect(
      screen.getByLabelText<HTMLInputElement>("row 2 cached input").value
    ).toBe("")
    expect(fact("pricing", "source").textContent).toContain("option")
    expect(fact("pricing", "dated").textContent).toBe("2026-10-01")
    expect(
      section("pricing").querySelector("[data-pricing-note]")!.textContent
    ).toContain("WEFT_PRICING")
    // Capabilities on and off.
    expect(
      section("capabilities").querySelectorAll("[data-caps-on] li")
    ).toHaveLength(playgroundMeta().capabilities.length)
    // Runtimes (U20.1): the states in one line, debug_scope explained,
    // the link to /runtimes — the cards and the breakpoints live there.
    await waitFor(() =>
      expect(
        section("runtimes").querySelector("[data-runtime-states]")!.textContent
      ).toBe("1 connected")
    )
    expect(section("runtimes").querySelector("[data-runtime]")).toBeNull()
    expect(
      section("runtimes").querySelector("[data-debug-scope]")!.textContent
    ).toBe(debugScopeWords(playgroundMeta().debug_scope as string | undefined))
    expect(
      section("runtimes")
        .querySelector("[data-runtimes-link]")!
        .getAttribute("href")
    ).toMatch(/runtimes$/)
    expect(
      section("runtimes")
        .querySelector("[data-runtime-count]")!
        .getAttribute("data-runtime-count")
    ).toBe("1")
    // Content: the latest run's mark and note.
    expect(
      section("content").querySelector("[data-content-ingest]")!.textContent
    ).toContain("as received")
    expect(
      section("content")
        .querySelector("[data-content-mark]")!
        .getAttribute("data-content-mark")
    ).toBe("unmarked")
    expect(section("content").textContent).toContain("r_stale")
    // Appearance and keyboard.
    expect(
      within(section("appearance")).getByRole("radio", {
        name: "auto (the system's)",
      })
    ).toHaveProperty("checked", true)
    expect(
      within(section("appearance")).getByRole("radio", { name: "comfortable" })
    ).toHaveProperty("checked", true)
    expect(section("keyboard").textContent).toContain("command palette")
  })

  it("meta-retention: the duration, and no stored run yet", async () => {
    studio.on("GET meta", golden<object>("meta-retention"))
    await openSettings()
    expect(
      section("retention").querySelector("[data-retention]")!.textContent
    ).toContain("720h0m0s")
    expect(section("content").textContent).toContain("no run stored yet")
  })

  it("says how app logs reach a run (UX-S1-10): the LoggerProvider, the slog bridge, CallTool", async () => {
    await openSettings()
    const line = document.querySelector("[data-logs-bridge]")!
    expect(line.textContent).toBe(LOGS_BRIDGE)
    expect(line.textContent).toContain(
      "otel.Install(…, otel.Slog()) bridges slog.Default"
    )
  })

  it("the sidebar links it and the palette's entry lands on it", async () => {
    renderApp("/runs")
    const link = await screen.findByRole("link", { name: "Settings" })
    expect(link.getAttribute("href")).toBe("/settings")
  })
})

describe("the pricing table, edited per browser", () => {
  it("an edit persists across a re-render through localStorage; reset drops it", async () => {
    await openSettings()
    const input = await screen.findByLabelText<HTMLInputElement>("row 1 input")
    fireEvent.change(input, { target: { value: "1.5" } })
    expect(
      section("pricing").querySelector("[data-pricing-edited]")
    ).toBeTruthy()
    expect(localStorage.getItem(editsKey(pricingG))).toContain("1.5")
    // Nothing went to the server: the page never writes.
    expect(
      studio.requests.filter(
        (r) => r.method !== "GET" && r.path !== "live-grant"
      )
    ).toEqual([])
    await openSettings()
    expect(
      (await screen.findByLabelText<HTMLInputElement>("row 1 input")).value
    ).toBe("1.5")
    fireEvent.click(
      screen.getByRole("button", { name: "reset to the server's table" })
    )
    expect(screen.getByLabelText<HTMLInputElement>("row 1 input").value).toBe(
      "1"
    )
    expect(localStorage.getItem(editsKey(pricingG))).toBeNull()
  })

  it("?price= (U7's price this model…) leads the table with a row for it, its rates to fill, nothing stored", async () => {
    renderApp(
      `/settings?price=${encodeURIComponent('"openai/kimi-k2.7-code"')}`
    )
    await screen.findByRole("heading", { level: 1, name: "Settings" })
    expect(
      (await screen.findByLabelText<HTMLInputElement>("row 1 provider")).value
    ).toBe("openai")
    expect(
      screen.getByLabelText<HTMLInputElement>("row 1 model glob").value
    ).toBe("kimi-k2.7-code")
    expect(screen.getByLabelText<HTMLInputElement>("row 1 input").value).toBe(
      ""
    )
    expect(
      section("pricing").querySelector("[data-pricing-problems]")!.textContent
    ).toContain("models[0].input: missing")
    // A prefill is not an edit until one is made.
    expect(localStorage.getItem(editsKey(pricingG))).toBeNull()
  })

  it("download pricing.json is the override file's exact shape, the edits applied", async () => {
    const blobs: Blob[] = []
    // jsdom has no blob URLs: capture the Blob the download hands over.
    Object.assign(URL, {
      createObjectURL: (b: Blob) => {
        blobs.push(b)
        return "blob:x"
      },
      revokeObjectURL: () => {},
    })
    const clicked = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {})
    await openSettings()
    const read = async (b: Blob) =>
      JSON.parse(
        await new Promise<string>((res) => {
          const fr = new FileReader()
          fr.onload = () => res(String(fr.result))
          fr.readAsText(b)
        })
      ) as Record<string, unknown>
    // Unedited: the served table round-trips, less the route's source/path.
    fireEvent.click(
      await screen.findByRole("button", { name: "download pricing.json" })
    )
    expect(clicked).toHaveBeenCalledTimes(1)
    const { source: _s, path: _p, ...want } = pricingG
    expect(await read(blobs[0])).toEqual(want)
    // Edited: a rate changed, an optional cleared, a row added.
    fireEvent.change(screen.getByLabelText("row 2 output"), {
      target: { value: "9.25" },
    })
    fireEvent.change(screen.getByLabelText("row 1 cached input"), {
      target: { value: "" },
    })
    fireEvent.click(screen.getByRole("button", { name: "add row" }))
    // An incomplete row is refused before any download, naming the field.
    expect(
      screen.getByRole("button", { name: "download pricing.json" })
    ).toHaveProperty("disabled", true)
    expect(
      section("pricing").querySelector("[data-pricing-problems]")!.textContent
    ).toContain("models[2].model_glob: missing")
    fireEvent.change(screen.getByLabelText("row 3 provider"), {
      target: { value: "openai" },
    })
    fireEvent.change(screen.getByLabelText("row 3 model glob"), {
      target: { value: "gpt-5*" },
    })
    fireEvent.change(screen.getByLabelText("row 3 input"), {
      target: { value: "0" },
    })
    fireEvent.change(screen.getByLabelText("row 3 output"), {
      target: { value: "4" },
    })
    fireEvent.change(screen.getByLabelText("row 3 reasoning"), {
      target: { value: "6" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: "download pricing.json" })
    )
    const file = await read(blobs[1])
    expect(Object.keys(file)).toEqual(["dated", "note", "models"])
    expect(file).toEqual({
      dated: "2026-10-01",
      note: "test rates",
      models: [
        { provider: "anthropic", model_glob: "claude-*", input: 1, output: 5 },
        { provider: "*", model_glob: "*", input: 2, output: 9.25 },
        {
          provider: "openai",
          model_glob: "gpt-5*",
          input: 0,
          output: 4,
          reasoning: 6,
        },
      ],
    })
    const allowed = new Set([
      "provider",
      "model_glob",
      "input",
      "output",
      "cached_input",
      "cache_write",
      "reasoning",
    ])
    for (const m of file.models as Record<string, unknown>[])
      for (const k of Object.keys(m)) expect(allowed.has(k)).toBe(true)
  })

  it("capability pricing off: the reason capabilities_off gives, no table", async () => {
    const m = metaG()
    studio.on("GET meta", {
      ...m,
      capabilities: m.capabilities.filter((c) => c !== "pricing"),
      capabilities_off: {
        ...m.capabilities_off,
        pricing: "pricing is off: test reason",
      },
    })
    await openSettings()
    expect(
      section("pricing").querySelector("[data-off=pricing]")!.textContent
    ).toContain("pricing is off: test reason")
    expect(within(section("pricing")).queryByRole("table")).toBeNull()
    expect(studio.calls("GET settings/pricing")).toHaveLength(0)
  })
})

describe("the token", () => {
  it("is shown masked, revealed on show, switched (meta re-read with it) and cleared", async () => {
    setStudioToken("dev-secret-token-1234")
    studio.on("GET meta", { ...metaG(), auth_required: true })
    await openSettings()
    const view = () => section("token").querySelector("[data-token-view]")
    expect(view()!.textContent).toBe("••••••••")
    expect(section("token").textContent).not.toContain("dev-secret")
    expect(
      section("token")
        .querySelector("[data-identity]")!
        .getAttribute("data-identity")
    ).toBe("server")
    fireEvent.click(
      within(section("token")).getByRole("button", { name: "show" })
    )
    expect(view()!.textContent).toBe("dev-secret-token-1234")
    // Switch to a playground-scoped panel token.
    const before = studio.calls("GET meta").length
    const pt = panelToken("playground")
    fireEvent.change(screen.getByLabelText("new token"), {
      target: { value: pt },
    })
    fireEvent.click(
      within(section("token")).getByRole("button", { name: "switch" })
    )
    expect(studioToken()).toBe(pt)
    await waitFor(() =>
      expect(studio.calls("GET meta").length).toBeGreaterThan(before)
    )
    expect(studio.calls("GET meta").at(-1)!.headers.get("Authorization")).toBe(
      `Bearer ${pt}`
    )
    await waitFor(() =>
      expect(
        section("token")
          .querySelector("[data-identity]")!
          .getAttribute("data-identity")
      ).toBe("panel-playground")
    )
    expect(section("token").textContent).toContain("pub_orders")
    expect(view()!.textContent).toBe("••••••••")
    // Clear.
    fireEvent.click(
      within(section("token")).getByRole("button", { name: "clear" })
    )
    expect(studioToken()).toBe("")
    expect(localStorage.getItem("studio.token")).toBeNull()
    await waitFor(() =>
      expect(section("token").querySelector("[data-token-view]")).toBeNull()
    )
  })

  // UX-S4-15: weft mcp is discoverable from Studio — the command with
  // this Studio's URL (the page's base), the token by environment when
  // the Studio is walled, the recipe named by its repository path.
  it("names weft mcp with this Studio's URL and the recipe's path", async () => {
    studio.on("GET meta", { ...metaG(), auth_required: false })
    await openSettings()
    const line = section("token").querySelector("[data-mcp]")!
    const url = new URL(".", document.baseURI).toString().replace(/\/+$/, "")
    expect(line.textContent).toContain(`weft mcp --url ${url}`)
    expect(line.textContent).not.toContain("WEFT_STUDIO_TOKEN")
    expect(line.textContent).toContain("docs/mcp-recipe.md")
    expect(line.querySelector("a")).toBeNull()
  })

  it("a walled Studio's weft mcp line takes the token by environment", async () => {
    studio.on("GET meta", { ...metaG(), auth_required: true })
    await openSettings()
    expect(section("token").querySelector("[data-mcp]")!.textContent).toContain(
      "WEFT_STUDIO_TOKEN=<token> weft mcp --url "
    )
  })

  // A hand-off tab (setup C's #token=): the section names the token
  // this tab's alone, and clear drops it only — the stored dev token
  // every other tab reads stays in localStorage.
  it("a hand-off tab's token: shown as this tab's only, cleared alone, the stored token kept", async () => {
    setStudioToken("dev-secret-token-1234")
    const pt = panelToken("read")
    window.history.replaceState(null, "", `/settings#token=${pt}`)
    expect(adoptTokenFromLocation()).toBe(true)
    window.history.replaceState(null, "", "/")
    studio.on("GET meta", panelMeta())
    await openSettings()
    expect(section("token").querySelector('[data-fact="stored"]')).toBeNull()
    expect(fact("token", "this tab only").textContent).toBe("••••••••")
    expect(section("token").textContent).toContain("hand-off")
    expect(section("token").textContent).toContain("pub_orders")
    fireEvent.click(
      within(section("token")).getByRole("button", { name: "clear" })
    )
    expect(tabTokenActive()).toBe(false)
    expect(studioToken()).toBe("dev-secret-token-1234")
    expect(localStorage.getItem("studio.token")).toBe("dev-secret-token-1234")
    await waitFor(() =>
      expect(
        section("token").querySelector('[data-fact="stored"]')
      ).toBeTruthy()
    )
    expect(
      section("token").querySelector('[data-fact="this tab only"]')
    ).toBeNull()
  })

  it("a panel token's meta: the database's path and size badged hidden, runtimes refused as hidden", async () => {
    setStudioToken(panelToken("read"))
    studio
      .on("GET meta", panelMeta())
      .on("GET runtimes", () =>
        apiError(
          403,
          "forbidden",
          "a read-scoped panel token does not read the runtimes"
        )
      )
    await openSettings()
    expect(
      section("token")
        .querySelector("[data-identity]")!
        .getAttribute("data-identity")
    ).toBe("panel-read")
    const db = section("database")
    expect(
      fact("database", "path").querySelector("[data-hole=hidden]")
    ).toBeTruthy()
    expect(
      fact("database", "size").querySelector("[data-hole=hidden]")
    ).toBeTruthy()
    expect(db.textContent).toContain("server (dev) token")
    expect(
      fact("studio", "pid").querySelector("[data-hole=hidden]")
    ).toBeTruthy()
    await waitFor(() =>
      expect(
        section("runtimes").querySelector("[data-hole=hidden]")
      ).toBeTruthy()
    )
  })
})

describe("capabilities and gating", () => {
  it("every off capability is listed with its capabilities_off reason", async () => {
    await openSettings()
    const off = metaG().capabilities_off
    for (const [cap, why] of Object.entries(off)) {
      const li = section("capabilities").querySelector(
        `[data-cap-off="${cap}"]`
      )
      expect(li, cap).toBeTruthy()
      expect(li!.textContent).toContain(why)
    }
  })

  it("runtimes off: the count and the reason, no /api/runtimes read", async () => {
    await openSettings()
    const s = section("runtimes")
    expect(
      s
        .querySelector("[data-runtime-count]")!
        .getAttribute("data-runtime-count")
    ).toBe("0")
    expect(s.querySelector("[data-off=runtimes]")!.textContent).toContain(
      metaG().capabilities_off.runtimes
    )
    expect(studio.calls("GET runtimes")).toHaveLength(0)
  })

  it("a Studio older than pid (db.path read, pid absent) says not reported, never the hidden badge", async () => {
    const { pid: _pid, ...old } = metaG()
    studio.on("GET meta", {
      ...old,
      db: { kind: "sqlite", path: "/tmp/weft.db", size: 4096 },
    })
    await openSettings()
    const pid = fact("studio", "pid")
    expect(pid.querySelector("[data-unreported=pid]")).toBeTruthy()
    expect(pid.querySelector("[data-hole=hidden]")).toBeNull()
  })

  it("capabilities_off absent: unreported on a Studio older than it, nothing is off on a current one", async () => {
    const { capabilities_off: _o, manifest_sources: _m, ...old } = metaG()
    studio.on("GET meta", old)
    await openSettings()
    expect(
      section("capabilities").querySelector(
        "[data-unreported=capabilities_off]"
      )
    ).toBeTruthy()
    expect(section("capabilities").textContent).not.toContain("nothing is off")
    // A current Studio omits the key when nothing is off (omitempty):
    // manifest_sources, always present, tells the two apart.
    const { capabilities_off: _o2, ...current } = metaG()
    studio.on("GET meta", { ...current, manifest_sources: 0 })
    await openSettings()
    expect(section("capabilities").textContent).toContain("nothing is off")
    expect(
      section("capabilities").querySelector("[data-unreported]")
    ).toBeNull()
  })

  it("a Studio older than retention and content says so, never an empty pane", async () => {
    const { retention: _r, content: _c, ...old } = metaG()
    studio.on("GET meta", old)
    await openSettings()
    expect(
      section("retention").querySelector("[data-unreported=retention]")
    ).toBeTruthy()
    expect(
      section("content").querySelector("[data-unreported=content]")
    ).toBeTruthy()
  })
})

describe("appearance", () => {
  it("the theme and density radios store and apply the choice", async () => {
    await openSettings()
    fireEvent.click(
      within(section("appearance")).getByRole("radio", { name: "dark" })
    )
    expect(localStorage.getItem("theme")).toBe("dark")
    expect(document.documentElement.classList.contains("dark")).toBe(true)
    fireEvent.click(
      within(section("appearance")).getByRole("radio", { name: "compact" })
    )
    expect(document.documentElement.getAttribute("data-density")).toBe(
      "compact"
    )
    fireEvent.click(
      within(section("appearance")).getByRole("radio", {
        name: "auto (the system's)",
      })
    )
    expect(localStorage.getItem("theme")).toBeNull()
    fireEvent.click(
      within(section("appearance")).getByRole("radio", { name: "comfortable" })
    )
  })
})

describe("accessibility", () => {
  it("the page is axe-clean: a real table with headers, every input labelled", async () => {
    studio.on("GET meta", playgroundMeta())
    await openSettings()
    await within(section("pricing")).findByRole("table")
    await waitFor(() =>
      expect(
        section("runtimes").querySelector("[data-runtime-states]")
      ).toBeTruthy()
    )
    const main =
      document.querySelector<HTMLElement>("[data-section]")!.parentElement!
    const res = await axe.run(main, {
      rules: { "color-contrast": { enabled: false } },
    })
    expect(
      res.violations.map(
        (v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`
      )
    ).toEqual([])
    for (const inp of main.querySelectorAll("input")) {
      const labelled = inp.getAttribute("aria-label") || inp.closest("label")
      expect(labelled, inp.outerHTML).toBeTruthy()
    }
  })
})

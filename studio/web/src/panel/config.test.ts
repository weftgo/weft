// §5.2's mounting contract: the data-* attributes, their defaults,
// the script-directory endpoint resolution, and §5.3's debug override.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { debugForced, findPanelScript, readConfig } from "./config"

function setScript(attrs: Record<string, string>) {
  const s = document.createElement("script")
  s.type = "module"
  s.src = "src" in attrs ? attrs.src : "/studio/panel.js"
  for (const [k, v] of Object.entries(attrs)) s.setAttribute(k, v)
  document.head.appendChild(s)
  return s
}

beforeEach(() => {
  // jsdom does not navigate on href assignment; the default location
  // (http://localhost:3000) stands, and pushState rewrites the URL's
  // path and search where a test needs them.
  window.history.pushState({}, "", "/chat/pub_7Hk2")
  localStorage.clear()
})

afterEach(() => {
  document.head.querySelectorAll("script").forEach((s) => s.remove())
  vi.restoreAllMocks()
})

describe("readConfig (§5.2 attributes and defaults)", () => {
  it("defaults: endpoint from the script's own directory, position bottom-right, open false, auto true", () => {
    setScript({ "data-public-id": "pub_7Hk2" })
    const cfg = readConfig()
    expect(cfg.endpoint).toBe("http://localhost:3000/studio/")
    expect(cfg.publicId).toBe("pub_7Hk2")
    expect(cfg.token).toBe("")
    expect(cfg.position).toBe("bottom-right")
    expect(cfg.open).toBe(false)
    expect(cfg.auto).toBe(true)
  })

  it("data-endpoint, data-token, data-position, data-open override the defaults", () => {
    setScript({
      "data-endpoint": "http://127.0.0.1:7331/studio",
      "data-token": "dev_4c",
      "data-position": "right-dock",
      "data-open": "true",
      "data-public-id": "pub_7Hk2",
    })
    const cfg = readConfig()
    expect(cfg.endpoint).toBe("http://127.0.0.1:7331/studio/")
    expect(cfg.token).toBe("dev_4c")
    expect(cfg.position).toBe("right-dock")
    expect(cfg.open).toBe(true)
  })

  it("an element's attributes ride on top of the script tag's", () => {
    setScript({ "data-public-id": "pub_script" })
    const el = document.createElement("weft-devtools")
    el.setAttribute("data-public-id", "pub_element")
    document.body.appendChild(el)
    expect(readConfig(el).publicId).toBe("pub_element")
  })

  it("an unknown position falls back to bottom-right", () => {
    setScript({ "data-position": "diagonal" })
    expect(readConfig().position).toBe("bottom-right")
  })

  it("data-auto=false is honoured; anything else means auto", () => {
    setScript({ "data-auto": "false" })
    expect(readConfig().auto).toBe(false)
  })
})

describe("findPanelScript (§5.1: panel.js, or the release asset's own name)", () => {
  const find = (src: string) => {
    const tag = setScript({ src })
    const found = findPanelScript() === tag
    tag.remove()
    return found
  }

  it("matches the panel's file on its path segment, served or as released", () => {
    expect(find("/studio/panel.js")).toBe(true)
    expect(find("panel.js?v=3")).toBe(true)
    expect(find("https://cdn.internal/assets/panel-v0.3.0.js")).toBe(true)
    expect(find("/static/panel-0.3.1-rc.1.js#x")).toBe(true)
  })

  it("the host page's own scripts are not the panel", () => {
    expect(find("/js/control-panel.js")).toBe(false)
    expect(find("/js/panel-admin.js")).toBe(false)
    expect(find("/js/panel.json")).toBe(false)
    expect(find("/js/app.js")).toBe(false)
  })

  it("an endpoint that cannot be used is no endpoint — never another origin", () => {
    setScript({ "data-endpoint": "http://" })
    expect(readConfig().endpoint).toBe("")
    document.head.querySelectorAll("script").forEach((s) => s.remove())
    setScript({ "data-endpoint": "javascript:alert(1)" })
    expect(readConfig().endpoint).toBe("")
  })
})

describe("debugForced (§5.3: ?weft=debug / localStorage.weft_debug=1)", () => {
  it("is off by default", () => {
    expect(debugForced()).toBe(false)
  })

  it("the namespaced ?weft=debug parameter forces the panel on", () => {
    window.history.pushState({}, "", "/chat?weft=debug")
    expect(debugForced()).toBe(true)
  })

  it("the app's own ?debug= does not (the collision §5.3 warns about)", () => {
    window.history.pushState({}, "", "/chat?debug=1")
    expect(debugForced()).toBe(false)
  })

  it("localStorage.weft_debug=1 forces it (staging)", () => {
    localStorage.setItem("weft_debug", "1")
    expect(debugForced()).toBe(true)
  })
})

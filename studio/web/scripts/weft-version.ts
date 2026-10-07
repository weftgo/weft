// The one version (B6): the framework module's tag, read from the Go
// source of version/version.go so nothing in the web build carries a
// second copy. vite.panel.config.ts stamps it into panel.js as
// __PANEL_STUDIO_VERSION__; the panel's version suite compares the
// "Studio is newer" guard against the same string.
import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

export function weftVersion(): string {
  // A path, not a URL: under vitest's jsdom, URL is jsdom's and
  // readFileSync refuses it.
  const here = path.dirname(fileURLToPath(import.meta.url))
  const go = readFileSync(
    path.resolve(here, "../../../version/version.go"),
    "utf8"
  )
  const m = /^const Version = "([^"]+)"$/m.exec(go)
  if (!m)
    throw new Error(
      "weft-version: const Version not found in version/version.go"
    )
  return m[1]
}

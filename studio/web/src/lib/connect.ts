// How to connect an app to THIS Studio (U21.2; UX-F-11, UX-S5a-10): the
// lines every page's empty state, the /runs first-run card and the
// Settings page show, built from the page's own base URL — never a
// fixed port, never "this command starts Studio" to someone already in
// it. The token is never written: a token-walled Studio's lines name
// where it lives (<db>.token, weft open --with-token).
import { mcpCommand } from "./settings"

/** studioURL is this Studio's URL as an app dials it: the page's base
 * (where this UI and /api are served), without the trailing slash. */
export function studioURL(baseURI: string = document.baseURI): string {
  return new URL(".", baseURI).toString().replace(/\/+$/, "")
}

/** One way in: who it is for, the line to paste, and a note. */
export interface ConnectLine {
  key: "app" | "panel" | "mcp"
  label: string
  code: string
  note: string
}

/** Where a token-walled Studio's token is (B7 owns the wall itself). */
export const TOKEN_PLACE =
  "the token: <db>.token beside the database, or `weft open <run id> --with-token`"

/** connectLines are the three ways an app, a page and a coding agent
 * reach the Studio at url. */
export function connectLines(
  url: string,
  authRequired?: boolean
): ConnectLine[] {
  // The attribute the panel reads its bearer from, with a placeholder:
  // the token itself is never written here.
  const attr = "data-token"
  const token = authRequired ? ` ${attr}="<token>"` : ""
  return [
    {
      key: "app",
      label: "Go app",
      code: `export WEFT_STUDIO_URL=${url}\n${authRequired ? "export WEFT_STUDIO_TOKEN=<token>\n" : ""}go run .   # an app that calls otel.Install()`,
      note: `otel.Install() records runs here; without WEFT_STUDIO_URL it finds the Studio a running \`weft studio\` names in its discovery file (studio.json). WEFT_STUDIO_URL turns discovery off: a Studio with an ingest token needs WEFT_STUDIO_TOKEN too${authRequired ? ` (${TOKEN_PLACE})` : ""}. runtime.Install(...) lets Studio replay them.`,
    },
    {
      key: "panel",
      label: "devtools panel",
      code: `<script type="module" src="${url}/panel.js" data-weft data-endpoint="${url}/"${token}></script>`,
      note: authRequired
        ? `in your app's page; ${TOKEN_PLACE}`
        : "in your app's page, during development",
    },
    {
      key: "mcp",
      label: "coding agent",
      code: authRequired
        ? `claude mcp add weft -e WEFT_STUDIO_TOKEN=<token> -- ${mcpCommand(url)}`
        : `claude mcp add weft -- ${mcpCommand(url)}`,
      note: "weft mcp reads this Studio for an MCP client (docs/mcp-recipe.md)",
    },
  ]
}

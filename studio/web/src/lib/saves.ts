// The two saves of plan F6, shared by Studio and the devtools panel
// (one rule, one set of words — parity.test.ts holds both to them:
// keep as prompt's line, the save-as-test label and title):
//
//   - "save as test": the run's wefttest zip (GET /api/runs/{id}/export
//     ?format=wefttest, capability "export"; studio/export.go) — a
//     generated <agent>_replay_test.go beside testdata/TestReplay_<run>/.
//     It is fetched with the bearer in the Authorization header, never a
//     token in a URL, and saved under the Content-Disposition's name.
//     Studio writes nothing to the user's files: the download is the
//     hand-off. The devtools panel's verb is a link to the run page with
//     ?save=test (lib/links.ts's saveTestLink), which downloads there:
//     the in-panel fetch-and-save measured ~0.8 KiB of panel.js.
//   - "keep as prompt": the clipboard copy, and — under capability
//     "proposals" and an identity that may propose (the dev token, or
//     setup A's loopback with no token; every panel token is 403,
//     studio/proposals.go) — PUT /api/agents/{name}/proposal, which the
//     Agents page shows as "proposed" beside "registered". The durable
//     path is WEFT-PROMPT.md's P3.
//
// Pure: no React, no DOM beyond the anchor save (the panel bundles it).

import { tokenScopeOf } from "./replay"

/** The export route of a run in one format (under the API base). */
export function exportPath(runID: string, format: string): string {
  return `runs/${encodeURIComponent(runID)}/export?format=${encodeURIComponent(format)}`
}

/** dispositionName reads a Content-Disposition's file name: RFC 6266's
 * filename* (UTF-8, percent-encoded) when present, else the quoted
 * filename, else the fallback. Path separators never survive. */
export function dispositionName(cd: string | null, fallback: string): string {
  const star = /filename\*=UTF-8''([^;]+)/i.exec(cd ?? "")
  let name = ""
  if (star) {
    try {
      name = decodeURIComponent(star[1])
    } catch {
      name = ""
    }
  }
  name ||= /filename="([^"]*)"/i.exec(cd ?? "")?.[1] ?? ""
  return name.replace(/[/\\]/g, "_") || fallback
}

/** saveBlob saves bytes as a file: a Blob URL on an anchor click,
 * revoked once the browser has it. */
export function saveBlob(
  blob: Blob,
  name: string,
  parent: Node = document.body
): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = name
  parent.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}

/** mayProposeWith is the client's reading of proposals.go's rule: a
 * dev token or none (the server answers loopback-with-no-token, else
 * 403 in its own words); never a panel token. */
export function mayProposeWith(
  capabilities: readonly string[],
  token: string
): boolean {
  return capabilities.includes("proposals") && tokenScopeOf(token) === ""
}

/** mayExportTestWith: the verb is drawn under capability "export" for
 * any identity but a read-scoped panel token (export.go refuses it the
 * wefttest format: fixtures carry prompts). */
export function mayExportTestWith(
  capabilities: readonly string[],
  token: string
): boolean {
  return capabilities.includes("export") && tokenScopeOf(token) !== "read"
}

export const SAVE_TEST_LABEL = "save as test"
export const SAVE_TEST_TITLE =
  "download this run as a wefttest replay test (a zip: <agent>_replay_test.go + testdata) — Studio writes nothing to your files"
export const KEEP_PROMPT_LABEL = "keep as prompt"
/** keepEditedTitle is the title of keep as prompt over an edited
 * prompt — the playground card's and the panel drawer's alike: it
 * proposes to agent where the identity may (mayProposeWith), else it
 * copies, naming the dev token where Studio serves proposals — and
 * with no agent registered, copy alone: there is nothing to propose
 * to, whatever the token. */
export function keepEditedTitle(
  agent: string,
  capabilities: readonly string[],
  token: string
): string {
  if (!agent) return "copy the edited prompt"
  return mayProposeWith(capabilities, token)
    ? `copy the edited prompt and propose it as ${agent}'s instructions (shown on the Agents page beside the registered ones)`
    : `copy the edited prompt${capabilities.includes("proposals") ? ` — ${PROPOSAL_NEEDS_DEV}` : ""}`
}

/** What a panel token (or a Studio without proposals) is told. */
export const PROPOSAL_NEEDS_DEV = "a proposal needs the dev token"
/** The Agents page's pointer, said once. */
export const PROPOSAL_POINTER =
  "the durable path is WEFT-PROMPT.md P3: a propose command over the runtime link"

/** One save's outcome, in words both surfaces say. */
export type SaveOutcome =
  | { kind: "save-test"; ok: true; file: string }
  | { kind: "save-test"; ok: false; message: string }
  | KeepPrompt
  | { kind: "withdraw"; agent: string; ok: boolean; message?: string }
  /** A plain copy (the Agents page's "copy proposed"). */
  | { kind: "copy"; ok: boolean }

/** keep as prompt's outcome: copied (or not), and the proposal's fate —
 * sent (the 200), refused (the server's words, verbatim), or not asked
 * (copy only: a panel token, or no proposals capability). */
export type KeepPrompt = {
  kind: "keep-prompt"
  agent: string
  copied: boolean
  proposal: "proposed" | "refused" | "copy-only"
  message?: string
  /** What the proposed text may carry (lib/request-pane.ts's
   * proposalOf), said after the line — Studio's toast only. */
  note?: string
}

export type SaveTone = "success" | "error" | "info"

export function saveWords(o: SaveOutcome): { title: string; tone: SaveTone } {
  switch (o.kind) {
    case "save-test":
      return o.ok
        ? {
            title: `saved ${o.file} — unzip into a package directory and go test`,
            tone: "success",
          }
        : { title: `save as test refused: ${o.message}`, tone: "error" }
    case "keep-prompt": {
      const w = keepPromptWords(o)
      return o.note ? { ...w, title: `${w.title} · ${o.note}` } : w
    }
    case "copy":
      return o.ok
        ? { title: "copied", tone: "success" }
        : {
            title: "copy failed — the browser refused the clipboard",
            tone: "error",
          }
    case "withdraw":
      return o.ok
        ? { title: `proposal for ${o.agent} withdrawn`, tone: "success" }
        : { title: `withdraw refused: ${o.message ?? ""}`, tone: "error" }
  }
}

/** keepPromptWords is keep as prompt's line (the panel bundles this
 * one alone: it has no save-as-test fetch or withdraw of its own). */
export function keepPromptWords(o: KeepPrompt): {
  title: string
  tone: SaveTone
} {
  const copied = o.copied
    ? "copied"
    : "copy failed — the browser refused the clipboard"
  if (o.proposal === "proposed")
    return {
      title: `${copied} · proposed as ${o.agent}'s instructions (see Agents)`,
      tone: o.copied ? "success" : "info",
    }
  if (o.proposal === "refused")
    return {
      title: `${copied} · proposal refused: ${o.message ?? ""}`,
      tone: "error",
    }
  // No agent: copy alone — no proposal to need a token for.
  if (!o.agent) return { title: copied, tone: o.copied ? "success" : "error" }
  return {
    title: `${copied} · ${PROPOSAL_NEEDS_DEV}`,
    tone: o.copied ? "info" : "error",
  }
}

// lib/saves.ts's pure parts and lib/request-pane.ts's proposalOf (plan
// F6): the Content-Disposition name, the words, and what keep as prompt
// proposes from a recorded system prompt.
import { describe, expect, it } from "vitest"

import type { ManifestAgent } from "./api"
import { proposalOf } from "./request-pane"
import {
  keptPrefixOf,
  dispositionName,
  keepEditedTitle,
  keepPromptWords,
  PROPOSAL_NEEDS_DEV,
  saveWords,
} from "./saves"

describe("dispositionName", () => {
  it("reads the quoted filename", () => {
    expect(dispositionName('attachment; filename="r_ok.zip"', "x.zip")).toBe(
      "r_ok.zip"
    )
  })
  it("never lets a path separator through", () => {
    expect(dispositionName('attachment; filename="../x.zip"', "f.zip")).toBe(
      ".._x.zip"
    )
    expect(dispositionName('attachment; filename="a\\\\b.zip"', "f.zip")).toBe(
      "a__b.zip"
    )
  })
  it("prefers RFC 6266's filename* (UTF-8, percent-encoded), separators spelled _", () => {
    expect(
      dispositionName(
        `attachment; filename="r__.zip"; filename*=UTF-8''r%C3%A9%2Fx.zip`,
        "f.zip"
      )
    ).toBe("ré_x.zip")
  })
  it("falls back on a missing header, an empty name or a bad encoding", () => {
    expect(dispositionName(null, "f.zip")).toBe("f.zip")
    expect(dispositionName('attachment; filename=""', "f.zip")).toBe("f.zip")
    expect(
      dispositionName(`attachment; filename*=UTF-8''%E0%A4%A`, "f.zip")
    ).toBe("f.zip")
  })
})

describe("the words", () => {
  it("a plain copy says copied, never the proposal hint", () => {
    expect(saveWords({ kind: "copy", ok: true })).toEqual({
      title: "copied",
      tone: "success",
    })
    expect(saveWords({ kind: "copy", ok: false }).tone).toBe("error")
  })
  it("no agent registered: copy alone — the title and the outcome never offer or hint at a proposal", () => {
    for (const tok of ["", "dev-token"])
      expect(keepEditedTitle("", ["proposals"], tok)).toBe(
        "copy the edited prompt"
      )
    expect(
      keepPromptWords({
        kind: "keep-prompt",
        agent: "",
        copied: true,
        proposal: "copy-only",
      })
    ).toEqual({ title: "copied", tone: "success" })
    expect(
      keepPromptWords({
        kind: "keep-prompt",
        agent: "",
        copied: false,
        proposal: "copy-only",
      }).title
    ).not.toContain(PROPOSAL_NEEDS_DEV)
    // With an agent, a panel token is still told what a proposal needs.
    expect(
      keepPromptWords({
        kind: "keep-prompt",
        agent: "orders",
        copied: true,
        proposal: "copy-only",
      }).title
    ).toBe(`copied · ${PROPOSAL_NEEDS_DEV}`)
  })
  it("a proposal's note follows the keep-as-prompt line", () => {
    const o = {
      kind: "keep-prompt" as const,
      agent: "orders",
      copied: true,
      proposal: "proposed" as const,
    }
    expect(saveWords(o).title).toBe(keepPromptWords(o).title)
    expect(saveWords({ ...o, note: "N" }).title).toBe(
      `${keepPromptWords(o).title} · N`
    )
  })
})

const agent = (snippets: Record<string, string>): ManifestAgent => ({
  name: "orders",
  model: { provider: "wefttest", name: "script" },
  instructions: "You help.",
  policy: {
    parallelism: 4,
    max_steps: 10,
    max_result_bytes: 65536,
    max_model_retries: 3,
  },
  tools: ["lookup_order", "refund", "ping"].map((name) =>
    snippets[name] ? { name, prompt_snippet: snippets[name] } : { name }
  ),
})
const SN = {
  lookup_order: "Look orders up first.",
  refund: "Refunds need a reason.",
}

describe("proposalOf: keep as prompt proposes the instructions", () => {
  it("strips the verified agent's snippets when they are the recorded text's suffix", () => {
    const reg = { agent: agent(SN), verified: true }
    const text =
      "You help, tersely.\n\nLook orders up first.\n\nRefunds need a reason."
    expect(proposalOf(text, ["lookup_order", "ping", "refund"], reg)).toEqual({
      text: "You help, tersely.",
    })
    // Snippets alone: empty instructions.
    expect(proposalOf("Look orders up first.", ["lookup_order"], reg)).toEqual({
      text: "",
    })
    // No snippet among the offered tools: the text is the instructions.
    expect(proposalOf("You help.", ["ping"], reg)).toEqual({
      text: "You help.",
    })
  })
  it("proposes as recorded and says a PrepareStep rewrite when verified snippets are not its suffix", () => {
    const p = proposalOf("Rewritten.", ["lookup_order"], {
      agent: agent(SN),
      verified: true,
    })
    expect(p.text).toBe("Rewritten.")
    expect(p.note).toContain("PrepareStep rewrite")
  })
  it("an unverified agent: as recorded, counting the snippets it may carry", () => {
    const text = "You help.\n\nLook orders up first."
    const p = proposalOf(text, ["lookup_order"], {
      agent: agent(SN),
      verified: false,
    })
    expect(p).toEqual({
      text,
      note: "proposed as recorded: may include 1 tool snippet or a PrepareStep rewrite (the manifest does not verify this run's agent)",
    })
    expect(
      proposalOf(text, ["lookup_order", "refund"], {
        agent: agent(SN),
        verified: false,
      }).note
    ).toContain("2 tool snippets")
    // Unknown tools (a ToolSource tool) or no manifest: the snippets are unknown.
    expect(proposalOf(text, ["dynamic"], undefined).note).toContain(
      "the tools' snippets or a PrepareStep rewrite"
    )
    expect(proposalOf(text, [], undefined).note).toContain(
      "may include a PrepareStep rewrite"
    )
  })
})

// UX-C-30: a replay's zip composes its source's fixtures for the steps
// it kept; the server names it (Weft-Kept-Prefix) and the toast says so.
describe("a replay's composed save as test", () => {
  it("reads the header and says which steps are the source's", () => {
    expect(keptPrefixOf("r#a#1")).toEqual({ sources: ["r#a"], steps: 1 })
    expect(keptPrefixOf("r3#1,p1#0")).toEqual({
      sources: ["r3", "p1"],
      steps: 1,
    })
    for (const bad of [null, "", "r", "r#0", "#2", "r#x", "r#1,"])
      expect(keptPrefixOf(bad)).toBeUndefined()
    expect(
      saveWords({
        kind: "save-test",
        ok: true,
        file: "orders_replay_test.zip",
        kept: { sources: ["r_src"], steps: 1 },
      }).title
    ).toBe(
      "saved orders_replay_test.zip (steps 0–0 from the source r_src, 1 on from the replay) — unzip into a package directory and go test"
    )
  })
})

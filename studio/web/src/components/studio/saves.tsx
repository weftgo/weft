// The saves of plan F6 on Studio's surfaces: "save as test" on the run
// page's header, "keep as prompt" on the Request pane's system prompt.
// The rules and the words are lib/saves.ts's (the panel says the same);
// the toasts are lib/notify.ts's notifySave. Studio writes nothing to
// the user's files: the download is the hand-off.
import { useQueryClient } from "@tanstack/react-query"
import { Download } from "lucide-react"
import { useState } from "react"

import {
  fetchTestExport,
  manifestQuery,
  putProposal,
  studioToken,
} from "@/lib/api"
import { proposalOf, registeredAgent } from "@/lib/request-pane"
import { copyText } from "@/lib/json"
import { notifySave } from "@/lib/notify"
import {
  KEEP_PROMPT_LABEL,
  mayExportTestWith,
  mayProposeWith,
  PROPOSAL_NEEDS_DEV,
  SAVE_TEST_LABEL,
  SAVE_TEST_TITLE,
  saveBlob,
} from "@/lib/saves"
import { useCapabilities } from "@/hooks/use-capabilities"
import { derivedWords } from "@/lib/honesty"
import { Button } from "@/components/ui/button"

/** saveTest fetches the run's wefttest zip with the bearer, saves it
 * under the Content-Disposition's name and toasts the outcome. */
export async function saveTest(runID: string): Promise<void> {
  try {
    const { blob, name, kept } = await fetchTestExport(runID)
    saveBlob(blob, name)
    notifySave({ kind: "save-test", ok: true, file: name, kept })
  } catch (e) {
    notifySave({
      kind: "save-test",
      ok: false,
      message: e instanceof Error ? e.message : String(e),
    })
  }
}

/** SaveAsTest is the run page's verb (capability "export"; hidden to a
 * read-scoped panel token, which export.go refuses the format). */
export function SaveAsTest({
  runID,
  derived = false,
}: {
  runID: string
  derived?: boolean
}) {
  const { caps } = useCapabilities()
  const [busy, setBusy] = useState(false)
  if (!mayExportTestWith(caps, studioToken())) return null
  // A derived run (plan I1.3) recorded no transcript: the wefttest
  // export refuses it 400 with the derived badge — the verb is shown
  // greyed with those words, never offered.
  if (derived) {
    const words = derivedWords()
    return (
      <Button
        variant="outline"
        size="xs"
        className="font-mono"
        title={words}
        disabled
        data-save-test="derived"
      >
        <Download data-slot="icon" />
        {SAVE_TEST_LABEL}
        <span className="sr-only">{` — ${words}`}</span>
      </Button>
    )
  }
  return (
    <Button
      variant="outline"
      size="xs"
      className="font-mono"
      title={SAVE_TEST_TITLE}
      disabled={busy}
      data-save-test
      onClick={() => {
        setBusy(true)
        void saveTest(runID).finally(() => setBusy(false))
      }}
    >
      <Download data-slot="icon" />
      {SAVE_TEST_LABEL}
    </Button>
  )
}

/** keepPrompt copies text and, when the identity may propose, PUTs it
 * as agent's proposed instructions; the outcome is toasted, a refusal
 * in the server's own words, with `note` (what the text may carry,
 * lib/request-pane.ts's proposalOf) after it. Answers whether a
 * proposal was stored. */
export async function keepPrompt(
  caps: readonly string[],
  agent: string,
  text: string,
  note?: string
): Promise<boolean> {
  const copied = await copyText(text)
  if (!agent || !mayProposeWith(caps, studioToken())) {
    notifySave({ kind: "keep-prompt", agent, copied, proposal: "copy-only" })
    return false
  }
  try {
    await putProposal(agent, text)
    notifySave({
      kind: "keep-prompt",
      agent,
      copied,
      proposal: "proposed",
      ...(note ? { note } : {}),
    })
    return true
  } catch (e) {
    notifySave({
      kind: "keep-prompt",
      agent,
      copied,
      proposal: "refused",
      message: e instanceof Error ? e.message : String(e),
    })
    return false
  }
}

/** KeepAsPrompt is the Request pane's verb on a step's system prompt:
 * copy, and propose the agent's instructions — the recorded text with
 * the verified agent's tool snippets stripped, else as recorded with a
 * note (proposalOf). `names` are the step's offered tools, `manifestHash`
 * the run's weft.manifest.hash (the verification). */
export function KeepAsPrompt({
  agent,
  text,
  names = [],
  manifestHash,
}: {
  agent?: string
  text: string
  names?: string[]
  manifestHash?: string
}) {
  const { caps } = useCapabilities()
  const qc = useQueryClient()
  const propose = !!agent && mayProposeWith(caps, studioToken())
  // Ink with the thread as its underline (U34-fix3): the thread reads
  // 2.95:1 on paper and thread-ink 4.45:1 on a subagent block's tint.
  return (
    <button
      type="button"
      className="min-h-6 font-mono text-[11px] text-foreground underline decoration-thread-ink underline-offset-2 hover:decoration-2"
      data-keep-prompt
      title={
        !agent
          ? "copy this system prompt"
          : propose
            ? `copy this step's instructions and propose them as ${agent}'s (the tools' snippets stripped when the manifest verifies them; shown on the Agents page beside the registered ones)`
            : `copy this system prompt${caps.includes("proposals") ? ` — ${PROPOSAL_NEEDS_DEV}` : ""}`
      }
      onClick={() => {
        void (async () => {
          if (!propose) return keepPrompt(caps, agent ?? "", text)
          const manifest = await qc
            .fetchQuery(manifestQuery())
            .catch(() => undefined)
          const p = proposalOf(
            text,
            names,
            registeredAgent(manifest, agent, manifestHash)
          )
          const stored = await keepPrompt(caps, agent, p.text, p.note)
          if (stored) await qc.invalidateQueries({ queryKey: ["manifest"] })
        })()
      }}
    >
      {KEEP_PROMPT_LABEL}
    </button>
  )
}

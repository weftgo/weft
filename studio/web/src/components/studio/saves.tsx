// The saves of plan F6 on Studio's surfaces: "save as test" on the run
// page's header, "keep as prompt" on the Request pane's system prompt.
// The rules and the words are lib/saves.ts's (the panel says the same);
// the toasts are lib/notify.ts's notifySave. Studio writes nothing to
// the user's files: the download is the hand-off.
import { useQueryClient } from "@tanstack/react-query"
import { Download } from "lucide-react"
import { useState } from "react"

import { fetchTestExport, putProposal, studioToken } from "@/lib/api"
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
import { Button } from "@/components/ui/button"

/** saveTest fetches the run's wefttest zip with the bearer, saves it
 * under the Content-Disposition's name and toasts the outcome. */
export async function saveTest(runID: string): Promise<void> {
  try {
    const { blob, name } = await fetchTestExport(runID)
    saveBlob(blob, name)
    notifySave({ kind: "save-test", ok: true, file: name })
  } catch (e) {
    notifySave({ kind: "save-test", ok: false, message: e instanceof Error ? e.message : String(e) })
  }
}

/** SaveAsTest is the run page's verb (capability "export"; hidden to a
 * read-scoped panel token, which export.go refuses the format). */
export function SaveAsTest({ runID }: { runID: string }) {
  const { caps } = useCapabilities()
  const [busy, setBusy] = useState(false)
  if (!mayExportTestWith(caps, studioToken())) return null
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
 * in the server's own words. Answers whether a proposal was stored. */
export async function keepPrompt(
  caps: readonly string[],
  agent: string,
  text: string
): Promise<boolean> {
  const copied = await copyText(text)
  if (!agent || !mayProposeWith(caps, studioToken())) {
    notifySave({ kind: "keep-prompt", agent, copied, proposal: "copy-only" })
    return false
  }
  try {
    await putProposal(agent, text)
    notifySave({ kind: "keep-prompt", agent, copied, proposal: "proposed" })
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
 * copy, and propose it as the run's agent's instructions. */
export function KeepAsPrompt({ agent, text }: { agent?: string; text: string }) {
  const { caps } = useCapabilities()
  const qc = useQueryClient()
  const propose = !!agent && mayProposeWith(caps, studioToken())
  return (
    <button
      type="button"
      className="font-mono text-[11px] text-thread hover:underline"
      data-keep-prompt
      title={
        propose
          ? `copy this system prompt and propose it as ${agent}'s instructions (shown on the Agents page beside the registered ones)`
          : `copy this system prompt${caps.includes("proposals") ? ` — ${PROPOSAL_NEEDS_DEV}` : ""}`
      }
      onClick={() => {
        void keepPrompt(caps, agent ?? "", text).then((stored) => {
          if (stored) void qc.invalidateQueries({ queryKey: ["manifest"] })
        })
      }}
    >
      {KEEP_PROMPT_LABEL}
    </button>
  )
}

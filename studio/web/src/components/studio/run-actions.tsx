// The run page's export and delete verbs (U26; UX-A-15). Rules and
// words are lib/run-actions.ts's; the export is fetched with the
// bearer and saved as a blob (never a token in a URL), the delete asks
// first in an in-page dialog naming the subagent runs that go with it.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useRouter } from "@tanstack/react-router"
import { FileDown, Trash2 } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { deleteRun, fetchExport, metaQuery, studioToken } from "@/lib/api"
import type { RunDoc } from "@/lib/api"
import { runLink, runsLink } from "@/lib/links"
import {
  DELETE_LABEL,
  childrenUnder,
  deleteConfirmWords,
  deletedWords,
  deleteOffWhy,
  EXPORT_LABEL,
  EXPORT_TREE,
  exportFormatsFor,
  exportWords,
} from "@/lib/run-actions"
import { saveBlob } from "@/lib/saves"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

/** ExportVerbs: "export json · jsonl · otlp" — each a download of the
 * run in that format (wefttest is save as test, beside it). Absent
 * without capability "export". */
export function ExportVerbs({
  runID,
  subagents = 0,
}: {
  runID: string
  /** The run's subagent runs (one level): with any, "with subagents"
   * is offered (UX-A-31: &tree=1). */
  subagents?: number
}) {
  const meta = useQuery(metaQuery())
  const formats = exportFormatsFor(meta.data?.capabilities ?? [], studioToken())
  const [busy, setBusy] = useState("")
  const [tree, setTree] = useState(false)
  if (!formats.length) return null
  const run = async (format: string) => {
    setBusy(format)
    try {
      const { blob, name } = await fetchExport(
        runID,
        format,
        tree && subagents > 0
      )
      saveBlob(blob, name)
      toast.success(exportWords({ ok: true, file: name }), { id: "export" })
    } catch (e) {
      toast.error(
        exportWords({
          ok: false,
          message: e instanceof Error ? e.message : String(e),
        }),
        { id: "export" }
      )
    } finally {
      setBusy("")
    }
  }
  return (
    <span
      className="flex items-center gap-1"
      role="group"
      aria-label={`${EXPORT_LABEL} this run`}
      data-export
    >
      <FileDown className="size-3 text-faint" aria-hidden />
      <span className="font-mono text-[11px] text-faint">{EXPORT_LABEL}</span>
      {formats.map((f) => (
        <Button
          key={f.format}
          variant="outline"
          size="xs"
          className="font-mono"
          title={f.title}
          disabled={busy !== ""}
          data-export-format={f.format}
          onClick={() => void run(f.format)}
        >
          {f.label}
        </Button>
      ))}
      {subagents > 0 ? (
        <label
          className="flex min-h-6 items-center gap-1 font-mono text-[11px] text-faint"
          title={EXPORT_TREE.title}
        >
          <input
            type="checkbox"
            className="mx-1 size-4"
            checked={tree}
            disabled={busy !== ""}
            onChange={(e) => setTree(e.target.checked)}
            data-export-tree
          />
          {EXPORT_TREE.label}
        </label>
      ) : null}
    </span>
  )
}

/** DeleteRun: "delete run…" and its in-page confirm, offered where the
 * server answers it (deleteOffWhy); elsewhere one faint line says why. */
export function DeleteRun({
  doc,
}: {
  doc: Pick<RunDoc, "id" | "parent_run_id"> & {
    children: readonly {
      id: string
      agent?: string
      children_count?: number
    }[]
  }
}) {
  const meta = useQuery(metaQuery())
  const qc = useQueryClient()
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const why = deleteOffWhy(
    meta.data?.capabilities ?? [],
    studioToken(),
    meta.data?.live_all,
    meta.data?.capabilities_off?.delete
  )
  if (!meta.data) return null
  if (why)
    return (
      <span
        className="font-mono text-[11px] text-faint"
        title={why}
        data-delete-off={why}
      >
        {why}
      </span>
    )
  const w = deleteConfirmWords(doc)
  const confirm = async () => {
    setBusy(true)
    setRefused("")
    try {
      await deleteRun(doc.id)
      setOpen(false)
      toast.success(deletedWords({ ok: true, id: doc.id }), { id: "delete" })
      qc.removeQueries({ queryKey: ["run", doc.id] })
      void qc.invalidateQueries({ queryKey: ["runs"] })
      // The parent's document lists the deleted child (children[],
      // children_count): read it again before landing on it.
      if (doc.parent_run_id)
        void qc.invalidateQueries({ queryKey: ["run", doc.parent_run_id] })
      await router.navigate(
        doc.parent_run_id ? runLink(doc.parent_run_id) : runsLink()
      )
    } catch (e) {
      setRefused(
        deletedWords({
          ok: false,
          message: e instanceof Error ? e.message : String(e),
        })
      )
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button
        variant="outline"
        size="xs"
        className="font-mono"
        onClick={() => setOpen(true)}
        data-delete-run
      >
        <Trash2 data-slot="icon" />
        {DELETE_LABEL}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) setRefused("")
        }}
      >
        <DialogContent data-delete-confirm>
          <DialogHeader>
            <DialogTitle className="font-mono text-sm break-all">
              {w.title}
            </DialogTitle>
            <DialogDescription>{w.body}</DialogDescription>
          </DialogHeader>
          <div className="space-y-1 text-xs">
            <p>{w.children}</p>
            {doc.children.length > 0 ? (
              <ul className="space-y-0.5 font-mono text-[11px] text-muted-foreground">
                {doc.children.map((c) => (
                  <li key={c.id} data-delete-child={c.id}>
                    {c.agent ? `${c.agent} · ` : ""}
                    {c.id}
                    {c.children_count
                      ? ` ${childrenUnder(c.children_count)}`
                      : ""}
                  </li>
                ))}
              </ul>
            ) : null}
            <p className="text-muted-foreground" data-delete-derived>
              {w.derived}
            </p>
            {refused ? (
              <p className="text-status-bad" role="alert" data-delete-refused>
                {refused}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <DialogClose render={<Button variant="outline" size="sm" />}>
              {w.cancel}
            </DialogClose>
            <Button
              variant="destructive"
              size="sm"
              disabled={busy}
              onClick={() => void confirm()}
              data-delete-confirm-action
            >
              {w.action}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

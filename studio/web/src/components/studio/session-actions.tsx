// The session header's verbs (UX-B-29) and its storage line (UX-S3-21):
// copy the public id, export the conversation (GET
// /api/sessions/{id}/export?format=jsonl, fetched with the bearer and
// saved as a blob — never a token in a URL), delete it (DELETE
// /api/sessions/{id}: studio's database only, the app's thread store
// untouched — asked first in an in-page dialog), and open it in the app
// (the newest turn's weft.page_url, run-header's OpenInApp). Each verb
// follows the run page's rules: export needs capability "export",
// delete deleteOffWhy's identity (one faint line says why, never a dead
// button). Words are lib/session-states.ts's.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useRouter } from "@tanstack/react-router"
import { Check, Copy, FileDown, Trash2 } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import {
  deleteSession,
  fetchSessionExport,
  metaQuery,
  studioToken,
} from "@/lib/api"
import type { RunRow, SessionDoc } from "@/lib/api"
import { copyText } from "@/lib/json"
import { deleteOffWhy } from "@/lib/run-actions"
import { saveBlob } from "@/lib/saves"
import { SESSION_ACTIONS, STORAGE, storageOf } from "@/lib/session-states"
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
import { HoleBadge } from "@/components/studio/hole-badge"
import { OpenInApp } from "@/components/studio/run-header"

export function SessionActions({
  doc,
  turns,
}: {
  doc: SessionDoc
  turns: RunRow[]
}) {
  const latest = turns.at(-1)
  return (
    <div
      className="flex flex-wrap items-center gap-2"
      role="group"
      aria-label="session actions"
      data-session-actions
    >
      {doc.public_id ? <CopyPublicID id={doc.public_id} /> : null}
      <ExportSession id={doc.id} />
      {latest ? <OpenInApp doc={latest} /> : null}
      <DeleteSession doc={doc} />
      <StorageLine turns={turns} />
    </div>
  )
}

function CopyPublicID({ id }: { id: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="outline"
      size="xs"
      className="font-mono"
      title={SESSION_ACTIONS.copyPublicTitle}
      onClick={() =>
        void copyText(id).then((ok) => {
          if (!ok) return
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        })
      }
      data-copy-public-id={id}
    >
      {done ? <Check data-slot="icon" /> : <Copy data-slot="icon" />}
      <span aria-live="polite">
        {done ? "copied" : SESSION_ACTIONS.copyPublic}
      </span>
    </Button>
  )
}

function ExportSession({ id }: { id: string }) {
  const meta = useQuery(metaQuery())
  const [busy, setBusy] = useState(false)
  if (!meta.data?.capabilities.includes("export")) return null
  const run = async () => {
    setBusy(true)
    try {
      const { blob, name } = await fetchSessionExport(id)
      saveBlob(blob, name)
      toast.success(SESSION_ACTIONS.exported(name), { id: "export" })
    } catch (e) {
      toast.error(
        SESSION_ACTIONS.exportRefused(
          e instanceof Error ? e.message : String(e)
        ),
        { id: "export" }
      )
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button
      variant="outline"
      size="xs"
      className="font-mono"
      title={SESSION_ACTIONS.export.title}
      disabled={busy}
      onClick={() => void run()}
      data-session-export
    >
      <FileDown data-slot="icon" />
      {SESSION_ACTIONS.export.label}
    </Button>
  )
}

function DeleteSession({ doc }: { doc: SessionDoc }) {
  const meta = useQuery(metaQuery())
  const qc = useQueryClient()
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  if (!meta.data) return null
  const why = deleteOffWhy(
    meta.data.capabilities,
    studioToken(),
    meta.data.live_all,
    meta.data.capabilities_off?.delete
  )
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
  const confirm = async () => {
    setBusy(true)
    setRefused("")
    try {
      await deleteSession(doc.id)
      setOpen(false)
      toast.success(SESSION_ACTIONS.deleted(doc.id), { id: "delete" })
      qc.removeQueries({ queryKey: ["session", doc.id] })
      void qc.invalidateQueries({ queryKey: ["sessions"] })
      void qc.invalidateQueries({ queryKey: ["runs"] })
      await router.navigate({ to: "/sessions" })
    } catch (e) {
      setRefused(
        SESSION_ACTIONS.deleteRefused(
          e instanceof Error ? e.message : String(e)
        )
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
        data-delete-session
      >
        <Trash2 data-slot="icon" />
        {SESSION_ACTIONS.delete}
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
              {SESSION_ACTIONS.deleteTitle(doc.id)}
            </DialogTitle>
            <DialogDescription>
              {SESSION_ACTIONS.deleteBody(doc.turns)}
            </DialogDescription>
          </DialogHeader>
          {refused ? (
            <p
              className="text-xs text-status-bad"
              role="alert"
              data-delete-refused
            >
              {refused}
            </p>
          ) : null}
          <DialogFooter>
            <DialogClose render={<Button variant="outline" size="sm" />}>
              {SESSION_ACTIONS.cancel}
            </DialogClose>
            <Button
              variant="destructive"
              size="sm"
              disabled={busy}
              onClick={() => void confirm()}
              data-delete-confirm-action
            >
              {SESSION_ACTIONS.deleteAction}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

/** The storage line (UX-S3-21): the app's own "storage" metadata when
 * its turns carry it, else STORAGE's not_recorded badge naming the
 * thread.WithRunMetadata line that would. */
function StorageLine({ turns }: { turns: RunRow[] }) {
  const v = storageOf(turns)
  if (v)
    return (
      <span
        className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
        title={STORAGE.title(v)}
        data-session-storage={v}
      >
        {STORAGE.label} · {v}
      </span>
    )
  return (
    <span data-session-storage="">
      <HoleBadge {...STORAGE.hole} />
    </span>
  )
}

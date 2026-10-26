// The Settings page (plan H4): what this Studio is and serves, read
// from /api/meta, /api/settings/pricing and /api/runtimes, beside this
// browser's own preferences. Each section is gated on its data: one
// whose data is refused (db.path to a panel token, the runtimes to a
// read-scoped one) says so with the hole badge, one whose capability
// is off says why (capabilities_off) — never an empty pane.
//
// Per browser: the token (lib/api's studio.token), theme, density and
// the pricing table's edits (lib/settings.ts — never sent: Studio
// never writes files; the download is the override file WEFT_PRICING
// or --pricing loads). Served: everything else.
import { useEffect, useMemo, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import {
  ApiError,
  apiBase,
  clearTabToken,
  metaQuery,
  pricingQuery,
  runtimesQuery,
  setStudioToken,
  studioToken,
  tabTokenActive,
} from "@/lib/api"
import type { Meta, PricingSettings } from "@/lib/api"
import { copyText, download } from "@/lib/json"
import { runLink, runtimesLink } from "@/lib/links"
import { countLine, debugScopeWords } from "@/lib/runtimes"
import { setDensity } from "@/lib/density"
import type { Density } from "@/lib/density"
import {
  RATE_FIELDS,
  ROW_FIELDS,
  clearEdits,
  draftOf,
  editsKey,
  emptyRow,
  loadEdits,
  maskToken,
  mcpCommand,
  INGEST_WORDS,
  LOGS_BRIDGE,
  MCP_RECIPE_PATH,
  SETTINGS_SECTIONS,
  pricingFile,
  pricingFileText,
  problems,
  sameDraft,
  saveEdits,
  tokenIdentity,
  withPriceRow,
} from "@/lib/settings"
import type {
  DraftProblem,
  PricingDraft,
  RowField,
  SettingsSectionID,
} from "@/lib/settings"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { HoleBadge } from "@/components/studio/hole-badge"
import { KbdHelpBody } from "@/components/studio/kbd-help"
import { ConceptsBody } from "@/components/studio/concepts"
import { ConnectApp } from "@/components/studio/empty-state"
import { setThemeChoice, useThemeChoice } from "@/components/studio/theme"
import type { ThemeChoice } from "@/components/studio/theme"
import { useDensity } from "@/components/studio/density-toggle"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { STATUS_TEXT } from "@/lib/status-class"

export const Route = createFileRoute("/settings")({
  // ?price=<provider/model>: the pricing table opens with a row for it
  // (U7, an unpriced model's "price this model…" link).
  validateSearch: (search: Record<string, unknown>): { price?: string } =>
    typeof search.price === "string" && search.price
      ? { price: search.price }
      : {},
  component: SettingsPage,
})

function SettingsPage() {
  useDocumentTitle({ page: "settings" })
  const meta = useQuery(metaQuery())
  // #concepts / #concept-<term> (a <Term>'s "more ›", U21.2) and
  // #settings-<section>: scrolled to once the page has drawn it.
  const drawn = !meta.isPending
  useEffect(() => {
    if (!drawn) return
    const id = decodeURIComponent(window.location.hash.slice(1))
    if (id) document.getElementById(id)?.scrollIntoView({ block: "start" })
  }, [drawn])
  if (meta.isPending)
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  if (meta.isError)
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        <h1 className="text-lg font-medium">Settings</h1>
        <p className={`text-sm ${STATUS_TEXT.bad}`} role="alert">
          /api/meta did not answer: {meta.error.message}
        </p>
        <Section id="appearance">
          <Appearance />
        </Section>
        <Section id="keyboard">
          <KbdHelpBody />
        </Section>
        <Section id="concepts">
          <Concepts />
        </Section>
      </div>
    )
  const m = meta.data
  return (
    <div className="mx-auto max-w-5xl pb-12 lg:grid lg:grid-cols-[10rem_minmax(0,1fr)] lg:gap-6">
      <SectionIndex />
      <div className="min-w-0 space-y-4">
        <h1 className="text-lg font-medium">Settings</h1>
        {/* How an app, a page and a coding agent reach THIS Studio
            (U21.2-fix, UX-S5a-10): the empty states' lines. */}
        <Section id="connect">
          <ConnectApp />
        </Section>
        <Section id="studio">
          <StudioFacts meta={m} />
        </Section>
        <Section id="token">
          <TokenSection meta={m} />
        </Section>
        <Section id="database">
          <DatabaseFacts meta={m} />
        </Section>
        <Section id="retention">
          <RetentionFacts meta={m} />
        </Section>
        <Section id="pricing">
          <PricingSection meta={m} />
        </Section>
        <Section id="capabilities">
          <Capabilities meta={m} />
        </Section>
        <Section id="runtimes">
          <RuntimesSection meta={m} />
        </Section>
        <Section id="content">
          <ContentFacts meta={m} />
        </Section>
        <Section id="appearance">
          <Appearance />
        </Section>
        <Section id="keyboard">
          <KbdHelpBody />
        </Section>
        <Section id="concepts">
          <Concepts />
        </Section>
      </div>
    </div>
  )
}

/** The section index (UX-A-32): every section in page order, sticky
 * beside them at desktop width, a wrapped row above them on a phone.
 * Each entry scrolls to its section and focuses its heading — never a
 * bare #fragment, which the page's <base> would resolve against the
 * Studio mount instead of this page. */
function SectionIndex() {
  return (
    <nav
      aria-label="settings sections"
      className="mb-4 lg:mb-0"
      data-settings-index
    >
      <ul className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-xs lg:sticky lg:top-4 lg:flex-col lg:gap-1">
        {SETTINGS_SECTIONS.map((s) => (
          <li key={s.id}>
            <a
              href={`#settings-${s.id}`}
              className="text-muted-foreground hover:text-thread-ink hover:underline"
              onClick={(e) => {
                const h = document.getElementById(`settings-${s.id}`)
                if (!h) return
                e.preventDefault()
                h.scrollIntoView({ block: "start" })
                h.focus({ preventScroll: true })
              }}
            >
              {s.title}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  )
}

function Section({
  id,
  children,
}: {
  id: SettingsSectionID
  children: React.ReactNode
}) {
  const h = `settings-${id}`
  const title = SETTINGS_SECTIONS.find((s) => s.id === id)?.title ?? id
  return (
    <section
      aria-labelledby={h}
      data-section={id}
      className="scroll-mt-4 space-y-3 rounded-2xl bg-card px-5 py-4 text-sm ring-1 ring-foreground/10"
    >
      <h2 id={h} tabIndex={-1} className="text-base font-medium outline-none">
        {title}
      </h2>
      {children}
    </section>
  )
}

/** The Concepts section (U21.2): the glossary, each term anchored
 * concept-<key>, the ? dialog's Concepts tab's list. */
function Concepts() {
  return (
    <div id="concepts" className="scroll-mt-4">
      <p className="mb-3 text-xs text-muted-foreground">
        The words Studio and the devtools panel use, one meaning each. Every
        dotted word in Studio opens its line here.
      </p>
      <ConceptsBody />
    </div>
  )
}

/** A definition list of facts: term, value. */
function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[minmax(8rem,auto)_1fr] gap-x-4 gap-y-1 text-xs">
      {rows.map(([k, v]) => (
        <div key={k} className="contents" data-fact={k}>
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="min-w-0 font-mono break-all">{v}</dd>
        </div>
      ))}
    </dl>
  )
}

/** The words for a capability that is off: meta's reason, else ours. */
function offWhy(meta: Meta, cap: string): string {
  return (
    meta.capabilities_off?.[cap] ??
    `this Studio does not serve capability "${cap}"`
  )
}

function Off({ meta, cap }: { meta: Meta; cap: string }) {
  return (
    <p className="text-xs text-muted-foreground" data-off={cap}>
      <span className="font-mono">{cap}</span> is off: {offWhy(meta, cap)}
    </p>
  )
}

function Unreported({ field }: { field: string }) {
  return (
    <p className="text-xs text-muted-foreground" data-unreported={field}>
      not reported: this Studio predates{" "}
      <span className="font-mono">{field}</span> in /api/meta
    </p>
  )
}

/** The hidden badge for a field db.path's rule withholds. */
function HiddenByRule({ what }: { what: string }) {
  return (
    <HoleBadge
      hole="hidden"
      detail
      reason={`${what} reach the server (dev) token and a loopback request to a Studio with no Token only`}
      fix="open this page with the server (dev) token"
    />
  )
}

// ── Studio ────────────────────────────────────────────────────────

function StudioFacts({ meta }: { meta: Meta }) {
  // The page's mount: the API lives at {base}api/.
  const base = new URL("..", apiBase()).toString()
  return (
    <Facts
      rows={[
        ["endpoint", base],
        ["api", apiBase()],
        ["title", meta.title],
        ["weft", meta.weft_version || "(unknown)"],
        ["studio", meta.studio_version],
        [
          "panel",
          meta.panel_version === undefined
            ? "(not reported)"
            : meta.panel_version === meta.studio_version
              ? meta.panel_version
              : `${meta.panel_version} (stale: the embedded panel predates this studio)`,
        ],
        [
          "pid",
          meta.pid !== undefined ? (
            String(meta.pid)
          ) : meta.db.path !== undefined ? (
            // db.path reached this caller, so the pid's rule did too: a
            // Studio older than the field, not a withheld one.
            <Unreported field="pid" />
          ) : (
            <HiddenByRule what="the pid, db.path and db.size" />
          ),
        ],
        [
          "interrupted after",
          `${meta.interrupted_after_ms} ms of silence a running run reads interrupted`,
        ],
        [
          "ingest",
          meta.ingest_open ? INGEST_WORDS.openFact : INGEST_WORDS.walled,
        ],
        ["ingest health", <IngestHealth meta={meta} />],
        [
          "app logs",
          <span data-logs-bridge className="text-muted-foreground">
            {LOGS_BRIDGE}
          </span>,
        ],
      ]}
    />
  )
}

// IngestHealth is meta.ingest.dropped (U5): what the exporting apps'
// heartbeats said their destination lost on the way here — the
// counter behind a run's "N of M events were lost" gap, and its knob.
function IngestHealth({ meta }: { meta: Meta }) {
  const dropped = meta.ingest?.dropped
  if (!dropped) return <Unreported field="ingest" />
  if (dropped.length === 0)
    return (
      <span data-ingest-health="ok">
        no app reported lost records (weft.records.dropped on its heartbeats and
        its runs&apos; run_start and run_finish records)
      </span>
    )
  return (
    <span data-ingest-health="dropped" className="space-y-0.5">
      {dropped.map((d) => (
        <span key={d.service} className="block" data-ingest-service={d.service}>
          {d.service || "(no service.name)"}:{" "}
          {d.records.toLocaleString("en-US")} records dropped by its destination
        </span>
      ))}
      <span className="block text-muted-foreground">
        raise the destination&apos;s queue with otel.QueueSize(n), or use
        otel.Blocking() under WEFT_ENV=dev (or otel.DevMode())
      </span>
    </span>
  )
}

// ── Token ─────────────────────────────────────────────────────────

function TokenSection({ meta }: { meta: Meta }) {
  const queryClient = useQueryClient()
  const [tok, setTok] = useState(() => studioToken())
  // A hand-off tab (the link fragment /api/panel-handoff hands over) runs on its own
  // panel token, held in this tab's sessionStorage: shown as this tab's,
  // and cleared alone — the stored token every other tab reads stays.
  const [tab, setTab] = useState(() => tabTokenActive())
  const [shown, setShown] = useState(false)
  const [draft, setDraft] = useState("")
  const [note, setNote] = useState("")
  const id = tokenIdentity(tok, meta)
  const refetch = (said: string) => {
    setTok(studioToken())
    setTab(tabTokenActive())
    setShown(false)
    setNote(said)
    // Everything is refetched with the new bearer (or none); meta first,
    // which decides whether the wall comes down or goes up.
    void queryClient.resetQueries()
  }
  const change = (next: string, said: string) => {
    setStudioToken(next)
    refetch(said)
  }
  const clear = () => {
    if (tab) {
      clearTabToken()
      refetch(
        "this tab's hand-off token cleared: the stored token serves again"
      )
    } else change("", "token cleared")
  }
  return (
    <div className="space-y-3">
      <Facts
        rows={[
          [
            tab ? "this tab only" : "stored",
            tok ? (
              <span data-token-view>{shown ? tok : maskToken(tok)}</span>
            ) : (
              "(none)"
            ),
          ],
          ["identity", <span data-identity={id.kind}>{id.words}</span>],
          ...(id.exp
            ? ([["expires", id.exp]] as [string, React.ReactNode][])
            : []),
          [
            "walled",
            meta.auth_required
              ? "yes: every /api request carries the bearer"
              : "no: this Studio has no Token",
          ],
        ]}
      />
      {tok && (
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            aria-pressed={shown}
            onClick={() => setShown((s) => !s)}
          >
            {shown ? "hide" : "show"}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              void copyText(tok).then((ok) =>
                setNote(
                  ok ? "token copied" : "the browser refused the clipboard"
                )
              )
            }
          >
            copy
          </Button>
          <Button size="sm" variant="outline" onClick={clear}>
            clear
          </Button>
        </div>
      )}
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          const next = draft.trim()
          if (!next) return
          setDraft("")
          change(next, "token switched: /api/meta re-read with it")
        }}
      >
        <Input
          type="password"
          autoComplete="off"
          aria-label="new token"
          placeholder="paste a token to switch"
          className="h-8 max-w-sm font-mono text-xs"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button type="submit" size="sm" disabled={!draft.trim()}>
          switch
        </Button>
      </form>
      <p className="text-[11px] text-faint">
        {tab
          ? "This tab runs on the hand-off token its link carried: it stays in this tab (sessionStorage, kept across a reload) and never replaces the stored token; clear drops it alone."
          : "The token stays in this browser (localStorage)."}{" "}
        It travels only as the Authorization header — never in a URL.
      </p>
      <p className="text-[11px] text-faint" data-mcp>
        Read this Studio from an MCP client (Claude Code, Cursor):{" "}
        <code className="font-mono text-foreground select-all">
          {mcpCommand(
            new URL(".", document.baseURI).toString(),
            meta.auth_required
          )}
        </code>{" "}
        — the recipe is <code className="font-mono">{MCP_RECIPE_PATH}</code> in
        the weft repository.
      </p>
      <p className="font-mono text-[11px] text-thread-ink" role="status">
        {note}
      </p>
    </div>
  )
}

// ── Database ──────────────────────────────────────────────────────

function humanBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ["KiB", "MiB", "GiB", "TiB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

function DatabaseFacts({ meta }: { meta: Meta }) {
  // live_all is db.path's rule: true, the caller may read the path —
  // its absence then means the backend names no file.
  const mayRead = meta.live_all === true || meta.pid !== undefined
  const fileless = "(none: this backend names no file)"
  return (
    <Facts
      rows={[
        ["kind", meta.db.kind],
        [
          "path",
          meta.db.path ??
            (mayRead ? (
              fileless
            ) : (
              <HiddenByRule what="db.path, db.size and the pid" />
            )),
        ],
        [
          "size",
          meta.db.size !== undefined ? (
            `${humanBytes(meta.db.size)} (${meta.db.size} B, the WAL included)`
          ) : mayRead ? (
            fileless
          ) : (
            <HiddenByRule what="db.path, db.size and the pid" />
          ),
        ],
      ]}
    />
  )
}

// ── Retention ─────────────────────────────────────────────────────

function RetentionFacts({ meta }: { meta: Meta }) {
  if (meta.retention === undefined) return <Unreported field="retention" />
  return (
    <div className="space-y-2">
      <p className="font-mono text-xs" data-retention={meta.retention ?? "off"}>
        {meta.retention
          ? `${meta.retention}: runs not running and last seen longer ago are swept`
          : "off (Studio keeps every run until told otherwise)"}
      </p>
      <p className="text-[11px] text-muted-foreground">
        Set it with <span className="font-mono">studio.Retain(d)</span>,{" "}
        <span className="font-mono">--retain 720h</span> or{" "}
        <span className="font-mono">WEFT_RETAIN=720h</span> (at least 1h) on{" "}
        <span className="font-mono">weft studio</span> /{" "}
        <span className="font-mono">weft dev</span>;{" "}
        <span className="font-mono">weft vacuum [--retain d]</span> sweeps once
        and shrinks the file.
      </p>
    </div>
  )
}

// ── Pricing ───────────────────────────────────────────────────────

function PricingSection({ meta }: { meta: Meta }) {
  const caps = useCapabilities()
  const on = caps.has("pricing")
  const pricing = useQuery({ ...pricingQuery(), enabled: on })
  const summary = meta.pricing ? (
    <Facts
      rows={[
        ["source", sourceWords(meta.pricing.source)],
        ["dated", meta.pricing.dated],
        ...(meta.pricing.path
          ? ([["path", meta.pricing.path]] as [string, React.ReactNode][])
          : []),
        ["rows", String(meta.pricing.models)],
      ]}
    />
  ) : null
  if (!on)
    return (
      <div className="space-y-2">
        {summary}
        <Off meta={meta} cap="pricing" />
      </div>
    )
  if (pricing.isPending) return <Spinner />
  if (pricing.isError)
    return (
      <div className="space-y-2">
        {summary}
        <Refused err={pricing.error} what="the pricing table" />
      </div>
    )
  return <PricingTable settings={pricing.data} />
}

function sourceWords(s: string): string {
  switch (s) {
    case "default":
      return "default (the pricing.json this weft ships)"
    case "file":
      return "file (WEFT_PRICING / --pricing)"
    case "option":
      return "option (studio.Pricing in Go)"
    default:
      return s
  }
}

/** A refused read: 403 is the hidden badge, anything else its words. */
function Refused({ err, what }: { err: Error; what: string }) {
  if (err instanceof ApiError && err.status === 403)
    return (
      <HoleBadge
        hole="hidden"
        detail
        reason={`your token's access does not reach ${what}: ${err.message}`}
      />
    )
  return (
    <p className={`text-xs ${STATUS_TEXT.bad}`} role="alert">
      {what} did not load: {err.message}
    </p>
  )
}

const FIELD_HEAD: Record<RowField, string> = {
  provider: "provider",
  model_glob: "model glob",
  input: "input",
  output: "output",
  cached_input: "cached input",
  cache_write: "cache write",
  reasoning: "reasoning",
}

function PricingTable({ settings }: { settings: PricingSettings }) {
  const key = editsKey(settings)
  const served = useMemo(() => draftOf(settings), [settings])
  const { price } = Route.useSearch()
  // A ?price= link (U7) leads the draft with a row for that model, its
  // rates to fill — an edit like any other, kept in this browser.
  const initial = () => {
    const d = loadEdits(key) ?? served
    return price ? withPriceRow(d, price) : d
  }
  const [draft, setDraft] = useState<PricingDraft>(initial)
  // Another table served (a restart with another file): its own edits.
  useEffect(() => setDraft(initial()), [key, served, price])
  useEffect(() => {
    if (price)
      document
        .getElementById("settings-pricing")
        ?.scrollIntoView({ block: "start" })
  }, [price])
  const edited = !sameDraft(draft, served)
  const update = (next: PricingDraft) => {
    setDraft(next)
    if (sameDraft(next, served)) clearEdits(key)
    else saveEdits(key, next)
  }
  const setCell = (i: number, f: RowField, v: string) =>
    update({
      ...draft,
      rows: draft.rows.map((r, j) => (j === i ? { ...r, [f]: v } : r)),
    })
  const bad = problems(draft)
  const badAt = (row: number, field: DraftProblem["field"]) =>
    bad.find((p) => p.row === row && p.field === field)
  const file = pricingFile(draft, settings.note)
  return (
    <div className="space-y-3">
      <Facts
        rows={[
          ["source", sourceWords(settings.source)],
          ...(settings.path
            ? ([["path", settings.path]] as [string, React.ReactNode][])
            : []),
          ["dated", settings.dated],
          ...(settings.note
            ? ([["note", settings.note]] as [string, React.ReactNode][])
            : []),
          [
            "state",
            edited ? (
              <span data-pricing-edited>edited in this browser</span>
            ) : (
              "the server's table"
            ),
          ],
        ]}
      />
      <p className="text-xs text-muted-foreground" data-pricing-note>
        Rates are USD per 1M tokens; first match wins; an empty optional rate
        prices that kind at the base rate; an empty provider (or{" "}
        <span className="font-mono">*</span>) matches the model on any
        transport. Edits stay in this browser; to apply them, download the file
        and run Studio with{" "}
        <span className="font-mono">--pricing &lt;file&gt;</span> or{" "}
        <span className="font-mono">WEFT_PRICING</span>.
      </p>
      <label className="flex items-center gap-2 text-xs">
        <span className="text-muted-foreground">dated</span>
        <Input
          value={draft.dated}
          aria-label="dated (YYYY-MM-DD)"
          aria-invalid={badAt(-1, "dated") ? true : undefined}
          className="h-7 w-32 font-mono text-xs"
          onChange={(e) => update({ ...draft, dated: e.target.value })}
        />
      </label>
      <div className="overflow-x-auto">
        <Table data-pricing-table>
          <caption className="sr-only">
            pricing table, USD per 1M tokens
          </caption>
          <TableHeader>
            <TableRow>
              <TableHead scope="col">#</TableHead>
              {ROW_FIELDS.map((f) => (
                <TableHead key={f} scope="col">
                  {FIELD_HEAD[f]}
                </TableHead>
              ))}
              <TableHead scope="col">
                <span className="sr-only">remove</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {draft.rows.map((r, i) => (
              <TableRow key={i} data-pricing-row={i}>
                <TableCell className="font-mono text-[11px] text-faint">
                  {i + 1}
                </TableCell>
                {ROW_FIELDS.map((f) => {
                  const p = badAt(i, f)
                  const rateField = (RATE_FIELDS as readonly string[]).includes(
                    f
                  )
                  return (
                    <TableCell key={f}>
                      <Input
                        value={r[f]}
                        inputMode={rateField ? "decimal" : undefined}
                        aria-label={`row ${i + 1} ${FIELD_HEAD[f]}`}
                        aria-invalid={p ? true : undefined}
                        title={p?.message}
                        placeholder={
                          rateField && f !== "input" && f !== "output"
                            ? "base"
                            : undefined
                        }
                        className={`h-7 font-mono text-xs ${rateField ? "w-20" : "w-36"}`}
                        onChange={(e) => setCell(i, f, e.target.value)}
                      />
                    </TableCell>
                  )
                })}
                <TableCell>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`remove row ${i + 1}`}
                    onClick={() =>
                      update({
                        ...draft,
                        rows: draft.rows.filter((_, j) => j !== i),
                      })
                    }
                  >
                    ×
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {bad.length > 0 && (
        <ul
          className={`space-y-0.5 text-xs ${STATUS_TEXT.bad}`}
          role="alert"
          data-pricing-problems
        >
          {bad.map((p) => (
            <li key={`${p.row}:${p.field}`} className="font-mono">
              {p.row < 0 ? p.field : `models[${p.row}].${p.field}`}: {p.message}
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            update({ ...draft, rows: [...draft.rows, emptyRow()] })
          }
        >
          add row
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={!edited}
          onClick={() => update(served)}
        >
          reset to the server&apos;s table
        </Button>
        <Button
          size="sm"
          disabled={!file}
          title={
            file
              ? "the override file WEFT_PRICING / --pricing loads"
              : "fix the fields above first"
          }
          onClick={() =>
            file && download("pricing.json", pricingFileText(file))
          }
        >
          download pricing.json
        </Button>
      </div>
    </div>
  )
}

// ── Capabilities ──────────────────────────────────────────────────

function Capabilities({ meta }: { meta: Meta }) {
  const off = Object.entries(meta.capabilities_off ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  )
  return (
    <div className="grid gap-4 md:grid-cols-[1fr_2fr]">
      <div className="space-y-1">
        <h3 className="eyebrow" id="caps-on">
          on · {meta.capabilities.length}
        </h3>
        <ul
          aria-labelledby="caps-on"
          className="flex flex-wrap gap-1.5"
          data-caps-on
        >
          {meta.capabilities.map((c) => (
            <li
              key={c}
              className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[11px]"
            >
              {c}
            </li>
          ))}
        </ul>
      </div>
      <div className="space-y-1">
        <h3 className="eyebrow" id="caps-off">
          off · {off.length}
        </h3>
        {meta.capabilities_off === undefined &&
        meta.manifest_sources === undefined ? (
          // capabilities_off is omitted when nothing is off; a Studio
          // without manifest_sources (always present, the same plan B4)
          // predates both.
          <Unreported field="capabilities_off" />
        ) : off.length === 0 ? (
          <p className="text-xs text-muted-foreground">nothing is off</p>
        ) : (
          <ul aria-labelledby="caps-off" className="space-y-1" data-caps-off>
            {off.map(([c, why]) => (
              <li key={c} className="text-xs" data-cap-off={c}>
                <span className="font-mono">{c}</span>{" "}
                <span className="text-muted-foreground">— {why}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

// ── Runtimes ──────────────────────────────────────────────────────

function RuntimesSection({ meta }: { meta: Meta }) {
  const caps = useCapabilities()
  const on = caps.has("runtimes")
  const runtimes = useQuery({
    ...runtimesQuery(),
    enabled: on,
    refetchInterval: on ? 5_000 : false,
  })
  const count =
    meta.runtimes === undefined ? null : (
      <p className="text-xs" data-runtime-count={meta.runtimes}>
        {meta.runtimes} connected (holding a command stream now)
      </p>
    )
  const mc = meta.manifest_check
  // The manifest check (UX-S4-01): what /agents badges, in numbers.
  const check = mc ? (
    <p className="text-xs" data-manifest-check>
      manifest: checked {mc.checked} of {mc.agents} · stale {mc.stale.length}
      {mc.stale.length ? ` (${mc.stale.join(", ")})` : ""} · differs{" "}
      {mc.differs?.length ?? 0}
      {mc.error ? ` · ${mc.error}` : ""}
    </p>
  ) : null
  if (!on)
    return (
      <div className="space-y-2">
        {count}
        {check}
        <Off meta={meta} cap="runtimes" />
      </div>
    )
  return (
    <div className="space-y-2">
      {count}
      {runtimes.data ? (
        <p className="text-xs" data-runtime-states>
          {countLine(runtimes.data.runtimes)}
        </p>
      ) : runtimes.isError ? (
        <Refused err={runtimes.error} what="the runtimes" />
      ) : null}
      {check}
      <p className="text-xs text-muted-foreground" data-debug-scope>
        {debugScopeWords(meta.debug_scope)}
      </p>
      <Link
        {...runtimesLink()}
        className="inline-block font-mono text-xs text-thread-ink hover:underline"
        data-runtimes-link
      >
        open Runtimes — each runtime, its agents, what Studio may do, its
        breakpoints
      </Link>
    </div>
  )
}

// ── Content policy ────────────────────────────────────────────────

function ContentFacts({ meta }: { meta: Meta }) {
  const c = meta.content
  if (!c) return <Unreported field="content" />
  const l = c.latest
  return (
    <div className="space-y-2 text-xs">
      <p data-content-ingest={c.ingest}>
        {c.ingest === "as_received"
          ? "Studio stores content as received: it never strips."
          : `Studio's ingest policy: ${c.ingest}`}
      </p>
      {c.error ? (
        <p className={`${STATUS_TEXT.bad}`} role="alert">
          the latest run could not be read: {c.error}
        </p>
      ) : l === null ? (
        <p className="text-muted-foreground">no run stored yet</p>
      ) : (
        <div className="space-y-1" data-content-mark={l.mark}>
          <p>
            latest run{" "}
            <Link
              {...runLink(l.run_id)}
              className="font-mono [overflow-wrap:anywhere] text-thread-ink hover:underline"
            >
              {l.run_id}
            </Link>
            : <span className="font-mono">{l.mark}</span> — {l.note}
          </p>
          {l.fix && <p className="text-muted-foreground">fix: {l.fix}</p>}
        </div>
      )}
      <p className="text-[11px] text-faint">
        Each destination&apos;s policy is the app&apos;s (
        <span className="font-mono">otel.NoContent()</span> on a destination,{" "}
        <span className="font-mono">weft.Content(false)</span> on an agent):
        Studio sees only what arrived, and says so per run.
      </p>
    </div>
  )
}

// ── Appearance ────────────────────────────────────────────────────

function Appearance() {
  const theme = useThemeChoice()
  const density = useDensity()
  const themes: [ThemeChoice, string][] = [
    ["system", "auto (the system's)"],
    ["light", "light"],
    ["dark", "dark"],
  ]
  const densities: [Density, string][] = [
    ["comfortable", "comfortable"],
    ["compact", "compact"],
  ]
  return (
    <div className="flex flex-wrap gap-8 text-xs">
      <fieldset className="space-y-1">
        <legend className="mb-1 text-muted-foreground">theme</legend>
        {themes.map(([v, label]) => (
          <label key={v} className="flex items-center gap-2">
            <input
              type="radio"
              name="settings-theme"
              value={v}
              checked={theme === v}
              onChange={() => setThemeChoice(v)}
            />
            {label}
          </label>
        ))}
      </fieldset>
      <fieldset className="space-y-1">
        <legend className="mb-1 text-muted-foreground">density</legend>
        {densities.map(([v, label]) => (
          <label key={v} className="flex items-center gap-2">
            <input
              type="radio"
              name="settings-density"
              value={v}
              checked={density === v}
              onChange={() => setDensity(v)}
            />
            {label}
          </label>
        ))}
      </fieldset>
      <p className="basis-full text-[11px] text-faint">
        Kept in this browser; the sidebar&apos;s toggles change the same.
      </p>
    </div>
  )
}

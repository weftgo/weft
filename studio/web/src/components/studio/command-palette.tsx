// The ⌘K palette (plan H1): one box that finds a run, a session, a
// trace or an experiment and jumps to it, lists the pages (Playground
// and Settings among them) and runs the Studio's own commands. Runs
// are searched through /api/search?q= when the Studio searches
// (capability "search": the prompt, answer, error and tool names),
// else matched by id over the recent runs; sessions over the newest
// page's ids, public ids and agents; traces over the newest page of
// /api/traces (ids, root names, services — the API has no text
// filter); experiments by name. A pasted id prefix, a public id or a
// key=value tag is looked up on the server and its runs rank first as
// exact matches, Enter opening the first; every other item matches by
// contiguous substrings, never a subsequence (lib/finder.ts, U26), and
// with no exact match Enter opens nothing until the user moves the
// selection (acceptSelection, P8-fix-b). Each group is there only under its
// capability ("traces", "playground" for experiments — and no panel
// token, which the experiments routes refuse). cmdk gives the
// dialog, the combobox and the listbox: ↑↓ move, Enter opens, Escape
// closes. Every jump is a links.ts link. No separator between the
// groups: a listbox may hold only groups and options (axe's
// aria-required-children), and the headings divide them.
import { useEffect, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useRouter } from "@tanstack/react-router"
import {
  Activity,
  Bot,
  FlaskConical,
  Keyboard,
  Link2,
  List,
  MessagesSquare,
  Network,
  PanelsLeftRight,
  Rows3,
  Server,
  Settings,
  SunMoon,
} from "lucide-react"

import {
  experimentsQuery,
  fetchRuns,
  fetchSearch,
  fetchTraces,
  metaQuery,
  runsQuery,
  sessionsQuery,
} from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { toggleDensity } from "@/lib/density"
import {
  acceptSelection,
  EXACT_VALUE,
  exactRows,
  lookupsOf,
  metaKeywords,
  movesSelection,
  paletteFilter,
  tagHintFor,
} from "@/lib/finder"
import { resetPaneSizes } from "@/lib/pane-sizes"
import {
  experimentLink,
  playgroundLink,
  runLink,
  runsLink,
  sessionLink,
  runtimesLink,
  settingsLink,
  traceLink,
  tracesLink,
} from "@/lib/links"
import { cycleTheme } from "@/components/studio/theme"
import { mayReadExperiments } from "@/components/studio/no-experiments"
import { useDensity } from "@/components/studio/density-toggle"
import { StatusDot, TitleText } from "@/components/studio/runs-table"
import { runTitle, sessionTitle, shortId } from "@/lib/title"
import type { Title } from "@/lib/title"
import { Kbd } from "@/components/ui/kbd"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"

export const PALETTE_PLACEHOLDER =
  "Search runs, sessions, traces, experiments… or a command"

/** The input's settled text: what the run search asks for. */
function useDebounced(value: string, ms: number): string {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

export function CommandPalette({
  open,
  onOpenChange,
  onCopyLink,
  onHelp,
  initial = "",
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCopyLink: () => void
  onHelp: () => void
  /** Words typed in as it opens (lib/not-found.ts's openPalette). */
  initial?: string
}) {
  const router = useRouter()
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const has = (c: string) => caps.includes(c)
  const density = useDensity()
  const [input, setInput] = useState("")
  // cmdk's selection, held here so its automatic pick of the first item
  // can be refused (acceptSelection). A refusal toggles "" and " " —
  // both read as no selection (cmdk trims) — because cmdk sets its own
  // value before it asks, and an unchanged prop would not undo that.
  const [selected, setSelected] = useState("")
  // The user moved the selection since the text last changed.
  const moved = useRef(false)
  useEffect(() => {
    if (!open) setInput("")
    else if (initial) setInput(initial)
  }, [open, initial])
  const term = useDebounced(input.trim(), 250)

  // Fetched only while the palette is open, each under its capability.
  const recent = useQuery({ ...runsQuery({}), enabled: open })
  const searchable = has("search")
  const hits = useQuery({
    queryKey: ["palette", "search", term],
    staleTime: 5_000,
    queryFn: () => fetchSearch({ q: term, limit: 10 }),
    enabled: open && searchable && term.length > 0,
  })
  // The exact lookups (lib/finder.ts): an id prefix (children too), a
  // public id, a tag — each its own small /api/runs read.
  const look = lookupsOf(term)
  const byPrefix = useQuery({
    queryKey: ["palette", "id_prefix", look.idPrefix],
    staleTime: 5_000,
    queryFn: () =>
      fetchRuns({ id_prefix: look.idPrefix, all: true, limit: 10 }),
    enabled: open && !!look.idPrefix,
  })
  const byPublic = useQuery({
    queryKey: ["palette", "public_id", look.publicId],
    staleTime: 5_000,
    queryFn: () =>
      fetchRuns({ public_id: look.publicId, all: true, limit: 10 }),
    enabled: open && !!look.publicId,
  })
  const byTag = useQuery({
    queryKey: ["palette", "tag", look.tag?.key, look.tag?.value],
    staleTime: 5_000,
    queryFn: () =>
      fetchRuns({
        tag: look.tag ? { [look.tag.key]: look.tag.value } : {},
        all: true,
        limit: 10,
      }),
    enabled: open && !!look.tag,
  })
  const sessions = useQuery({ ...sessionsQuery({}), enabled: open })
  const tracesOn = has("traces")
  const traces = useQuery({
    queryKey: ["traces", "palette"],
    staleTime: 5_000,
    queryFn: () => fetchTraces({ limit: 50 }),
    enabled: open && tracesOn,
  })
  const playgroundOn = has("playground")
  // Never under a panel token: the experiments routes refuse it.
  const experimentsOn = mayReadExperiments(playgroundOn ? ["playground"] : [])
  const experiments = useQuery({
    ...experimentsQuery(),
    enabled: open && experimentsOn,
  })

  const close = () => onOpenChange(false)
  const go = (nav: () => Promise<unknown>) => {
    close()
    void nav()
  }

  // Search hits first (kept whatever cmdk's own match says: they matched
  // on the prompt, answer, error or a tool name — but only while they
  // answer the text in the box: a previous term's hits under new text
  // are matched by cmdk like any row), then the recent runs cmdk
  // matches by id, agent and prompt.
  // The exact matches first, only while they answer the text in the
  // box (a stale answer under new text is dropped, not ranked first).
  const settled = term === input.trim()
  const exact = settled
    ? exactRows(look, {
        idPrefix: byPrefix.data?.runs,
        publicId: byPublic.data?.runs,
        tag: byTag.data?.runs,
      })
    : []
  const exactIDs = new Set(exact.map((r) => r.id))
  const tagHint = tagHintFor(look, exact.length, settled)
  const onSelection = (v: string) => {
    if (
      acceptSelection({
        text: input,
        exactCount: exact.length,
        moved: moved.current,
      })
    )
      setSelected(v)
    else setSelected((cur) => (cur === "" ? " " : ""))
  }
  const hitRows =
    term && searchable
      ? (hits.data?.runs ?? []).filter((r) => !exactIDs.has(r.id))
      : []
  const hitIDs = new Set(hitRows.map((r) => r.id))
  const recentRows = (recent.data?.runs ?? [])
    .filter((r) => !hitIDs.has(r.id) && !exactIDs.has(r.id))
    .slice(0, 25)
  const runItem = (r: RunRow, hit: boolean, isExact = false) => (
    <CommandItem
      key={r.id}
      value={`${isExact ? EXACT_VALUE : ""}run:${r.id} ${r.agent} ${r.status}`}
      // Found by what it is titled with (U6-fix): the prompt is a
      // keyword beside the id, so a run is found by its words even when
      // this Studio cannot search; its metadata pairs too (U26).
      keywords={[
        ...(r.prompt_preview ? [r.prompt_preview] : []),
        ...metaKeywords(r.meta),
        ...(hit && settled ? [input] : []),
      ]}
      onSelect={() => go(() => router.navigate(runLink(r.id)))}
      data-palette-kind="run"
      data-palette-id={r.id}
    >
      <StatusDot row={r} />
      <PaletteTitle t={runTitle(r)} id={r.id} />
      <span className="ml-auto shrink-0 text-xs text-muted-foreground">
        {r.agent}
      </span>
    </CommandItem>
  )

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Command palette"
      description="Find a run, session, trace or experiment, or jump to a page"
    >
      {/* The dialog is the frame; cmdk's root (its store) is Command —
          without it every item throws on open. */}
      <Command
        filter={paletteFilter}
        value={selected}
        onValueChange={onSelection}
        onKeyDown={(e) => {
          if (movesSelection(e)) moved.current = true
        }}
      >
        <CommandInput
          placeholder={PALETTE_PLACEHOLDER}
          value={input}
          onValueChange={(v) => {
            moved.current = false
            setInput(v)
          }}
        />
        <CommandList
          onPointerMoveCapture={() => {
            moved.current = true
          }}
        >
          <CommandEmpty>Nothing matches.</CommandEmpty>
          {exact.length > 0 ? (
            <CommandGroup heading="Exact match" data-palette-exact>
              {exact.map((r) => runItem(r, false, true))}
            </CommandGroup>
          ) : null}
          <CommandGroup
            heading={
              searchable ? "Runs" : "Runs (by id: this Studio cannot search)"
            }
          >
            {hitRows.map((r) => runItem(r, true))}
            {recentRows.map((r) => runItem(r, false))}
            {(recent.isPending || (searchable && term && hits.isPending)) &&
            open ? (
              <CommandItem disabled value="loading runs">
                loading…
              </CommandItem>
            ) : null}
            {term && searchable && hitRows.length > 0 ? (
              <CommandItem
                value={`run:all matches ${input}`}
                keywords={term === input.trim() ? [input] : undefined}
                onSelect={() =>
                  go(() => router.navigate(runsLink({ q: term })))
                }
              >
                <List data-slot="icon" />
                All runs matching “{term}”
              </CommandItem>
            ) : null}
          </CommandGroup>
          <CommandGroup heading="Sessions">
            {(sessions.data?.sessions ?? []).slice(0, 50).map((s) => (
              <CommandItem
                key={s.id}
                value={`session:${s.id} ${s.public_id} ${s.agent}`}
                keywords={[s.title, s.first_prompt].filter(
                  (k): k is string => !!k
                )}
                onSelect={() => go(() => router.navigate(sessionLink(s.id)))}
                data-palette-kind="session"
                data-palette-id={s.id}
              >
                <MessagesSquare data-slot="icon" />
                <PaletteTitle t={sessionTitle(s)} id={s.id} />
                <span className="ml-auto truncate font-mono text-[11px] text-muted-foreground">
                  {s.public_id || s.agent}
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
          {tracesOn ? (
            <CommandGroup heading="Traces">
              {(traces.data?.traces ?? []).map((t) => (
                <CommandItem
                  key={t.trace_id}
                  value={`trace:${t.trace_id} ${t.root?.name ?? ""} ${t.service}`}
                  onSelect={() =>
                    go(() => router.navigate(traceLink(t.trace_id)))
                  }
                  data-palette-kind="trace"
                >
                  <Network data-slot="icon" />
                  <span className="truncate text-xs">
                    {t.root?.name ?? "(no root span)"}
                  </span>
                  <span className="ml-auto truncate font-mono text-[11px] text-muted-foreground">
                    {t.trace_id.slice(0, 12)} · {t.service}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          ) : null}
          {experimentsOn ? (
            <CommandGroup heading="Experiments">
              {(experiments.data?.experiments ?? []).slice(0, 50).map((x) => (
                <CommandItem
                  key={x.id}
                  value={`experiment:${x.name} ${x.id} ${x.agent}`}
                  onSelect={() =>
                    go(() => router.navigate(experimentLink(x.id)))
                  }
                  data-palette-kind="experiment"
                >
                  <FlaskConical data-slot="icon" />
                  <span className="truncate text-xs">{x.name || x.id}</span>
                  <span className="ml-auto text-xs text-muted-foreground">
                    {x.agent}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          ) : null}
          <CommandGroup heading="Go to">
            <CommandItem
              value="runs list"
              onSelect={() => go(() => router.navigate(runsLink()))}
            >
              <List data-slot="icon" />
              Runs
            </CommandItem>
            <CommandItem
              value="sessions threads"
              onSelect={() => go(() => router.navigate({ to: "/sessions" }))}
            >
              <MessagesSquare data-slot="icon" />
              Sessions
            </CommandItem>
            {tracesOn ? (
              <CommandItem
                value="traces list spans"
                onSelect={() => go(() => router.navigate(tracesLink()))}
              >
                <Network data-slot="icon" />
                Traces
              </CommandItem>
            ) : null}
            <CommandItem
              value="live streaming"
              onSelect={() => go(() => router.navigate({ to: "/live" }))}
            >
              <Activity data-slot="icon" />
              Live
            </CommandItem>
            {meta.data?.has_manifest || playgroundOn ? (
              <CommandItem
                value="agents manifest"
                onSelect={() => go(() => router.navigate({ to: "/agents" }))}
              >
                <Bot data-slot="icon" />
                Agents
              </CommandItem>
            ) : null}
            {playgroundOn ? (
              <CommandItem
                value="playground experiment"
                onSelect={() => go(() => router.navigate(playgroundLink()))}
              >
                <FlaskConical data-slot="icon" />
                Playground
              </CommandItem>
            ) : null}
            {meta.data?.capabilities.includes("runtimes") ? (
              <CommandItem
                value="runtimes runtime link breakpoints"
                onSelect={() => go(() => router.navigate(runtimesLink()))}
              >
                <Server data-slot="icon" />
                Runtimes
              </CommandItem>
            ) : null}
            {/* Settings (plan H4). */}
            <CommandItem
              value="settings pricing preferences"
              onSelect={() => go(() => router.navigate(settingsLink()))}
            >
              <Settings data-slot="icon" />
              Settings
            </CommandItem>
          </CommandGroup>
          <CommandGroup heading="Studio">
            <CommandItem
              value="copy link to this page url share yank"
              onSelect={() => {
                close()
                onCopyLink()
              }}
            >
              <Link2 data-slot="icon" />
              Copy link to this page
              <Kbd className="ml-auto">y</Kbd>
            </CommandItem>
            <CommandItem
              value="theme toggle dark light"
              onSelect={() => {
                cycleTheme()
                close()
              }}
            >
              <SunMoon data-slot="icon" />
              Cycle theme
            </CommandItem>
            <CommandItem
              value="density compact comfortable spacing toggle"
              onSelect={() => {
                toggleDensity()
                close()
              }}
            >
              <Rows3 data-slot="icon" />
              {density === "compact"
                ? "Comfortable density"
                : "Compact density"}
            </CommandItem>
            <CommandItem
              value="reset pane layout split sizes"
              onSelect={() => {
                resetPaneSizes()
                close()
              }}
            >
              <PanelsLeftRight data-slot="icon" />
              Reset pane layout
            </CommandItem>
            <CommandItem
              value="keyboard help shortcuts"
              onSelect={() => {
                close()
                onHelp()
              }}
            >
              <Keyboard data-slot="icon" />
              Keyboard help
            </CommandItem>
          </CommandGroup>
        </CommandList>
        {/* Outside the listbox (only groups and options belong there). */}
        {tagHint ? (
          <p
            className="border-t px-3 py-1.5 text-[11px] text-muted-foreground"
            role="note"
            data-palette-tag-hint
          >
            {tagHint}
          </p>
        ) : null}
      </Command>
    </CommandDialog>
  )
}

/** PaletteTitle is a run's or a session's line in the palette (U6):
 * its title (lib/title.ts), the short id beside it in mono — the id
 * itself when it is all there is. */
function PaletteTitle({ t, id }: { t: Title; id: string }) {
  return (
    <span className="flex min-w-0 items-baseline gap-1.5 text-xs">
      <TitleText t={t} />
      {t.kind === "id" ? null : (
        <span className="shrink-0 font-mono text-[11px] text-faint">
          {shortId(id)}
        </span>
      )}
    </span>
  )
}

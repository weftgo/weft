// The app shell (plan §4.3): sidebar navigation (Runs, Sessions,
// Traces under its capability, Live; Agents only when a manifest is
// configured), a header with the current place, the ⌘K palette
// (command-palette.tsx: runs, sessions, traces, experiments, pages,
// commands), and a footer with the
// theme and density toggles and the weft version and store from api/meta.
//
// Hydration rule: the shell is prerendered once, at /runs, and every
// deep link hydrates against it. Anything whose markup depends on the
// location (the outlet, the place breadcrumb, the active nav item)
// renders inside <ClientOnly>, so the first client render matches the
// shell byte for byte. A mismatch is not cosmetic here: React would
// re-render the document, the runtime <base> would move, and every
// module script would resolve against the page path instead of the
// mount (plan §9 Q3).
import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  ClientOnly,
  Link,
  Outlet,
  useLocation,
  useNavigate,
} from "@tanstack/react-router"
import {
  Activity,
  Bot,
  FlaskConical,
  Grid3x3,
  Keyboard,
  List,
  MessagesSquare,
  Network,
  Search,
  Server,
  Settings,
} from "lucide-react"

import { metaQuery, runQuery, studioToken } from "@/lib/api"
import { ownTurn } from "@/lib/honesty"
import { TURN_STEPPER } from "@/lib/timeline"
import { shortId } from "@/lib/title"
import { canReplay } from "@/lib/replay"
import { applyStoredDensity } from "@/lib/density"
import { goChord, isPlainShortcut } from "@/lib/keys"
import { useCopyLink } from "@/hooks/use-copy-link"
import { useLiveGaveUpNotices, useRuntimeNotices } from "@/hooks/use-notices"
import { Toaster } from "@/components/ui/sonner"
import { KbdHelpBody } from "@/components/studio/kbd-help"
import { ConceptsBody } from "@/components/studio/concepts"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { TokenWall, isUnauthorized } from "@/components/studio/token-wall"
import { PALETTE_EVENT } from "@/lib/not-found"
import { SKIP_TO_CONTENT, TOKEN_WALL } from "@/lib/settings"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ThemeToggle, applyStoredTheme } from "@/components/studio/theme"
import { DensityToggle } from "@/components/studio/density-toggle"
import { WeftWordmark } from "@/components/studio/weft-mark"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { CommandPalette } from "@/components/studio/command-palette"
import { mayReadExperiments } from "@/components/studio/no-experiments"
import {
  experimentsLink,
  playgroundLink,
  runLink,
  sessionLink,
  runtimesLink,
  settingsLink,
  tracesLink,
} from "@/lib/links"

const NEEDS_TOKEN = TOKEN_WALL.needs

/** skipToContent focuses the page's first stop after its chrome: an
 * element the page marks data-skip-target (the runs list's table, past
 * its filters), else the main content. A plain #fragment would resolve
 * against the Studio mount's <base>, not this page. */
function skipToContent(e: React.MouseEvent) {
  e.preventDefault()
  const t =
    document.querySelector<HTMLElement>("[data-skip-target]") ??
    document.getElementById("main-content")
  t?.focus()
  t?.scrollIntoView({ block: "nearest" })
}

/** useTurnPlace is the session a run page's run is a turn of (UX-B-21)
 * and the run's parent, read from the run document the page already
 * holds (never fetched here); "" off a run page and for any other run. */
function useTurnPlace(): { session: string; turn: number; parent: string } {
  const location = useLocation()
  const parts = location.pathname.split("/").filter(Boolean)
  const id = parts[0] === "runs" ? safeDecode(parts.slice(1).join("/")) : ""
  const run = useQuery({ ...runQuery(id), enabled: false })
  const parent = (id && run.data?.parent_run_id) || ""
  return run.data && id && ownTurn(run.data)
    ? { session: run.data.session_id, turn: run.data.turn, parent }
    : { session: "", turn: 0, parent }
}

function Nav() {
  const meta = useQuery(metaQuery())
  const location = useLocation()
  // A session's turn is a place in its session: Sessions is lit.
  const inSession = !!useTurnPlace().session
  // UX-F-26: behind the token wall meta answers 401, so nothing says
  // which capability-gated pages exist; they stay listed, disabled, so
  // the sidebar does not change shape once the token is in.
  const walled = isUnauthorized(meta.error)
  const locked = (label: string, icon: React.ReactNode) => (
    <SidebarMenuItem key={label}>
      <SidebarMenuButton
        aria-disabled
        tooltip={`${label} · ${NEEDS_TOKEN}`}
        data-nav-locked={label}
      >
        {icon}
        <span className="flex min-w-0 flex-1 items-baseline justify-between gap-2">
          <span>{label}</span>
          {/* faint on the sidebar; on the hovered or pressed item's
              wash (sidebar-accent, 4.18:1 for faint) the item's own ink
              (U34-fix4, palette.test.ts). */}
          <span className="font-mono text-[10px] text-faint group-hover/menu-button:text-sidebar-accent-foreground group-active/menu-button:text-sidebar-accent-foreground">
            {NEEDS_TOKEN}
          </span>
        </span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
  const item = (to: string, label: string, icon: React.ReactNode) => (
    <SidebarMenuItem key={to}>
      <SidebarMenuButton
        render={<Link to={to} />}
        isActive={
          to === "/sessions"
            ? location.pathname.startsWith(to) ||
              location.pathname.startsWith("/conversations") ||
              inSession
            : location.pathname.startsWith(to) && !(to === "/runs" && inSession)
        }
        tooltip={label}
      >
        {icon}
        <span>{label}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
  return (
    <SidebarGroup>
      <SidebarGroupContent>
        <SidebarMenu>
          {item("/runs", "Runs", <List data-slot="icon" />)}
          {item("/sessions", "Sessions", <MessagesSquare data-slot="icon" />)}
          {/* The trace list (plan H1): under capability "traces". */}
          {walled && locked("Traces", <Network data-slot="icon" />)}
          {meta.data?.capabilities.includes("traces") &&
            item(tracesLink().to, "Traces", <Network data-slot="icon" />)}
          {item("/live", "Live", <Activity data-slot="icon" />)}
          {/* Agents: a manifest (weft.json or a registration), or a
              runtime link a runtime may register one through (B4). */}
          {walled && locked("Agents", <Bot data-slot="icon" />)}
          {(meta.data?.has_manifest ||
            meta.data?.capabilities.includes("playground")) &&
            item("/agents", "Agents", <Bot data-slot="icon" />)}
          {/* The playground (step 8): offered exactly when the runtime
              link's routes are registered (the capability is computed
              from them, never hard-coded). */}
          {walled && locked("Playground", <FlaskConical data-slot="icon" />)}
          {meta.data?.capabilities.includes("playground") &&
            item(
              playgroundLink().to,
              "Playground",
              <FlaskConical data-slot="icon" />
            )}
          {/* The saved experiments (plan F5): the playground's history,
              under the same capability — never under a panel token,
              which the experiments routes refuse. */}
          {walled && locked("Experiments", <Grid3x3 data-slot="icon" />)}
          {meta.data &&
            mayReadExperiments(meta.data.capabilities) &&
            item(
              experimentsLink().to,
              "Experiments",
              <Grid3x3 data-slot="icon" />
            )}
          {/* The runtimes (U20.1): under capability "runtimes" — every
              "Runs in" line and refusal links here. */}
          {walled && locked("Runtimes", <Server data-slot="icon" />)}
          {meta.data?.capabilities.includes("runtimes") &&
            item(runtimesLink().to, "Runtimes", <Server data-slot="icon" />)}
          {/* Settings (plan H4): every Studio has one. */}
          {item(settingsLink().to, "Settings", <Settings data-slot="icon" />)}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

/** decodeURIComponent that never throws: a run id holding a bare "%"
 * is a malformed escape, and the header must not take the app down. */
function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

/** Where the reader is, for the header — every page's one breadcrumb:
 * runs › [parent ›] id, or a session's turn sessions › its session ›
 * tN (UX-B-21). */
function Place() {
  const location = useLocation()
  const parts = location.pathname.split("/").filter(Boolean)
  const turn = useTurnPlace()
  if (parts[0] === "runs" && parts.length > 1 && turn.session) {
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <Link to="/sessions" className="hover:text-foreground">
          {TURN_STEPPER.crumb}
        </Link>
        <span className="text-faint">›</span>
        <Link
          {...sessionLink(turn.session)}
          className="truncate font-mono hover:text-foreground"
          title={turn.session}
          data-crumb-session={turn.session}
        >
          {shortId(turn.session)}
        </Link>
        <span className="text-faint">›</span>
        <span className="font-mono text-foreground">t{turn.turn}</span>
      </span>
    )
  }
  if (parts[0] === "runs" && parts.length > 1) {
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <Link to="/runs" className="hover:text-foreground">
          runs
        </Link>
        <span className="text-faint">›</span>
        {turn.parent ? (
          <>
            <Link
              {...runLink(turn.parent)}
              className="truncate font-mono hover:text-foreground"
              title="the parent run"
              data-crumb-parent={turn.parent}
            >
              {shortId(turn.parent)}
            </Link>
            <span className="text-faint">›</span>
          </>
        ) : null}
        <span className="truncate font-mono text-foreground">
          {safeDecode(parts.slice(1).join("/"))}
        </span>
      </span>
    )
  }
  const places = [
    "runs",
    "sessions",
    "conversations",
    "traces",
    "live",
    "agents",
    "playground",
    "experiments",
    "runtimes",
    "settings",
  ]
  // min-w-0 + truncate (UX-C-33): at 360 px "playground" or
  // "experiments" beside the wordmark pushed Search 10 px past the edge.
  return (
    <span className="min-w-0 truncate text-xs text-muted-foreground">
      {places.includes(parts[0]) ? parts[0] : "runs"}
    </span>
  )
}

export function AppShell() {
  const meta = useQuery(metaQuery())
  const [paletteOpen, setPaletteOpen] = useState(false)
  // A page opens the palette with words typed in (lib/not-found.ts's
  // openPalette: a 404'd run's "search for it").
  const [paletteQuery, setPaletteQuery] = useState("")
  useEffect(() => {
    const on = (e: Event) => {
      setPaletteQuery(
        String((e as CustomEvent<{ q?: string } | null>).detail?.q ?? "")
      )
      setPaletteOpen(true)
    }
    window.addEventListener(PALETTE_EVENT, on)
    return () => window.removeEventListener(PALETTE_EVENT, on)
  }, [])

  // The theme class survives hydration (see applyStoredTheme), and the
  // density attribute with it (plan H3).
  useEffect(applyStoredTheme, [])
  useEffect(applyStoredDensity, [])

  // The notices no page owns (plan H5): a runtime coming or going, a
  // live stream that gave up. Toasts, raised by lib/notify.ts.
  // A read-scoped panel token may not read the runtimes (canReplay).
  useRuntimeNotices(canReplay(meta.data?.capabilities ?? [], studioToken()))
  useLiveGaveUpNotices()

  // ⌘K / ctrl-K opens the palette; "?" opens the keyboard help from
  // anywhere (A4). "/" is reserved for search where there is a list
  // (wired with the runs list).
  const [helpOpen, setHelpOpen] = useState(false)
  // y copies the page's link as it stands (plan G2), from anywhere and
  // from the palette; the header says "link copied" for a moment.
  const link = useCopyLink()
  const copyLink = link.copy
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.altKey &&
        e.key.toLowerCase() === "k"
      ) {
        e.preventDefault()
        setPaletteOpen((o) => !o)
        return
      }
      if (e.key === "?" && isPlainShortcut(e)) setHelpOpen(true)
      // Not on key repeat: a held y copies once.
      else if (e.key === "y" && !e.repeat && isPlainShortcut(e)) void copyLink()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [copyLink])
  // The go-to chord (g r, g a; kbd-help's global rows) on every page
  // (P8-pre-web #4): heard first (capture), and a key that belongs to
  // it reaches no page's single-key handler — the run page's r is its
  // raw view.
  const navigate = useNavigate()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const c = goChord(e)
      if (c === null) return
      e.stopImmediatePropagation()
      if (c === "/runs" || c === "/agents") {
        e.preventDefault()
        void navigate({ to: c })
      }
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [navigate])

  return (
    <SidebarProvider>
      {/* UX-F-25: the first tab stop on every page. */}
      <a
        href="#main-content"
        onClick={skipToContent}
        className="sr-only z-50 rounded-md bg-background px-3 py-2 text-sm ring-2 ring-ring focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
        data-skip-link
      >
        {SKIP_TO_CONTENT}
      </a>
      {/* The sidebar is the app's navigation landmark (U34-fix3): its
          wordmark, links, toggles and version lines sit in one, as the
          page sits in <main> (axe's region rule). */}
      <Sidebar collapsible="icon" role="navigation" aria-label="Studio">
        <SidebarHeader>
          <div className="flex h-12 items-center px-2">
            <WeftWordmark />
          </div>
        </SidebarHeader>
        <SidebarContent>
          <ClientOnly fallback={null}>
            <Nav />
          </ClientOnly>
        </SidebarContent>
        <SidebarFooter>
          <div className="flex items-center justify-between px-2 pb-1 group-data-[collapsible=icon]:justify-center">
            <span className="flex items-center gap-0.5 group-data-[collapsible=icon]:flex-col">
              <ThemeToggle />
              <DensityToggle />
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground group-data-[collapsible=icon]:hidden"
              aria-label="keyboard help"
              title="keyboard help (?)"
              onClick={() => setHelpOpen(true)}
            >
              <Keyboard data-slot="icon" />
            </Button>
          </div>
          {meta.data && (
            <div className="space-y-0.5 px-3 pb-2 font-mono text-[10px] text-faint group-data-[collapsible=icon]:hidden">
              <div>weft {meta.data.weft_version || "(unknown)"}</div>
              <div>
                studio {meta.data.studio_version} · {meta.data.db.kind}
              </div>
            </div>
          )}
        </SidebarFooter>
      </Sidebar>
      {/* min-w-0 (H9, UX-A-09): the page never widens past the window — a
          wide table scrolls inside its own frame, never the page. */}
      <SidebarInset className="min-w-0">
        <header className="flex h-12 min-w-0 items-center gap-3 border-b px-4">
          <SidebarTrigger className="-ml-1" />
          <WeftWordmark className="md:hidden" />
          <ClientOnly fallback={null}>
            <Place />
          </ClientOnly>
          <span
            className="ml-auto font-mono text-[11px] text-thread-ink"
            role="status"
            data-link-copied={link.copied ? "" : undefined}
          >
            {link.copied
              ? "link copied"
              : link.state === "failed"
                ? "copy failed — the browser refused the clipboard"
                : ""}
          </span>
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-2 text-muted-foreground"
            onClick={() => setPaletteOpen(true)}
          >
            <Search data-slot="icon" />
            <span className="hidden sm:inline">Search</span>
            <Kbd className="ml-1">⌘K</Kbd>
          </Button>
        </header>
        <div
          id="main-content"
          tabIndex={-1}
          className="min-w-0 flex-1 p-4 outline-none"
        >
          <ClientOnly
            fallback={
              <div className="flex justify-center py-24">
                <Spinner />
              </div>
            }
          >
            <TokenWall>
              <Outlet />
            </TokenWall>
          </ClientOnly>
        </div>
      </SidebarInset>

      <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Help</DialogTitle>
            <DialogDescription>
              Studio is keyboard-first; the words it uses are under concepts.
            </DialogDescription>
          </DialogHeader>
          {/* Keys and Concepts (U21.2, UX-A-20): the glossary beside
              the bindings — also /settings#concepts. */}
          <Tabs defaultValue="keys" data-help-tabs>
            <TabsList>
              <TabsTrigger value="keys">keys</TabsTrigger>
              <TabsTrigger value="concepts" data-help-tab="concepts">
                concepts
              </TabsTrigger>
            </TabsList>
            <TabsContent value="keys">
              <KbdHelpBody />
            </TabsContent>
            <TabsContent value="concepts">
              <ConceptsBody anchors={false} />
            </TabsContent>
          </Tabs>
        </DialogContent>
      </Dialog>

      <CommandPalette
        open={paletteOpen}
        onOpenChange={(o) => {
          setPaletteOpen(o)
          if (!o) setPaletteQuery("")
        }}
        initial={paletteQuery}
        onCopyLink={() => void copyLink()}
        onHelp={() => setHelpOpen(true)}
      />

      {/* The one toaster (plan H5): bottom-right, the app's theme, no
          hotkey — it never takes focus or a key from the page. */}
      <ClientOnly fallback={null}>
        <Toaster position="bottom-right" />
      </ClientOnly>
    </SidebarProvider>
  )
}

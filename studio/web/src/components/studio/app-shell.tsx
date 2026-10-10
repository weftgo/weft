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
} from "@tanstack/react-router"
import {
  Activity,
  Bot,
  FlaskConical,
  Keyboard,
  List,
  MessagesSquare,
  Network,
  Search,
  Settings,
} from "lucide-react"

import { metaQuery, studioToken } from "@/lib/api"
import { canReplay } from "@/lib/replay"
import { applyStoredDensity } from "@/lib/density"
import { isPlainShortcut } from "@/lib/keys"
import { useCopyLink } from "@/hooks/use-copy-link"
import { useLiveGaveUpNotices, useRuntimeNotices } from "@/hooks/use-notices"
import { Toaster } from "@/components/ui/sonner"
import { KbdHelpBody } from "@/components/studio/kbd-help"
import { TokenWall } from "@/components/studio/token-wall"
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
import { playgroundLink, settingsLink, tracesLink } from "@/lib/links"

function Nav() {
  const meta = useQuery(metaQuery())
  const location = useLocation()
  const item = (to: string, label: string, icon: React.ReactNode) => (
    <SidebarMenuItem key={to}>
      <SidebarMenuButton
        render={<Link to={to} />}
        isActive={location.pathname.startsWith(to)}
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
          {meta.data?.capabilities.includes("traces") &&
            item(tracesLink().to, "Traces", <Network data-slot="icon" />)}
          {item("/live", "Live", <Activity data-slot="icon" />)}
          {/* Agents: a manifest (weft.json or a registration), or a
              runtime link a runtime may register one through (B4). */}
          {(meta.data?.has_manifest ||
            meta.data?.capabilities.includes("playground")) &&
            item("/agents", "Agents", <Bot data-slot="icon" />)}
          {/* The playground (step 8): offered exactly when the runtime
              link's routes are registered (the capability is computed
              from them, never hard-coded). */}
          {meta.data?.capabilities.includes("playground") &&
            item(
              playgroundLink().to,
              "Playground",
              <FlaskConical data-slot="icon" />
            )}
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

/** Where the reader is, for the header: runs › id. */
function Place() {
  const location = useLocation()
  const parts = location.pathname.split("/").filter(Boolean)
  if (parts[0] === "runs" && parts.length > 1) {
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <Link to="/runs" className="hover:text-foreground">
          runs
        </Link>
        <span className="text-faint">›</span>
        <span className="truncate font-mono text-foreground">
          {safeDecode(parts.slice(1).join("/"))}
        </span>
      </span>
    )
  }
  const places = ["runs", "sessions", "traces", "live", "agents", "playground", "settings"]
  return (
    <span className="text-xs text-muted-foreground">
      {places.includes(parts[0]) ? parts[0] : "runs"}
    </span>
  )
}

export function AppShell() {
  const meta = useQuery(metaQuery())
  const [paletteOpen, setPaletteOpen] = useState(false)

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

  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
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
      <SidebarInset>
        <header className="flex h-12 items-center gap-3 border-b px-4">
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
        <div className="flex-1 p-4">
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
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Keyboard</DialogTitle>
            <DialogDescription>Studio is keyboard-first.</DialogDescription>
          </DialogHeader>
          <KbdHelpBody />
        </DialogContent>
      </Dialog>

      <CommandPalette
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
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

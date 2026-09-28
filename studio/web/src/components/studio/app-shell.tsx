// The app shell (plan §4.3): sidebar navigation (Runs; Agents only
// when a manifest is configured), a header with the current place, a
// command palette that jumps to any recent run, and a footer with the
// theme toggle and the weft version and store from api/meta.
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
  useRouter,
} from "@tanstack/react-router"
import { Bot, Keyboard, List, Search, SunMoon } from "lucide-react"

import { metaQuery, runsQuery } from "@/lib/api"
import { isPlainShortcut } from "@/lib/keys"
import { KbdHelpBody } from "@/components/studio/kbd-help"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  ThemeToggle,
  applyStoredTheme,
  cycleTheme,
} from "@/components/studio/theme"
import { StatusDot } from "@/components/studio/runs-table"
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
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"

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
          {meta.data?.has_manifest &&
            item("/agents", "Agents", <Bot data-slot="icon" />)}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
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
          {decodeURIComponent(parts.slice(1).join("/"))}
        </span>
      </span>
    )
  }
  return (
    <span className="text-xs text-muted-foreground">
      {parts[0] === "agents" ? "agents" : "runs"}
    </span>
  )
}

export function AppShell() {
  const meta = useQuery(metaQuery())
  const router = useRouter()
  const [paletteOpen, setPaletteOpen] = useState(false)
  // Recent runs feed the palette; fetched only once it opens.
  const recent = useQuery({ ...runsQuery({}), enabled: paletteOpen })

  // The theme class survives hydration (see applyStoredTheme).
  useEffect(applyStoredTheme, [])

  // ⌘K / ctrl-K opens the palette; "?" opens the keyboard help from
  // anywhere (A4). "/" is reserved for search where there is a list
  // (wired with the runs list).
  const [helpOpen, setHelpOpen] = useState(false)
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
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const go = (to: string, params?: Record<string, string>) => {
    setPaletteOpen(false)
    void router.navigate({ to, params })
  }

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
            <ThemeToggle />
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
                studio {meta.data.studio_version} · {meta.data.store}
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
          <Button
            variant="outline"
            size="sm"
            className="ml-auto h-7 gap-2 text-muted-foreground"
            onClick={() => setPaletteOpen(true)}
          >
            <Search data-slot="icon" />
            <span className="hidden sm:inline">Jump to run</span>
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
            <Outlet />
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

      <CommandDialog open={paletteOpen} onOpenChange={setPaletteOpen}>
        <CommandInput placeholder="Run id, agent, or a command…" />
        <CommandList>
          <CommandEmpty>Nothing matches.</CommandEmpty>
          <CommandGroup heading="Recent runs">
            {(recent.data?.runs ?? []).slice(0, 25).map((r) => (
              <CommandItem
                key={r.id}
                value={`${r.id} ${r.agent} ${r.status}`}
                onSelect={() => go("/runs/$id", { id: r.id })}
              >
                <StatusDot status={r.status} />
                <span className="font-mono text-xs">{r.id}</span>
                <span className="ml-auto text-xs text-muted-foreground">
                  {r.agent}
                </span>
              </CommandItem>
            ))}
            {recent.isPending && paletteOpen ? (
              <CommandItem disabled value="loading">
                loading…
              </CommandItem>
            ) : null}
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Go to">
            <CommandItem value="runs list" onSelect={() => go("/runs")}>
              <List data-slot="icon" />
              Runs
            </CommandItem>
            {meta.data?.has_manifest && (
              <CommandItem
                value="agents manifest"
                onSelect={() => go("/agents")}
              >
                <Bot data-slot="icon" />
                Agents
              </CommandItem>
            )}
          </CommandGroup>
          <CommandGroup heading="Studio">
            <CommandItem
              value="theme toggle dark light"
              onSelect={() => {
                cycleTheme()
                setPaletteOpen(false)
              }}
            >
              <SunMoon data-slot="icon" />
              Cycle theme
            </CommandItem>
            <CommandItem
              value="keyboard help shortcuts"
              onSelect={() => {
                setPaletteOpen(false)
                setHelpOpen(true)
              }}
            >
              <Keyboard data-slot="icon" />
              Keyboard help
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </SidebarProvider>
  )
}

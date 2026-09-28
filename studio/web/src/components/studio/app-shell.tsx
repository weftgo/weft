// The app shell (plan §4.3): sidebar navigation (Runs; Agents only
// when a manifest is configured), a header carrying the mark, the
// wordmark, a mono "studio" chip and the command palette trigger, and
// a footer with the theme toggle and the weft version from api/meta.
import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, Outlet, useLocation, useRouter } from "@tanstack/react-router"
import { Bot, List, Search } from "lucide-react"

import { metaQuery } from "@/lib/api"
import { KbdHelpBody } from "@/components/studio/kbd-help"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ThemeToggle, applyStoredTheme } from "@/components/studio/theme"
import { WeftWordmark } from "@/components/studio/weft-mark"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
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
} from "@/components/ui/command"

function Nav() {
  const meta = useQuery(metaQuery())
  const location = useLocation()
  const item = (to: string, label: string, icon: React.ReactNode) => (
    <SidebarMenuItem key={to}>
      <SidebarMenuButton
        render={<Link to={to} />}
        isActive={location.pathname === to}
        tooltip={label}
      >
        {icon}
        <span>{label}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
  return (
    <SidebarGroup>
      <SidebarGroupLabel>weft</SidebarGroupLabel>
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

export function AppShell() {
  const meta = useQuery(metaQuery())
  const router = useRouter()
  const [paletteOpen, setPaletteOpen] = useState(false)

  // The theme class survives hydration (see applyStoredTheme).
  useEffect(applyStoredTheme, [])

  // ⌘K / ctrl-K opens the palette; "?" opens the keyboard help from
  // anywhere (A4). "/" is reserved for search where there is a list
  // (wired with the runs list).
  const [helpOpen, setHelpOpen] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setPaletteOpen((o) => !o)
        return
      }
      const el = e.target as HTMLElement
      const typing =
        el.tagName === "INPUT" ||
        el.tagName === "TEXTAREA" ||
        el.isContentEditable
      if (e.key === "?" && !typing) setHelpOpen(true)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader>
          <div className="flex h-12 items-center px-2">
            <WeftWordmark />
          </div>
        </SidebarHeader>
        <SidebarContent>
          <Nav />
        </SidebarContent>
        <SidebarFooter>
          <div className="flex items-center justify-between px-2 pb-2 group-data-[collapsible=icon]:justify-center">
            <ThemeToggle />
          </div>
          {meta.data && (
            <div className="px-3 pb-2 font-mono text-[10px] text-faint group-data-[collapsible=icon]:hidden">
              weft {meta.data.weft_version || "(unknown)"}
            </div>
          )}
        </SidebarFooter>
      </Sidebar>
      <SidebarInset>
        <header className="flex h-12 items-center gap-3 border-b px-4">
          <SidebarTrigger className="-ml-1" />
          <WeftWordmark className="md:hidden" />
          <Button
            variant="outline"
            size="sm"
            className="ml-auto h-7 gap-2 text-muted-foreground"
            onClick={() => setPaletteOpen(true)}
          >
            <Search data-slot="icon" />
            <span className="hidden sm:inline">Search</span>
            <Kbd className="ml-1">⌘K</Kbd>
          </Button>
        </header>
        <div className="flex-1 p-4">
          <Outlet />
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
        <CommandInput placeholder="Type a command…" />
        <CommandList>
          <CommandEmpty>No matching command.</CommandEmpty>
          <CommandGroup heading="Go to">
            <CommandItem
              onSelect={() => {
                setPaletteOpen(false)
                void router.navigate({ to: "/runs" })
              }}
            >
              Runs
            </CommandItem>
            {meta.data?.has_manifest && (
              <CommandItem
                onSelect={() => {
                  setPaletteOpen(false)
                  void router.navigate({ to: "/agents" })
                }}
              >
                Agents
              </CommandItem>
            )}
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </SidebarProvider>
  )
}

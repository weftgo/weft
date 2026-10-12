// The Studio classes of a status role (U1): a role is a panel token
// (lib/palette.ts's ROLE) and STUDIO_NAMES the styles.css variable that
// token equals; Tailwind needs the literal class, so this table spells
// it once per variable — palette.test.ts holds it to every ROLE.
// Studio only: the panel draws the token itself (panel/styles.ts).
import { ROLE, STUDIO_NAMES } from "./palette"
import type { Role } from "./palette"

export const ROLE_CLASS: Record<string, { dot: string; text: string }> = {
  "status-run": { dot: "bg-status-run", text: "text-status-run" },
  "status-ok": { dot: "bg-status-ok", text: "text-status-ok" },
  "status-bad": { dot: "bg-status-bad", text: "text-status-bad" },
  "status-int": { dot: "bg-status-int", text: "text-status-int" },
  "status-parked": { dot: "bg-status-parked", text: "text-status-parked" },
  "muted-foreground": {
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
  },
}

/** studioVar is the styles.css variable a role is drawn in. */
export const studioVar = (r: Role) => STUDIO_NAMES.light[ROLE[r]]

export const statusClass = (r: Role) => ROLE_CLASS[studioVar(r)]

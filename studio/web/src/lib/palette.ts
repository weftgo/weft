// The one palette (plan D2): the devtools panel's colours are the
// Studio app's — the tokens src/styles.css defines on :root (light)
// and .dark — so the two surfaces read as one. The panel cannot load
// styles.css (it is a single shadow-root script), so this module holds
// the panel's values and names, for each, the Studio variable it
// equals; palette.test.ts parses styles.css and fails on any drift.
// A token with no Studio name is the panel's own: faint text that
// clears 4.5:1 (Studio's --faint is for eyebrow labels and does not),
// the parked status, the shortcuts overlay's scrim, the dock's shadow.

export type Theme = "light" | "dark"

/** The panel's colour tokens, each --weft-<name> on the panel's :host. */
export const PALETTE: Record<Theme, Record<string, string>> = {
  light: {
    bg: "#fbfaf7",
    bg2: "#fbfaf7",
    bg3: "#f3f1ea",
    fg: "#191713",
    dim: "#6e6759",
    faint: "#766f61",
    line: "#e7e3d9",
    accent: "#d13c10",
    "on-accent": "#fbfaf7",
    warn: "#9a6a06",
    err: "#b42318",
    info: "#2f5bd3",
    ok: "#1f7a45",
    parked: "#8a3fb0",
    scrim: "rgba(251,250,247,.92)",
    shadow: "rgba(25,23,19,.18)",
  },
  dark: {
    bg: "#131211",
    bg2: "#1c1a17",
    bg3: "#2a2722",
    fg: "#ece6d6",
    dim: "#a39c8f",
    faint: "#8d887b",
    line: "#2a2722",
    accent: "#ff7d52",
    "on-accent": "#131211",
    warn: "#e3b341",
    err: "#f87171",
    info: "#7aa2ff",
    ok: "#4ade80",
    parked: "#c678dd",
    scrim: "rgba(19,18,17,.92)",
    shadow: "rgba(0,0,0,.45)",
  },
}

/** The Studio variable (styles.css, :root for light, .dark for dark)
 * each shared token equals; a token not named here is the panel's own. */
export const STUDIO_NAMES: Record<Theme, Record<string, string>> = {
  light: {
    bg: "background",
    bg2: "card",
    bg3: "secondary",
    fg: "foreground",
    dim: "muted-foreground",
    line: "border",
    accent: "thread-ink",
    "on-accent": "primary-foreground",
    warn: "status-int",
    err: "status-bad",
    info: "ev-tool",
    ok: "status-ok",
  },
  dark: {
    bg: "background",
    bg2: "card",
    bg3: "sidebar-accent",
    fg: "foreground",
    dim: "muted-foreground",
    faint: "code-mut",
    line: "border",
    accent: "thread-ink",
    "on-accent": "primary-foreground",
    warn: "status-int",
    err: "status-bad",
    info: "ev-tool",
    ok: "status-ok",
  },
}

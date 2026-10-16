// The one palette (plan D2): the devtools panel's colours are the
// Studio app's — the tokens src/styles.css defines on :root (light)
// and .dark — so the two surfaces read as one. The panel cannot load
// styles.css (it is a single shadow-root script), so this module holds
// the panel's values and names, for each, the Studio variable it
// equals; palette.test.ts parses styles.css and fails on any drift.
// A token with no Studio name is the panel's own: faint text that
// clears 4.5:1 (Studio's --faint is for eyebrow labels and does not),
// the shortcuts overlay's scrim, the dock's shadow. ROLE (U1) names, for
// each status word lib/status.ts draws and the live dot, the one token
// it is drawn in — and so, through STUDIO_NAMES, Studio's variable: a
// status reads the same colour on both surfaces, never a literal.

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
    result: "#4d7a2a",
    parked: "#8a3fb0",
    "status-run": "#ef4d21",
    "status-run-ink": "#d13c10",
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
    result: "#8fbf6a",
    parked: "#c678dd",
    "status-run": "#ff5c2b",
    "status-run-ink": "#ff5c2b",
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
    result: "ev-result",
    parked: "status-parked",
    "status-run": "status-run",
    "status-run-ink": "status-run-ink",
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
    result: "ev-result",
    parked: "status-parked",
    "status-run": "status-run",
    "status-run-ink": "status-run-ink",
  },
}

/** A status's colour role (U1): the panel token it is drawn in;
 * Studio draws the variable STUDIO_NAMES gives that token — the same
 * hex per theme (lib/status-class.ts; palette.test.ts holds them). */
export const ROLE = {
  running: "status-run",
  succeeded: "ok",
  failed: "err",
  interrupted: "warn",
  parked: "parked",
  cancelled: "warn",
  resumed: "dim",
  live: "ok",
} as const

export type Role = keyof typeof ROLE

/** The token a role's WORD is drawn in, where its mark's token is a UI
 * colour under 4.5:1 as text (U1-fix): the running dot, pulse and pill
 * border stay --status-run (3:1 is enough for a mark); the running word
 * is --status-run-ink. Any other token is its own ink. */
export const INK: Record<string, string> = { "status-run": "status-run-ink" }

/** A run tree row's tone (U8.3; lib/waterfall.ts's rowTone): the panel
 * token the Timeline draws it in. Studio's Waterfall draws the tone's
 * own variable (TONE_STUDIO) — the same hex per theme (palette.test.ts):
 * error-as-data is mustard (--ev-error = --weft-warn), a failed row red. */
export const TONE = {
  step: "fg",
  tool: "info",
  result: "result",
  error: "warn",
  bad: "err",
  running: "status-run-ink",
  never: "warn",
} as const
export const TONE_STUDIO: Record<keyof typeof TONE, string> = {
  step: "foreground",
  tool: "ev-tool",
  result: "ev-result",
  error: "ev-error",
  bad: "status-bad",
  running: "status-run-ink",
  never: "ev-error",
}

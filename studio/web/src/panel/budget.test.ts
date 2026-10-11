// The per-item size table the panel build prints (plan phase 3's gate
// evidence; scripts/panel-budget.ts over the committed ledger).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"
import { budgetTable, gzipSize, normalizeStamp, STAMP_PLACEHOLDER } from "../../scripts/panel-budget"
import { weftVersion } from "../../scripts/weft-version"
import type { Ledger } from "../../scripts/panel-budget"

const ledger = JSON.parse(readFileSync(resolve(process.cwd(), "panel-budget.json"), "utf8")) as Ledger

describe("the size ledger (panel-budget.json)", () => {
  it("is append-only: the recorded rows stay as recorded", () => {
    expect(ledger.rows.slice(0, 49).map((r) => [r.label, r.item, r.gzip])).toEqual([
      ["C1", "", 32359],
      ["G1", "", 32700],
      ["C5.2", "", 32810],
      ["C3.2", "C3", 35379],
      ["C3.3", "C3", 36889],
      ["C3.3 fixes", "C3", 37303],
      ["C3.4", "C3", 38764],
      ["C4.2", "C4", 41313],
      ["C3.4 fixes", "C3", 41465],
      ["D1", "D1", 45016],
      ["C4.2 fixes", "C4", 45664],
      ["D1 fixes", "D1", 45836],
      ["D2", "D2", 46953],
      ["D3", "D3", 48102],
      ["D2 fixes", "D2", 48298],
      ["D4", "D4", 52951],
      ["D3 fixes", "D3", 53386],
      ["D4 fixes", "D4", 54040],
      ["D5", "", 55239],
      ["E1", "E1", 60868],
      ["D5 fixes", "", 61258],
      ["E1 fixes", "E1", 61898],
      ["scope fix", "", 61927],
      ["phase 3 review fixes", "", 62591],
      ["verification fixes", "", 62734],
      ["ci fixes", "", 62591],
      ["F1", "F1", 67199],
      ["F1 fixes", "F1", 67245],
      ["F1 review fixes", "F1", 67774],
      ["F1 final fixes", "F1", 67817],
      ["E3", "E3", 70042],
      ["E3 fixes", "E3", 70280],
      ["F3", "F3", 73101],
      ["F3 fixes", "F3", 73193],
      ["F2", "F2", 78015],
      ["F2 fixes", "F2", 79522],
      ["G2 replay URL", "", 79600],
      ["F2 final", "F2", 79707],
      ["review fixes (session 8)", "", 80058],
      ["final web fixes (session 8)", "", 80088],
      ["web closer (session 8)", "", 80096],
      ["A6.1 raw_off cause", "", 80240],
      ["A6.2 wire link", "", 80413],
      ["F6.2 saves", "", 80996],
      ["G3.2 handoff", "", 81175],
      ["G3.2 fixes", "", 81214],
      ["G3.2/F6.2 web round", "", 81315],
      ["final web round", "", 81344],
      ["pre-release web fixes", "", 81358],
    ])
    // The minify lever (plan A5.3.0): one row, the new baseline every later row is measured against.
    expect([ledger.rows[49].label, ledger.rows[49].item, ledger.rows[49].gzip]).toEqual(["minified", "", 73706])
    for (let i = 1; i < ledger.rows.length; i++) expect(ledger.items.some((x) => x.id === ledger.rows[i].item) || ledger.rows[i].item === "").toBe(true)
  })

  it("its last row is the committed panel.js, measured with the version stamp normalized", () => {
    const code = readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")
    // gzipSize, as the build measures: a pure-JS deflate, the same bytes under any host Node.
    const built = gzipSize(normalizeStamp(code, weftVersion()))
    expect(ledger.rows.at(-1)?.gzip).toBe(built)
    // The committed bundle is within the cap, and the cap is 80 KiB.
    expect(ledger.cap).toBe(80 * 1024)
    expect(built).toBeLessThanOrEqual(80 * 1024)
  })

  it("ships minified (plan A5.3.0): no indented line, the newlines left are inside template literals", () => {
    const code = readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")
    const lines = code.split("\n")
    // The unminified es-lib build indented every nested line with a tab (6,443 of its 7,567 lines);
    // the stylesheet's template literal, the only multi-line text left, indents with spaces.
    expect(lines.filter((l) => l.startsWith("\t"))).toEqual([])
    expect(lines.length).toBeLessThan(1000)
  })

  it("a version bump moves no measured byte: the stamp is normalized to a fixed-length placeholder", () => {
    const code = readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")
    // The minifier reprints the define's literal as a template literal.
    const tpl = (v: string) => `\`${v}\``
    const stamp = tpl(weftVersion())
    expect(code.split(stamp).length).toBe(2) // exactly once
    const measure = (v: string) => gzipSize(normalizeStamp(code.split(stamp).join(tpl(v)), v))
    const at = measure(weftVersion())
    for (const v of ["v0.12.0", "v0.100.0", "v1.0.0", "v0.12.0-rc.1"]) expect(measure(v)).toBe(at)
    expect(normalizeStamp(`x=${stamp}`, weftVersion())).toBe(`x=${tpl(STAMP_PLACEHOLDER)}`)
    // The define's JSON-quoted form (an unminified build) is the stamp too.
    expect(normalizeStamp(`x=${JSON.stringify(weftVersion())}`, weftVersion())).toBe(`x=${JSON.stringify(STAMP_PLACEHOLDER)}`)
    expect(() => normalizeStamp("no stamp here", weftVersion())).toThrow(/version stamp/)
  })

  it("prints one line per item: estimate, measured delta, over past half again its estimate; the total and headroom", () => {
    // Over the 49 unminified-era rows: the "minified" row's −7.5 KiB would land in the unbudgeted sum.
    const { lines, over } = budgetTable({ ...ledger, rows: ledger.rows.slice(0, 49) }, 61898)
    expect(over).toBe(false)
    const row = (id: string) => lines.find((l) => l.startsWith(id.padEnd(6))) ?? ""
    expect(row("D1")).toMatch(/drag\/resize\/dock\/persist\s+\+4\.0 KiB\s+\+3\.6 KiB\s+ok$/) // D1 + its fixes
    expect(row("D2")).toMatch(/theme tokens\s+\+1\.0 KiB\s+\+1\.3 KiB\s+ok$/) // D2 + its fixes
    expect(row("D3")).toMatch(/keyed renderer \+ ARIA\s+\+2\.0 KiB\s+\+1\.5 KiB\s+ok$/)
    expect(row("C3")).toMatch(/\+3\.0 KiB\s+\+6\.0 KiB\s+over \(accepted\)$/)
    expect(row("C4")).toMatch(/\+1\.0 KiB\s+\+3\.1 KiB\s+over \(accepted\)$/) // C4.2 + its fixes
    expect(row("D4")).toMatch(/JSON tree \+ filter\s+\+5\.0 KiB\s+\+5\.2 KiB\s+ok$/) // D4 + its fixes
    expect(row("E1")).toMatch(/Request tab\s+\+4\.0 KiB\s+\+6\.1 KiB\s+over \(accepted\)$/) // E1 + its fixes: past half again its estimate, accepted
    expect(row("F2")).toMatch(/transcript editor \+ preview\s+\+4\.0 KiB\s+\+6\.\d KiB\s+over \(accepted\)$/) // F2 + its fixes: past half again its estimate, accepted
    expect(row("F1")).toMatch(/replay from here\s+\+3\.0 KiB\s+\+5\.1 KiB\s+over \(accepted\)$/) // F1 + its fixes: past half again its estimate, accepted
    expect(lines).toContain("baseline C1: 32,359 B")
    expect(lines.find((l) => l.includes("unbudgeted"))).toMatch(/G1, C5\.2, D5, D5 fixes.*\+4\.4 KiB/)
    expect(lines.at(-1)).toBe("total 61,898 B (60.4 KiB) of 81,920 B · headroom 20,022 B (19.6 KiB)")
  })

  it("prints the full ledger's table: the minified and later unbudgeted rows net into one line, 4,614 B of headroom", () => {
    // The whole ledger at its last row (A5.3 fixes, 77,306 B): the "minified" row's −7,652 B against the
    // earlier unbudgeted rows, I1.3's +578 B, F4.3's +1,217 B and F4.3 fixes' +599 B is the unbudgeted
    // line's −0.8 KiB (U+2212, not a hyphen); A5.3 has its own line: A5.3's +1,010 B and its fixes' +196 B
    // against the raised +1,536 B (its accepted note).
    expect(ledger.rows.at(-1)!.label).toBe("A5.3 fixes")
    const { lines, over } = budgetTable(ledger, 77306)
    expect(over).toBe(false)
    const unbudgeted = lines.find((l) => l.includes("unbudgeted")) ?? ""
    expect(unbudgeted).toMatch(/pre-release web fixes, minified, I1\.3, I1\.3 fixes, I1\.3 fixes 2, F4\.3, F4\.3 fixes\)/)
    expect(unbudgeted.endsWith("\u22120.8 KiB")).toBe(true)
    const a53 = lines.find((l) => l.startsWith("A5.3".padEnd(6))) ?? ""
    expect(a53).toMatch(/panel cost per turn \/ experiment\s+\+1\.5 KiB\s+\+1\.2 KiB\s+ok$/)
    const item = ledger.items.find((x) => x.id === "A5.3")!
    expect(item.estimate).toBe(1536)
    expect(76511 - 75501 + (77306 - 77110)).toBeLessThanOrEqual(item.estimate)
    expect(lines.at(-1)).toBe("total 77,306 B (75.5 KiB) of 81,920 B · headroom 4,614 B (4.5 KiB)")
  })

  it("says when the build differs from the last row, and only the cap fails", () => {
    const last = ledger.rows.at(-1)!
    const drift = budgetTable(ledger, last.gzip + 71)
    expect(drift.over).toBe(false)
    expect(drift.lines.some((l) => l.includes(`differs from the ledger's last row (${last.label}, ${last.gzip.toLocaleString("en-US")} B) by +0.1 KiB`))).toBe(true)
    const big = budgetTable(ledger, 81921)
    expect(big.over).toBe(true)
    expect(big.lines.at(-1)).toContain("OVER THE CAP")
  })
})

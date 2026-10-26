// The Concepts list (U21.2; UX-A-20, UX-F-10): every glossary term of
// lib/glossary.ts, by group — the ? dialog's Concepts tab and the
// Settings page's Concepts section (/settings#concepts) draw it. Each
// entry is anchored concept-<key>, where a <Term>'s "more ›" lands.
import { GROUPS, TERMS } from "@/lib/glossary"
import type { TermGroup, TermName } from "@/lib/glossary"

const DOCS = "https://github.com/weftgo/weft/blob/main/"

export function ConceptsBody({ anchors = true }: { anchors?: boolean }) {
  const byGroup = new Map<TermGroup, TermName[]>()
  for (const k of Object.keys(TERMS) as TermName[]) {
    const g = TERMS[k].group
    byGroup.set(g, [...(byGroup.get(g) ?? []), k])
  }
  return (
    <div className="space-y-4" data-concepts>
      {(Object.keys(GROUPS) as TermGroup[]).map((g) => (
        <section key={g} aria-label={GROUPS[g]} className="space-y-1.5">
          <h3 className="eyebrow">{GROUPS[g]}</h3>
          <dl className="space-y-2">
            {(byGroup.get(g) ?? []).map((k) => {
              const t = TERMS[k]
              return (
                <div
                  key={k}
                  id={anchors ? `concept-${k}` : undefined}
                  data-concept={k}
                  className="scroll-mt-4"
                >
                  <dt className="font-mono text-xs font-medium">{t.term}</dt>
                  <dd className="space-y-0.5 text-xs text-muted-foreground">
                    {t.long.split("\n").map((l) => (
                      <p key={l}>{l}</p>
                    ))}
                    <a
                      href={DOCS + t.docs}
                      target="_blank"
                      rel="noreferrer"
                      className="font-mono text-[10px] text-faint hover:text-thread-ink hover:underline"
                    >
                      {t.docs}
                    </a>
                  </dd>
                </div>
              )
            })}
          </dl>
        </section>
      ))}
    </div>
  )
}

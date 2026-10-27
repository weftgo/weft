/** DotWrap is a " · "-joined phrase that wraps only at its dots
 * (P020.2): a narrow column breaks "20 in · 10 out" or "awaiting a
 * decision · 1 call" between its parts, never inside one ("10" apart
 * from "out", "awaiting a" apart from "decision"). */
export function DotWrap({ text }: { text: string }) {
  const parts = text.split(" · ")
  return (
    <>
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 ? " " : null}
          <span className="whitespace-nowrap" data-dot-part>
            {p}
            {i < parts.length - 1 ? " ·" : null}
          </span>
        </span>
      ))}
    </>
  )
}

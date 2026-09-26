
## Amendment (2026-09-26 — the sequence pad widens to five digits)

The filename's sequence field pads to five digits: `<seq:05d>`. The
replayer loads fixtures in directory-listing (lexical) order and
groups them per key in that order, and a three-digit pad sorted
`1000-x.json` before `999-x.json` — so a 1000+-request recording
whose identical key recurred across the boundary replayed out of
order. Replay matches on the key inside the file, never the name, so
fixtures recorded under the old pad still replay unchanged; only
newly recorded names differ (the committed fixtures were renamed for
consistency, git-history preserves the old ones).

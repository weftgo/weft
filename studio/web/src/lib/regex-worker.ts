// The pass predicate's regex runner (U24.2-fix 1): a module worker
// (lib/regex-run.ts spawns it) that tests one user-typed pattern against
// one answer per message and posts the outcome — off the page's thread,
// so a pattern that backtracks catastrophically is terminated by its
// timeout instead of freezing the tab. Built by Vite as its own asset
// under assets/ (CSP: script-src 'self' covers it).
interface Job {
  id: number
  source: string
  flags: string
  text: string
}

self.onmessage = (e: MessageEvent<Job>) => {
  const { id, source, flags, text } = e.data
  let out: { id: number; hit?: boolean; error?: string }
  try {
    out = { id, hit: new RegExp(source, flags).test(text) }
  } catch (err) {
    out = { id, error: err instanceof Error ? err.message : String(err) }
  }
  self.postMessage(out)
}

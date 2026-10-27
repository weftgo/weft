// The retired-words scanner (U21.2; UX-F-10, UX-F-12, UX-F-14, UX-F-17,
// UX-C-22, UX-B-24): a pure function over (path, source text) that finds
// every glossary alias (lib/glossary.ts TERMS[*].aliases — the words a
// renderer must stop saying) in the text that reaches the screen: JSX
// text, string and template literals that are not a module path, a type,
// a comparison operand, an object key or a non-visible attribute
// (className, data-*, href, …).
//
// Matching is whole-word / exact-phrase and case-insensitive. An alias
// that is a common English word ("continue", "skip") only hits when it
// is the whole label — a button or a chip — never inside a sentence.
//
// scripts/vocab-audit.ts (U21.3) runs it over both renderers —
// components/, routes/, lib/ and panel/ — against one reasoned allowlist
// (scripts/vocab-allow.json), inside `make studio-check`; vocab.studio
// .test.ts is the scanner's unit test and the audit's. It imports
// nothing but the TypeScript parser.
import ts from "typescript"

/** One retired word, the concept it belongs to and how it matches. */
export interface Alias {
  /** The retired word or phrase. */
  alias: string
  /** The glossary key of the concept that retires it. */
  term: string
  /** true: only hits as a whole label (a common word: "continue"). */
  labelOnly?: boolean
}

/** One retired word found in user-visible text. */
export interface VocabHit {
  path: string
  /** 1-based line of the literal. */
  line: number
  alias: string
  term: string
  /** The visible text it was found in. */
  text: string
}

/** Aliases that are common words: hit only as a whole label. */
export const LABEL_ONLY = new Set(["continue", "skip"])

/** aliasesOf flattens a TERMS-shaped table into the scanner's aliases. */
export function aliasesOf(
  terms: Record<string, { aliases: readonly string[] }>
): Alias[] {
  const out: Alias[] = []
  for (const [term, t] of Object.entries(terms))
    for (const alias of t.aliases)
      out.push({ alias, term, labelOnly: LABEL_ONLY.has(alias.toLowerCase()) })
  return out
}

/** JSX attributes whose value never reaches the screen as text. */
const HIDDEN_ATTR =
  /^(className|class|key|id|ref|style|to|href|src|type|role|name|value|htmlFor|variant|size|side|align|mode|kind|rel|target|method|autoComplete|inputMode|params|search|hash|tone|axis|data-.*|aria-(controls|labelledby|describedby|owns|haspopup|current|expanded|selected|checked|pressed|hidden|live|level|sort|orientation|valuenow|valuemin|valuemax))$/

/** Calls whose string argument is a key, a capability or a selector. */
const KEY_CALLS =
  /^(includes|has|get|set|delete|startsWith|endsWith|indexOf|test|getItem|setItem|removeItem|querySelector|querySelectorAll|getAttribute|setAttribute|hasAttribute|closest|matches|addEventListener|removeEventListener|useCapability)$/

/** KEY_CALLS whose later arguments are values, not keys. */
const SETTERS = /^(set|setItem|setAttribute)$/

/** Object keys whose value is an identifier, never a word on screen. */
const HIDDEN_KEY =
  /^(key|id|kind|value|to|href|className|type|name|cap|capability)$/

/** Keys whose value is an id in one place and words in another
 * (VIEWS' view: "timeline" vs COMPACTION_CHIP_TITLE.view, a hover
 * title): hidden only when the value has no white space — an id. */
const ID_KEY = /^(view|verb)$/

const COMPARE = new Set([
  ts.SyntaxKind.EqualsEqualsEqualsToken,
  ts.SyntaxKind.ExclamationEqualsEqualsToken,
  ts.SyntaxKind.EqualsEqualsToken,
  ts.SyntaxKind.ExclamationEqualsToken,
])

/** visible reports whether a literal's text can reach the screen. */
function visible(node: ts.Node): boolean {
  const p = node.parent
  if (ts.isImportDeclaration(p) || ts.isExportDeclaration(p)) return false
  if (ts.isExternalModuleReference(p)) return false
  if (ts.isLiteralTypeNode(p)) return false
  if (
    ts.isCallExpression(p) &&
    p.expression.kind === ts.SyntaxKind.ImportKeyword
  )
    return false
  if (ts.isBinaryExpression(p) && COMPARE.has(p.operatorToken.kind))
    return false
  if (ts.isCaseClause(p)) return false
  if (ts.isCallExpression(p) && p.arguments.includes(node as ts.Expression)) {
    const callee = ts.isPropertyAccessExpression(p.expression)
      ? p.expression.name.text
      : p.expression.getText()
    // A setter's key is argument 0; its value (set's, setItem's,
    // setAttribute's second) may be words on screen (P10b-pre-web-fix).
    if (
      KEY_CALLS.test(callee) &&
      (!SETTERS.test(callee) || p.arguments[0] === node)
    )
      return false
  }
  if (ts.isPropertyAssignment(p) && p.name === node) return false
  // A glossary entry's retired words (aliases: [...]) are the list the
  // audit reads, never a label.
  if (
    ts.isArrayLiteralExpression(p) &&
    ts.isPropertyAssignment(p.parent) &&
    ts.isIdentifier(p.parent.name) &&
    p.parent.name.text === "aliases"
  )
    return false
  // A value under a key that never reaches the screen ({ key: "ttft" },
  // cmdk's search value).
  if (
    ts.isPropertyAssignment(p) &&
    (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name)) &&
    (HIDDEN_KEY.test(p.name.text) ||
      (ID_KEY.test(p.name.text) &&
        (ts.isStringLiteral(node) ||
          ts.isNoSubstitutionTemplateLiteral(node)) &&
        !/\s/.test(node.text)))
  )
    return false
  if (ts.isElementAccessExpression(p) && p.argumentExpression === node)
    return false
  if (ts.isJsxAttribute(p)) return !HIDDEN_ATTR.test(p.name.getText())
  if (ts.isJsxExpression(p) && ts.isJsxAttribute(p.parent))
    return !HIDDEN_ATTR.test(p.parent.name.getText())
  return true
}

/** looksLikeCode: a literal with no space that reads as a path, an id,
 * a URL or a selector, never as a word. */
const looksLikeCode = (s: string) =>
  !/\s/.test(s.trim()) && /[/_.:=#[\]]|[a-z][A-Z]/.test(s.trim())

const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

/** scanVocab finds every alias in the visible text of one source file. */
export function scanVocab(
  path: string,
  text: string,
  aliases: readonly Alias[]
): VocabHit[] {
  const kind =
    path.endsWith(".tsx") || path.endsWith(".jsx")
      ? ts.ScriptKind.TSX
      : ts.ScriptKind.TS
  const sf = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, kind)
  const res = aliases.map((a) => ({
    a,
    // A word, not part of a path or an identifier: weft/thread and
    // thread.PublicID are code; "a thread." ends a sentence.
    re: new RegExp(`(?<![\\w/.-])${esc(a.alias)}(?![\\w/-]|\\.\\w)`, "i"),
  }))
  const hits: VocabHit[] = []
  const check = (node: ts.Node, raw: string) => {
    // One space for every run of white space: a phrase JSX wraps over
    // two lines is one phrase on screen.
    const s = raw.replace(/\s+/g, " ")
    // A sentence's last word keeps its colon or full stop: "threads:"
    // is a word, not code.
    if (!s.trim() || looksLikeCode(s.trim().replace(/[:.]+$/, ""))) return
    const label = s
      .trim()
      .replace(/^[^\p{L}]+|[^\p{L}]+$/gu, "")
      .toLowerCase()
    for (const { a, re } of res) {
      const hit = a.labelOnly ? label === a.alias.toLowerCase() : re.test(s)
      if (!hit) continue
      const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1
      hits.push({ path, line, alias: a.alias, term: a.term, text: s.trim() })
    }
  }
  const walk = (node: ts.Node) => {
    if (ts.isJsxText(node)) check(node, node.text)
    else if (
      ts.isStringLiteral(node) ||
      ts.isNoSubstitutionTemplateLiteral(node)
    ) {
      if (visible(node)) check(node, node.text)
    } else if (ts.isTemplateExpression(node)) {
      if (visible(node)) {
        // The literal parts, joined with a placeholder word so a phrase
        // never matches across an interpolation.
        const parts = [
          node.head.text,
          ...node.templateSpans.map((s) => s.literal.text),
        ]
        check(node, parts.join(" … "))
      }
      node.templateSpans.forEach((s) => walk(s.expression))
      return
    }
    ts.forEachChild(node, walk)
  }
  walk(sf)
  return hits
}

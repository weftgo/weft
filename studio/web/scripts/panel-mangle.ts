// The panel's own member names, proven safe to mangle (P9-trim).
//
// panel.js ships fully minified (plan A5.3.0), but a minifier renames
// only bindings: a class member stays `this.scopeExplicit` in the bytes
// at every use. oxc's mangleProps renames property names too — by name,
// across the whole bundle — which is sound only for a name that no
// object from outside the bundle (a response's JSON, the DOM, a host's
// options, storage, an event's detail, the host API) ever carries. This
// module computes that set from the source with the TypeScript
// checker, never from a hand list:
//
//   - a candidate is a `private` member (field, method, accessor,
//     constructor parameter property) of a class in the panel's module
//     graph — TypeScript lets no code outside the class reach it — or
//     any member of an INTERNAL_CLASSES class or an INTERNAL_TYPES
//     interface;
//   - it is kept only when EVERY property-name occurrence of that text
//     in the graph's source (an access, an object literal's key — read
//     as the member its contextual type declares —, a destructuring, an
//     interface's or a type literal's member, a field or method
//     declaration) resolves to such a candidate member: one occurrence
//     on anything else (an interface a response is read through, a DOM
//     or ES lib member, an untyped value) leaves the name verbatim;
//   - and when no string literal in the graph spells it (a computed
//     access, an `in` check, a key list) and it is not one the built
//     bundle's own tests read (RESERVED).
//
// An internal interface is sound only while no value from outside is
// asserted to it: a type assertion to one fails the analysis (and the
// build) instead of mangling around it.
//
// vite.panel.config.ts passes the names as mangleProps' include and
// `reserved` as the names it may never emit (the short members the DOM
// and ES libs declare: the element inherits them, the bundle need not
// spell them); oxc itself never emits a name the bundle spells.
// src/panel/mangle.test.ts pins the analysis and the bytes.
import ts from "typescript"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..")

/** Names the built bundle's tests read verbatim (built-bundle.test.ts:
 * `.fire(\`run\`,{`), never mangled. */
export const RESERVED = ["fire"]

/** The bundle's internal classes: an instance of one is made by the
 * element and held in a private field, never handed to a host (no
 * event detail, no host API return, no global) — so every member, not
 * only a private one, is the bundle's own name. */
export const INTERNAL_CLASSES = ["PanelModel", "ThemeWatch", "Push"]

/** The bundle's internal object shapes, each built by the panel's own
 * code from its own values and read only there: never parsed from a
 * response or storage, never serialized, never drawn by the Raw tab's
 * JSON tree, never handed to a host or to another copy of panel.js.
 *   PanelState        state.ts's emptyPanelState and its spreads
 *   TurnView          state.ts's per-turn view (the Raw tab picks its
 *                     record fields into a fresh object)
 *   ExperimentResult  playground.ts's result, built in state.ts
 *   TreeState         tree.ts's open/filter state (newTree)
 *   RequestTabDeps    element.ts's arguments to renderRequestTab
 *   HeaderRungOptions element.ts's arguments to installHeaderRung
 *   MarkerRungOptions element.ts's arguments to installMarkerRung
 * A type that starts crossing one of those lines comes off this list. */
export const INTERNAL_TYPES = [
  "PanelState",
  "TurnView",
  "ExperimentResult",
  "TreeState",
  "RequestTabDeps",
  "HeaderRungOptions",
  "MarkerRungOptions",
]

export interface MangleAnalysis {
  /** The names mangleProps may rename, sorted. */
  names: string[]
  /** Each candidate left verbatim, with the first reason. */
  kept: Record<string, string>
  /** The short property names the DOM and ES libs declare, sorted:
   * never emitted as a mangled name. */
  reserved: string[]
}

/** isMember: a declaration whose name is the bundle's own to rename. */
function isMember(d: ts.Declaration): boolean {
  if (
    (ts.isPropertySignature(d) || ts.isMethodSignature(d)) &&
    ts.isInterfaceDeclaration(d.parent)
  )
    return INTERNAL_TYPES.includes(d.parent.name.text)
  if (
    !ts.isPropertyDeclaration(d) &&
    !ts.isMethodDeclaration(d) &&
    !ts.isGetAccessorDeclaration(d) &&
    !ts.isSetAccessorDeclaration(d) &&
    !ts.isParameter(d)
  )
    return false
  const flags = ts.getCombinedModifierFlags(d)
  // A constructor parameter is a member only as a parameter property.
  if (ts.isParameter(d))
    if (
      !ts.isConstructorDeclaration(d.parent) ||
      !(flags & ts.ModifierFlags.ParameterPropertyModifier)
    )
      return false
  const cls = ts.isParameter(d) ? d.parent.parent : d.parent
  if (!ts.isClassDeclaration(cls)) return false
  if (cls.name && INTERNAL_CLASSES.includes(cls.name.text)) return true
  return (flags & ts.ModifierFlags.Private) !== 0
}

/** analyzeMangle walks the panel entry's module graph (src/panel/main.ts,
 * the graph the panel build bundles) and returns the safe set. */
export function analyzeMangle(
  entry = resolve(web, "src/panel/main.ts")
): MangleAnalysis {
  const cfg = ts.readConfigFile(resolve(web, "tsconfig.json"), (p) =>
    ts.sys.readFile(p)
  )
  const parsed = ts.parseJsonConfigFileContent(cfg.config, ts.sys, web)
  const program = ts.createProgram([entry], { ...parsed.options, noEmit: true })
  const checker = program.getTypeChecker()
  const src = resolve(web, "src")
  const all = program.getSourceFiles()
  const files = all.filter(
    (f) => !f.isDeclarationFile && f.fileName.startsWith(src)
  )
  const own = new Set(files)
  const rel = (f: ts.SourceFile, n: ts.Node) =>
    `${f.fileName.slice(web.length + 1)}:${f.getLineAndCharacterOfPosition(n.getStart(f)).line + 1}`

  const candidates = new Set<string>()
  const kept: Record<string, string> = {}
  const keep = (name: string, why: string) => {
    if (!(name in kept)) kept[name] = why
  }
  const strings = new Set<string>()
  const spelled: { name: string; node: ts.Identifier; file: ts.SourceFile }[] =
    []

  for (const f of files) {
    const visit = (n: ts.Node) => {
      if (
        (ts.isPropertyDeclaration(n) ||
          ts.isMethodDeclaration(n) ||
          ts.isGetAccessorDeclaration(n) ||
          ts.isSetAccessorDeclaration(n) ||
          ts.isPropertySignature(n) ||
          ts.isMethodSignature(n) ||
          ts.isParameter(n)) &&
        ts.isIdentifier(n.name) &&
        isMember(n)
      )
        candidates.add(n.name.text)
      if (ts.isStringLiteralLike(n)) strings.add(n.text)
      if (ts.isTemplateExpression(n)) {
        strings.add(n.head.text)
        for (const s of n.templateSpans) strings.add(s.literal.text)
      }
      if (
        (ts.isAsExpression(n) || ts.isTypeAssertionExpression(n)) &&
        ts.isTypeReferenceNode(n.type) &&
        ts.isIdentifier(n.type.typeName) &&
        INTERNAL_TYPES.includes(n.type.typeName.text)
      )
        throw new Error(
          `panel-mangle: ${rel(f, n)} asserts a value to ${n.type.typeName.text}, an internal type whose members are mangled — build it instead, or take the type off INTERNAL_TYPES`
        )
      // Every place a property name is spelled.
      let name: ts.Node | undefined
      if (ts.isPropertyAccessExpression(n)) name = n.name
      else if (
        ts.isPropertyAssignment(n) ||
        ts.isShorthandPropertyAssignment(n) ||
        ts.isMethodDeclaration(n) ||
        ts.isPropertyDeclaration(n) ||
        ts.isGetAccessorDeclaration(n) ||
        ts.isSetAccessorDeclaration(n) ||
        ts.isPropertySignature(n) ||
        ts.isMethodSignature(n)
      )
        name = n.name
      else if (ts.isBindingElement(n) && ts.isObjectBindingPattern(n.parent))
        name = n.propertyName ?? n.name
      else if (ts.isParameter(n) && isMember(n)) name = n.name
      else if (ts.isQualifiedName(n)) name = n.right
      if (name && ts.isIdentifier(name))
        spelled.push({ name: name.text, node: name, file: f })
      ts.forEachChild(n, visit)
    }
    visit(f)
  }

  for (const o of spelled) {
    if (!candidates.has(o.name) || o.name in kept) continue
    const where = rel(o.file, o.node)
    const parent = o.node.parent
    let sym: ts.Symbol | undefined
    if (ts.isShorthandPropertyAssignment(parent))
      sym = checker.getSymbolAtLocation(parent.name)
    else sym = checker.getSymbolAtLocation(o.node)
    // An object literal's key declares the literal's own property: the
    // member it fills is its contextual type's.
    const lit = parent.parent
    if (
      ts.isObjectLiteralExpression(lit) &&
      (parent as ts.ObjectLiteralElement).name === o.node
    ) {
      const ctx = checker.getContextualType(lit)
      const prop = ctx && checker.getPropertyOfType(ctx, o.name)
      if (prop) sym = prop
    }
    const decls = sym?.declarations ?? []
    if (!decls.length) {
      keep(o.name, `${where}: unresolved`)
      continue
    }
    for (const d of decls) {
      const df = d.getSourceFile()
      if (!own.has(df)) {
        keep(
          o.name,
          `${where}: ${df.fileName.replace(/^.*node_modules\//, "")}`
        )
        break
      }
      if (!isMember(d)) {
        keep(o.name, `${where}: ${ts.SyntaxKind[d.kind]} at ${rel(df, d)}`)
        break
      }
      // The element's private member read off another element: that one
      // may be a second copy of panel.js (another version, the npm
      // build beside the served one), whose names are its own build's.
      const cls = ts.isParameter(d) ? d.parent.parent : d.parent
      if (
        ts.isPropertyAccessExpression(parent) &&
        parent.expression.kind !== ts.SyntaxKind.ThisKeyword &&
        ts.isClassDeclaration(cls) &&
        !(cls.name && INTERNAL_CLASSES.includes(cls.name.text))
      ) {
        keep(o.name, `${where}: read off another instance than this`)
        break
      }
    }
  }
  for (const c of candidates) {
    if (strings.has(c)) keep(c, "a string literal spells it")
    if (RESERVED.includes(c))
      keep(c, "reserved: the built bundle's tests read it")
  }

  // The short names an instance inherits without the bundle spelling
  // them — the element's from HTMLElement (id, dir, …), the internal
  // classes' from Object: never an output name.
  const reserved = new Set<string>()
  for (const g of ["HTMLElement", "Object"]) {
    const sym = checker.resolveName(g, undefined, ts.SymbolFlags.Type, false)
    if (!sym) throw new Error(`panel-mangle: no ${g} type in the program`)
    for (const p of checker.getDeclaredTypeOfSymbol(sym).getProperties())
      if (p.name.length <= 3) reserved.add(p.name)
  }

  const names = [...candidates].filter((c) => !(c in kept)).sort()
  return { names, kept, reserved: [...reserved].sort() }
}

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
// An internal type is sound only while no value from outside becomes
// one and no reflection reads one's names: the analysis (and the build)
// fails, naming each line, instead of mangling around it (checkFlows), on
//   - a type assertion to a type that is or contains one;
//   - a value of type any reaching a place whose contextual type is or
//     contains one (`const t: TreeState = JSON.parse(…)`, `await
//     res.json()` returned as an ExperimentResult, an any argument to an
//     internal parameter), or a callback's internal-typed parameter its
//     caller feeds any;
//   - a value whose type is or contains one handed to a reflection or
//     serialization sink (JSON.stringify, structuredClone, postMessage,
//     Object.keys/entries/values/assign/fromEntries, Storage.setItem,
//     dispatchEvent, a CustomEvent's or MessageEvent's init, the
//     element's fire, the Raw tab's treeView) — except where the sink's
//     own parameter declares an internal type (treeView's TreeState);
//   - a for…in over one, or one spread into an object literal whose
//     contextual type contains no internal type;
//   - (P9-trim2-fix) one passed as an argument to a parameter typed
//     unknown, any, object, {} or a bare type parameter (fmtJSON,
//     render.ts stringify, a generic helper: what it does with the
//     names is unknown to the checker) — the standard containers' own
//     type parameters (Array, Map, Set, WeakMap, WeakSet, Promise and
//     their constructors, STD_GENERICS) excepted: they hold the value
//     and hand it back typed, never read its names — read as the call
//     instantiates them (P9-pre-web-fix2), so Map<string, unknown>'s set,
//     unknown[]'s push, Set<unknown>'s add or Array.of<unknown> widen it
//     as an unknown parameter does, and a container whose type argument
//     is opaque (Promise<unknown>, Map<string, object>) is an opaque
//     place to hold one; a type parameter whose constraint is or
//     contains one (`keep<T extends TurnView>`) is no opaque parameter;
//   - (P9-pre-web-fix2) an element access or an `in` by a quoted name on
//     one (`state["turnList"]`, `"turnList" in state`): a quoted name is
//     never mangled;
//   - one returned by a call through its signature's type parameter
//     (panelGet<T> / panelPost<T> / panelPut<T>: the response's JSON,
//     whether T is written or inferred from an internal contextual
//     type), the same containers excepted;
//   - a user-defined type guard (`v is X`, `asserts v is X`) whose X is
//     or contains one, over a parameter whose type does not: it makes
//     one of a parsed or fetched value.
//
// INTERNAL_TYPES and INTERNAL_CLASSES name declarations (file#Name), so
// a wire interface elsewhere that shares a listed name is not internal;
// a listed declaration missing from the graph fails the analysis.
//
// vite.panel.config.ts passes the names as mangleProps' include and
// `reserved` as the names it may never emit (the short members the DOM
// and ES libs declare: the element inherits them, the bundle need not
// spell them); oxc itself never emits a name the bundle spells.
// src/panel/mangle.test.ts pins the analysis and the bytes.
import ts from "typescript"
import { dirname, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..")

/** Names the built bundle's tests read verbatim (built-bundle.test.ts:
 * `.fire(\`run\`,{`), never mangled. */
export const RESERVED = [
  "fire",
  // The engine's and the DOM's protocol names: an object carrying one is
  // called by that name from outside the bundle (an EventListener
  // object, JSON.stringify, await, iteration, a coercion, a Proxy's
  // traps). Symbol-named members (Symbol.iterator, …) are computed
  // names, never a property-name occurrence: oxc does not touch them.
  "handleEvent",
  "toJSON",
  "then",
  "next",
  "return",
  "throw",
  "valueOf",
  "toString",
  "toLocaleString",
  "get",
  "set",
  "has",
  "apply",
  "construct",
  "deleteProperty",
  "defineProperty",
  "ownKeys",
  "getOwnPropertyDescriptor",
  "getPrototypeOf",
  "setPrototypeOf",
  "isExtensible",
  "preventExtensions",
]

/** The bundle's internal classes: an instance of one is made by the
 * element and held in a private field, never handed to a host (no
 * event detail, no host API return, no global) — so every member, not
 * only a private one, is the bundle's own name.
 *
 * This list and INTERNAL_TYPES name a declaration, never a bare name:
 * "<file relative to studio/web>#<Name>". A wire interface elsewhere in
 * src/ that shares a listed name (a Cursor, a Lane, a Geometry) is not
 * internal; a listed key with no such declaration in the graph fails
 * the analysis (a moved or renamed type is re-listed, never dropped). */
export const INTERNAL_CLASSES = [
  "src/panel/state.ts#PanelModel",
  "src/panel/theme.ts#ThemeWatch",
  "src/panel/layout.ts#Push",
]

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
 * and (P9-trim2) state.ts's ChildView (a loaded child's view), Lane (a
 * followed run's fold), Cursor (the runs page cursor) and EventWalk (one
 * events walk); editor.ts's PanelPreview (the drawer's preview state, the
 * response kept under its doc); element.ts's OpenState,
 * AckCatalog, ReplayCtx, FoldCtx and EdCtx (its own render contexts);
 * tree.ts's TreeCtx; playground.ts's TurnWords; layout.ts's Geometry
 * (the dock's computed box — the stored Layout stays verbatim).
 * A type that starts crossing one of those lines comes off this list. */
export const INTERNAL_TYPES = [
  "src/panel/state.ts#PanelState",
  "src/panel/state.ts#TurnView",
  "src/panel/playground.ts#ExperimentResult",
  "src/panel/tree.ts#TreeState",
  "src/panel/request.ts#RequestTabDeps",
  "src/panel/detect.ts#HeaderRungOptions",
  "src/panel/markers.ts#MarkerRungOptions",
  "src/panel/state.ts#ChildView",
  "src/panel/state.ts#Lane",
  "src/panel/state.ts#Cursor",
  "src/panel/state.ts#EventWalk",
  "src/panel/element.ts#OpenState",
  "src/panel/element.ts#AckCatalog",
  "src/panel/element.ts#ReplayCtx",
  "src/panel/element.ts#FoldCtx",
  "src/panel/element.ts#EdCtx",
  "src/panel/tree.ts#TreeCtx",
  "src/panel/playground.ts#TurnWords",
  "src/panel/editor.ts#PanelPreview",
  "src/panel/layout.ts#Geometry",
]

/** The lists an analysis reads: INTERNAL_TYPES and INTERNAL_CLASSES,
 * their keys relative to base (studio/web). A test passes its own. */
export interface InternalLists {
  types: string[]
  classes: string[]
  base: string
}

/** Internal: the lists resolved against declarations. */
interface Internal {
  /** d is a listed interface. */
  type: (d: ts.Node) => boolean
  /** d is a listed class. */
  cls: (d: ts.Node) => boolean
  /** d is either. */
  any: (d: ts.Node) => boolean
}

function internalOf(lists: InternalLists): Internal {
  const types = new Set(lists.types)
  const classes = new Set(lists.classes)
  const key = (d: ts.Node, name: string) =>
    `${relative(lists.base, d.getSourceFile().fileName).replace(/\\/g, "/")}#${name}`
  const type = (d: ts.Node) =>
    ts.isInterfaceDeclaration(d) && types.has(key(d, d.name.text))
  const cls = (d: ts.Node) =>
    ts.isClassDeclaration(d) && !!d.name && classes.has(key(d, d.name.text))
  return { type, cls, any: (d) => type(d) || cls(d) }
}

/** The standard containers whose type parameters hold an internal
 * value and hand it back typed, never reading its names: a value of an
 * internal type may be stored in, and come back out of, one of these
 * (new Map<string, Lane>(), set.add(view), Promise.all([...]),
 * xs.map(...)). Any other type parameter — an own generic helper, a
 * library's — is opaque to checkFlows. */
const STD_GENERICS = new Set([
  "Array",
  "ReadonlyArray",
  "ArrayConstructor",
  "Map",
  "ReadonlyMap",
  "MapConstructor",
  "WeakMap",
  "WeakMapConstructor",
  "Set",
  "ReadonlySet",
  "SetConstructor",
  "WeakSet",
  "WeakSetConstructor",
  "Promise",
  "PromiseLike",
  "PromiseConstructor",
])

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
function isMemberOf(d: ts.Declaration, internal: Internal): boolean {
  if (
    (ts.isPropertySignature(d) || ts.isMethodSignature(d)) &&
    ts.isInterfaceDeclaration(d.parent)
  )
    return internal.type(d.parent)
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
  if (internal.cls(cls)) return true
  return (flags & ts.ModifierFlags.Private) !== 0
}

/** analyzeMangle walks the panel entry's module graph (src/panel/main.ts,
 * the graph the panel build bundles) and returns the safe set; it
 * throws on an internal type's value-level reflection (checkFlows) and
 * on a listed internal declaration the graph does not hold.
 * root bounds the graph's own files (src/; a test passes its own, and
 * its own lists). */
export function analyzeMangle(
  entry = resolve(web, "src/panel/main.ts"),
  root = resolve(web, "src"),
  lists: InternalLists = {
    types: INTERNAL_TYPES,
    classes: INTERNAL_CLASSES,
    base: web,
  }
): MangleAnalysis {
  const internal = internalOf(lists)
  const isMember = (d: ts.Declaration) => isMemberOf(d, internal)
  const cfg = ts.readConfigFile(resolve(web, "tsconfig.json"), (p) =>
    ts.sys.readFile(p)
  )
  const parsed = ts.parseJsonConfigFileContent(cfg.config, ts.sys, web)
  const program = ts.createProgram([entry], { ...parsed.options, noEmit: true })
  const checker = program.getTypeChecker()
  const src = root
  const all = program.getSourceFiles()
  const files = all.filter(
    (f) => !f.isDeclarationFile && f.fileName.startsWith(src)
  )
  const own = new Set(files)
  const rel = (f: ts.SourceFile, n: ts.Node) =>
    `${relative(web, f.fileName)}:${f.getLineAndCharacterOfPosition(n.getStart(f)).line + 1}`

  // Every listed declaration is in the graph: a key that names none is
  // a stale list (a moved or renamed type), never silently nothing.
  const found = new Set<string>()
  const lookFor = (n: ts.Node) => {
    if (
      (ts.isInterfaceDeclaration(n) || ts.isClassDeclaration(n)) &&
      n.name &&
      internal.any(n)
    )
      found.add(
        `${relative(lists.base, n.getSourceFile().fileName).replace(/\\/g, "/")}#${n.name.text}`
      )
    ts.forEachChild(n, lookFor)
  }
  for (const f of files) lookFor(f)
  const missing = [...lists.types, ...lists.classes].filter(
    (k) => !found.has(k)
  )
  if (missing.length)
    throw new Error(
      `panel-mangle: INTERNAL_TYPES / INTERNAL_CLASSES name a declaration the panel's graph does not hold (file#Name, relative to studio/web) — re-list the moved or renamed type:\n  ${missing.join("\n  ")}`
    )

  const flows = checkFlows(checker, files, own, rel, internal)
  if (flows.length)
    throw new Error(
      `panel-mangle: an internal type's names would be filled from outside the bundle or read by reflection — build the value instead, or take the type off INTERNAL_TYPES / INTERNAL_CLASSES:\n  ${flows.join("\n  ")}`
    )

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
        !internal.cls(cls)
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

/** The calls that read a value's property names by reflection or carry
 * the value out of the bundle: a global by the callee's text, a method
 * by its name (any receiver: a Storage's setItem, an element's or a
 * window's dispatchEvent/postMessage, the element's fire), a
 * constructor whose init's detail/data leaves with the event. */
const SINK_CALLS = new Set([
  "JSON.stringify",
  "structuredClone",
  "Object.keys",
  "Object.entries",
  "Object.values",
  "Object.assign",
  "Object.fromEntries",
  "Object.getOwnPropertyNames",
  "Reflect.ownKeys",
  "treeView",
])
const SINK_METHODS = new Set([
  "postMessage",
  "setItem",
  "dispatchEvent",
  "fire",
])
const SINK_NEWS = new Set(["CustomEvent", "MessageEvent"])

/** checkFlows lists each place an internal type's value is filled from
 * an untyped one or read by reflection (the module comment's list),
 * as "file:line: what — source". */
export function checkFlows(
  checker: ts.TypeChecker,
  files: ts.SourceFile[],
  own: Set<ts.SourceFile>,
  rel: (f: ts.SourceFile, n: ts.Node) => string,
  internal: Internal
): string[] {
  /** listed: s is declared by a listed interface or class. */
  const listed = (s: ts.Symbol | undefined) =>
    !!s && (s.declarations ?? []).some((d) => internal.any(d))
  const ownDecl = (s: ts.Symbol) =>
    (s.declarations ?? []).some((d) => own.has(d.getSourceFile()))
  /** mentions: the internal type t is or contains — through a union, an
   * intersection, a type argument (arrays, promises, maps, records) or
   * a property of an object type the graph declares (an anonymous one,
   * or one of its own interfaces and, with classes, classes) — else
   * null. A class's fields are left out where a value is only named or
   * filled (an assertion, an any): an instance is constructed, never
   * parsed, and the element's private members read off another
   * instance stay verbatim (isMember's caller); a sink reads them all. */
  const mentions = (
    t: ts.Type | undefined,
    classes = true,
    seen = new Set<ts.Type>(),
    depth = 0
  ): string | null => {
    if (!t || seen.has(t) || depth > 4) return null
    seen.add(t)
    for (const s of [t.aliasSymbol, t.getSymbol()])
      if (s && listed(s)) return s.name
    // A type parameter is what its constraint is (`T extends TurnView`):
    // a sink inside a generic helper reads the constraint's names.
    if (t.flags & ts.TypeFlags.TypeParameter) {
      const c = checker.getBaseConstraintOfType(t)
      return c && c !== t ? mentions(c, classes, seen, depth + 1) : null
    }
    if (t.isUnionOrIntersection())
      for (const u of t.types) {
        const m = mentions(u, classes, seen, depth + 1)
        if (m) return m
      }
    const ref =
      t.flags & ts.TypeFlags.Object &&
      (t as ts.ObjectType).objectFlags & ts.ObjectFlags.Reference
        ? checker.getTypeArguments(t as ts.TypeReference)
        : []
    for (const a of [...(t.aliasTypeArguments ?? []), ...ref]) {
      const m = mentions(a, classes, seen, depth + 1)
      if (m) return m
    }
    if (t.flags & ts.TypeFlags.Object) {
      const sym = t.getSymbol()
      const anon = !sym || sym.name === "__type" || sym.name === "__object"
      const cls = !!sym && (sym.flags & ts.SymbolFlags.Class) !== 0
      if (anon || (ownDecl(sym) && (classes || !cls)))
        for (const p of t.getProperties()) {
          const d = p.valueDeclaration ?? p.declarations?.[0]
          if (!d || !own.has(d.getSourceFile())) continue
          const m = mentions(
            checker.getTypeOfSymbolAtLocation(p, d),
            classes,
            seen,
            depth + 1
          )
          if (m) return `${m} (via .${p.name})`
        }
      for (const info of checker.getIndexInfosOfType(t)) {
        const m = mentions(info.type, classes, seen, depth + 1)
        if (m) return m
      }
    }
    return null
  }
  /** isAny: t is any, or a promise of any (`res.json()` returned from
   * an async function), or an array of any. */
  const isAny = (t: ts.Type): boolean => {
    if (t.flags & ts.TypeFlags.Any) return true
    const awaited = t.getProperty("then") && checker.getAwaitedType(t)
    if (awaited && awaited !== t && awaited.flags & ts.TypeFlags.Any)
      return true
    if (!checker.isArrayType(t)) return false
    const el = checker.getTypeArguments(t as ts.TypeReference)[0] as
      ts.Type | undefined
    return !!el && (el.flags & ts.TypeFlags.Any) !== 0
  }
  /** stdParam: a type parameter one of STD_GENERICS (a lib declaration)
   * declares, its own or one of its methods'. */
  const stdParam = (t: ts.Type): boolean => {
    for (const d of t.getSymbol()?.declarations ?? []) {
      if (own.has(d.getSourceFile()) || !d.getSourceFile().isDeclarationFile)
        continue
      // From d itself: a container's polymorphic `this` is declared by
      // the interface (Map's set returns this).
      const p = ts.findAncestor(
        d,
        (q) => ts.isInterfaceDeclaration(q) || ts.isClassDeclaration(q)
      )
      if (p?.name && STD_GENERICS.has(p.name.text)) return true
    }
    return false
  }
  /** opaque: why a parameter's declared type says nothing of an
   * internal value's names (unknown, any, object, {}, a bare type
   * parameter but the standard containers'), else null. */
  const opaque = (t: ts.Type): string | null => {
    if (t.isUnion()) {
      for (const u of t.types) {
        const w = opaque(u)
        if (w) return w
      }
      return null
    }
    if (t.flags & ts.TypeFlags.Any) return "any"
    if (t.flags & ts.TypeFlags.Unknown) return "unknown"
    if (t.flags & ts.TypeFlags.NonPrimitive) return "object"
    if (t.flags & ts.TypeFlags.TypeParameter)
      return stdParam(t) || constrained(t)
        ? null
        : `type parameter ${checker.typeToString(t)}`
    if (
      t.flags & ts.TypeFlags.Object &&
      !t.getProperties().length &&
      !t.getCallSignatures().length &&
      !t.getConstructSignatures().length
    ) {
      // {} — or an index signature alone (Record<string, unknown>,
      // { [k: string]: object }): no name of its own to hold the
      // value's names by, unless the index type is itself internal.
      const infos = checker.getIndexInfosOfType(t)
      if (infos.every((i) => !mentions(i.type))) return checker.typeToString(t)
    }
    if (checker.isArrayType(t)) {
      // unknown[], object[]: an element is held by no name either.
      const el = checker.getTypeArguments(t as ts.TypeReference)[0] as
        ts.Type | undefined
      return el && !mentions(el) ? opaque(el) : null
    }
    // A standard container of an opaque type argument (Set<unknown>,
    // Map<string, object>, Promise<unknown>): what it hands back is held
    // by no name either (P9-pre-web-fix2 2).
    const sym = t.getSymbol()
    if (
      t.flags & ts.TypeFlags.Object &&
      (t as ts.ObjectType).objectFlags & ts.ObjectFlags.Reference &&
      sym &&
      STD_GENERICS.has(sym.name) &&
      checker
        .getTypeArguments(t as ts.TypeReference)
        .some((a) => !mentions(a) && opaque(a))
    )
      return checker.typeToString(t)
    return null
  }
  /** constrained: a type parameter whose constraint is or contains an
   * internal type (`keep<T extends TurnView>`): the callee reads it by
   * the constraint's names — the ones the build renames — never
   * opaquely. */
  const constrained = (t: ts.Type): boolean => {
    const c = checker.getBaseConstraintOfType(t)
    return !!c && c !== t && !!mentions(c)
  }
  /** stdSig: the call's resolved signature is a standard container's
   * (a lib declaration of STD_GENERICS). */
  const stdSig = (call: ts.CallExpression | ts.NewExpression): boolean => {
    const d = checker.getResolvedSignature(call)?.getDeclaration()
    if (!d || !d.getSourceFile().isDeclarationFile) return false
    const p = ts.findAncestor(
      d,
      (q) => ts.isInterfaceDeclaration(q) || ts.isClassDeclaration(q)
    )
    return !!p?.name && STD_GENERICS.has(p.name.text)
  }
  /** narrowedBack: an assertion to an opaque type whose enclosing
   * assertion names an internal type again (`x as unknown as T`). */
  const narrowedBack = (e: ts.Node): boolean => {
    let n: ts.Node = e.parent
    while (
      ts.isParenthesizedExpression(n) ||
      ts.isAsExpression(n) ||
      ts.isTypeAssertionExpression(n)
    ) {
      if (
        (ts.isAsExpression(n) || ts.isTypeAssertionExpression(n)) &&
        n.expression !== e &&
        mentions(checker.getTypeFromTypeNode(n.type))
      )
        return true
      n = n.parent
    }
    return false
  }
  /** typeParamIn: a type parameter (but the standard containers') t is
   * or carries as a type argument, else null. */
  const typeParamIn = (t: ts.Type, depth = 0): string | null => {
    if (depth > 4) return null
    if (t.flags & ts.TypeFlags.TypeParameter)
      return stdParam(t) || constrained(t) ? null : checker.typeToString(t)
    const inner = [
      ...(t.isUnionOrIntersection() ? t.types : []),
      ...(t.aliasTypeArguments ?? []),
      ...(t.flags & ts.TypeFlags.Object &&
      (t as ts.ObjectType).objectFlags & ts.ObjectFlags.Reference
        ? checker.getTypeArguments(t as ts.TypeReference)
        : []),
    ]
    for (const u of inner) {
      const w = typeParamIn(u, depth + 1)
      if (w) return w
    }
    return null
  }
  const out: string[] = []
  const sunk = new Set<ts.Node>()
  for (const f of files) {
    const say = (n: ts.Node, what: string) =>
      out.push(
        `${rel(f, n)}: ${what} — ${n.getText(f).replace(/\s+/g, " ").slice(0, 80)}`
      )
    const visit = (n: ts.Node) => {
      // A type assertion builds nothing: it names an outside value one.
      if (ts.isAsExpression(n) || ts.isTypeAssertionExpression(n)) {
        const m = mentions(checker.getTypeFromTypeNode(n.type), false)
        if (m) say(n, `asserts a value to ${m}`)
      }
      // A value of type any where an internal type is expected (a
      // declaration's initializer, an assignment, a return, an argument,
      // a property of a literal). A parenthesized one is checked once.
      if (
        ts.isExpression(n) &&
        !ts.isParenthesizedExpression(n.parent) &&
        isAny(checker.getTypeAtLocation(n))
      ) {
        const m = mentions(checker.getContextualType(n), false)
        if (m) say(n, `a value of type any fills ${m}`)
      }
      // Widening: an internal value held where its declared or
      // contextual type (a declaration, an assignment, a return, an
      // array element, a literal's property, an assertion) is opaque —
      // unknown, any, object, {}, an index signature alone, a type
      // parameter — loses the type every later sink would have named
      // (`const u: unknown = g; JSON.stringify(u)`). An argument is the
      // call rules' (below); an element nested in one (a Map's entry) is
      // read on the call's instantiated contextual type — Map<string,
      // unknown>'s V is unknown, an inferred Map's the value's own type.
      if (
        ts.isExpression(n) &&
        !ts.isParenthesizedExpression(n.parent) &&
        !(ts.isPropertyAssignment(n.parent) && n.parent.name === n) &&
        !(
          (ts.isCallExpression(n.parent) || ts.isNewExpression(n.parent)) &&
          !!n.parent.arguments?.includes(n)
        )
      ) {
        const m = mentions(checker.getTypeAtLocation(n))
        const ctx = m ? checker.getContextualType(n) : undefined
        const why = ctx && !mentions(ctx) ? opaque(ctx) : null
        if (m && why && !narrowedBack(n)) say(n, `${m} widened to ${why}`)
      }
      // A template stringifies the value whole.
      if (ts.isTemplateSpan(n)) {
        const m = mentions(checker.getTypeAtLocation(n.expression))
        if (m) say(n.expression, `${m} interpolated into a template`)
      }
      // A callback's internal-typed parameter its caller feeds any
      // (`.then((r: ExperimentResult) => …)` on a Promise<any>).
      if (ts.isArrowFunction(n) || ts.isFunctionExpression(n)) {
        // The contextual type is often optional (`onfulfilled?: …`).
        const ctx = checker.getContextualType(n)
        const sigs = ctx
          ? checker.getNonNullableType(ctx).getCallSignatures()
          : []
        for (const sig of sigs)
          n.parameters.forEach((p, i) => {
            const given = sig.parameters[i] as ts.Symbol | undefined
            if (!p.type || !given) return
            const m = mentions(checker.getTypeFromTypeNode(p.type), false)
            if (m && isAny(checker.getTypeOfSymbolAtLocation(given, n)))
              say(p, `a parameter typed ${m} is given any`)
          })
      }
      // Reflection and serialization sinks.
      if (ts.isCallExpression(n) || ts.isNewExpression(n)) {
        const text = n.expression.getText(f)
        const last = ts.isPropertyAccessExpression(n.expression)
          ? n.expression.name.text
          : text
        const sink = ts.isNewExpression(n)
          ? SINK_NEWS.has(text)
          : SINK_CALLS.has(text) || SINK_METHODS.has(last)
        if (sink) {
          const sig = checker.getResolvedSignature(n)
          ;(n.arguments ?? []).forEach((a, i) => {
            const m = mentions(checker.getTypeAtLocation(a))
            if (!m) return
            // The sink's own parameter declares an internal type: the
            // bundle's code on the far side reads it by its (mangled)
            // names, as treeView reads its TreeState.
            const ps = sig?.parameters ?? []
            const pd = ps[Math.min(i, ps.length - 1)]?.valueDeclaration
            if (
              pd &&
              ts.isParameter(pd) &&
              pd.type &&
              own.has(pd.getSourceFile()) &&
              mentions(checker.getTypeFromTypeNode(pd.type))
            )
              return
            sunk.add(a)
            say(
              a,
              `${m} handed to ${ts.isNewExpression(n) ? "new " : ""}${text}`
            )
          })
        }
        const sig = checker.getResolvedSignature(n)
        const decl = sig?.getDeclaration()
        if (sig && decl) {
          // An argument to a parameter the callee declares opaque: what
          // it does with the names (stringify them, walk them) the
          // checker cannot see. A standard container's parameter is read
          // as this call instantiates it (P9-pre-web-fix2 2): its own V /
          // T says nothing, Map<string, unknown>'s V is unknown.
          const std = stdSig(n)
          ;(n.arguments ?? []).forEach((a, i) => {
            if (sunk.has(a)) return
            const m = mentions(checker.getTypeAtLocation(a))
            if (!m) return
            const ps = decl.parameters
            if (!ps.length) return
            const k = Math.min(i, ps.length - 1)
            const pd = ps[k]
            const given = sig.parameters[Math.min(i, sig.parameters.length - 1)]
            let pt =
              std && given
                ? checker.getTypeOfSymbolAtLocation(given, n)
                : checker.getTypeAtLocation(pd)
            if (pd.dotDotDotToken)
              pt =
                checker.isArrayType(pt) || checker.isTupleType(pt)
                  ? (checker.getTypeArguments(pt as ts.TypeReference)[0] ?? pt)
                  : pt
            const why = opaque(pt)
            if (why) say(a, `${m} passed to ${text}'s parameter typed ${why}`)
          })
          // A return through the signature's type parameter: the caller
          // names what comes back (panelGet<T>'s response JSON).
          const declared = checker.getSignatureFromDeclaration(decl)
          const tp = declared && typeParamIn(declared.getReturnType())
          if (tp) {
            const m = mentions(sig.getReturnType(), false)
            if (m)
              say(n, `${m} returned through ${text}'s type parameter ${tp}`)
          }
        }
      }
      // A user-defined type guard makes an internal type of a value no
      // internal type describes (a parsed or fetched one).
      if (ts.isTypePredicateNode(n) && n.type) {
        const m = mentions(checker.getTypeFromTypeNode(n.type), false)
        const fn = n.parent
        const pname = n.parameterName
        const p =
          ts.isIdentifier(pname) && ts.isFunctionLike(fn)
            ? fn.parameters.find(
                (q) => ts.isIdentifier(q.name) && q.name.text === pname.text
              )
            : undefined
        if (m && !(p && mentions(checker.getTypeAtLocation(p), false)))
          say(
            n,
            `a type guard makes ${m} of a value no internal type describes`
          )
      }
      if (ts.isForInStatement(n)) {
        const m = mentions(checker.getTypeAtLocation(n.expression))
        if (m) say(n.expression, `for…in over ${m}`)
      }
      // A quoted name is never mangled (P9-pre-web-fix2 2): an element
      // access or an `in` by string on an internal type reads a name the
      // build renamed — or keeps the name, and a value read by it, apart.
      if (
        ts.isElementAccessExpression(n) &&
        ts.isStringLiteralLike(n.argumentExpression)
      ) {
        const m = mentions(checker.getTypeAtLocation(n.expression))
        if (m)
          say(n, `${m} read by the quoted name "${n.argumentExpression.text}"`)
      }
      if (
        ts.isBinaryExpression(n) &&
        n.operatorToken.kind === ts.SyntaxKind.InKeyword &&
        ts.isStringLiteralLike(n.left)
      ) {
        const m = mentions(checker.getTypeAtLocation(n.right))
        if (m)
          say(n, `${m} tested with \`in\` by the quoted name "${n.left.text}"`)
      }
      // A spread copies the names into whatever the literal becomes.
      if (ts.isSpreadAssignment(n)) {
        const m = mentions(checker.getTypeAtLocation(n.expression))
        if (m && !mentions(checker.getContextualType(n.parent)))
          say(n, `${m} spread into an object no internal type describes`)
      }
      ts.forEachChild(n, visit)
    }
    visit(f)
  }
  return out
}

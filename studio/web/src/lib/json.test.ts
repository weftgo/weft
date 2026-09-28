// JSON detection and the colouring tokenizer.
import { describe, expect, it } from "vitest"

import { tokenize, tryJSON } from "./json"

describe("json helpers", () => {
  it("detects JSON objects and arrays, not prose", () => {
    expect(tryJSON('{"a":1}')).toEqual({ a: 1 })
    expect(tryJSON(" [1, 2] ")).toEqual([1, 2])
    expect(tryJSON("order 42: shipped")).toBeNull()
    expect(tryJSON("{not json")).toBeNull()
  })
  it("tokenizes keys, strings, numbers and literals", () => {
    const toks = tokenize('{\n  "a": "x", "n": -1.5e3, "t": true, "z": null\n}')
    const kinds = toks.filter((t) => t.kind !== "ws").map((t) => t.kind)
    expect(kinds).toEqual([
      "punct",
      "key",
      "punct",
      "string",
      "punct",
      "key",
      "punct",
      "number",
      "punct",
      "key",
      "punct",
      "literal",
      "punct",
      "key",
      "punct",
      "literal",
      "punct",
    ])
    // round-trips: concatenating the tokens gives the text back
    expect(toks.map((t) => t.text).join("")).toBe(
      '{\n  "a": "x", "n": -1.5e3, "t": true, "z": null\n}'
    )
  })
})

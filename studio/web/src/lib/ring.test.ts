import { describe, expect, it } from "vitest"

import { EVENT_RING_CAPACITY, Ring } from "./ring"

describe("Ring (plan H2)", () => {
  it("keeps everything under capacity, in order", () => {
    const r = new Ring<number>(5)
    for (let i = 0; i < 4; i++) r.push(i)
    expect(r.snapshot()).toEqual([0, 1, 2, 3])
    expect(r.base).toBe(0)
    expect(r.total).toBe(4)
  })

  it("evicts the oldest past capacity; base names the first kept index", () => {
    const r = new Ring<number>(5)
    for (let i = 0; i < 13; i++) r.push(i)
    expect(r.length).toBe(5)
    expect(r.base).toBe(8)
    expect(r.total).toBe(13)
    expect(r.snapshot()).toEqual([8, 9, 10, 11, 12])
  })

  it("a snapshot is stable until the next push, and never mutated after", () => {
    const r = new Ring<number>(3)
    r.push(1)
    r.push(2)
    const a = r.snapshot()
    expect(r.snapshot()).toBe(a)
    r.push(3)
    r.push(4)
    const b = r.snapshot()
    expect(b).not.toBe(a)
    expect(a).toEqual([1, 2])
    expect(b).toEqual([2, 3, 4])
  })

  it("documents a capacity that holds a 5,000-step run whole", () => {
    expect(EVENT_RING_CAPACITY).toBeGreaterThanOrEqual(5_000 * 4 + 2)
  })
})

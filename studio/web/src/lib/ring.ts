// The run page's bounded event window (plan H2): the fold keeps the
// last `capacity` events of a stream in memory, never more. A push is
// O(1) — once the ring is full, the oldest event is overwritten in
// place — and a snapshot (one per publish, not per live frame) copies
// the window in stream order. Evicted events are not lost: they are
// the API's (api/runs/{id}/events?after=&limit=), and the fold itself
// (steps, calls, holes) already holds everything they contributed —
// what eviction drops is the raw window and the replay prefix.

/** How many events the run page keeps in memory: a 5,000-step run
 * with a tool call per step (four events a step) fits whole, with
 * room for its deltas; past it the oldest are evicted (base moves). */
export const EVENT_RING_CAPACITY = 50_000

export class Ring<T> {
  readonly capacity: number
  private buf: T[] = []
  private start = 0 // the index in buf of the oldest kept item
  private pushed = 0 // every item ever pushed
  private snap: T[] | null = null // the last snapshot, while still current

  constructor(capacity: number = EVENT_RING_CAPACITY) {
    this.capacity = Math.max(1, Math.floor(capacity))
  }

  push(v: T): void {
    this.pushed++
    this.snap = null
    if (this.buf.length < this.capacity) {
      this.buf.push(v)
      return
    }
    this.buf[this.start] = v
    this.start = (this.start + 1) % this.capacity
  }

  /** Items held (≤ capacity). */
  get length(): number {
    return this.buf.length
  }

  /** Items pushed in all, held or evicted. */
  get total(): number {
    return this.pushed
  }

  /** Items evicted from the front: the stream index of snapshot()[0]. */
  get base(): number {
    return this.pushed - this.buf.length
  }

  /** The held items, oldest first: a fresh array, never mutated after
   * it is returned — the same array again while nothing was pushed. */
  snapshot(): T[] {
    if (this.snap) return this.snap
    this.snap =
      this.start === 0
        ? this.buf.slice()
        : this.buf.slice(this.start).concat(this.buf.slice(0, this.start))
    return this.snap
  }
}

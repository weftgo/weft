// Test double for EventSource (jsdom has none): the tests drive the
// stream by hand — open it, emit named frames, drop it the way a
// browser reports a lost or refused connection.

export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  /** Every instance constructed since the last reset, oldest first. */
  static instances: FakeEventSource[] = []
  static reset() {
    FakeEventSource.instances = []
  }
  /** The instances not closed by the code under test. */
  static open(): FakeEventSource[] {
    return FakeEventSource.instances.filter((es) => !es.closedByClient)
  }

  readyState = FakeEventSource.CONNECTING
  closedByClient = false
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>()

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  addEventListener(name: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn])
  }

  close() {
    this.closedByClient = true
    this.readyState = FakeEventSource.CLOSED
  }

  // ── the test's side ──

  /** The connection is up. */
  connect() {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.()
  }

  /** One named frame; id becomes the frame's lastEventId. */
  emit(name: string, data: unknown, id = "") {
    const e = { data: JSON.stringify(data), lastEventId: id } as MessageEvent
    for (const fn of this.listeners.get(name) ?? []) fn(e)
  }

  /** The connection dropped and the browser is retrying on its own. */
  dropRetrying() {
    this.readyState = FakeEventSource.CONNECTING
    this.onerror?.()
  }

  /** The connection failed for good (a non-200 answer, e.g. the wall's
   * 401): the browser will not retry. */
  fail() {
    this.readyState = FakeEventSource.CLOSED
    this.onerror?.()
  }
}

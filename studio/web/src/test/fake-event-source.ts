// Test double for EventSource (jsdom has none): the tests drive the
// stream by hand — open it, emit named frames, drop it the way a
// browser reports a lost or refused connection. The URL passes the
// fake live grant's door (fake-live-grant.ts's checkLiveURL) or the
// stream is refused the way a browser reports a 401: CLOSED, onerror.
import { checkLiveURL, resetGrants } from "./fake-live-grant"

export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  /** Every instance constructed since the last reset, oldest first. */
  static instances: FakeEventSource[] = []
  static reset() {
    FakeEventSource.instances = []
    resetGrants()
  }
  /** The nth instance, or a throw while it does not exist yet: a
   * stream opens once its live grant has answered, so a test waits for
   * it — await waitFor(() => FakeEventSource.nth(0)). */
  static nth(i: number): FakeEventSource {
    const es = FakeEventSource.instances.at(i)
    if (!es) throw new Error(`no stream #${i} yet`)
    return es
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

  /** Why the server refused this URL (a 401), or null. */
  refused: string | null

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
    this.refused = checkLiveURL(url)
    if (this.refused) queueMicrotask(() => !this.closedByClient && this.fail())
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
    if (this.refused) throw new Error(`FakeEventSource: ${this.url} was refused: ${this.refused}`)
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

  /** The browser's own reconnect after a drop: the same URL (the
   * same sig) knocks again — open while the grant lasts, refused (CLOSED)
   * once it is spent. */
  retry() {
    if (checkLiveURL(this.url)) this.fail()
    else this.connect()
  }

  /** A panel token's stream ended at its expiry: the one final frame
   * (no id), then the server hangs up. */
  expire() {
    this.emit("expired", {})
  }

  /** The connection failed for good (a non-200 answer, e.g. the wall's
   * 401): the browser will not retry. */
  fail() {
    this.readyState = FakeEventSource.CLOSED
    this.onerror?.()
  }
}

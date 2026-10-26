// P10a-pre-web-fix: the regex runner's time bound starts when a loaded
// worker takes the job — a module load slower than REGEX_TIMEOUT_MS is
// never a "timeout" for the first job, nor for the job after a timeout
// respawned the worker.
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  REGEX_TIMEOUT_MS,
  REGEX_WORDS,
  resetRegexRunner,
  testRegex,
} from "./regex-run"

/** FakeWorker loads in LOAD ms (then posts ready), and answers a job at
 * once — unless its pattern is "hang", which it never answers. */
const LOAD = REGEX_TIMEOUT_MS + 150
let spawned = 0
class FakeWorker {
  onmessage: ((e: MessageEvent) => void) | null = null
  onerror: ((e: ErrorEvent) => void) | null = null
  onmessageerror: (() => void) | null = null
  dead = false
  constructor() {
    spawned++
    setTimeout(() => this.send({ ready: true }), LOAD)
  }
  send(data: unknown) {
    if (!this.dead) this.onmessage?.({ data } as MessageEvent)
  }
  postMessage(j: { id: number; source: string; flags: string; text: string }) {
    if (j.source === "hang") return
    setTimeout(
      () =>
        this.send({
          id: j.id,
          hit: new RegExp(j.source, j.flags).test(j.text),
        }),
      0
    )
  }
  terminate() {
    this.dead = true
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
  resetRegexRunner()
  spawned = 0
})

describe("the regex runner's bound", () => {
  it("a slow worker load is not charged to the first job, nor to the job after a respawn", async () => {
    vi.stubGlobal("Worker", FakeWorker)
    // The first job waits for the load (longer than the bound) and runs.
    expect(await testRegex("b", "", "abc")).toEqual({
      hit: true,
      capped: false,
    })
    // A loaded worker: the next job runs on it, no new load.
    expect(await testRegex("z", "", "abc")).toEqual({
      hit: false,
      capped: false,
    })
    expect(spawned).toBe(1)
    // A job past its bound terminates the worker…
    expect(await testRegex("hang", "", "abc")).toEqual({
      why: REGEX_WORDS.timeout,
    })
    // …and the next one pays the fresh worker's load outside its bound.
    expect(await testRegex("c$", "", "abc")).toEqual({
      hit: true,
      capped: false,
    })
    expect(spawned).toBe(2)
  })
})

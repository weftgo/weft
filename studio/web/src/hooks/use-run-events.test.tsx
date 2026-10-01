// The run page's events walk is the app's one raw fetch beside
// lib/api's get/post — and the only one that used to miss the bearer
// token, so under setups B/C (a token-walled Studio) every page of the
// walk 401'd and the run page showed the error instead of the story
// (programme audit P1-3). Pinned here: with a token stored the way the
// reader pastes it, every fetch the hook makes carries the header.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, renderHook, waitFor } from "@testing-library/react"

import { setStudioToken } from "@/lib/api"
import { useRunEvents } from "./use-run-events"

const page = (pos: number, done: boolean) => ({
  events: [
    {
      pos,
      time: "2026-10-01T09:00:00Z",
      event: { type: "run_start", id: "r1", model: { provider: "p", name: "m" } },
    },
  ],
  next_after: null,
  done,
  gap_count: 0,
})

describe("useRunEvents fetch auth", () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    setStudioToken("")
  })
  beforeEach(() => {
    setStudioToken("")
  })

  it("sends the stored bearer token on every page fetch", async () => {
    setStudioToken("dev-secret")
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify(page(0, true)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
    )
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRunEvents("r1", "succeeded"))
    await waitFor(() => {
      expect(result.current.done).toBe(true)
      expect(result.current.error).toBeNull()
    })

    expect(fetchMock).toHaveBeenCalled()
    const headers = (fetchMock.mock.calls[0][1] as RequestInit).headers
    expect(new Headers(headers).get("Authorization")).toBe("Bearer dev-secret")
  })

  it("sends no Authorization header without a stored token (setup A)", async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify(page(0, true)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
    )
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRunEvents("r1", "succeeded"))
    await waitFor(() => expect(result.current.done).toBe(true))

    const headers = (fetchMock.mock.calls[0][1] as RequestInit).headers
    expect(new Headers(headers).get("Authorization")).toBeNull()
  })
})

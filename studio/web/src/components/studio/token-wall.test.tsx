// The token wall's front door (S4.6, setups B and C): a token-walled
// Studio answers every API call 401 until the reader supplies the
// token — and the UI used to have nowhere to put it (the stored token
// was read everywhere and written nowhere), so the binary's own UI
// could only show "the API requires a token". Pinned here: a 401 on
// meta renders the prompt, a pasted token is stored and sent, and the
// app behind the wall then renders.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken, studioToken } from "@/lib/api"
import { noRetryOnRefusal } from "@/lib/query"
import { TokenWall } from "@/components/studio/token-wall"

const meta = {
  weft_version: "v0.7.0",
  studio_version: "v0.3.0",
  db: { kind: "sqlite" },
  title: "weft studio",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live"],
}

function walled(token: string) {
  return vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const auth = new Headers(init?.headers).get("Authorization")
    if (auth === `Bearer ${token}`)
      return new Response(JSON.stringify(meta), { status: 200 })
    return new Response(
      JSON.stringify({
        error: {
          code: "unauthorized",
          message: auth
            ? "bad or expired token"
            : "the API requires a token: Authorization: Bearer <token>",
        },
      }),
      { status: 401 }
    )
  })
}

function renderWall() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: noRetryOnRefusal } },
  })
  return render(
    <QueryClientProvider client={client}>
      <TokenWall>
        <p>the app</p>
      </TokenWall>
    </QueryClientProvider>
  )
}

describe("TokenWall", () => {
  beforeEach(() => setStudioToken(""))
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    setStudioToken("")
  })

  it("renders the app when the API is open (setup A)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(meta), { status: 200 }))
    )
    renderWall()
    expect(await screen.findByText("the app")).toBeTruthy()
    expect(screen.queryByLabelText("API token")).toBeNull()
  })

  it("asks for the token on a 401, stores the paste, and lets the app in", async () => {
    const fetchMock = walled("dev-secret")
    vi.stubGlobal("fetch", fetchMock)
    renderWall()
    const input = await screen.findByLabelText("API token")
    expect(screen.queryByText("the app")).toBeNull()
    // One refused request, not a retry loop.
    expect(fetchMock).toHaveBeenCalledTimes(1)

    fireEvent.change(input, { target: { value: "  dev-secret \n" } })
    fireEvent.click(screen.getByRole("button", { name: "unlock" }))
    expect(await screen.findByText("the app")).toBeTruthy()
    expect(studioToken()).toBe("dev-secret")
  })

  it("says so when the pasted token is refused, and asks again", async () => {
    vi.stubGlobal("fetch", walled("dev-secret"))
    renderWall()
    const input = await screen.findByLabelText("API token")
    fireEvent.change(input, { target: { value: "wrong" } })
    fireEvent.click(screen.getByRole("button", { name: "unlock" }))
    await waitFor(() =>
      expect(screen.getByText(/bad or expired token/)).toBeTruthy()
    )
    expect(screen.getByLabelText("API token")).toBeTruthy()
    expect(screen.queryByText("the app")).toBeNull()
  })
})

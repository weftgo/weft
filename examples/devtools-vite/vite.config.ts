import { defineConfig } from "vite"

// In dev, the app's backend is studio-local (go run ./examples/studio-local,
// on 127.0.0.1:8080 beside a running Studio, e.g. under `weft dev`):
// /run is its chat endpoint, /studio its embedded Studio. `vite build`
// ignores the proxy and emits a static dist/.
const backend = process.env.WEFT_BACKEND ?? "http://127.0.0.1:8080"

export default defineConfig({
  server: {
    proxy: {
      "/run": backend,
      "/studio": { target: backend, ws: false },
    },
  },
})

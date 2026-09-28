# ADR 0023 — `thread/sandbox`: a file firewall every file tool goes through

- Status: **proposed** (2026-09-28; to be decided at the start of
  `weft/thread` v0.6, plan §9)
- Depends on: ADR 0003 (the tool contract), ADR 0006 (the tool seam),
  ADR 0007 (approval is not a security boundary), ADR 0011

## Context

TODO §4.4's scope note: approvals are a policy seam, "the boundary is
OS-level sandboxing (§14 `SandboxFS`)". DeerFlow's lesson is the path
firewall as a type: the model sees only virtual paths, host paths are
masked out of tool output, writes are size-capped (80 KiB), and a tool
cannot bypass it because it never gets the host filesystem — "enforced
by interface, not review". Process isolation (containers, seccomp,
microVMs) is deployment, not a library.

## Proposed decision

1. `sandbox.FS` — an interface with the operations file tools need
   (`Read`, `Write`, `Stat`, `List`, `Remove`, `Mkdir`, `Glob`) over
   **virtual paths** (`/workspace/…`), built on `os.Root` (Go 1.24+) so
   symlink and `..` escapes are refused by the standard library.
2. Mounts: `sandbox.New(sandbox.Mount("/workspace", dir, sandbox.RW),
   sandbox.Mount("/docs", other, sandbox.RO))`; anything unmounted does
   not exist.
3. Limits: per-write size cap, total bytes written per session, file
   count; exceeding one is a `ToolError` with a code the model can read.
4. Output masking: a `ToolMiddleware` (`sandbox.Mask`) rewrites host
   paths in tool results back to virtual paths.
5. Reference tools: `sandbox.Tools(fs)` — read, write, edit, list,
   glob — built on `weft.Tool`, so an application can use them or write
   its own against the same `FS`.
6. Session tie-in: the sandbox's write log can be recorded as `custom`
   entries (files read / modified), which compaction's summary lists
   (ADR 0020).
7. A remote `FS` (ACP's client `fs/*`, a container over RPC) is a second
   implementation of the same interface, outside this module.

## Open questions (answer at v0.6)

- Process execution (`run_command`) — out of scope, or an `Exec`
  interface with the same mount table and a deployment-provided runner?
- Is `os.Root` enough on every supported OS, or does Windows need extra
  checks?
- Does the FS belong on the session (one per session) or on the agent's
  tools (one per construction)?

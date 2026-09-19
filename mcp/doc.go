// Package mcp is the bridge between weft and the Model Context
// Protocol, both directions over the official Go SDK
// (github.com/modelcontextprotocol/go-sdk, aliased `sdk` in examples —
// this package keeps the name `mcp`):
//
//	tools, _ := mcp.Tools(ctx, sess, mcp.Prefix("gh_"))
//	mcp.AddTools(srv, lookup, refund)
//	mcp.Serve(srv, agt, "Support agent.")
//
// Consuming (§7.3): the tools of a connected client session become
// ordinary weft tools — a RawTool per server tool, the server's schema
// bytes verbatim (weft.ParseSchema keeps every keyword the core's
// Schema type cannot express). Exposing (§7.2): weft tools and whole
// agents register on an SDK server; every failure — undecodable
// arguments, a handler error, a panic — is a tool result with isError,
// carrying the same text weft's own model would see (ADR 0002's pinned
// bytes, over the wire).
//
// The bridge is mechanical by design (ADR 0003's shape bet, measured
// by §7.1's corpus): field-for-field maps between two
// already-compatible shapes, no translation layer of its own. This
// module is a satellite (ADR 0005): it imports the core, the core's
// internal/adapterkit, and the SDK — nothing else — and the core
// never imports it or names it.
package mcp

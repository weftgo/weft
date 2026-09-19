package weft

import "os"

// ModelRequestsAllowed reports whether adapters may call a provider. It
// is false when WEFT_MODEL_REQUESTS=deny — the guard for test suites
// that must never reach the network (Pydantic AI's ALLOW_MODEL_REQUESTS).
// First-party adapters check it at the top of Stream and yield
// ErrModelRequestsDenied when it is false, before any network I/O — but
// only for a client they built themselves from credentials; a client
// the caller injected through the adapter's Client(c) option is a test
// double by construction and stays reachable (ADR 0013's kill-switch
// clause). wefttest models ignore the switch entirely.
//
// The environment is read on every call, not cached: a model call is
// network-bound so one getenv is noise, test suites can toggle the
// switch per test with t.Setenv, and the package keeps no state at all.
func ModelRequestsAllowed() bool {
	return os.Getenv("WEFT_MODEL_REQUESTS") != "deny"
}

// Package contentflag carries an agent's Content(false) on a run's
// context, so mw.Audit's line can leave out what the agent keeps out of
// its records (the error text: model-visible content) without a public
// accessor. The loop sets it at a run's start only when it matters — the
// agent set Content(false), or a parent run's mark must be lifted for a
// child that did not — so a run without Content(false) pays nothing.
package contentflag

import "context"

type key struct{}

// With returns ctx marked with the running agent's content choice: off
// is true under Content(false).
func With(ctx context.Context, off bool) context.Context {
	return context.WithValue(ctx, key{}, off)
}

// Off reports whether the run on ctx is an agent's under Content(false).
func Off(ctx context.Context) bool {
	off, _ := ctx.Value(key{}).(bool)
	return off
}

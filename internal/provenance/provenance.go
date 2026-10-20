// Package provenance carries, on the context a thread session's
// decision is recorded under, the class its resumed calls' approvals
// are reported as (weft.tool.approved_by, record contract §3) when a
// layer above thread decides through the session's own door on
// someone's behalf — weft/runtime's playground deciding a fork's
// parked turn reads "runtime", as its ephemeral resumes do, never the
// Decide door's "user".
//
// It is reporting only: the decision, its validation and its record
// are the session's, unchanged. thread reads the class from the
// context that armed a resume (Session.Decide's, Resume's), never from
// the parked turn's — a later decision by another door is reported as
// that door's.
package provenance

import "context"

type key struct{}

// With returns ctx carrying the approval class by; "" clears one an
// outer context carried.
func With(ctx context.Context, by string) context.Context {
	return context.WithValue(ctx, key{}, by)
}

// From returns the approval class ctx carries, "" when none.
func From(ctx context.Context) string {
	by, _ := ctx.Value(key{}).(string)
	return by
}

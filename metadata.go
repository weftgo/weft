package weft

import (
	"context"
	"maps"
	"slices"
)

// Metadata's limits (ADR 0024, S1.1): metadata is identity, not a
// payload. Anything over is dropped, never truncated, and counted on the
// run's invoke_agent span as weft.metadata.dropped.
const (
	metadataMaxKeys     = 64
	metadataMaxKeyLen   = 128  // bytes
	metadataMaxValueLen = 1024 // bytes
)

// Metadata returns the RunOption attaching caller key/value pairs to the
// run: every span and every record of the run carries them, and so do the
// runs of its subagents (the pairs ride the context). Several Metadata
// options merge in order; a later key wins. Keys under "weft." are the
// weft modules' namespace by convention (thread writes weft.session.id);
// the core does not police callers.
//
// Limits, because metadata rides every span and record: at most 64 keys,
// a key at most 128 bytes, a value at most 1024 bytes. An entry over a
// limit — or with an empty key — is dropped, never truncated, and
// counted on the run's invoke_agent span (weft.metadata.dropped). Read
// the merged, limited view back with MetadataFromContext, e.g. inside a
// Tap, a tool handler, or a Subagent's child run.
func Metadata(kv map[string]string) RunOption { return metadataOption{kv} }

type metadataOption struct{ kv map[string]string }

func (o metadataOption) applyRun(c *runConfig) {
	if len(o.kv) == 0 {
		return
	}
	if c.metadata == nil {
		c.metadata = make(map[string]string, len(o.kv))
	}
	maps.Copy(c.metadata, o.kv)
}

// metadataKey is the context key for the metadata in force on a run: the
// run's own merged over its ancestors', limited, placed before any span
// starts so every child run inherits it.
type metadataKey struct{}

func withMetadata(ctx context.Context, md map[string]string) context.Context {
	return context.WithValue(ctx, metadataKey{}, md)
}

// metadataFromCtx returns the map in force on ctx, or nil; the caller
// must not mutate it.
func metadataFromCtx(ctx context.Context) map[string]string {
	md, _ := ctx.Value(metadataKey{}).(map[string]string)
	return md
}

// MetadataFromContext returns a copy of the metadata in force on ctx —
// the run's own merged over its ancestors'. nil when there is none.
func MetadataFromContext(ctx context.Context) map[string]string {
	md := metadataFromCtx(ctx)
	if len(md) == 0 {
		return nil
	}
	return maps.Clone(md)
}

// mergeMetadata builds a run's effective metadata: the context's
// (inherited from the parent run, already within every limit) overlaid
// with the run's own Metadata options, later key winning. The own
// entries apply in sorted key order so the 64-key cap drops
// deterministically; an entry with an empty key, an over-long key, or an
// over-long value is dropped and counted, never truncated. nil when
// nothing survives.
func mergeMetadata(inherited, own map[string]string) (map[string]string, int) {
	if len(inherited) == 0 && len(own) == 0 {
		return nil, 0
	}
	out := make(map[string]string, len(inherited)+len(own))
	maps.Copy(out, inherited)
	dropped := 0
	for _, k := range slices.Sorted(maps.Keys(own)) {
		v := own[k]
		if k == "" || len(k) > metadataMaxKeyLen || len(v) > metadataMaxValueLen {
			dropped++
			continue
		}
		if _, exists := out[k]; !exists && len(out) >= metadataMaxKeys {
			dropped++
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, dropped
	}
	return out, dropped
}

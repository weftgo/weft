package otel

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/weftgo/weft/core"
)

// ContentThread is the kind Redact sees for a weft/thread session
// record's content fields (record contract §4, kind thread): who
// decided, a deny reason, resolved content, a grant's reason, a queued
// or steered message's text, a branch summary, a delegated task and its
// answer — each string under the body's "content" object. A content-off
// destination receives none of them (the object is removed, weft.content
// = stripped); the record's ids, enums, counts and app-chosen names
// (a label's name) ride either way.
const ContentThread core.ContentKind = "thread"

// threadContentKey is the body key the content fields ride under.
const threadContentKey = "content"

// stripThread removes a thread record's content object and marks the
// record stripped — what a content-off destination receives. A body
// that does not parse as an object is dropped (reported false) unless
// the record says it carries no content: it could hold content this
// chain cannot find.
func (p *destProc) stripThread(clone *sdklog.Record) (keep bool) {
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return threadContentMark(clone) == "none"
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body.AsString()), &m); err != nil {
		return threadContentMark(clone) == "none"
	}
	if _, ok := m[threadContentKey]; !ok {
		return true
	}
	delete(m, threadContentKey)
	b, err := json.Marshal(m)
	if err != nil {
		return false
	}
	clone.SetBody(attribute.StringValue(string(b)))
	setAttr(clone, attrContentKey, attribute.StringValue(contentStripped))
	return true
}

// shapeThread applies the destination's Redact then MaxBytes to each
// content string of a thread record (ContentThread), recording a cut as
// weft.content.truncated_bytes. Redact runs on the caller's goroutine;
// a panic falls back to the stripped shape, counted as the
// destination's drop — the content never leaves unredacted.
func (p *destProc) shapeThread(clone *sdklog.Record) (keep bool) {
	defer func() {
		if v := recover(); v != nil {
			keep = p.stripThread(clone)
			p.drops.dropped(1, fmt.Errorf("content redaction panicked (%T): the thread record was exported stripped", v))
		}
	}()
	cfg := p.contentC
	if cfg.Redact == nil && cfg.MaxBytes < 0 {
		return true
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 32 << 10
	}
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return true
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body.AsString()), &m); err != nil {
		return p.stripThread(clone)
	}
	raw, ok := m[threadContentKey]
	if !ok {
		return true
	}
	var content map[string]string
	if err := json.Unmarshal(raw, &content); err != nil {
		return p.stripThread(clone)
	}
	cut := 0
	for k, v := range content {
		s, n := shapeString(v, ContentThread, cfg.Redact, cfg.MaxBytes)
		content[k] = s
		cut += n
	}
	cb, err := json.Marshal(content)
	if err != nil {
		return p.stripThread(clone)
	}
	m[threadContentKey] = cb
	b, err := json.Marshal(m)
	if err != nil {
		return p.stripThread(clone)
	}
	clone.SetBody(attribute.StringValue(string(b)))
	if cut > 0 {
		addAttr(clone, attrTruncated, attribute.Int64Value(int64(cut)))
	}
	return true
}

// threadContentMark is the record's weft.content value.
func threadContentMark(r *sdklog.Record) string {
	var v string
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == attrContentKey {
			v = kv.Value.AsString()
			return false
		}
		return true
	})
	return v
}

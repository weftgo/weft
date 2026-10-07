package otel

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weftgo/weft/core"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The content policy of ADR 0028's three record kinds (§6), per
// destination chain:
//
//	kind      content-on                                   content-off
//	request   Redact over each params.stop                 kept: params.stop and messages_ref.index removed, stripped
//	prompt    Redact over text, MaxBytes caps it           dropped (filtered)
//	tools     MaxBytes caps the body                       dropped (filtered)
//
// A content-off request also loses messages_ref.index (the messages
// records it points at were dropped there). The hashes are never touched: they were computed over the text before
// any destination shaped it, so a capped or redacted prompt still names
// itself. A cap that cut sets weft.content.truncated_bytes. A body that
// cannot be shaped — it does not decode, or Redact panicked — is never
// sent unshaped: a prompt or tools record is dropped and counted as the
// destination's loss, a request record goes out stripped.

// maxBytes is the destination's cap with the 32 KiB default.
func (p *destProc) maxBytes() int {
	if p.contentC.MaxBytes == 0 {
		return 32 << 10
	}
	return p.contentC.MaxBytes
}

// shapeContentRecord applies a content-on destination's Redact and cap
// to a prompt or tools record and reports whether it may go on.
func (p *destProc) shapeContentRecord(clone *sdklog.Record, kind string) (keep bool) {
	defer func() {
		if v := recover(); v != nil {
			keep = false
			// The panic's type only: its value is commonly built from
			// the very content Redact was given.
			p.drops.dropped(1, fmt.Errorf("content redaction panicked (%T): the %s record was dropped", v, kind))
		}
	}()
	body := clone.Body()
	if body.Type() != attribute.STRING {
		p.drops.dropped(1, fmt.Errorf("a %s record without a string body cannot be shaped: dropped", kind))
		return false
	}
	var (
		out []byte
		cut int
		err error
	)
	if kind == "prompt" {
		out, cut, err = shapePrompt(body.AsString(), p.contentC.Redact, p.maxBytes())
	} else {
		out, cut, err = capTools(body.AsString(), p.maxBytes())
	}
	if err != nil {
		p.drops.dropped(1, err)
		return false
	}
	if out != nil {
		clone.SetBody(attribute.StringValue(string(out)))
	}
	if cut > 0 {
		addAttr(clone, attrTruncated, attribute.Int64Value(int64(cut)))
	}
	return true
}

// shapePrompt redacts then caps a prompt record's text (on a rune
// boundary). It returns nil bytes when nothing changed.
func shapePrompt(body string, redact func(core.ContentKind, string) string, maxBytes int) ([]byte, int, error) {
	var pr struct {
		Hash string `json:"hash"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body), &pr); err != nil {
		return nil, 0, errors.New("a prompt record that does not decode cannot be shaped: dropped")
	}
	text, cut := shapeString(pr.Text, core.ContentPrompt, redact, maxBytes)
	if text == pr.Text && cut == 0 {
		return nil, 0, nil
	}
	pr.Text = text
	b, err := json.Marshal(pr)
	if err != nil {
		return nil, 0, errors.New("a shaped prompt record did not re-encode: dropped")
	}
	return b, cut, nil
}

// capTools caps a tools record's body at maxBytes without breaking it:
// a schema is a JSON document a byte cut would make undecodable, so
// whole tool entries are dropped from the end of the name-ordered list
// until the body fits, and the bytes removed are the cut. It returns
// nil bytes when the body already fits.
func capTools(body string, maxBytes int) ([]byte, int, error) {
	if maxBytes < 0 || len(body) <= maxBytes {
		return nil, 0, nil
	}
	var tr struct {
		Hash  string            `json:"hash"`
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		return nil, 0, errors.New("a tools record that does not decode cannot be capped: dropped")
	}
	if tr.Tools == nil {
		tr.Tools = []json.RawMessage{}
	}
	for {
		b, err := json.Marshal(tr)
		if err != nil {
			return nil, 0, errors.New("a capped tools record did not re-encode: dropped")
		}
		if len(b) <= maxBytes || len(tr.Tools) == 0 {
			return b, len(body) - len(b), nil
		}
		tr.Tools = tr.Tools[:len(tr.Tools)-1]
	}
}

// redactRequest applies a content-on destination's Redact to a request
// record's one text field, each params.stop string. A Redact that
// panics, or a body that does not decode, sends the record stripped.
func (p *destProc) redactRequest(clone *sdklog.Record) {
	redact := p.contentC.Redact
	if redact == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			p.stripRequest(clone)
			p.drops.dropped(1, fmt.Errorf("content redaction panicked (%T): the request record was exported stripped", v))
		}
	}()
	_, ok := editStop(clone, func(stop []string) ([]string, bool) {
		changed := false
		for i, s := range stop {
			if r := redact(core.ContentStop, s); r != s {
				stop[i], changed = r, true
			}
		}
		return stop, changed
	})
	if !ok {
		p.stripRequest(clone)
	}
}

// stripRequest is a content-off chain's request record: params.stop
// emptied and messages_ref.index removed — this destination never
// received the messages records it points at, so only the count is
// kept (ADR 0028 §3) — marked weft.content=stripped; the hashes, names
// and numbers stay.
func (p *destProc) stripRequest(clone *sdklog.Record) {
	if _, ok := editStop(clone, func([]string) ([]string, bool) { return nil, true }); !ok || !dropMessagesIndex(clone) {
		// Not a weft request body: the fields to strip cannot be found
		// — drop the body's text entirely.
		clone.SetBody(attribute.StringValue("{}"))
	}
	setAttr(clone, attrContentKey, attribute.StringValue(contentStripped))
}

// dropMessagesIndex removes messages_ref.index from a request body,
// re-encoding only when it was there. ok is false when the body is not
// a request object.
func dropMessagesIndex(clone *sdklog.Record) (ok bool) {
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return false
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body.AsString()), &req); err != nil || req == nil {
		return false
	}
	raw, has := req["messages_ref"]
	if !has {
		return true
	}
	var ref map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ref); err != nil {
		return false
	}
	if _, has := ref["index"]; !has {
		return true
	}
	delete(ref, "index")
	rb, err := json.Marshal(ref)
	if err != nil {
		return false
	}
	req["messages_ref"] = rb
	b, err := json.Marshal(req)
	if err != nil {
		return false
	}
	clone.SetBody(attribute.StringValue(string(b)))
	return true
}

// editStop rewrites a request body's params.stop through fn (nil
// removes it), re-encoding only when fn changed it. ok is false when
// the body is not a request object.
func editStop(clone *sdklog.Record, fn func([]string) ([]string, bool)) (changed, ok bool) {
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return false, false
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body.AsString()), &req); err != nil || req == nil {
		return false, false
	}
	raw, has := req["params"]
	if !has {
		return false, true
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return false, false
	}
	stopRaw, has := params["stop"]
	if !has {
		return false, true
	}
	var stop []string
	if err := json.Unmarshal(stopRaw, &stop); err != nil {
		return false, false
	}
	stop, changed = fn(stop)
	if !changed {
		return false, true
	}
	if stop == nil {
		delete(params, "stop")
	} else {
		b, err := json.Marshal(stop)
		if err != nil {
			return false, false
		}
		params["stop"] = b
	}
	pb, err := json.Marshal(params)
	if err != nil {
		return false, false
	}
	req["params"] = pb
	b, err := json.Marshal(req)
	if err != nil {
		return false, false
	}
	clone.SetBody(attribute.StringValue(string(b)))
	return true, true
}

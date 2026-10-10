package obsdb

import (
	"encoding/json"
	"time"
)

// RecordRaw is the raw record kind (ADR 0028 §12, plan A6): one model
// call attempt's wire exchange — the provider request and response a
// first-party adapter sent and received — emitted only under
// otel.RawBodies(true) with WEFT_ENV=dev, to the Local and Studio
// destinations. It is positioned by weft.raw.index and stored beside
// the request records; DB.Requests attaches it to its attempt's row
// (RequestRecord.Wire).
const RecordRaw = "raw"

// RawRecord is one stored raw record: the attempt's wire exchange, as
// the core shaped it — credential headers stripped and named in
// RedactedHeaders, credential query values replaced and named in
// RedactedQuery, each body capped at 256 KiB with the cut counted.
// Index is weft.raw.index, Step the stored weft.step.index, Attempt the
// body's attempt (the attempt in flight when the adapter reported).
// TruncatedBytes is weft.content.truncated_bytes, both bodies' cut;
// Content is HoleTruncated when it is above 0, HoleDerived when the
// body did not parse (a malformed producer: the sides are zero).
type RawRecord struct {
	Index           int64     `json:"index"`
	Step            int       `json:"step"`
	Attempt         int64     `json:"attempt"`
	Time            time.Time `json:"time"`
	Request         RawSide   `json:"request"`
	Response        *RawSide  `json:"response"`
	RedactedHeaders []string  `json:"redacted_headers"`
	RedactedQuery   []string  `json:"redacted_query,omitempty"`
	Content         Hole      `json:"content"`
	TruncatedBytes  int64     `json:"truncated_bytes"`
}

// RawSide is one side of the exchange. Body is the bytes as text, or
// base64 when Encoding is "base64" (the bytes were not UTF-8); Bytes is
// the whole body's size before the cap, TruncatedBytes what the cap
// removed. Method and URL are the request's, Status the response's
// (absent on the other side). A nil Response is an exchange that got no
// response (a transport error).
type RawSide struct {
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Status         int                 `json:"status,omitempty"`
	Headers        map[string][]string `json:"headers"`
	MediaType      string              `json:"media_type,omitempty"`
	Body           string              `json:"body"`
	Encoding       string              `json:"encoding,omitempty"`
	Bytes          int64               `json:"bytes"`
	TruncatedBytes int64               `json:"truncated_bytes,omitempty"`
}

// RawRecordOf builds a RawRecord from a stored raw record.
func RawRecordOf(r StoredRecord) RawRecord {
	var b struct {
		Step            int      `json:"step"`
		Attempt         int64    `json:"attempt"`
		Request         RawSide  `json:"request"`
		Response        *RawSide `json:"response"`
		RedactedHeaders []string `json:"redacted_headers"`
		RedactedQuery   []string `json:"redacted_query"`
	}
	out := RawRecord{
		Index: r.Index, Step: r.Step, Time: r.Time,
		Content: contentHole("", r.TruncatedBytes), TruncatedBytes: r.TruncatedBytes,
		RedactedHeaders: []string{},
	}
	if json.Unmarshal(r.Body, &b) != nil {
		out.Content = HoleDerived
		out.Request.Headers = map[string][]string{}
		return out
	}
	out.Attempt, out.Request, out.Response, out.RedactedQuery = b.Attempt, b.Request, b.Response, b.RedactedQuery
	if b.RedactedHeaders != nil {
		out.RedactedHeaders = b.RedactedHeaders
	}
	if out.Request.Headers == nil {
		out.Request.Headers = map[string][]string{}
	}
	if out.Response != nil && out.Response.Headers == nil {
		out.Response.Headers = map[string][]string{}
	}
	return out
}

// AttachRaw sets each request record's Wire to the raw record of its
// attempt — the same step and attempt — and leaves it nil where the run
// holds none (RawBodies off, a third-party adapter that does not
// report, a run written before the raw record). Where an SDK's own
// transport retries put several exchanges on one attempt, the last
// (the highest weft.raw.index) is the attempt's. raws are stored raw
// records in any order; every backend answers through it.
func AttachRaw(reqs []RequestRecord, raws []StoredRecord) {
	if len(reqs) == 0 || len(raws) == 0 {
		return
	}
	type key struct {
		step    int
		attempt int64
	}
	byAttempt := map[key]*RawRecord{}
	for _, s := range raws {
		r := RawRecordOf(s)
		if r.Content == HoleDerived {
			continue
		}
		k := key{r.Step, r.Attempt}
		if prev, ok := byAttempt[k]; !ok || r.Index > prev.Index {
			byAttempt[k] = &r
		}
	}
	for i := range reqs {
		if r, ok := byAttempt[key{reqs[i].Step, reqs[i].Attempt}]; ok {
			c := *r
			reqs[i].Wire = &c
		}
	}
}

// StepRange is the smallest and largest Step of reqs, for a backend's
// raw-record read; ok is false for an empty page.
func StepRange(reqs []RequestRecord) (lo, hi int, ok bool) {
	for i, r := range reqs {
		if i == 0 || r.Step < lo {
			lo = r.Step
		}
		if i == 0 || r.Step > hi {
			hi = r.Step
		}
	}
	return lo, hi, len(reqs) > 0
}

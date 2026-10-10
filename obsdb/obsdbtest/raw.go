package obsdbtest

import (
	"fmt"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// The raw record fixture (ADR 0028 §12): a two-step run whose step 1
// was retried, with raw records for step 0's attempt and both of step
// 1's — attempt 2 carrying two exchanges (an SDK transport retry), the
// later of which is the attempt's — and a cut response.

func fxRaw(step, attempt int, reqBody, respBody string) string {
	return fmt.Sprintf(`{"step":%d,"attempt":%d,"request":{"method":"POST","url":"https://api.example/v1/messages","headers":{"Content-Type":["application/json"]},"media_type":"application/json","body":%q,"bytes":%d},"response":{"status":200,"headers":{"Request-Id":["req_%d_%d"]},"media_type":"text/event-stream","body":%q,"bytes":%d},"redacted_headers":["authorization","x-api-key"]}`,
		step, attempt, reqBody, len(reqBody), step, attempt, respBody, len(respBody))
}

func (f *fxRun) raw(index int64, step, attempt int, body string, extra map[string]any) {
	a := map[string]any{
		"weft.content": "full", "weft.raw.index": index,
		"weft.step.index": int64(step), "weft.attempt.index": int64(attempt),
	}
	for k, v := range extra {
		a[k] = v
	}
	f.add(obsdb.RecordRaw, "weft.raw", body, a)
}

func requestRaw(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		f := &fxRun{id: "rw"}
		f.start(true)
		f.prompt(0, fxSystem1, fxPrompt1, nil)
		f.tools(0, fxCatalog1, fxTools1)
		f.request(0, 0, 1, fxSystem1, fxCatalog1, "full", fxRequest0)
		f.raw(0, 0, 1, fxRaw(0, 1, `{"step":0}`, "data: a\n\n"), nil)
		f.raw(1, 1, 1, fxRaw(1, 1, `{"step":1}`, "data: b\n\n"), map[string]any{"weft.content.truncated_bytes": int64(7)})
		f.request(1, 1, 1, fxSystem1, fxCatalog1, "full", fxRequest(1, 1, fxSystem1, fxCatalog1, "lookup", "script", 2, 3))
		f.raw(2, 1, 2, fxRaw(1, 2, `{"try":1}`, "overloaded"), nil)
		f.raw(3, 1, 2, fxRaw(1, 2, `{"try":2}`, "data: c\n\n"), nil)
		f.request(2, 1, 2, fxSystem1, fxCatalog1, "full", fxRequest(1, 2, fxSystem1, fxCatalog1, "lookup", "backup", 2, 3))
		f.finish(1, 2)
		plain := &fxRun{id: "rw_none"}
		plain.start(true)
		plain.request(0, 0, 1, fxSystem1, fxCatalog1, "full", fxRequest0)
		plain.finish(1, 1)
		recs := append(append([]obsdb.Record{}, f.recs...), plain.recs...)
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		// A retried transport: nothing moves.
		if err := db.Write(ctx(), obsdb.Batch{Records: f.recs}); err != nil {
			t.Fatal(err)
		}
		reqs, err := db.Requests(ctx(), "rw", obsdb.RequestQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(reqs) != 3 {
			t.Fatalf("requests = %d, want 3", len(reqs))
		}
		for i, c := range []struct {
			index          int64
			reqBody, resp  string
			truncated      int64
			hole           obsdb.Hole
			requestID      string
			redactedHeader string
		}{
			{0, `{"step":0}`, "data: a\n\n", 0, "", "req_0_1", "authorization"},
			{1, `{"step":1}`, "data: b\n\n", 7, obsdb.HoleTruncated, "req_1_1", "authorization"},
			{3, `{"try":2}`, "data: c\n\n", 0, "", "req_1_2", "authorization"},
		} {
			w := reqs[i].Wire
			if w == nil {
				t.Fatalf("request %d: no raw record attached", i)
			}
			if w.Index != c.index || w.Step != reqs[i].Step || w.Attempt != reqs[i].Attempt {
				t.Errorf("request %d: raw index %d step %d attempt %d; want %d %d %d", i, w.Index, w.Step, w.Attempt, c.index, reqs[i].Step, reqs[i].Attempt)
			}
			if w.Request.Body != c.reqBody || w.Response == nil || w.Response.Body != c.resp {
				t.Errorf("request %d: bodies %q / %+v", i, w.Request.Body, w.Response)
				continue
			}
			if w.TruncatedBytes != c.truncated || w.Content != c.hole {
				t.Errorf("request %d: truncated %d hole %q; want %d %q", i, w.TruncatedBytes, w.Content, c.truncated, c.hole)
			}
			if w.Request.Method != "POST" || w.Request.MediaType != "application/json" || w.Response.Status != 200 ||
				w.Response.MediaType != "text/event-stream" || w.Response.Headers["Request-Id"][0] != c.requestID {
				t.Errorf("request %d: wire = %+v / %+v", i, w.Request, *w.Response)
			}
			if len(w.RedactedHeaders) != 2 || w.RedactedHeaders[0] != c.redactedHeader {
				t.Errorf("request %d: redacted_headers = %v", i, w.RedactedHeaders)
			}
		}
		// A step filter and a page that starts past the raw records still
		// attach what is theirs.
		one := 1
		page, err := db.Requests(ctx(), "rw", obsdb.RequestQuery{Step: &one, From: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 || page[0].Wire == nil || page[0].Wire.Index != 3 {
			t.Errorf("step 1 from 2 = %+v", page)
		}
		// A run without raw records: every Wire is nil.
		none, err := db.Requests(ctx(), "rw_none", obsdb.RequestQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(none) != 1 || none[0].Wire != nil {
			t.Errorf("rw_none requests = %+v, want one without Wire", none)
		}
		// The raw record's position is contract, never run metadata.
		run, err := db.Run(ctx(), "rw")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := run.Meta["weft.raw.index"]; ok {
			t.Errorf("weft.raw.index landed in the run's metadata: %v", run.Meta)
		}
		if run.RequestCount != 3 {
			t.Errorf("request count = %d, want 3 (raw records are not requests)", run.RequestCount)
		}
	}
}

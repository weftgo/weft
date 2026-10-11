package core_test

// The raw record (ADR 0028 §12, plan A6): Reporter.Raw becomes one
// weft.raw record per reported exchange when a destination asks for the
// event name, and nothing — not even a body buffered by the adapter, as
// WantsRaw says — when none does.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/log"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

type rawSide struct {
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	Status         int                 `json:"status"`
	Headers        map[string][]string `json:"headers"`
	MediaType      string              `json:"media_type"`
	Body           string              `json:"body"`
	Encoding       string              `json:"encoding"`
	Bytes          int64               `json:"bytes"`
	TruncatedBytes int64               `json:"truncated_bytes"`
}

type rawDoc struct {
	Step            int      `json:"step"`
	Attempt         int64    `json:"attempt"`
	Request         rawSide  `json:"request"`
	Response        *rawSide `json:"response"`
	RedactedHeaders []string `json:"redacted_headers"`
	RedactedQuery   []string `json:"redacted_query"`
}

// markedLP is a provider carrying the RawBodies marker, as weft/otel's
// pipeline provider does under RawBodies and WEFT_ENV=dev.
type markedLP struct{ *recLogProvider }

func (markedLP) RawBodies() bool { return true }

// wrappedLP forwards to an inner provider and exposes it through
// Unwrap, so the marker stays reachable.
type wrappedLP struct{ log.LoggerProvider }

func (w wrappedLP) Unwrap() log.LoggerProvider { return w.LoggerProvider }

// unmarkedLP carries the marker answering false.
type unmarkedLP struct{ *recLogProvider }

func (unmarkedLP) RawBodies() bool { return false }

// The core asks two things before it builds a raw record: Enabled for
// weft.raw and the provider's RawBodies marker. A provider whose
// processors answer Enabled true for every name (a stock SDK provider;
// recLogProvider does the same) but carries no marker — or a marker
// answering false — never gets one; a wrapper reaches the marker
// through Unwrap.
func TestRawNeedsTheProviderMarker(t *testing.T) {
	for _, c := range []struct {
		name string
		lp   func(*recLogProvider) log.LoggerProvider
		want bool
	}{
		{"no marker", func(p *recLogProvider) log.LoggerProvider { return p }, false},
		{"marker false", func(p *recLogProvider) log.LoggerProvider { return unmarkedLP{p} }, false},
		{"marker true", func(p *recLogProvider) log.LoggerProvider { return markedLP{p} }, true},
		{"marker behind Unwrap", func(p *recLogProvider) log.LoggerProvider { return wrappedLP{markedLP{p}} }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newRecLogProvider()
			var wanted bool
			agt := core.New(wefttest.Script(wefttest.Say("done")), core.LoggerProvider(c.lp(p)),
				core.WrapModel(reportVia(func(ctx context.Context) {
					r := core.ReportFromContext(ctx)
					wanted = r.WantsRaw()
					r.Raw(core.RawPair{Request: []byte("x")})
				})))
			if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
				t.Fatal(err)
			}
			if n := len(p.ofKind(t, "raw")); wanted != c.want || (n == 1) != c.want {
				t.Errorf("WantsRaw %v, raw records %d; want %v", wanted, n, c.want)
			}
		})
	}
}

// rawRun runs one scripted step whose chain reports through fn, under
// p, and returns the raw records.
func rawRun(t testing.TB, p *recLogProvider, fn func(ctx context.Context), opts ...core.Option) []recLogRecord {
	t.Helper()
	opts = append([]core.Option{core.LoggerProvider(markedLP{p}), core.WrapModel(reportVia(fn))}, opts...)
	agt := core.New(wefttest.Script(wefttest.Say("done")), opts...)
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []recLogRecord
	for _, r := range p.records {
		if r.attr("weft.record") == "raw" {
			out = append(out, r)
		}
	}
	return out
}

func decodeRaw(t testing.TB, r recLogRecord) rawDoc {
	t.Helper()
	var d rawDoc
	if err := json.Unmarshal([]byte(r.body), &d); err != nil {
		t.Fatalf("raw body does not decode: %v\n%s", err, r.body)
	}
	return d
}

func TestRawRecordPerReport(t *testing.T) {
	p := newRecLogProvider()
	var wanted bool
	recs := rawRun(t, p, func(ctx context.Context) {
		r := core.ReportFromContext(ctx)
		wanted = r.WantsRaw()
		r.Raw(core.RawPair{
			Method: "POST", URL: "https://user:pw@api.example/v1/messages?key=sk-q&beta=true",
			Request: []byte(`{"model":"m"}`), MediaType: "application/json",
			RequestHeaders: map[string][]string{
				"Content-Type": {"application/json"}, "X-Api-Key": {"sk-secret"},
				"Authorization": {"Bearer sk-secret"}, "X-Trace": {"Bearer sk-secret"},
			},
			Status: 200, Response: []byte("event: ping\ndata: {}\n\n"), ResponseMediaType: "text/event-stream",
			ResponseHeaders: map[string][]string{"Request-Id": {"req_1"}, "Set-Cookie": {"s=sk-secret"}},
			RedactedHeaders: []string{"X-Gateway-Thing"},
		})
		r.Attempt(core.AttemptInfo{Model: "m"})
		// The second exchange belongs to attempt 2: the one in flight.
		r.Raw(core.RawPair{Request: []byte("again"), Status: 500})
	})
	if !wanted {
		t.Fatal("WantsRaw = false with every event name enabled")
	}
	if len(recs) != 2 {
		t.Fatalf("raw records = %d, want 2", len(recs))
	}
	r := recs[0]
	if r.eventName != "weft.raw" || r.attr("weft.content") != "full" || r.attr("weft.run.id") == "" {
		t.Errorf("record = %s content %q run %q", r.eventName, r.attr("weft.content"), r.attr("weft.run.id"))
	}
	for i, rec := range recs {
		idx, _ := rec.intAttr("weft.raw.index")
		att, _ := rec.intAttr("weft.attempt.index")
		step, _ := rec.intAttr("weft.step.index")
		if idx != int64(i) || att != int64(i+1) || step != 0 {
			t.Errorf("record %d: index %d attempt %d step %d", i, idx, att, step)
		}
	}
	if strings.Contains(r.body, "sk-") || strings.Contains(r.body, "pw@") {
		t.Fatalf("a credential reached the record: %s", r.body)
	}
	d := decodeRaw(t, r)
	if d.Request.Body != `{"model":"m"}` || d.Response == nil || d.Response.Body != "event: ping\ndata: {}\n\n" {
		t.Errorf("bodies = %q / %+v", d.Request.Body, d.Response)
	}
	if d.Request.Method != "POST" || d.Request.URL != "https://api.example/v1/messages?beta=true&key=REDACTED" {
		t.Errorf("request line = %s %s", d.Request.Method, d.Request.URL)
	}
	if d.Response.Status != 200 || d.Response.MediaType != "text/event-stream" || d.Request.MediaType != "application/json" {
		t.Errorf("status %d media %q / %q", d.Response.Status, d.Response.MediaType, d.Request.MediaType)
	}
	if got := strings.Join(d.RedactedHeaders, ","); got != "authorization,set-cookie,x-api-key,x-gateway-thing,x-trace" {
		t.Errorf("redacted_headers = %s", got)
	}
	if got := strings.Join(d.RedactedQuery, ","); got != "key,userinfo" {
		t.Errorf("redacted_query = %s", got)
	}
	if d.Request.Headers["Content-Type"][0] != "application/json" || d.Response.Headers["Request-Id"][0] != "req_1" {
		t.Errorf("kept headers = %v / %v", d.Request.Headers, d.Response.Headers)
	}
	if d.Request.Bytes != int64(len(`{"model":"m"}`)) || r.hasAttr("weft.content.truncated_bytes") {
		t.Errorf("bytes %d, truncated attr present %v", d.Request.Bytes, r.hasAttr("weft.content.truncated_bytes"))
	}
}

// Nobody asking for weft.raw, the reporter says so before any body is
// built and records nothing; weft.Content(false) wins over a
// destination that asks, and Content(true) does not force it.
func TestRawNotWanted(t *testing.T) {
	for _, c := range []struct {
		name    string
		enabled func(string) bool
		opts    []core.Option
	}{
		{"no destination asks", func(n string) bool { return n != "weft.raw" }, nil},
		{"Content(false)", nil, []core.Option{core.Content(false)}},
		{"Content(true) alone", func(n string) bool { return n != "weft.raw" }, []core.Option{core.Content(true)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newRecLogProvider()
			p.enabled = c.enabled
			wanted := true
			recs := rawRun(t, p, func(ctx context.Context) {
				r := core.ReportFromContext(ctx)
				wanted = r.WantsRaw()
				r.Raw(core.RawPair{Request: []byte("x")})
			}, c.opts...)
			if wanted || len(recs) != 0 {
				t.Errorf("WantsRaw = %v, raw records = %d; want false, 0", wanted, len(recs))
			}
		})
	}
	if (core.Reporter{}).WantsRaw() || core.ReportFromContext(context.Background()).WantsRaw() {
		t.Error("WantsRaw true outside a run")
	}
}

// Each body is capped at 256 KiB, the cut counted on the body and in
// weft.content.truncated_bytes with the reporter's own cut; a body that
// is not UTF-8 is base64.
func TestRawCapAndEncoding(t *testing.T) {
	p := newRecLogProvider()
	big := strings.Repeat("é", 200<<10) // 400 KiB of two-byte runes
	bin := []byte{0xff, 0xfe, 0x00, 0x01}
	recs := rawRun(t, p, func(ctx context.Context) {
		core.ReportFromContext(ctx).Raw(core.RawPair{
			Request: []byte(big), RequestTruncated: 10,
			Status: 200, Response: bin,
		})
	})
	if len(recs) != 1 {
		t.Fatalf("raw records = %d", len(recs))
	}
	d := decodeRaw(t, recs[0])
	if len(d.Request.Body) != 256<<10 || !strings.HasPrefix(big, d.Request.Body) {
		t.Errorf("capped body = %d bytes", len(d.Request.Body))
	}
	wantCut := int64(len(big)-256<<10) + 10
	if d.Request.TruncatedBytes != wantCut || d.Request.Bytes != int64(len(big))+10 {
		t.Errorf("truncated %d bytes %d, want %d %d", d.Request.TruncatedBytes, d.Request.Bytes, wantCut, len(big)+10)
	}
	if n, _ := recs[0].intAttr("weft.content.truncated_bytes"); n != wantCut {
		t.Errorf("weft.content.truncated_bytes = %d, want %d", n, wantCut)
	}
	if d.Response.Encoding != "base64" || d.Response.Body != base64.StdEncoding.EncodeToString(bin) {
		t.Errorf("binary response = %q %q", d.Response.Encoding, d.Response.Body)
	}
}

// The raw record is encoded without HTML escaping, so the record's size
// tracks the capped bodies: a body of '<' and '&' (6 bytes each under
// json.Marshal's HTML escaping) stays one byte per byte, and the record
// is at most the two capped bodies plus a small envelope. JSON's own
// escaping remains — a C0 control is six bytes (\u0001) — so the real
// worst case is six times the capped bodies, also checked.
func TestRawEncodingIsNotHTMLEscaped(t *testing.T) {
	const envelope = 4 << 10
	html := []byte(strings.Repeat("<&>", 100<<10)) // 300 KiB, cut to 256 KiB
	p := newRecLogProvider()
	recs := rawRun(t, p, func(ctx context.Context) {
		core.ReportFromContext(ctx).Raw(core.RawPair{Request: html, Status: 200, Response: html})
	})
	if len(recs) != 1 {
		t.Fatalf("raw records = %d", len(recs))
	}
	body := recs[0].body
	if strings.Contains(body, `\u003c`) || strings.Contains(body, `\u0026`) {
		t.Errorf("raw record is HTML-escaped (%d bytes)", len(body))
	}
	if bound := 2*256<<10 + envelope; len(body) > bound {
		t.Errorf("raw record = %d bytes, want <= %d (two capped bodies + envelope)", len(body), bound)
	}
	if d := decodeRaw(t, recs[0]); d.Request.Body != string(html[:256<<10]) || d.Response.Body != d.Request.Body {
		t.Errorf("bodies do not round-trip")
	}

	ctl := []byte(strings.Repeat("\x01", 300<<10))
	p = newRecLogProvider()
	recs = rawRun(t, p, func(ctx context.Context) {
		core.ReportFromContext(ctx).Raw(core.RawPair{Request: ctl, Status: 200, Response: ctl})
	})
	if bound := 6*2*256<<10 + envelope; len(recs[0].body) > bound {
		t.Errorf("control-byte raw record = %d bytes, want <= %d (the stated worst case)", len(recs[0].body), bound)
	}
}

// Under mw.Retry the adapter's two exchanges land on attempts 1 and 2:
// each Raw is reported before the retry layer reports its attempt.
func TestRawAttemptsUnderRetry(t *testing.T) {
	p := newRecLogProvider()
	model := wefttest.Script(wefttest.Fail(core.ErrStreamIdle), wefttest.Say("ok"))
	raw := func(next core.Model) core.Model {
		return reportingFunc(func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool) {
			core.ReportFromContext(ctx).Raw(core.RawPair{Request: []byte("try")})
			for ev, err := range next.Stream(ctx, req) {
				if !yield(ev, err) {
					return
				}
			}
		})
	}
	agt := core.New(model, core.LoggerProvider(markedLP{p}), core.WrapModel(mw.Retry(mw.BaseDelay(1), mw.MaxWait(1e9)), raw))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	recs := p.ofKind(t, "raw")
	if len(recs) != 2 {
		t.Fatalf("raw records = %d, want 2", len(recs))
	}
	for i, r := range recs {
		if a, _ := r.intAttr("weft.attempt.index"); a != int64(i+1) {
			t.Errorf("raw %d attempt = %d", i, a)
		}
	}
}

// rawSecrets are credential header names in the case variants an SDK
// or a gateway may send.
var rawSecrets = []string{
	"Authorization", "authorization", "AUTHORIZATION", "X-Api-Key", "x-api-key", "X-API-Key",
	"Api-Key", "api-key", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Goog-Api-Key",
	"x-goog-api-key", "X-Auth-Token", "X-Client-Secret", "Anthropic-Api-Key", "Openai_Api_Key",
}

// FuzzRawRedaction: whatever header names and values a reporter hands
// over, no value under a credential header (any case) and no Bearer or
// Basic token reaches the emitted record's bytes, and every stripped
// name is listed. The fuzzed value is suffixed with a marker so the
// byte search cannot match the record's own JSON by accident.
func FuzzRawRedaction(f *testing.F) {
	for _, s := range rawSecrets {
		f.Add(s, "sk-ant-SEKRET", "X-Other", "fine")
	}
	f.Add("X-Trace", "Bearer SEKRET", "Content-Type", "application/json")
	f.Add("x-custom", "basic SEKRET", "Accept", "*/*")
	f.Add("Authorization", `"quoted"<&>`, "X-Other", "fine")
	f.Fuzz(func(t *testing.T, name, value, other, otherValue string) {
		if name == "" || other == "" {
			return
		}
		secret := value + "~sk9~"
		p := newRecLogProvider()
		h := map[string][]string{name: {secret}, other: {otherValue}}
		recs := rawRun(t, p, func(ctx context.Context) {
			core.ReportFromContext(ctx).Raw(core.RawPair{RequestHeaders: h, ResponseHeaders: h, Status: 200})
		})
		if len(recs) != 1 {
			t.Fatalf("raw records = %d", len(recs))
		}
		body := []byte(recs[0].body)
		d := decodeRaw(t, recs[0])
		for _, side := range []map[string][]string{d.Request.Headers, d.Response.Headers} {
			for k, vs := range side {
				lk := strings.ToLower(k)
				for _, s := range rawSecrets {
					if lk == strings.ToLower(s) {
						t.Fatalf("credential header %q survived: %v", k, side)
					}
				}
				for _, v := range vs {
					lv := strings.ToLower(strings.TrimSpace(v))
					if strings.HasPrefix(lv, "bearer ") || strings.HasPrefix(lv, "basic ") {
						t.Fatalf("auth token survived under %q", k)
					}
				}
			}
		}
		if name != other && (core.SensitiveHeader(name) || core.SensitiveValue(secret)) &&
			!strings.Contains(otherValue, secret) && !strings.Contains(other, secret) && !strings.Contains(name, secret) {
			esc, _ := json.Marshal(secret)
			if bytes.Contains(body, []byte(secret)) || bytes.Contains(body, esc[1:len(esc)-1]) {
				t.Fatalf("the value under %q reached the record: %s", name, body)
			}
			listed := false
			for _, r := range d.RedactedHeaders {
				listed = listed || r == strings.ToLower(strings.TrimSpace(name))
			}
			if !listed {
				t.Fatalf("stripped %q not listed in %v", name, d.RedactedHeaders)
			}
		}
	})
}
